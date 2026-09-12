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
