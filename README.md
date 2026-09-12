# retry-lint

A static linter for Go that looks specifically at retry loops. It parses
source files with `go/parser` and flags two shapes that look fine in a code
review but cause real incidents in production:

- **Unbounded retries.** A `for { ... }` loop that sleeps between attempts
  but never checks anything against a limit. One dependency going down
  turns into a goroutine retrying forever.
- **Fixed delays.** A `time.Sleep` call whose duration is a constant. Every
  client that hit the same failure retries on the same clock, so they all
  come back at once and take the dependency down again the moment it
  recovers.

It does not type-check your code or resolve imports, so it never needs your
module's dependencies to run — it only reads syntax.

## Usage

```
go run ./cmd/retrylint ./...
```

or point it at specific files or directories:

```
go run ./cmd/retrylint internal/fetcher
```

Given this file:

```go
func fetchWithRetry(url string) (*http.Response, error) {
	for {
		resp, err := http.Get(url)
		if err == nil {
			return resp, nil
		}
		time.Sleep(2 * time.Second)
	}
}
```

retrylint reports:

```
fetcher.go:2:2: infinite retry loop has no attempt limit or bound check [unbounded-retry]
fetcher.go:7:3: retry delay is a fixed duration with no backoff or jitter [fixed-delay-no-jitter]
```

The exit code is 1 if any finding was reported, 0 otherwise, so it can be
dropped into a CI step the same way as `go vet`.

## What it does not catch (yet)

- Retries implemented through a helper function instead of a literal
  `for` loop with `time.Sleep` in it.
- Attempt limits expressed as `attempts == max` rather than an ordering
  comparison (`<`, `<=`, `>`, `>=`). Equality is intentionally excluded
  from the bound check, because `err == nil` and `err != nil` are the most
  common comparisons inside a retry loop and would otherwise make almost
  every unbounded loop look bounded.
- Retrying on errors that should never be retried, like a cancelled
  context or a 4xx response.

## Library use

The checks live in the root package and can be called directly:

```go
findings, err := retrylint.Analyze("fetcher.go", src)
```

`Finding` carries the rule name, a human-readable message, and the line and
column where it applies.

## Status

First working version. See the checks in `rules.go` and the table-driven
cases in `analyzer_test.go` for exactly what is and isn't flagged.

## License

MIT, see `LICENSE`.
