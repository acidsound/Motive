// Package version carries the build version injected by the linker at release
// time (see .goreleaser.yaml). Local builds report "dev".
package version

// Version is set via -ldflags "-X .../internal/version.Version=<tag>" by
// GoReleaser, where <tag> is the short commit hash. It defaults to "dev" for
// local builds so the binary always reports a meaningful version.
var Version = "dev"
