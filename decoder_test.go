package jsonlimit_test

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/__GH_OWNER__/jsonlimit"
)

func unlimited() jsonlimit.Limits {
	return jsonlimit.Limits{} // zero value: every check disabled
}

func TestDecode_MatchesUnmarshalOnOrdinaryInput(t *testing.T) {
	cases := []string{
		`null`,
		`true`,
		`false`,
		`42`,
		`-3.5`,
		`"hello"`,
		`[]`,
		`{}`,
		`[1,2,3]`,
		`{"a":1,"b":[true,false,null],"c":{"d":"e"}}`,
		`  {"padded": true}  `,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			got, err := jsonlimit.Decode(strings.NewReader(in), unlimited())
			if err != nil {
				t.Fatalf("Decode(%q) = _, %v", in, err)
			}
			var want any
			if err := json.Unmarshal([]byte(in), &want); err != nil {
				t.Fatalf("json.Unmarshal(%q) = _, %v", in, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Decode(%q) = %#v, want %#v (from json.Unmarshal)", in, got, want)
			}
		})
	}
}

func TestDecode_RepeatedKeyLastWriterWins(t *testing.T) {
	// json.Unmarshal into `any` keeps the last occurrence of a duplicate
	// key; Decode should match that, even though it also counts the key
	// twice against MaxKeys.
	got, err := jsonlimit.Decode(strings.NewReader(`{"a":1,"a":2}`), unlimited())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := map[string]any{"a": 2.0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode = %#v, want %#v", got, want)
	}
}

func TestDecode_StopsAtFirstTopLevelValue(t *testing.T) {
	// Decode reads exactly one JSON value and must not choke on, or
	// silently consume, whatever follows it.
	got, err := jsonlimit.Decode(strings.NewReader(`1 2 3`), unlimited())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != 1.0 {
		t.Fatalf("Decode = %#v, want 1.0", got)
	}
}

func TestDecode_EmptyInputIsUnexpectedEOF(t *testing.T) {
	_, err := jsonlimit.Decode(strings.NewReader(""), unlimited())
	if err == nil {
		t.Fatal("Decode(\"\") succeeded, want an error")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Decode(\"\") error = %v, want wrapping io.ErrUnexpectedEOF", err)
	}
}

func TestDecode_SyntaxErrorPassesThroughUnwrapped(t *testing.T) {
	_, err := jsonlimit.Decode(strings.NewReader(`{"a": }`), unlimited())
	if err == nil {
		t.Fatal("Decode of malformed JSON succeeded, want an error")
	}
	var le jsonlimit.LimitError
	if errors.As(err, &le) {
		t.Fatalf("malformed JSON produced a LimitError (%v), want a plain syntax error", err)
	}
	var se *json.SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("error = %v, want it to wrap a *json.SyntaxError", err)
	}
}

func TestDecode_MaxDepth(t *testing.T) {
	// 5 levels of array nesting: well inside encoding/json's own 10000
	// ceiling, so this exercises jsonlimit's own check, not the stdlib's.
	in := strings.Repeat("[", 5) + "1" + strings.Repeat("]", 5)

	if _, err := jsonlimit.Decode(strings.NewReader(in), jsonlimit.Limits{MaxDepth: 5}); err != nil {
		t.Fatalf("MaxDepth=5 on depth-5 input: %v", err)
	}

	_, err := jsonlimit.Decode(strings.NewReader(in), jsonlimit.Limits{MaxDepth: 4})
	if err == nil {
		t.Fatal("MaxDepth=4 on depth-5 input succeeded, want *DepthLimitError")
	}
	var de *jsonlimit.DepthLimitError
	if !errors.As(err, &de) {
		t.Fatalf("error = %v (%T), want *DepthLimitError", err, err)
	}
	if de.Limit() != 4 {
		t.Errorf("Limit() = %d, want 4", de.Limit())
	}
	if de.Observed() != 5 {
		t.Errorf("Observed() = %d, want 5 (limit+1)", de.Observed())
	}
	if de.Offset() != 5 { // 5 '[' bytes read by the time the 5th is rejected
		t.Errorf("Offset() = %d, want 5", de.Offset())
	}
	if !strings.Contains(de.Error(), "max depth 4") {
		t.Errorf("Error() = %q, want it to mention the limit", de.Error())
	}
}

