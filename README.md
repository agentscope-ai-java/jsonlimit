# jsonlimit

`encoding/json` decoding with hard limits on object key count, string length,
and decoded size — enforced token by token, as the document is read, not
after it has already been built.

```
$ go test -run '^$' -bench . -benchmem

deep nesting — 5,000 levels of [[[...]]] (encoding/json accepts this fine; its own ceiling is 10,000)
  encoding/json      970µs   449 KB   15,019 allocs   parsed
  jsonlimit            3µs     5 KB       47 allocs   refused — nesting depth 33 exceeded max depth 32 at offset 33

key explosion — 200,000 keys in one object
  encoding/json     80.6ms  26.4 MB  607,716 allocs   parsed
  jsonlimit          2.3ms   2.3 MB   46,762 allocs   refused — key count 10001 exceeded max keys 10000 at offset 98899

giant string — one 50 MB string value
  encoding/json     36.3ms  62.9 MB        6 allocs   parsed
  jsonlimit (Decode only)         44.5ms 197.1 MB   27 allocs   refused — but slower and heavier than parsing it (see below)
  jsonlimit (+ Reader byte cap)    0.9ms  14.7 MB   27 allocs   refused — before the string is even fully read

ordinary document — ~4,000 objects, nothing wrong with it
  encoding/json     7.57ms   3.57 MB   96,515 allocs   parsed
  jsonlimit         5.46ms   4.21 MB  116,455 allocs   parsed — 1.3-1.5x *faster*, not slower
```

