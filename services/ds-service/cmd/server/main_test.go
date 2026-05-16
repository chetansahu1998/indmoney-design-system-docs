package main

import (
	"testing"
	"time"
)

// TestAutosyncIntervalFromEnv covers all six input shapes of
// FIGMA_AUTOSYNC_INTERVAL plus the F8 lower-bound clamp. The function
// is pure (reads os.Getenv), so each case sets + unsets the env var
// inside its scope via t.Setenv.
func TestAutosyncIntervalFromEnv(t *testing.T) {
	cases := []struct {
		name, env string
		want      time.Duration
	}{
		{"unset → 15m default", "", 15 * time.Minute},
		{"explicit 0 disables", "0", 0},
		{"duration form, well-above floor", "15m", 15 * time.Minute},
		{"duration form, hours", "2h", 2 * time.Hour},
		{"duration form below floor → clamped", "30s", autosyncMinInterval},
		{"duration form 1ms → clamped", "1ms", autosyncMinInterval},
		{"int-seconds form, above floor", "900", 15 * time.Minute},
		{"int-seconds form 1 → clamped (operator typo)", "1", autosyncMinInterval},
		{"int-seconds form 0 → disabled (same as raw 0)", "0", 0},
		{"negative duration → default", "-5s", 15 * time.Minute},
		{"negative int → default", "-30", 15 * time.Minute},
		{"garbage → default", "not-a-duration", 15 * time.Minute},
		{"whitespace trimmed", "  10m  ", 10 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FIGMA_AUTOSYNC_INTERVAL", tc.env)
			got := autosyncIntervalFromEnv()
			if got != tc.want {
				t.Errorf("FIGMA_AUTOSYNC_INTERVAL=%q: got %s want %s", tc.env, got, tc.want)
			}
		})
	}
}
