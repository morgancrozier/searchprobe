package main

import (
	"runtime/debug"
	"strings"
)

// resolveVersion returns the linker-provided Version when set, otherwise a
// value derived from the embedded build info: the module version for
// `go install module@version` builds, or the VCS revision for builds from a
// checkout (`go install ./cmd/gsc`), with a "modified" marker for dirty trees.
func resolveVersion() string {
	if Version != "" && Version != "dev" {
		return Version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return versionFromBuildInfo(bi)
}

func versionFromBuildInfo(bi *debug.BuildInfo) string {
	base := "dev"
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		base = v
	}
	var rev, modified string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if rev == "" {
		return base
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if base != "dev" && strings.Contains(base, rev) {
		// Go pseudo-versions already embed the revision (and "+dirty").
		if modified == "true" && !strings.Contains(base, "dirty") {
			base += " (modified)"
		}
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString(" (")
	b.WriteString(rev)
	if modified == "true" {
		b.WriteString(", modified")
	}
	b.WriteString(")")
	return b.String()
}
