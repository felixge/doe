// Package version reports the identity of the running doe binary.
package version

import "runtime/debug"

// Info returns the version string and whether it identifies a reproducible
// binary. Development builds use VCS metadata supplied by the Go toolchain.
func Info() (string, bool) {
	info, ok := debug.ReadBuildInfo()
	return fromBuildInfo(info, ok)
}

func fromBuildInfo(info *debug.BuildInfo, ok bool) (string, bool) {
	if !ok {
		return "devel-unknown", false
	}
	var revision string
	dirty := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if revision == "" {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version, true
		}
		return "devel-unknown", false
	}
	if dirty {
		return revision + "-dirty", false
	}
	return revision, true
}
