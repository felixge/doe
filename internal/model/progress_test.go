package model

import (
	"testing"
	"time"
)

func TestEstimateTotalDurationWaitsForSample(t *testing.T) {
	if got, ok := EstimateTotalDuration([]int{0, 1}, nil, 3*time.Second); ok || got != 0 {
		t.Errorf("estimate = %s, %t; want no estimate", got, ok)
	}
}

func TestEstimateTotalDurationUsesGlobalAverageForUnseenPoints(t *testing.T) {
	if got, ok := EstimateTotalDuration([]int{1, 2}, map[int][]time.Duration{0: {4 * time.Second}}, time.Second); !ok || got != 7*time.Second {
		t.Errorf("estimate = %s, %t; want 7s", got, ok)
	}
}

func TestEstimateTotalDurationUsesPointAverages(t *testing.T) {
	samples := map[int][]time.Duration{0: {2 * time.Second, 6 * time.Second}, 1: {10 * time.Second}}
	if got, ok := EstimateTotalDuration([]int{0, 0, 1}, samples, time.Second); !ok || got != 17*time.Second {
		t.Errorf("estimate = %s, %t; want 17s", got, ok)
	}
}

func TestEstimateTotalDurationClampsElapsedTime(t *testing.T) {
	samples := map[int][]time.Duration{0: {2 * time.Second}}
	for _, elapsed := range []time.Duration{3 * time.Second, time.Hour} {
		if got, ok := EstimateTotalDuration([]int{1}, samples, elapsed); !ok || got != 0 {
			t.Errorf("estimate after %s = %s, %t; want 0s", elapsed, got, ok)
		}
	}
	if got, ok := EstimateTotalDuration([]int{1}, samples, -time.Second); !ok || got != 2*time.Second {
		t.Errorf("estimate with negative elapsed = %s, %t; want 2s", got, ok)
	}
}
