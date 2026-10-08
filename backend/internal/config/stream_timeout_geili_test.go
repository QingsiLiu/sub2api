package config

import "testing"

func TestLongThinkingStreamTimeoutGeiliBounds(t *testing.T) {
	for _, seconds := range []int{0, 30, 600, 1800} {
		if err := validateLongThinkingStreamTimeout(seconds); err != nil {
			t.Fatal(err)
		}
	}
	for _, seconds := range []int{-1, 1, 29, 1801} {
		if err := validateLongThinkingStreamTimeout(seconds); err == nil {
			t.Fatalf("accepted %d", seconds)
		}
	}
}
