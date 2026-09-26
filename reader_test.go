package jsonlimit_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/__GH_OWNER__/jsonlimit"
)

func TestReader_UnderLimitReadsEverything(t *testing.T) {
	data := []byte("hello, world")
	r := jsonlimit.NewReader(bytes.NewReader(data), int64(len(data)))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("ReadAll = %q, want %q", got, data)
	}
}

func TestReader_ExactlyAtLimitSucceeds(t *testing.T) {
	data := []byte("0123456789")
	r := jsonlimit.NewReader(bytes.NewReader(data), int64(len(data)))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("input exactly at the limit: ReadAll = %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("ReadAll = %q, want %q", got, data)
	}
}

func TestReader_OneByteOverLimitFails(t *testing.T) {
	data := []byte("0123456789X") // 11 bytes, limit 10
	r := jsonlimit.NewReader(bytes.NewReader(data), 10)
	_, err := io.ReadAll(r)
	if err == nil {
		t.Fatal("11 bytes through a 10-byte Reader succeeded, want an error")
	}
	var be *jsonlimit.BytesLimitError
	if !errors.As(err, &be) {
		t.Fatalf("error = %v (%T), want *BytesLimitError", err, err)
	}
	if be.Limit() != 10 {
		t.Errorf("Limit() = %d, want 10", be.Limit())
	}
	if be.Observed() != 10 {
		t.Errorf("Observed() = %d, want 10", be.Observed())
	}
	if be.Offset() != -1 {
		t.Errorf("Offset() = %d, want -1 (no document offset for an input-side error)", be.Offset())
	}
}

func TestReader_ErrorIsSticky(t *testing.T) {
	data := []byte(strings.Repeat("x", 100))
	r := jsonlimit.NewReader(bytes.NewReader(data), 10)
	buf := make([]byte, 4)
	var lastErr error
	for i := 0; i < 5; i++ {
		_, lastErr = r.Read(buf)
		if lastErr != nil {
			break
		}
	}
	if lastErr == nil {
		t.Fatal("expected an error within 5 reads of 4 bytes against a 10-byte limit")
	}
	// Once tripped, every subsequent call must return the same error
	// without touching the underlying reader again.
	for i := 0; i < 3; i++ {
		if _, err := r.Read(buf); !errors.Is(err, lastErr) {
			t.Fatalf("Read after limit error = %v, want the same sticky %v", err, lastErr)
		}
	}
}

func TestReader_ZeroOrNegativeDisablesLimit(t *testing.T) {
	data := []byte(strings.Repeat("y", 10_000))
	for _, max := range []int64{0, -1} {
		r := jsonlimit.NewReader(bytes.NewReader(data), max)
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("max=%d: ReadAll = %v", max, err)
		}
		if len(got) != len(data) {
			t.Fatalf("max=%d: read %d bytes, want %d", max, len(got), len(data))
		}
	}
}

func TestReader_StopsDecodeBeforeAGiantStringIsFullyBuffered(t *testing.T) {
	// The scenario the README's benchmarks measure: a single string value
	// far larger than any reasonable request body. Decode's own
	// MaxStringLength can only reject it after encoding/json's tokenizer
	// has already read and unescaped the whole thing (documented on
	// StringLengthError.Observed). Reader, wrapped around the input
	// instead, must refuse mid-stream, long before that.
	const bodyLimit = 1 << 10 // 1 KiB
	giant := `"` + strings.Repeat("z", 10*bodyLimit) + `"`

	r := jsonlimit.NewReader(strings.NewReader(giant), bodyLimit)
	_, err := jsonlimit.Decode(r, jsonlimit.DefaultLimits())
	if err == nil {
		t.Fatal("decoding a 10x-over-limit body through a capped Reader succeeded, want an error")
	}
	var be *jsonlimit.BytesLimitError
	if !errors.As(err, &be) {
		t.Fatalf("error = %v (%T), want it to wrap *BytesLimitError from the Reader", err, err)
	}
}
