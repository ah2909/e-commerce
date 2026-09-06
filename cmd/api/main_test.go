package main

import (
	"testing"
	"time"
)

func TestAPIWriteTimeoutAllowsReadinessToFinish(t *testing.T) {
	tests := []struct {
		name              string
		dependencyTimeout time.Duration
		want              time.Duration
	}{
		{name: "default", dependencyTimeout: 2 * time.Second, want: 15 * time.Second},
		{name: "maximum", dependencyTimeout: 30 * time.Second, want: 35 * time.Second},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := apiWriteTimeout(test.dependencyTimeout); got != test.want {
				t.Fatalf("apiWriteTimeout(%s) = %s, want %s", test.dependencyTimeout, got, test.want)
			}
			if got := apiWriteTimeout(test.dependencyTimeout); got <= test.dependencyTimeout {
				t.Fatalf("apiWriteTimeout(%s) = %s, want greater than dependency timeout", test.dependencyTimeout, got)
			}
		})
	}
}
