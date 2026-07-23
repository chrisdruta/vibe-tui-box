package domain

import (
	"fmt"
	"runtime"
	"strings"
)

// Platform is an OS/architecture pair in Go naming ("linux-amd64").
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// supported lists the release targets from the architecture document.
var supported = map[Platform]bool{
	{OS: "linux", Arch: "amd64"}:  true,
	{OS: "linux", Arch: "arm64"}:  true,
	{OS: "darwin", Arch: "arm64"}: true,
}

func ParsePlatform(s string) (Platform, error) {
	os, arch, ok := strings.Cut(s, "-")
	if !ok || os == "" || arch == "" {
		return Platform{}, fmt.Errorf("%w: platform %q: want <os>-<arch>", ErrInvalid, s)
	}
	p := Platform{OS: os, Arch: arch}
	if !supported[p] {
		return Platform{}, fmt.Errorf("%w: platform %q", ErrNotSupported, s)
	}
	return p, nil
}

// CurrentPlatform reports the running build's platform. It errors on
// targets the release matrix does not cover.
func CurrentPlatform() (Platform, error) {
	return ParsePlatform(runtime.GOOS + "-" + runtime.GOARCH)
}

func (p Platform) String() string { return p.OS + "-" + p.Arch }

func (p Platform) IsZero() bool { return p == Platform{} }
