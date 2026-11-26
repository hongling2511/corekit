// Package version reports the build information that the Go toolchain embeds
// in every binary (runtime/debug.ReadBuildInfo), so no -ldflags are needed.
package version

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// Info is the build information of the running binary. Fields the toolchain
// did not record (for example VCS data in a test binary) are empty.
type Info struct {
	Path      string // main package path
	Module    string // main module path
	Version   string // main module version, derived from VCS by go build
	GoVersion string
	Revision  string // VCS commit
	Time      string // VCS commit time, RFC 3339
	Modified  bool   // the working tree had uncommitted changes
}

// Get reads the build information embedded in the running binary.
func Get() Info {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return Info{}
	}
	info := Info{
		Path:      bi.Path,
		Module:    bi.Main.Path,
		Version:   bi.Main.Version,
		GoVersion: bi.GoVersion,
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			info.Revision = s.Value
		case "vcs.time":
			info.Time = s.Value
		case "vcs.modified":
			info.Modified = s.Value == "true"
		}
	}
	return info
}

// String renders the information as "key: value" lines.
func (i Info) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "version: %s\n", i.Version)
	fmt.Fprintf(&b, "path: %s\n", i.Path)
	fmt.Fprintf(&b, "module: %s\n", i.Module)
	fmt.Fprintf(&b, "go: %s\n", i.GoVersion)
	fmt.Fprintf(&b, "revision: %s\n", i.Revision)
	fmt.Fprintf(&b, "time: %s\n", i.Time)
	fmt.Fprintf(&b, "modified: %t\n", i.Modified)
	return b.String()
}
