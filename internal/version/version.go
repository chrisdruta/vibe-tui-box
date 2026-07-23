// Package version carries build-injected release metadata.
package version

import "runtime/debug"

// Set at release build time via
//
//	-ldflags "-X .../internal/version.release=v2.0.0 -X .../internal/version.commit=<sha>"
var (
	release string
	commit  string
)

// Info describes the running binary.
type Info struct {
	Release string // "devel" for unstamped builds
	Commit  string
}

func Get() Info {
	info := Info{Release: release, Commit: commit}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if info.Release == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Release = bi.Main.Version
		}
		if info.Commit == "" {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" {
					info.Commit = s.Value
				}
			}
		}
	}
	if info.Release == "" {
		info.Release = "devel"
	}
	return info
}

func (i Info) String() string {
	if i.Commit != "" {
		short := i.Commit
		if len(short) > 12 {
			short = short[:12]
		}
		return i.Release + " (" + short + ")"
	}
	return i.Release
}
