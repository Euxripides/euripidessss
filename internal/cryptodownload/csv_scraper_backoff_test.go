package cryptodownload

import (
	"testing"
	"time"
)

func TestCSVEmailTimeoutBackoff(t *testing.T) {
	cases := []struct {
		name    string
		timeout int
		want    time.Duration
	}{
		{"first timeout", 0, 3 * time.Minute},
		{"second consecutive", 1, 6 * time.Minute},
		{"third consecutive", 2, 12 * time.Minute},
		{"many consecutive", 9, 30 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := csvEmailTimeoutBackoff(tc.timeout); got != tc.want {
				t.Fatalf("csvEmailTimeoutBackoff(%d) = %s, want %s", tc.timeout, got, tc.want)
			}
		})
	}
}
