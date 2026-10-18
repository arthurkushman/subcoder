package version

import "fmt"

var (
	Version = "0.1.0"
	Commit  = "dev"
	Date    = "unknown"
)

func String() string {
	return fmt.Sprintf("%s (%s, %s)", Version, Commit, Date)
}
