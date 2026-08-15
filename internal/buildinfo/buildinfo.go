package buildinfo

// These values are replaced with -ldflags by the release workflow. Keeping
// useful development defaults makes source builds and tests self-describing.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)
