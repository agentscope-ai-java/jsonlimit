package jsonlimit

import "fmt"

// LimitError is implemented by every error jsonlimit returns when a
// configured limit is exceeded (as opposed to a plain JSON syntax error,
// which is returned unwrapped from the underlying encoding/json decoder).
//
// Offset reports the byte offset into the input at which the violation was
// observed, or -1 when no offset is meaningful — currently only
// *BytesLimitError, raised by a Reader before any JSON has been parsed.
type LimitError interface {
	error

	// Limit is the configured ceiling that was crossed.
	Limit() int64

	// Observed is the value that crossed it. For MaxDepth and MaxKeys,
	// this is always Limit()+1: enforcement stops at the first offending
	// unit of depth or key rather than measuring how much further the
	// document goes. For MaxStringLength and MaxDecodedSize, Observed is
	// the true observed value, because both are only knowable after the
	// fact — see the README for why.
	Observed() int64

	// Offset is the byte offset into the input where the violation was
	// found, or -1 if none applies.
	Offset() int64
}

// DepthLimitError reports that a JSON document nested arrays or objects
// deeper than Limits.MaxDepth allows.
type DepthLimitError struct {
	limit, observed, offset int64
}

func (e *DepthLimitError) Error() string {
	return fmt.Sprintf("jsonlimit: nesting depth %d exceeded max depth %d at offset %d", e.observed, e.limit, e.offset)
}

// Limit returns the configured Limits.MaxDepth.
func (e *DepthLimitError) Limit() int64 { return e.limit }

// Observed returns Limit()+1: the decoder refuses at the first level past
// the limit rather than measuring the document's actual depth.
func (e *DepthLimitError) Observed() int64 { return e.observed }

// Offset returns the byte offset of the bracket that crossed the limit.
func (e *DepthLimitError) Offset() int64 { return e.offset }

// KeyLimitError reports that a JSON document contained more object members,
// counted across the whole document, than Limits.MaxKeys allows.
type KeyLimitError struct {
	limit, observed, offset int64
}

func (e *KeyLimitError) Error() string {
	return fmt.Sprintf("jsonlimit: key count %d exceeded max keys %d at offset %d", e.observed, e.limit, e.offset)
}

// Limit returns the configured Limits.MaxKeys.
func (e *KeyLimitError) Limit() int64 { return e.limit }

// Observed returns Limit()+1: the decoder refuses at the first key past the
// limit rather than counting every key in the document.
func (e *KeyLimitError) Observed() int64 { return e.observed }

// Offset returns the byte offset of the key that crossed the limit.
func (e *KeyLimitError) Offset() int64 { return e.offset }

// StringLengthError reports that a single JSON string — a key or a value —
// was longer than Limits.MaxStringLength allows.
type StringLengthError struct {
	limit, observed, offset int64
}

func (e *StringLengthError) Error() string {
	return fmt.Sprintf("jsonlimit: string length %d exceeded max string length %d at offset %d", e.observed, e.limit, e.offset)
}

// Limit returns the configured Limits.MaxStringLength.
func (e *StringLengthError) Limit() int64 { return e.limit }

// Observed returns the string's true decoded length. Unlike depth and key
// limits, this cannot be capped to Limit()+1: encoding/json's tokenizer
// hands the whole decoded string to Decode in one Token call, so by the
// time the length is known, the string has already been fully read and
// unescaped. See the README for how to bound this earlier, at the byte
// level, with Reader.
func (e *StringLengthError) Observed() int64 { return e.observed }

// Offset returns the byte offset immediately after the string that crossed
// the limit.
func (e *StringLengthError) Offset() int64 { return e.offset }

// DecodedSizeError reports that the running estimate of the decoded value's
// memory footprint exceeded Limits.MaxDecodedSize. See Limits.MaxDecodedSize
// for how that estimate is computed.
type DecodedSizeError struct {
	limit, observed, offset int64
}

func (e *DecodedSizeError) Error() string {
	return fmt.Sprintf("jsonlimit: decoded size %d exceeded max decoded size %d at offset %d", e.observed, e.limit, e.offset)
}

// Limit returns the configured Limits.MaxDecodedSize.
func (e *DecodedSizeError) Limit() int64 { return e.limit }

// Observed returns the running size estimate at the point it crossed the
// limit — the true observed value, not a fixed limit+1, since size grows
// unevenly per token.
func (e *DecodedSizeError) Observed() int64 { return e.observed }

// Offset returns the byte offset of the token whose accounting crossed the
// limit.
func (e *DecodedSizeError) Offset() int64 { return e.offset }

// BytesLimitError reports that a Reader was asked to deliver more bytes
// than its configured maximum.
type BytesLimitError struct {
	limit, observed int64
}

func (e *BytesLimitError) Error() string {
	return fmt.Sprintf("jsonlimit: input exceeded max bytes %d", e.limit)
}

// Limit returns the byte cap the Reader was constructed with.
func (e *BytesLimitError) Limit() int64 { return e.limit }

// Observed returns the number of bytes the Reader had already delivered
// when the next read would have crossed the limit. It is always equal to
// Limit(): the Reader detects the overage by requesting one byte more than
// the remaining budget, and reports the budget itself as what was observed,
// not the extra byte.
func (e *BytesLimitError) Observed() int64 { return e.observed }

// Offset always returns -1: a BytesLimitError is raised on the raw byte
// stream, before any JSON has been parsed, so there is no document offset
// to report.
func (e *BytesLimitError) Offset() int64 { return -1 }

var (
	_ LimitError = (*DepthLimitError)(nil)
	_ LimitError = (*KeyLimitError)(nil)
	_ LimitError = (*StringLengthError)(nil)
	_ LimitError = (*DecodedSizeError)(nil)
	_ LimitError = (*BytesLimitError)(nil)
)
