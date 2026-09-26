package jsonlimit

import "io"

// Reader wraps an io.Reader and refuses to deliver more than max bytes in
// total, returning a *BytesLimitError instead of silently truncating.
//
// This bounds the input side, independently of and before anything in
// Limits: a request body can be capped by Reader before a single byte
// reaches Decode, the same way Limits.MaxDecodedSize bounds the decoded
// value built from whatever bytes did get through. The two are not
// redundant. A single giant string, for instance, must be fully read and
// unescaped before Decode's own Limits.MaxStringLength check can see its
// length (encoding/json's tokenizer hands over the whole decoded string in
// one call) — but Reader stops that same input mid-stream, at the byte
// level, well before it is fully buffered. See the README's benchmarks for
// the size of that difference.
//
// Reader is modeled on net/http.MaxBytesReader's read-ahead trick — it asks
// the underlying reader for one byte more than the remaining budget, so an
// input that lands exactly on the limit is not mistaken for one that
// overflows it — but has no dependency on net/http and returns jsonlimit's
// own error type.
type Reader struct {
	r      io.Reader
	limit  int64
	remain int64
	err    error
}

// NewReader returns a Reader over r that refuses to deliver more than max
// bytes. A non-positive max disables the limit; Read then simply delegates
// to r.
func NewReader(r io.Reader, max int64) *Reader {
	if max < 0 {
		max = 0
	}
	return &Reader{r: r, limit: max, remain: max}
}

// Read implements io.Reader. Once the configured byte budget has been
// exhausted, Read returns a *BytesLimitError (wrapped in the sense of
// errors.As, though there is nothing further to unwrap) on every
// subsequent call; the underlying reader is never consulted again.
func (l *Reader) Read(p []byte) (int, error) {
	if l.err != nil {
		return 0, l.err
	}
	if l.limit <= 0 {
		return l.r.Read(p)
	}
	if len(p) == 0 {
		return 0, nil
	}
	// Ask for at most one byte more than the remaining budget: enough to
	// tell whether the input stops exactly at the limit (fine) or keeps
	// going past it (not fine), without over-reading arbitrarily far
	// beyond the cap we exist to enforce.
	if int64(len(p))-1 > l.remain {
		p = p[:l.remain+1]
	}
	n, err := l.r.Read(p)
	if int64(n) <= l.remain {
		l.remain -= int64(n)
		l.err = err
		return n, err
	}
	n = int(l.remain)
	l.remain = 0
	l.err = &BytesLimitError{limit: l.limit, observed: l.limit}
	return n, l.err
}