All four rows are from `go test -bench . -benchmem`, Go 1.27.1, Apple M5 Pro.
The full commands and raw output are in [Benchmarks](#benchmarks). Read the
third row carefully — it is the one case where this package, used the
obvious way, is a net loss, and that is reported honestly below rather than
cut from the table.

## The problem, and where it doesn't apply

`encoding/json` has no limit on the number of keys in an object, the length
of a single string, or how much memory a decoded value ends up occupying.
Every byte-size limit an HTTP server puts on a request body is a limit on
*input* bytes, and input bytes are a poor proxy for what decoding costs:

| A request body can contain | `json.Unmarshal` result |
| --- | --- |
| 200,000 keys in one object | one `map[string]any`, 200,000 entries deep, built in full |
| one 50 MB string | decoded in full, and held in memory as a Go `string` |
| 5,000 levels of `[[[...]]]` nesting | decoded in full — see below for why this one is less interesting than it looks |

### encoding/json is already partly safe — verify this before reading further

Unlike the classic "attacker sends a million levels of nesting and your
parser recurses off the end of the stack" bug, **`encoding/json`'s own
scanner already refuses excessive nesting**, unconditionally:

```go
// This limits the max nesting depth to prevent stack overflow.
// This is permitted by https://tools.ietf.org/html/rfc7159#section-9
const maxNestingDepth = 10000
```

That's `$GOROOT/src/encoding/json/scanner.go:148`, checked directly against
Go 1.27.1 on this machine. It is not configurable, and it needs no library
to benefit from — `json.Unmarshal([]byte(strings.Repeat("[", 10001)+...), &v)`
fails on its own, with a plain `*json.SyntaxError`, at exactly 10,001
levels:

```
$ go doc -all encoding/json | grep -i depth   # nothing — it isn't exposed as an option
```

`encoding/json/v2`'s tokenizer (`encoding/json/jsontext`, new in Go 1.27 —
see the next section) hardcodes the identical constant at
`jsontext/state.go:53`, so this isn't a v1-only accident that a rewrite
fixed; it's a considered, permanent ceiling in both encoders. **This
package does not raise, lower, or otherwise touch that ceiling by default**
(`Limits.MaxDepth`, when set, enforces a *stricter* bound underneath it —
see [Options](#options)). If your only concern is a crash from pathological
nesting, `encoding/json` already has you covered, for both `v1` and `v2`,
and no third-party code changes that.

### encoding/json/v2 does not close the remaining gap either — verify this too

Go 1.27 is the first release where `encoding/json/v2` and its tokenizer,
`encoding/json/jsontext`, ship as real, usable standard-library packages —
confirmed on this machine by building and running a program that imports
`encoding/json/v2` with no `GOEXPERIMENT` set at all:

```
$ grep -n "JSONv2" "$(go env GOROOT)/src/internal/buildcfg/exp.go"
		JSONv2:                                       true,
```

`JSONv2: true` is the *default* baseline for the Go 1.27 toolchain, not an
opt-in experiment you have to enable. It is real and it is on.

It still does not add a key-count limit, a string-length limit, or a
decoded-size limit — checked directly by reading
`encoding/json/jsontext/options.go` rather than assuming. The two options
that sound like they might be exactly this are in the file, but commented
out:

```go
/*
// TODO(https://go.dev/issue/56733): Implement WithByteLimit and WithDepthLimit.
// Remember to also update the "Security Considerations" section.

// WithByteLimit sets a limit on the number of bytes of input or output bytes
// that may be consumed or produced for each top-level JSON value.
...
func WithByteLimit(n int64) Options { ... }

// WithDepthLimit sets a limit on the maximum depth of JSON nesting
...
func WithDepthLimit(n int) Options { ... }
*/
```

That's `jsontext/options.go:272-304`. Both functions are inside a `/* */`
block — dead source, not compiled, gated behind an open, unresolved issue
(go.dev/issue/56733). Even if they shipped as written, they would only
cover bytes and depth, the two limits this package cares about least: depth
because §1 already handles it, and a total byte count because that's
already easy to enforce yourself with an `io.LimitReader` (or this
package's `Reader`, which does the same thing with a typed error). Nothing
in `v2`, shipped or drafted, bounds key count or string length — the two
limits that matter most and are hardest to retrofit around `Unmarshal`.

**This is the actual gap this package fills**: `Limits.MaxKeys` and
`Limits.MaxStringLength`, neither of which `encoding/json` or
`encoding/json/v2` offers in Go 1.27, plus `Limits.MaxDepth` and
`Limits.MaxDecodedSize` as defense-in-depth below stdlib's own ceiling.

## Install

```sh
go get github.com/__GH_OWNER__/jsonlimit
```

Go 1.27+. No runtime dependencies.

## Use

```go
import "github.com/__GH_OWNER__/jsonlimit"

value, err := jsonlimit.Decode(r, jsonlimit.DefaultLimits())
if err != nil {
    var le jsonlimit.LimitError
    if errors.As(err, &le) {
        // le.Limit(), le.Observed(), le.Offset() — a 413, not a 400
        return
    }
    // plain malformed JSON — a 400
    return
}
```

`Decode` takes an `io.Reader` and returns exactly what `json.Unmarshal`
would return into a `var v any`: `nil`, `bool`, `float64`, `string`,
`[]any`, or `map[string]any`. A limit violation comes back as one of
`*DepthLimitError`, `*KeyLimitError`, `*StringLengthError`, or
`*DecodedSizeError` — all implement `LimitError`. A syntax error comes back
as whatever `encoding/json` itself would return, unwrapped, so an existing
`errors.As(err, &syntaxErr)` keeps working unmodified.

Cap the input independently of anything `Decode` does, before it ever
reaches the decoder:

```go
body := jsonlimit.NewReader(r.Body, 5<<20) // 5 MiB, hard cap on the wire
value, err := jsonlimit.Decode(body, jsonlimit.DefaultLimits())
```

See [What it does not do](#what-it-does-not-do) for why both matter and
neither alone is enough for the giant-string case.

## Design: enforcing limits during the token walk, not before or after it

The other two languages in this series (a Node and a Python sibling of
this package, `json-guard` and `jsonguard`) both work the same way: one
linear pass over the raw text, counting brackets and string boundaries,
*before* the real parser ever runs. They have to — `JSON.parse` and
`json.loads` are opaque, single-shot calls with no way to observe or
interrupt them mid-parse. That pre-scan is a second full pass over the
input, and it shows up as their main measured cost: roughly 2x in the Node
sibling, 12-18x in the Python one.

Go doesn't need a pre-scan, because `encoding/json.Decoder` exposes
`Token()`: a method that returns exactly one JSON token — a delimiter, a
string, a number, a bool, or `nil` — per call, advancing through the input
lazily. `Decode` calls `Token()` in a loop and builds the same
`map[string]any` / `[]any` tree `json.Unmarshal` would, but it checks every
limit *as each token arrives*, in the same pass that builds the value,
instead of scanning first and parsing second:

- On every `{` or `[`, before pushing a new container onto the stack:
  compare the new depth to `MaxDepth` and refuse before that container is
  ever allocated.
- On every string that lands in key position: count it against `MaxKeys`
  and refuse before its value is read at all.
- On every string, key or value: compare its decoded length to
  `MaxStringLength`.
- After every token: add its estimated footprint to a running total and
  compare it to `MaxDecodedSize`.

Because the walk is driven by an explicit `[]*container` stack rather than
a recursive Go function calling itself once per nesting level, `Decode`'s
own call stack never grows with the input's depth — independent of
whatever `MaxDepth` is configured to allow. A naive alternative
implementation (recurse into each nested value with an ordinary Go
function) would tie Decode's safety to Go's own goroutine stack growth
limit instead of to `MaxDepth`, which is exactly the kind of
implementation detail this package exists to not depend on.

The result: on ordinary, well-formed input, this package is not paying for
a second pass the way its siblings are, and the benchmarks below show that
directly — `Decode` measures *faster* than `json.Unmarshal` on an ordinary
document, not merely "less slow than expected." The one place this
single-pass design still doesn't come for free is a single very large
string, covered honestly in the next section.

## Options

`Limits`, passed to `Decode`:

| Field | Type | What it bounds |
| --- | --- | --- |
| `MaxDepth` | `int` | Nesting levels. A flat object or array is 1. |
| `MaxKeys` | `int` | Object members across the whole document, not per object. |
| `MaxStringLength` | `int` | Any single string's decoded length in bytes, keys included. |
| `MaxDecodedSize` | `int64` | An estimate of the decoded value's memory footprint — see below. |

`DefaultLimits()` returns `{MaxDepth: 64, MaxKeys: 100_000,
MaxStringLength: 1<<20, MaxDecodedSize: 8<<20}`. The zero value, `Limits{}`,
disables every check here (still subject to `encoding/json`'s own
depth-10000 ceiling, which this package never touches). Zero or negative on
any individual field disables that one check.

`MaxKeys` counts syntactically: `{"a":1,"a":2}` is 2 keys even though the
decoded map has one entry, matching what the decoder actually pays for.

`MaxDecodedSize` is a fixed-overhead estimate, not `unsafe.Sizeof` or a
runtime memory profile: every string's decoded byte length, plus 48 bytes
for every object or array (`map[string]any` header and initial buckets, or
a `[]any` header) and 16 bytes for every number, bool, or `null` (an `any`
holding one). It's deliberately conservative and documented, not exact —
Go's actual per-entry map and slice overhead varies with load factor and
growth history in ways not worth chasing for a resource-abuse guard.

`Reader`, wrapping an `io.Reader`:

| Constructor | What it bounds |
| --- | --- |
| `NewReader(r, max)` | Total bytes `Read` will ever deliver. Non-positive `max` disables it. |

Modeled on `net/http.MaxBytesReader`'s read-ahead trick (request one byte
past the remaining budget, so input that lands exactly on the limit isn't
mistaken for input that overflows it), with no dependency on `net/http` and
its own error type.

### Errors

All implement `LimitError`, with `Limit()`, `Observed()`, and `Offset()`
(`int64` in each case; `Offset()` is `-1` when no input offset applies).

| Error | `Observed()` | Notes |
| --- | --- | --- |
| `DepthLimitError` | always `Limit()+1` | refuses at the first level past the limit |
| `KeyLimitError` | always `Limit()+1` | refuses at the first key past the limit |
| `StringLengthError` | the string's true length | see below for why this isn't `Limit()+1` |
| `DecodedSizeError` | the running total at the crossing | grows unevenly per token, unlike depth or keys |
| `BytesLimitError` | always `Limit()` | raised by `Reader`; `Offset()` is always `-1` |

```go
var le jsonlimit.LimitError
if errors.As(err, &le) {
    log.Printf("refused: %s (limit %d, observed %d, offset %d)",
        err, le.Limit(), le.Observed(), le.Offset())
}
```

## What it does not do

- **`StringLengthError.Observed()` is the string's real length, not
  `Limit()+1`, because it can't be anything else.** `Decoder.Token()` hands
  `Decode` the whole decoded string in a single call — by the time
  `Decode` can measure it, `encoding/json`'s own tokenizer has already read
  and unescaped every byte of it. Unlike depth and key checks, which refuse
  *before* the next unit of work happens, `MaxStringLength` on its own only
  refuses *after* that unit of work (reading and unescaping one string) has
  already happened. For a single enormous string, that's most of the cost.
  This is exactly the scenario `Reader` exists for: it caps raw bytes off
  the wire, before `Token()` ever gets far enough to buffer the string in
  the first place. See [Benchmarks](#benchmarks) — used alone, `Decode`'s
  `MaxStringLength` is measurably *worse* than plain `json.Unmarshal` on a
  50 MB string; paired with `Reader`, the same input is refused about 39x
  faster and with about a quarter of the memory.
- **It is not a schema validator.** It bounds shape, not meaning. Run your
  existing struct-level `Unmarshal` or a validator afterward; this exists
  to keep a hostile document from costing you anything before that.
- **It does not bound array length directly.** A million-element array of
  small numbers is bounded only by `MaxDecodedSize` (and, on the wire, by
  `Reader`). `MaxKeys` covers object members, where the per-entry cost is
  materially higher (a map entry vs. a slice append); for flat arrays,
  `MaxDecodedSize` is the relevant proxy.
- **It decodes into `any`, not into your own struct types.** `Decode`
  builds the same generic tree `json.Unmarshal(data, &v)` would for `var v
  any`. If you need `Unmarshal`-into-a-struct with these limits applied,
  decode into `any` first and convert, or reuse `Limits`' accounting logic
  directly — `Decode` doesn't (and, being a token walk rather than a
  reflection-driven `Unmarshal`, structurally can't easily) drive arbitrary
  struct field assignment the way `encoding/json.Unmarshal` does.
- **It reads exactly one JSON value and stops.** Trailing bytes after that
  value are never inspected; `Decode` doesn't error on them, and doesn't
  consume them either.
- **`MaxDecodedSize`'s accounting is an estimate**, not a measurement of
  actual Go heap bytes — see [Options](#options) for the exact formula.
  It's conservative and deterministic, not a promise about `runtime`
  statistics.

## Benchmarks

```sh
go test -run '^$' -bench . -benchmem -benchtime=30x -count=3
```

Go 1.27.1, `darwin/arm64`, Apple M5 Pro. Figures below are the mean of 3
runs at `-benchtime=30x` (`-benchtime=15x` for the 50 MB giant-string
cases, which are otherwise slow to repeat); raw `go test -bench` output,
unedited, is reproducible with the command above.

**Deep nesting**, 5,000 levels of `[[[...]]]` — comfortably under
`encoding/json`'s own 10,000 ceiling, so `Unmarshal` accepts it, and this
measures `jsonlimit`'s own `MaxDepth: 32` catching what stdlib doesn't:

| | time/op | bytes/op | allocs/op |
| --- | --- | --- | --- |
| `json.Unmarshal` | 970 µs | 449 KB | 15,019 |
| `jsonlimit.Decode` | 2.7 µs | 4.7 KB | 47 |
| | **~360x faster** | **~96x less** | **~320x fewer** |

**Key explosion**, 200,000 keys in one object, `MaxKeys: 10_000`:

| | time/op | bytes/op | allocs/op |
| --- | --- | --- | --- |
| `json.Unmarshal` | 80.6 ms | 26.4 MB | 607,716 |
| `jsonlimit.Decode` | 2.3 ms | 2.3 MB | 46,762 |
| | **~35x faster** | **~11.6x less** | **~13x fewer** |

**Giant string**, one 50 MB string value, `MaxStringLength: 1 MiB` — the
case worth reading carefully:

| | time/op | bytes/op | allocs/op |
| --- | --- | --- | --- |
| `json.Unmarshal` | 36.3 ms | 62.9 MB | 6 |
| `jsonlimit.Decode` alone | 44.5 ms | 197.1 MB | 27 |
| `jsonlimit.Decode` + `Reader(1 MiB)` | 0.9 ms | 14.7 MB | 27 |

`Decode`'s own `MaxStringLength`, used by itself, is **~1.2x slower and
~3.1x heavier** than just letting `json.Unmarshal` parse the whole thing —
worse than "undramatic," an outright regression, and reported here rather
than left out. The reason is in
[What it does not do](#what-it-does-not-do): `Decode` must fully read and
unescape the string before it can measure it, and does so through
`Decoder`'s general-purpose streaming machinery, which costs more per byte
than `Unmarshal`'s direct, single-shot decode of an in-memory `[]byte`.
Paired with `Reader` capping the same input to 1 MiB on the wire, the
picture reverses completely: **~39x faster, ~4.3x less memory**, because
the byte cap stops the read while the string is a fraction built, long
before `Decode`'s string check would ever get a chance to run.

**Ordinary document**, ~4,000 objects with nested fields and array values,
nothing pathological — the cost paid on every normal request:

| | time/op | bytes/op | allocs/op |
| --- | --- | --- | --- |
| `json.Unmarshal` | 7.57 ms | 3.57 MB | 96,515 |
| `jsonlimit.Decode` | 5.46 ms (range: 4.95-6.16 ms) | 4.21 MB | 116,455 |

`jsonlimit.Decode` measured **roughly 1.3-1.5x *faster*** than
`json.Unmarshal` here across three runs, while using about 18% more memory
and 21% more allocations. The likely reason: `Unmarshal` into `any` goes
through `encoding/json`'s general-purpose, reflection-driven decode state
machine, while `Decode`'s token walk builds `map[string]any` / `[]any`
directly with no reflection at all. That's a genuinely different
performance profile from the Node and Python siblings, both of which pay a
real, unavoidable tax (2x and 12-18x respectively) on ordinary input for
their pre-scan; nothing here is being pre-scanned twice.

## Develop

```sh
go build ./...
go vet ./...
go test -race ./...
gofmt -l .
go test -run '^$' -bench . -benchmem
```

## License

MIT
