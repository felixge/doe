package version

import (
	"runtime/debug"
	"testing"
)

func TestFromBuildInfo(t *testing.T) {
	revision := "7e0e96216c758e45ece0128fb5da4475a223a176"
	tests := []struct {
		name         string
		info         *debug.BuildInfo
		ok           bool
		want         string
		reproducible bool
	}{
		{"release", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, true, "v1.2.3", true},
		{"clean development", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: revision}, {Key: "vcs.modified", Value: "false"}}}, true, revision, true},
		{"dirty pseudo-version", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20260918-x+dirty"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: revision}, {Key: "vcs.modified", Value: "true"}}}, true, revision + "-dirty", false},
		{"unknown", nil, false, "devel-unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reproducible := fromBuildInfo(tt.info, tt.ok)
			if got != tt.want || reproducible != tt.reproducible {
				t.Fatalf("fromBuildInfo() = %q, %v; want %q, %v", got, reproducible, tt.want, tt.reproducible)
			}
		})
	}
}
