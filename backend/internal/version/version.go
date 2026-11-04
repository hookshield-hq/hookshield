// Package version exposes build metadata injected at link time:
//
//	go build -ldflags "-X github.com/hookshield-hq/hookshield/backend/internal/version.Version=v0.1.0 \
//	  -X github.com/hookshield-hq/hookshield/backend/internal/version.Commit=$(git rev-parse --short HEAD)"
package version

var (
	Version = "dev"
	Commit  = "none"
)

// String returns a human readable version label.
func String() string { return Version + " (" + Commit + ")" }
