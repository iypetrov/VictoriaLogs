package logstorage

import (
	"testing"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/fs"
)

func TestMatchArrayContains(t *testing.T) {
	t.Parallel()

	f := func(s, value string, resultExpected bool) {
		t.Helper()
		result := matchArrayContains(s, value)
		if result != resultExpected {
			t.Fatalf("unexpected result for s=%q, value=%q; got %v; want %v", s, value, result, resultExpected)
		}
	}

	// Not an array
	f("", "foo", false)
	f("foo", "foo", false)
	f("{}", "foo", false)

	// Array doesn't contain value
	f("[]", "foo", false)
	f(`["bar"]`, "foo", false)
	f(`["bar","baz"]`, "foo", false)
	f(`[1,2]`, "3", false)

	// Array contains value
	f(`["foo"]`, "foo", true)
	f(`["bar","foo"]`, "foo", true)
	f(`["foo","bar"]`, "foo", true)
	f(`["a","foo","b"]`, "foo", true)

	// Mixed types
	f(`[123]`, "123", true)
	f(`[true]`, "true", true)
	f(`["123"]`, "123", true)

	// Leading whitespace (valid JSON)
	f(" \t\r\n[\"foo\"]", "foo", true)

	// Tricky cases
	f(`["foo bar"]`, "foo", false) // partial match
	f(`["foobar"]`, "foo", false)  // partial match
	f(`["foo"]`, "fo", false)      // partial match

	// Escaped strings in JSON
	f(`["a\"b"]`, "a\"b", true)  // \" escape => a"b
	f(`["a\nb"]`, "a\nb", true)  // \n escape
	f(`["a\u0062"]`, "ab", true) // \u0062 => 'b'
	f(`["a\/b"]`, "a/b", true)   // \/ escape is valid in JSON

	// Nested structures (ignored by current implementation)
	f(`[{"a":"b"}]`, `{"a":"b"}`, false) // nested object ignored
	f(`[["a"]]`, `["a"]`, false)         // nested array ignored
	f(`[["a"], "b"]`, "b", true)         // mixed with simple value
}

func TestFilterArrayContains(t *testing.T) {
	t.Parallel()

	t.Run("const-column", func(t *testing.T) {
		columns := []column{
			{
				name: "foo",
				values: []string{
					`["a","b"]`,
					`["a","b"]`,
					`["a","b"]`,
				},
			},
		}

		// match
		fa := &filterArrayContains{
			fieldName: "foo",
			value:     "a",
		}
		testFilterMatchForColumns(t, columns, fa, "foo", []int{0, 1, 2})

		fa = &filterArrayContains{
			fieldName: "foo",
			value:     "b",
		}
		testFilterMatchForColumns(t, columns, fa, "foo", []int{0, 1, 2})

		// mismatch
		fa = &filterArrayContains{
			fieldName: "foo",
			value:     "c",
		}
		testFilterMatchForColumns(t, columns, fa, "foo", nil)

		fa = &filterArrayContains{
			fieldName: "non-existing-column",
			value:     "a",
		}
		testFilterMatchForColumns(t, columns, fa, "foo", nil)
	})

	t.Run("dict", func(t *testing.T) {
		columns := []column{
			{
				name: "foo",
				values: []string{
					"",
					`["a"]`,
					`["b"]`,
					`["a","b"]`,
					`"a"`, // not an array
					`[1,2]`,
				},
			},
		}

		// match
		fa := &filterArrayContains{
			fieldName: "foo",
			value:     "a",
		}
		testFilterMatchForColumns(t, columns, fa, "foo", []int{1, 3})

		fa = &filterArrayContains{
			fieldName: "foo",
			value:     "b",
		}
		testFilterMatchForColumns(t, columns, fa, "foo", []int{2, 3})

		// mismatch
		fa = &filterArrayContains{
			fieldName: "foo",
			value:     "c",
		}
		testFilterMatchForColumns(t, columns, fa, "foo", nil)
	})

	t.Run("strings", func(t *testing.T) {
		columns := []column{
			{
				name: "foo",
				values: []string{
					`["apple", "banana"]`,
					`["orange"]`,
					`not array`,
					`["apple"]`,
					`[]`,
				},
			},
		}

		// match
		fa := &filterArrayContains{
			fieldName: "foo",
			value:     "apple",
		}
		testFilterMatchForColumns(t, columns, fa, "foo", []int{0, 3})

		// mismatch
		fa = &filterArrayContains{
			fieldName: "foo",
			value:     "pear",
		}
		testFilterMatchForColumns(t, columns, fa, "foo", nil)
	})

	// Remove the remaining data files for the test
	fs.MustRemoveDir(t.Name())
}
