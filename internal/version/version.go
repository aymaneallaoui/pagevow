// Package version reports the build identity of the binary.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Info describes one build of pagevow.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// Get returns the build identity, falling back to Go build metadata when no linker flags were given.
func Get() Info {
	info := Info{Version: version, Commit: commit, Date: date, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if info.Version == "dev" && build.Main.Version != "" && build.Main.Version != "(devel)" {
		info.Version = build.Main.Version
	}
	for _, setting := range build.Settings {
		switch {
		case setting.Key == "vcs.revision" && info.Commit == "none":
			info.Commit = setting.Value
		case setting.Key == "vcs.time" && info.Date == "unknown":
			info.Date = setting.Value
		}
	}
	return info
}

// String renders the info as one line.
func (i Info) String() string {
	return fmt.Sprintf("pagevow %s (commit %s, built %s, %s %s/%s)", i.Version, i.Commit, i.Date, i.Go, i.OS, i.Arch)
}
