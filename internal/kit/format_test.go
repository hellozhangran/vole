package kit

import (
	"testing"
	"time"
)

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{0: "0 B", 999: "999 B", 1000: "1.00 KB", 22_990_000_000: "23.0 GB", 5e9: "5.00 GB", 371e9: "371 GB", 1.5e12: "1.50 TB"}
	for in, want := range cases {
		if got := HumanSize(in); got != want {
			t.Errorf("HumanSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestElapsed(t *testing.T) {
	cases := map[time.Duration]string{3 * time.Second: "3s", 65 * time.Second: "1:05", 3723 * time.Second: "1:02:03"}
	for in, want := range cases {
		if got := Elapsed(in); got != want {
			t.Errorf("Elapsed(%v) = %q, want %q", in, got, want)
		}
	}
}
