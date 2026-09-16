package retrylint

import (
	"reflect"
	"testing"
)

func TestAnalyze(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		want    []Finding
		wantErr bool
	}{
		{
			name: "bounded counter loop with a fixed sleep is not unbounded",
			src: `package sample

import "time"

func retry() {
	for attempts := 0; attempts < 5; attempts++ {
		time.Sleep(time.Second)
	}
}
`,
			want: []Finding{
				{Rule: "fixed-delay-no-jitter", Message: "retry delay is a fixed duration with no backoff or jitter", Line: 7, Column: 3},
			},
		},
		{
			name: "infinite loop with a fixed sleep and no attempt limit",
			src: `package sample

import "time"

func retry() error {
	for {
		if err := attempt(); err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
}

func attempt() error { return nil }
`,
			want: []Finding{
				{Rule: "unbounded-retry", Message: "infinite retry loop has no attempt limit or bound check", Line: 6, Column: 2},
				{Rule: "fixed-delay-no-jitter", Message: "retry delay is a fixed duration with no backoff or jitter", Line: 10, Column: 3},
			},
		},
		{
			name: "infinite loop with an ordering comparison is treated as bounded",
			src: `package sample

import "time"

func retry() error {
	attempts := 0
	for {
		attempts++
		if attempts > 5 {
			return errRetriesExhausted
		}
		time.Sleep(time.Second)
	}
}
`,
			want: []Finding{
				{Rule: "fixed-delay-no-jitter", Message: "retry delay is a fixed duration with no backoff or jitter", Line: 12, Column: 3},
			},
		},
		{
			name: "a counter equality check does not count as a bound, only ordering does",
			src: `package sample

import "time"

func retry() {
	attempts := 0
	for {
		attempts++
		if attempts == 5 {
			return
		}
		time.Sleep(time.Second)
	}
}
`,
			want: []Finding{
				{Rule: "unbounded-retry", Message: "infinite retry loop has no attempt limit or bound check", Line: 7, Column: 2},
				{Rule: "fixed-delay-no-jitter", Message: "retry delay is a fixed duration with no backoff or jitter", Line: 12, Column: 3},
			},
		},
		{
			name: "sleep duration computed from a backoff call is not fixed-delay",
			src: `package sample

import "time"

func retry() {
	for attempt := 0; attempt < 5; attempt++ {
		time.Sleep(backoff(attempt))
	}
}

func backoff(n int) time.Duration { return time.Duration(n) * time.Second }
`,
			want: nil,
		},
		{
			name: "sleep duration with jitter mixed into the expression is not fixed-delay",
			src: `package sample

import (
	"math/rand"
	"time"
)

func retry() {
	attempts := 0
	for {
		attempts++
		if attempts >= 10 {
			return
		}
		time.Sleep(time.Second + time.Duration(rand.Intn(500))*time.Millisecond)
	}
}
`,
			want: nil,
		},
		{
			name: "a sleep inside a nested bounded loop is not attributed to the outer loop",
			src: `package sample

import "time"

func retry() {
	for {
		for i := 0; i < 3; i++ {
			time.Sleep(time.Second)
		}
		return
	}
}
`,
			want: []Finding{
				{Rule: "fixed-delay-no-jitter", Message: "retry delay is a fixed duration with no backoff or jitter", Line: 8, Column: 4},
			},
		},
		{
			name: "multiple fixed sleeps in one loop are each reported",
			src: `package sample

import "time"

func retry() {
	for attempts := 0; attempts < 3; attempts++ {
		time.Sleep(time.Second)
		time.Sleep(2 * time.Second)
	}
}
`,
			want: []Finding{
				{Rule: "fixed-delay-no-jitter", Message: "retry delay is a fixed duration with no backoff or jitter", Line: 7, Column: 3},
				{Rule: "fixed-delay-no-jitter", Message: "retry delay is a fixed duration with no backoff or jitter", Line: 8, Column: 3},
			},
		},
		{
			name: "a range loop is not treated as a retry loop",
			src: `package sample

import "time"

func retry(delays []time.Duration) {
	for _, d := range delays {
		time.Sleep(d)
	}
}
`,
			want: nil,
		},
		{
			name: "a loop with no sleep at all is left alone",
			src: `package sample

func poll() {
	for {
		if ready() {
			return
		}
	}
}

func ready() bool { return true }
`,
			want: nil,
		},
		{
			name: "a canceled context falls through to the retry instead of returning",
			src: `package sample

import (
	"context"
	"time"
)

func retry(ctx context.Context) error {
	attempts := 0
	for {
		attempts++
		if attempts > 5 {
			return errRetriesExhausted
		}
		err := attempt(ctx)
		if err == context.Canceled {
			time.Sleep(backoff(attempts))
			continue
		}
		if err == nil {
			return nil
		}
		time.Sleep(backoff(attempts))
	}
}

func attempt(ctx context.Context) error { return nil }
func backoff(n int) time.Duration       { return time.Duration(n) * time.Second }
`,
			want: []Finding{
				{Rule: "retry-on-non-retryable-error", Message: "retries even when the context was canceled, which should not be retried", Line: 16, Column: 3},
			},
		},
		{
			name: "a canceled context that returns immediately is not flagged",
			src: `package sample

import (
	"context"
	"time"
)

func retry(ctx context.Context) error {
	for attempts := 0; attempts < 5; attempts++ {
		err := attempt(ctx)
		if err == context.Canceled {
			return err
		}
		if err == nil {
			return nil
		}
		time.Sleep(backoff(attempts))
	}
	return errRetriesExhausted
}

func attempt(ctx context.Context) error { return nil }
func backoff(n int) time.Duration       { return time.Duration(n) * time.Second }
`,
			want: nil,
		},
		{
			name: "errors.Is against a canceled context is recognized the same as direct comparison",
			src: `package sample

import (
	"context"
	"errors"
	"time"
)

func retry(ctx context.Context) error {
	for attempts := 0; attempts < 5; attempts++ {
		err := attempt(ctx)
		if errors.Is(err, context.Canceled) {
			time.Sleep(backoff(attempts))
			continue
		}
	}
	return nil
}

func attempt(ctx context.Context) error { return nil }
func backoff(n int) time.Duration       { return time.Duration(n) * time.Second }
`,
			want: []Finding{
				{Rule: "retry-on-non-retryable-error", Message: "retries even when the context was canceled, which should not be retried", Line: 12, Column: 3},
			},
		},
		{
			name: "a 404 response falls through to the retry instead of giving up",
			src: `package sample

import (
	"net/http"
	"time"
)

func retry(url string) (*http.Response, error) {
	for attempts := 0; attempts < 5; attempts++ {
		resp, err := http.Get(url)
		if err != nil {
			time.Sleep(backoff(attempts))
			continue
		}
		if resp.StatusCode == http.StatusNotFound {
			time.Sleep(backoff(attempts))
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
	}
	return nil, errRetriesExhausted
}

func backoff(n int) time.Duration { return time.Duration(n) * time.Second }
`,
			want: []Finding{
				{Rule: "retry-on-non-retryable-error", Message: "retries even when the response status was 404, which should not be retried", Line: 15, Column: 3},
			},
		},
		{
			name: "a 429 response is deliberately not flagged, unlike the rest of the 4xx range",
			src: `package sample

import (
	"net/http"
	"time"
)

func retry(url string) (*http.Response, error) {
	for attempts := 0; attempts < 5; attempts++ {
		resp, err := http.Get(url)
		if err != nil {
			time.Sleep(backoff(attempts))
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			time.Sleep(backoff(attempts))
			continue
		}
		return resp, nil
	}
	return nil, errRetriesExhausted
}

func backoff(n int) time.Duration { return time.Duration(n) * time.Second }
`,
			want: nil,
		},
		{
			name:    "invalid syntax is reported as an error, not a panic",
			src:     "package sample\n\nfunc broken( {\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Analyze("sample.go", []byte(tt.src))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Analyze() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Analyze() unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Analyze() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
