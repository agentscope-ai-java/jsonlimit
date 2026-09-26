package jsonlimit_test

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/__GH_OWNER__/jsonlimit"
)

// deepNesting returns depth levels of array nesting: well under
// encoding/json's own hardcoded maxNestingDepth of 10000, so
// json.Unmarshal accepts it without complaint and this measures jsonlimit
// against the case encoding/json itself does not consider pathological.
func deepNesting(depth int) []byte {
	var b strings.Builder
	b.Grow(depth*2 + 1)
	for i := 0; i < depth; i++ {
		b.WriteByte('[')
	}
	b.WriteByte('1')
	for i := 0; i < depth; i++ {
		b.WriteByte(']')
	}
	return []byte(b.String())
}

// keyExplosion returns a single JSON object with n short keys.
func keyExplosion(n int) []byte {
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"k`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`":1`)
	}
	b.WriteByte('}')
	return []byte(b.String())
}

// giantString returns a JSON document that is a single string value n
// bytes long (excluding the surrounding quotes).
func giantString(n int) []byte {
	var b strings.Builder
	b.Grow(n + 2)
	b.WriteByte('"')
	b.WriteString(strings.Repeat("a", n))
	b.WriteByte('"')
	return []byte(b.String())
}

func BenchmarkDeepNesting_StdlibUnmarshal(b *testing.B) {
	data := deepNesting(5000)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDeepNesting_Jsonlimit(b *testing.B) {
	data := deepNesting(5000)
	lim := jsonlimit.Limits{MaxDepth: 32}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		_, err := jsonlimit.Decode(bytes.NewReader(data), lim)
		if err == nil {
			b.Fatal("expected a DepthLimitError, got nil")
		}
	}
}

func BenchmarkKeyExplosion_StdlibUnmarshal(b *testing.B) {
	data := keyExplosion(200_000)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKeyExplosion_Jsonlimit(b *testing.B) {
	data := keyExplosion(200_000)
	lim := jsonlimit.Limits{MaxKeys: 10_000}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		_, err := jsonlimit.Decode(bytes.NewReader(data), lim)
		if err == nil {
			b.Fatal("expected a KeyLimitError, got nil")
		}
	}
}

func BenchmarkGiantString_StdlibUnmarshal(b *testing.B) {
	data := giantString(50 << 20) // 50 MiB
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGiantString_JsonlimitDecode enforces MaxStringLength through
// Decode alone. Per StringLengthError's documented limitation, the whole
// string must already be read and unescaped by encoding/json's tokenizer
// before Decode ever sees its length, so this is expected to cost roughly
// what stdlib Unmarshal costs -- see the README for why, and for the
// Reader-based benchmark below that actually avoids the cost.
func BenchmarkGiantString_JsonlimitDecode(b *testing.B) {
	data := giantString(50 << 20)
	lim := jsonlimit.Limits{MaxStringLength: 1 << 20}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		_, err := jsonlimit.Decode(bytes.NewReader(data), lim)
		if err == nil {
			b.Fatal("expected a StringLengthError, got nil")
		}
	}
}

// BenchmarkGiantString_JsonlimitReader wraps the input in Reader, capping
// raw bytes read before the decoder ever sees them, instead of relying on
// Decode's own MaxStringLength. This is the fast path for this case.
func BenchmarkGiantString_JsonlimitReader(b *testing.B) {
	data := giantString(50 << 20)
	const capBytes = 1 << 20
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		r := jsonlimit.NewReader(bytes.NewReader(data), capBytes)
		_, err := jsonlimit.Decode(r, jsonlimit.Limits{MaxStringLength: capBytes})
		if err == nil {
			b.Fatal("expected an error, got nil")
		}
	}
}

// BenchmarkGiantString_JsonlimitReaderOnly measures Reader in isolation
// (io.Copy to io.Discard, no JSON involved), to separate the Reader's own
// cost from Decode's.
func BenchmarkGiantString_JsonlimitReaderOnly(b *testing.B) {
	data := giantString(50 << 20)
	const capBytes = 1 << 20
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		r := jsonlimit.NewReader(bytes.NewReader(data), capBytes)
		if _, err := io.Copy(io.Discard, r); err == nil {
			b.Fatal("expected a BytesLimitError, got nil")
		}
	}
}

// BenchmarkOrdinary measures the overhead Decode adds on a document with
// nothing wrong with it: the price paid on every request, not just the
// pathological ones above.
func BenchmarkOrdinary_StdlibUnmarshal(b *testing.B) {
	data := ordinaryDocument()
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOrdinary_Jsonlimit(b *testing.B) {
	data := ordinaryDocument()
	lim := jsonlimit.DefaultLimits()
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		if _, err := jsonlimit.Decode(bytes.NewReader(data), lim); err != nil {
			b.Fatal(err)
		}
	}
}

// ordinaryDocument returns a document with shallow nesting and a few tens
// of thousands of keys, modeled on the sibling packages' overhead
// benchmarks: nothing pathological, just a realistic mid-sized payload.
func ordinaryDocument() []byte {
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < 4_000; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"item`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`":{"id":`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`,"name":"widget-`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`","active":true,"tags":["a","b","c"]}`)
	}
	b.WriteByte('}')
	return []byte(b.String())
}
