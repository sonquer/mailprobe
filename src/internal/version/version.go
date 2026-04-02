package version

import (
	"runtime/debug"
)

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func init() {
	if Commit != "unknown" && Date != "unknown" {
		return
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}

	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if Commit == "unknown" {
				if len(s.Value) > 7 {
					Commit = s.Value[:7]
				} else {
					Commit = s.Value
				}
			}
		case "vcs.time":
			if Date == "unknown" {
				Date = s.Value
			}
		}
	}
}

// Info holds the build version, commit hash, and build date.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// Get returns the current build version information.
func Get() Info {
	return Info{
		Version: Version,
		Commit:  Commit,
		Date:    Date,
	}
}
