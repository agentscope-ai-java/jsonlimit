// Package jsonlimit adds hard, configurable resource limits to JSON decoding:
// nesting depth, object key count, string length, and total decoded size. It
// also provides a byte-capping io.Reader for the input side.
//
// encoding/json's own scanner already hardcodes a maximum nesting depth of
// 10000 (see the "Why encoding/json is already partly safe" section of the
// README), so this package does not exist to prevent a stack overflow. It
// exists because nothing in the standard library bounds the number of keys
// in an object, the length of a single string, or how much memory a decoded
// value ends up occupying — and encoding/json/v2's jsontext package, new in
// Go 1.27, does not add any of these either; see the README for exactly
// where that was checked.
package jsonlimit

// Limits configures the checks Decode performs while it walks a JSON
// document. Every field is a hard ceiling: crossing it aborts the decode
// immediately with a typed error, before the offending value is fully
// built.
//
// The zero value, Limits{}, disables every check here and leaves Decode
// behaving like json.Unmarshal into an any (still subject to encoding/json's
// own hardcoded depth-10000 ceiling, which this package does not touch).
// Use DefaultLimits for a sensible non-zero starting point.
type Limits struct {
	// MaxDepth bounds nesting levels. A flat object or array is depth 1.
	// Zero or negative disables the check.
	MaxDepth int

	// MaxKeys bounds the number of object members across the whole
	// document, not per object. A repeated key such as {"a":1,"a":2}
	// counts twice, matching what the decoder actually pays for. Zero or
	// negative disables the check.
	MaxKeys int

	// MaxStringLength bounds the length, in bytes of the decoded (already
	// unescaped) string, of any single string — object keys included.
	// Zero or negative disables the check.
	MaxStringLength int

	// MaxDecodedSize bounds an estimate of the memory footprint of the
	// value being built: the byte length of every string encountered,
	// plus a fixed per-node overhead for every object, array, number, and
	// boolean. See the README for the exact accounting; it is a
	// deliberately conservative proxy, not a call to unsafe.Sizeof or
	// runtime memory-profiling. Zero or negative disables the check.
	MaxDecodedSize int64
}

// Fixed overhead, in bytes, charged against MaxDecodedSize for each JSON
// node that is not a string. These are rough proxies for Go's own runtime
// representation (a map header and its buckets, a slice header and its
// backing array growth, an interface{} word pair for a float64 or bool) —
// see the README for why exact accounting is not attempted.
const (
	containerOverhead = 48 // map[string]any or []any, freshly allocated
	scalarOverhead    = 16 // float64, bool, or nil, boxed in an any
)

// DefaultLimits returns conservative, non-zero limits suitable for
// untrusted input: 64 levels of nesting, 100,000 object keys, 1 MiB per
// string, and an 8 MiB decoded-size budget. Callers with tighter or looser
// requirements should build their own Limits rather than mutate this one.
func DefaultLimits() Limits {
	return Limits{
		MaxDepth:        64,
		MaxKeys:         100_000,
		MaxStringLength: 1 << 20,
		MaxDecodedSize:  8 << 20,
	}
}