func TestDecode_MaxKeys(t *testing.T) {
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < 10; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"k` + strconv.Itoa(i) + `":1`)
	}
	b.WriteByte('}')
	in := b.String()

	if _, err := jsonlimit.Decode(strings.NewReader(in), jsonlimit.Limits{MaxKeys: 10}); err != nil {
		t.Fatalf("MaxKeys=10 on a 10-key object: %v", err)
	}

	_, err := jsonlimit.Decode(strings.NewReader(in), jsonlimit.Limits{MaxKeys: 9})
	var ke *jsonlimit.KeyLimitError
	if !errors.As(err, &ke) {
		t.Fatalf("error = %v (%T), want *KeyLimitError", err, err)
	}
	if ke.Limit() != 9 || ke.Observed() != 10 {
		t.Errorf("Limit()=%d Observed()=%d, want 9 and 10", ke.Limit(), ke.Observed())
	}
}

func TestDecode_MaxKeysCountsDuplicates(t *testing.T) {
	// {"a":1,"a":2} has one key in the result but the decoder reads two.
	_, err := jsonlimit.Decode(strings.NewReader(`{"a":1,"a":2}`), jsonlimit.Limits{MaxKeys: 1})
	var ke *jsonlimit.KeyLimitError
	if !errors.As(err, &ke) {
		t.Fatalf("error = %v (%T), want *KeyLimitError for a repeated key", err, err)
	}
}

func TestDecode_MaxStringLength(t *testing.T) {
	value := strings.Repeat("x", 100)
	in := `"` + value + `"`

	if _, err := jsonlimit.Decode(strings.NewReader(in), jsonlimit.Limits{MaxStringLength: 100}); err != nil {
		t.Fatalf("MaxStringLength=100 on a 100-byte string: %v", err)
	}

	_, err := jsonlimit.Decode(strings.NewReader(in), jsonlimit.Limits{MaxStringLength: 99})
	var se *jsonlimit.StringLengthError
	if !errors.As(err, &se) {
		t.Fatalf("error = %v (%T), want *StringLengthError", err, err)
	}
	if se.Limit() != 99 {
		t.Errorf("Limit() = %d, want 99", se.Limit())
	}
	if se.Observed() != 100 {
		t.Errorf("Observed() = %d, want the string's true length 100, not limit+1", se.Observed())
	}
}

func TestDecode_MaxStringLengthAppliesToKeysToo(t *testing.T) {
	key := strings.Repeat("k", 50)
	in := `{"` + key + `":1}`
	_, err := jsonlimit.Decode(strings.NewReader(in), jsonlimit.Limits{MaxStringLength: 49})
	var se *jsonlimit.StringLengthError
	if !errors.As(err, &se) {
		t.Fatalf("error = %v (%T), want *StringLengthError for an oversized key", err, err)
	}
}

func TestDecode_MaxDecodedSize(t *testing.T) {
	// A handful of short strings in an array; work out a budget that
	// admits them all, then one that doesn't.
	in := `["aaaa","bbbb","cccc","dddd"]`

	full, err := jsonlimit.Decode(strings.NewReader(in), jsonlimit.Limits{MaxDecodedSize: 1 << 20})
	if err != nil {
		t.Fatalf("generous MaxDecodedSize: %v", err)
	}
	if arr, ok := full.([]any); !ok || len(arr) != 4 {
		t.Fatalf("Decode = %#v, want a 4-element slice", full)
	}

	_, err = jsonlimit.Decode(strings.NewReader(in), jsonlimit.Limits{MaxDecodedSize: 1})
	var de *jsonlimit.DecodedSizeError
	if !errors.As(err, &de) {
		t.Fatalf("error = %v (%T), want *DecodedSizeError with a 1-byte budget", err, err)
	}
}

func TestDecode_ZeroLimitsDisableChecks(t *testing.T) {
	// 200 levels of nesting and 200 keys both pass encoding/json's own
	// 10000-deep ceiling comfortably, so a zero-value Limits must let
	// them through unmodified.
	deep := strings.Repeat("[", 200) + "1" + strings.Repeat("]", 200)
	if _, err := jsonlimit.Decode(strings.NewReader(deep), jsonlimit.Limits{}); err != nil {
		t.Fatalf("zero-value Limits rejected 200 levels of nesting: %v", err)
	}

	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < 200; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"k` + strconv.Itoa(i) + `":1`)
	}
	b.WriteByte('}')
	if _, err := jsonlimit.Decode(strings.NewReader(b.String()), jsonlimit.Limits{}); err != nil {
		t.Fatalf("zero-value Limits rejected a 200-key object: %v", err)
	}
}

func TestDecode_StdlibDepthCeilingStillApplies(t *testing.T) {
	// Even with MaxDepth disabled, encoding/json's own hardcoded
	// maxNestingDepth (10000) still refuses first. jsonlimit does not
	// touch or claim to raise that ceiling.
	over := strings.Repeat("[", 10001) + "1" + strings.Repeat("]", 10001)
	_, err := jsonlimit.Decode(strings.NewReader(over), jsonlimit.Limits{})
	if err == nil {
		t.Fatal("10001 levels of nesting with MaxDepth disabled succeeded, want encoding/json's own depth error")
	}
	var le jsonlimit.LimitError
	if errors.As(err, &le) {
		t.Fatalf("error = %v, want encoding/json's own error, not a jsonlimit.LimitError", err)
	}
}

func TestDecode_DecoderNeverRecursesWithInputDepth(t *testing.T) {
	// Decode builds containers on an explicit stack. This is a smoke test,
	// not a stack-depth measurement: it just confirms a depth close to
	// encoding/json's own ceiling decodes correctly and without a Go
	// runtime stack-overflow fatal error, at a MaxDepth generous enough to
	// admit it.
	const depth = 9999
	in := strings.Repeat("[", depth) + "1" + strings.Repeat("]", depth)
	v, err := jsonlimit.Decode(strings.NewReader(in), jsonlimit.Limits{MaxDepth: depth})
	if err != nil {
		t.Fatalf("Decode at depth %d: %v", depth, err)
	}
	got := 0
	for arr, ok := v.([]any); ok; arr, ok = v.([]any) {
		got++
		if len(arr) == 0 {
			break
		}
		v = arr[0]
	}
	if got != depth {
		t.Fatalf("measured depth %d, want %d", got, depth)
	}
}
