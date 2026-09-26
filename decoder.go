package jsonlimit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Decode reads exactly one JSON value from r and returns it using the same
// representation json.Unmarshal produces for a target of type any: nil,
// bool, float64, string, []any, or map[string]any.
//
// Decode enforces lim while it reads, using json.Decoder's token-level
// Token method rather than Unmarshal. That means a document that violates
// a limit is rejected during the single pass that would otherwise have
// built it — not after, and not via a separate pre-scan over the raw
// bytes beforehand. See the README for why that ordering is the point of
// this package, and for exactly which of the four limits it does and does
// not let Decode refuse before the offending value is fully materialized.
//
// Decode also builds every nested object and array on an explicit stack
// rather than by recursive descent, so — independent of whatever
// Limits.MaxDepth is configured to — Decode's own call stack never grows
// with the input's nesting depth.
//
// A plain JSON syntax error from the underlying decoder is returned
// unwrapped. A limit violation is returned as one of *DepthLimitError,
// *KeyLimitError, *StringLengthError, or *DecodedSizeError; all satisfy
// LimitError, so callers can distinguish "malformed" from "refused" with a
// single type switch or errors.As(err, &jsonlimit.LimitError(nil)).
func Decode(r io.Reader, lim Limits) (any, error) {
	dec := json.NewDecoder(r)
	return decodeWith(dec, lim)
}

// container is one open object or array on the explicit stack Decode walks
// instead of recursing.
type container struct {
	isObject  bool
	obj       map[string]any
	arr       []any
	key       string // valid only while isObject && !expectKey
	expectKey bool   // valid only while isObject
}

func decodeWith(dec *json.Decoder, lim Limits) (any, error) {
	var (
		stack  []*container
		keys   int64
		size   int64
		result any
	)

	checkDepth := func() error {
		if lim.MaxDepth > 0 && int64(len(stack))+1 > int64(lim.MaxDepth) {
			return &DepthLimitError{
				limit:    int64(lim.MaxDepth),
				observed: int64(lim.MaxDepth) + 1,
				offset:   dec.InputOffset(),
			}
		}
		return nil
	}

	checkKeys := func() error {
		keys++
		if lim.MaxKeys > 0 && keys > int64(lim.MaxKeys) {
			return &KeyLimitError{
				limit:    int64(lim.MaxKeys),
				observed: int64(lim.MaxKeys) + 1,
				offset:   dec.InputOffset(),
			}
		}
		return nil
	}

	checkString := func(s string) error {
		if lim.MaxStringLength > 0 && len(s) > lim.MaxStringLength {
			return &StringLengthError{
				limit:    int64(lim.MaxStringLength),
				observed: int64(len(s)),
				offset:   dec.InputOffset(),
			}
		}
		return nil
	}

	addSize := func(n int64) error {
		size += n
		if lim.MaxDecodedSize > 0 && size > lim.MaxDecodedSize {
			return &DecodedSizeError{
				limit:    lim.MaxDecodedSize,
				observed: size,
				offset:   dec.InputOffset(),
			}
		}
		return nil
	}

	// attach places v as the value just completed: the top-level result if
	// the stack is empty, an appended array element, or the value for the
	// key most recently read from an object.
	attach := func(v any) {
		if len(stack) == 0 {
			result = v
			return
		}
		top := stack[len(stack)-1]
		if top.isObject {
			top.obj[top.key] = v
			top.expectKey = true
		} else {
			top.arr = append(top.arr, v)
		}
	}

	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Reached only if no top-level value was ever completed —
				// every success path above returns as soon as one is.
				return nil, fmt.Errorf("jsonlimit: %w", io.ErrUnexpectedEOF)
			}
			return nil, fmt.Errorf("jsonlimit: %w", err)
		}

		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{', '[':
				if err := checkDepth(); err != nil {
					return nil, err
				}
				c := &container{isObject: t == '{'}
				if c.isObject {
					c.obj = make(map[string]any)
					c.expectKey = true
				} else {
					c.arr = make([]any, 0)
				}
				stack = append(stack, c)
				if err := addSize(containerOverhead); err != nil {
					return nil, err
				}
			case '}', ']':
				n := len(stack)
				top := stack[n-1]
				stack = stack[:n-1]
				if top.isObject {
					attach(top.obj)
				} else {
					attach(top.arr)
				}
				if len(stack) == 0 {
					// The outermost container just closed: the document's
					// one top-level value is complete. Stop reading rather
					// than calling Token again, which would otherwise
					// start parsing a second top-level value (or a syntax
					// error on trailing garbage) that Decode has no
					// business consuming.
					return result, nil
				}
			}

		case string:
			var atKey bool
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				atKey = top.isObject && top.expectKey
			}
			if err := checkString(t); err != nil {
				return nil, err
			}
			if atKey {
				if err := checkKeys(); err != nil {
					return nil, err
				}
				if err := addSize(int64(len(t))); err != nil {
					return nil, err
				}
				top := stack[len(stack)-1]
				top.key = t
				top.expectKey = false
				continue
			}
			if err := addSize(int64(len(t))); err != nil {
				return nil, err
			}
			attach(t)
			if len(stack) == 0 {
				return result, nil // a bare top-level string
			}

		case float64:
			if err := addSize(scalarOverhead); err != nil {
				return nil, err
			}
			attach(t)
			if len(stack) == 0 {
				return result, nil // a bare top-level number
			}

		case bool:
			if err := addSize(scalarOverhead); err != nil {
				return nil, err
			}
			attach(t)
			if len(stack) == 0 {
				return result, nil // a bare top-level boolean
			}

		case nil:
			if err := addSize(scalarOverhead); err != nil {
				return nil, err
			}
			attach(nil)
			if len(stack) == 0 {
				return result, nil // a bare top-level null
			}
		}
	}
}
