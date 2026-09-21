package model

import "testing"

func TestIsReservedRunField(t *testing.T) {
	for _, name := range []string{"run_id", "experiment_id", "replicate", "start", "end"} {
		if !IsReservedRunField(name) {
			t.Errorf("IsReservedRunField(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "id", "run", "Start", "ended"} {
		if IsReservedRunField(name) {
			t.Errorf("IsReservedRunField(%q) = true, want false", name)
		}
	}
}
