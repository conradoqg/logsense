package version

import "fmt"

// These variables are populated at build time via -ldflags.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

func String() string {
	base := Version
	if Commit != "" {
		base += fmt.Sprintf(" (%s)", Commit)
	}
	if Date != "" {
		base += fmt.Sprintf(" %s", Date)
	}
	return base
}
