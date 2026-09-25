# retry-lint

A static linter for Go that looks specifically at retry loops. It parses
source files with `go/parser` and flags shapes that look fine in a code
review but cause real incidents in production:

- **Unbounded retries.** A `for { ... }` loop that sleeps between attempts
  but never checks anything against a limit. One dependency going down
  turns into a goroutine retrying forever.
- **Fixed delays.** A `time.Sleep` call whose duration is a constant. Every
  client that hit the same failure retries on the same clock, so they all
  come back at once and take the dependency down again the moment it
  recovers.
- **Retrying a non-retryable error.** A branch that recognizes a canceled
  context or a 4xx response but doesn't return or break, so execution
  falls through into the retry logic anyway. The request will fail the
  same way on every attempt; retrying just burns the time budget.

It does not type-check your code or resolve imports, so it never needs your
module's dependencies to run — it only reads syntax.

All three checks also apply to a function that retries by calling itself
again instead of using a `for` loop:

```go
func fetchWithRetry(url string, attempt int) (*http.Response, error) {
	resp, err := http.Get(url)
	if err != nil {
		time.Sleep(2 * time.Second)
		return fetchWithRetry(url, attempt+1)
	}
	return resp, nil
}
```

is reported the same way a `for` loop with the same shape would be, since
the recursive call is standing in for the loop.

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

- Retries implemented through anything other than a direct, named
  self-call. Mutual recursion (`a` calls `b`, `b` calls `a`), a retry
  dispatched through a passed-in callback, or a call through a method
  receiver (`c.fetchWithRetry()`) don't look like recursion to a check
  that only compares call names against the enclosing function's name.
- A non-retryable branch that recurses to continue retrying instead of
  falling through (`if canceled { return fetchWithRetry(...) }`) reads as
  an exit, the same as `return err` would, so it isn't flagged. Only the
  fall-through shape is caught.
- Attempt limits expressed as `attempts == max` rather than an ordering
  comparison (`<`, `<=`, `>`, `>=`). Equality is intentionally excluded
  from the bound check, because `err == nil` and `err != nil` are the most
  common comparisons inside a retry loop and would otherwise make almost
  every unbounded loop look bounded.
- Non-retryable errors that aren't a direct `== context.Canceled` /
  `== context.DeadlineExceeded` comparison, an `errors.Is` call against
  one of those, or an equality check against a `StatusCode` field. A
  wrapped error compared with `errors.Is` against something else, or a
  status range check like `code >= 400 && code < 500`, isn't recognized.
  429 is deliberately treated as retryable, not flagged, since it's the
  one 4xx status meant to be retried.

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
