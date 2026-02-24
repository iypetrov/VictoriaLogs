package logstorage

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/VictoriaMetrics/VictoriaLogs/lib/prefixfilter"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/bytesutil"
	"github.com/valyala/fastjson"
)

// filterArrayContains matches if the JSON array in the given field contains the given value.
//
// Example LogsQL: `tags:array_contains("prod")`
type filterArrayContains struct {
	fieldName string
	value     string
}

func (fa *filterArrayContains) String() string {
	return fmt.Sprintf("%sarray_contains(%s)", quoteFieldNameIfNeeded(fa.fieldName), quoteTokenIfNeeded(fa.value))
}

func (fa *filterArrayContains) updateNeededFields(pf *prefixfilter.Filter) {
	pf.AddAllowFilter(fa.fieldName)
}

func (fa *filterArrayContains) matchRow(fields []Field) bool {
	v := getFieldValueByName(fields, fa.fieldName)
	return matchArrayContains(v, fa.value)
}

func (fa *filterArrayContains) applyToBlockResult(br *blockResult, bm *bitmap) {
	c := br.getColumnByName(fa.fieldName)
	if c.isConst {
		v := c.valuesEncoded[0]
		if !matchArrayContains(v, fa.value) {
			bm.resetBits()
		}
		return
	}
	if c.isTime {
		bm.resetBits()
		return
	}

	switch c.valueType {
	case valueTypeString:
		values := c.getValues(br)
		bm.forEachSetBit(func(idx int) bool {
			v := values[idx]
			return matchArrayContains(v, fa.value)
		})
	case valueTypeDict:
		bb := bbPool.Get()
		for _, v := range c.dictValues {
			c := byte(0)
			if matchArrayContains(v, fa.value) {
				c = 1
			}
			bb.B = append(bb.B, c)
		}
		valuesEncoded := c.getValuesEncoded(br)
		bm.forEachSetBit(func(idx int) bool {
			n := valuesEncoded[idx][0]
			return bb.B[n] == 1
		})
		bbPool.Put(bb)
	default:
		bm.resetBits()
	}
}

func (fa *filterArrayContains) applyToBlockSearch(bs *blockSearch, bm *bitmap) {
	fieldName := fa.fieldName
	value := fa.value

	v := bs.getConstColumnValue(fieldName)
	if v != "" {
		if !matchArrayContains(v, value) {
			bm.resetBits()
		}
		return
	}

	// Verify whether filter matches other columns
	ch := bs.getColumnHeader(fieldName)
	if ch == nil {
		// Fast path - there are no matching columns.
		bm.resetBits()
		return
	}

	switch ch.valueType {
	case valueTypeString:
		matchStringByArrayContains(bs, ch, bm, value)
	case valueTypeDict:
		matchValuesDictByArrayContains(bs, ch, bm, value)
	default:
		bm.resetBits()
	}
}

func matchValuesDictByArrayContains(bs *blockSearch, ch *columnHeader, bm *bitmap, value string) {
	bb := bbPool.Get()
	for _, v := range ch.valuesDict.values {
		c := byte(0)
		if matchArrayContains(v, value) {
			c = 1
		}
		bb.B = append(bb.B, c)
	}
	matchEncodedValuesDict(bs, ch, bm, bb.B)
	bbPool.Put(bb)
}

func matchStringByArrayContains(bs *blockSearch, ch *columnHeader, bm *bitmap, value string) {
	visitValues(bs, ch, bm, func(v string) bool {
		return matchArrayContains(v, value)
	})
}

func matchArrayContains(s, value string) bool {
	if s == "" {
		return false
	}

	// Fast check: must start with '[' (optionally after JSON whitespace).
	if s[0] != '[' {
		// Slow path: JSON allows leading whitespace.
		i := 0
		for i < len(s) {
			switch s[i] {
			case ' ', '\t', '\n', '\r':
				i++
			default:
				goto doneSkippingSpaces
			}
		}
	doneSkippingSpaces:
		if i >= len(s) || s[i] != '[' {
			return false
		}
		s = s[i:]
	}

	valueBytes := bytesutil.ToUnsafeBytes(value)

	// Use shared fastjson.ParserPool in order to avoid per-call parser allocations.
	p := jspp.Get()
	v, err := p.Parse(s)
	if err != nil {
		jspp.Put(p)
		return false
	}

	// Check if it is an array
	jsa, err := v.Array()
	if err != nil {
		jspp.Put(p)
		return false
	}

	var bb bytesutil.ByteBuffer
	for _, elem := range jsa {
		// We only support checking against string representation of values in the array.
		switch elem.Type() {
		case fastjson.TypeString:
			// Use StringBytes() instead of GetStringBytes() in order to avoid extra allocations and follow fastjson conventions.
			bRaw, err := elem.StringBytes()
			if err != nil {
				continue
			}
			// Fast path: fastjson returns raw string without JSON unescaping, so compare raw bytes first.
			if bytes.Equal(bRaw, valueBytes) {
				jspp.Put(p)
				return true
			}
			if bytes.IndexByte(bRaw, '\\') < 0 {
				// The raw string doesn't contain escape sequences, so it cannot match.
				continue
			}
			// Slow path: JSON-unescape bRaw and compare with the searched value.
			// This is needed for strings containing JSON escape sequences such as \" or \n.
			bb.B = append(bb.B[:0], '"')
			bb.B = append(bb.B, bRaw...)
			bb.B = append(bb.B, '"')
			var us string
			if err := json.Unmarshal(bb.B, &us); err != nil {
				continue
			}
			if us == value {
				jspp.Put(p)
				return true
			}
			continue
		case fastjson.TypeNumber, fastjson.TypeTrue, fastjson.TypeFalse, fastjson.TypeNull:
			// Fast path: use MarshalTo() for non-string scalar values, since it doesn't wrap numbers/bools/null into quotes.
			bb.B = elem.MarshalTo(bb.B[:0])
			if bytes.Equal(bb.B, valueBytes) {
				jspp.Put(p)
				return true
			}
			continue
		default:
			continue
		}
	}

	jspp.Put(p)
	return false
}
