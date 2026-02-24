package logstorage

import (
	"fmt"
	"strings"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/logger"
	"github.com/valyala/fastjson"

	"github.com/VictoriaMetrics/VictoriaLogs/lib/prefixfilter"
)

// filterJSONArrayContains matches if the JSON array in the given field contains the given value.
//
// Example LogsQL: `tags:json_array_contains("prod")`
type filterJSONArrayContains struct {
	fieldName string
	value     string
}

func (fa *filterJSONArrayContains) String() string {
	return fmt.Sprintf("%sjson_array_contains(%s)", quoteFieldNameIfNeeded(fa.fieldName), quoteTokenIfNeeded(fa.value))
}

func (fa *filterJSONArrayContains) updateNeededFields(pf *prefixfilter.Filter) {
	pf.AddAllowFilter(fa.fieldName)
}

func (fa *filterJSONArrayContains) matchRow(fields []Field) bool {
	v := getFieldValueByName(fields, fa.fieldName)
	return matchJSONArrayContains(v, fa.value)
}

func (fa *filterJSONArrayContains) applyToBlockResult(br *blockResult, bm *bitmap) {
	c := br.getColumnByName(fa.fieldName)
	if c.isConst {
		v := c.valuesEncoded[0]
		if !matchJSONArrayContains(v, fa.value) {
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
			return matchJSONArrayContains(v, fa.value)
		})
	case valueTypeDict:
		bb := bbPool.Get()
		for _, v := range c.dictValues {
			c := byte(0)
			if matchJSONArrayContains(v, fa.value) {
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

func (fa *filterJSONArrayContains) applyToBlockSearch(bs *blockSearch, bm *bitmap) {
	fieldName := fa.fieldName
	value := fa.value

	v := bs.getConstColumnValue(fieldName)
	if v != "" {
		if !matchJSONArrayContains(v, value) {
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
		matchStringByJSONArrayContains(bs, ch, bm, value)
	case valueTypeDict:
		matchValuesDictByJSONArrayContains(bs, ch, bm, value)
	default:
		bm.resetBits()
	}
}

func matchValuesDictByJSONArrayContains(bs *blockSearch, ch *columnHeader, bm *bitmap, value string) {
	bb := bbPool.Get()
	for _, v := range ch.valuesDict.values {
		c := byte(0)
		if matchJSONArrayContains(v, value) {
			c = 1
		}
		bb.B = append(bb.B, c)
	}
	matchEncodedValuesDict(bs, ch, bm, bb.B)
	bbPool.Put(bb)
}

func matchStringByJSONArrayContains(bs *blockSearch, ch *columnHeader, bm *bitmap, value string) {
	visitValues(bs, ch, bm, func(v string) bool {
		return matchJSONArrayContains(v, value)
	})
}

func matchJSONArrayContains(s, value string) bool {
	if s == "" {
		// Fast path for empty strings.
		return false
	}

	s = trimJSONWhitespace(s)

	if !strings.HasPrefix(s, "[") {
		// Fast path - this is not a JSON array.
		return false
	}

	p := jspp.Get()
	defer jspp.Put(p)

	v, err := p.Parse(s)
	if err != nil {
		return false
	}
	if v.Type() != fastjson.TypeArray {
		return false
	}
	jsa, err := v.Array()
	if err != nil {
		logger.Panicf("BUG: v.Array() mustn't return error; got %s", err)
	}

	for _, e := range jsa {
		// We only support checking against string representation of values in the array.
		switch e.Type() {
		case fastjson.TypeString:
			bRaw, err := e.StringBytes()
			if err != nil {
				logger.Panicf("BUG: e.StringBytes() mustn't return error; got %s", err)
			}
			if string(bRaw) == value {
				return true
			}
		case fastjson.TypeNumber, fastjson.TypeTrue, fastjson.TypeFalse, fastjson.TypeNull:
			bb := bbPool.Get()
			bb.B = e.MarshalTo(bb.B[:0])
			ok := string(bb.B) == value
			bbPool.Put(bb)
			if ok {
				return true
			}
		}
	}

	return false
}

func trimJSONWhitespace(s string) string {
	// trim whitespace prefix
	for len(s) > 0 {
		c := s[0]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		s = s[1:]
	}

	// trim whitespace suffix
	for len(s) > 0 {
		c := s[len(s)-1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		s = s[:len(s)-1]
	}

	return s
}
