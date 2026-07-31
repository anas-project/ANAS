package runner

// Deployment artifacts are sealed read-only the moment they are finished, and
// they carry a verbatim copy of the config they were built from. Both exist for
// the snapshot subsystem, and both are worth having on their own.
//
// Sealing turns §13's "release assets become read-only once sealed" from a
// convention into a permission. Immutability used to rest on nobody happening
// to write to a rendered file after render, which was true of the code but not
// enforced by anything. It becomes load-bearing once a snapshot reuses the
// artifact by hard link: a hard link shares the inode, so one in-place write to
// a deployment file would silently rewrite the same bytes inside every snapshot
// that references it — corruption that only surfaces on the day someone
// restores.
//
// Only regular files are sealed; directories keep 0700. Read-only directories
// would also block unlink, and an unlink-and-replace allocates a new inode,
// which is exactly the operation hard links are safe against. Sealing the
// directories would therefore buy no extra safety while making a deployment
// impossible to garbage-collect without first walking it back to writable.

import (
	"fmt"
	"os"
	"path/filepath"
)

// deploymentConfigSourceName is the config a deployment was built from, kept
// verbatim.
//
// Nothing else in the system retains the original text: saveAppliedConfig
// stores only per-setting sha256 hashes, the release keeps only a redacted
// resolution, and the manifest keeps only fingerprints. A fingerprint detects
// that a config differs; it cannot produce the one that matched. Without this
// file a pre-upgrade snapshot could only carry whatever config happened to be
// on disk when it was taken — which for a pre-upgrade snapshot is the *new*
// config the user is about to apply, the very state the snapshot exists to
// escape.
//
// This does not contradict §9.1. That rule forbids the config as a *startup
// input*, so that config.yml never doubles as both desired state and runtime
// input. This copy is never read to start anything; it is read to restore.
const deploymentConfigSourceName = "config.source.yml"

func deploymentConfigSourcePath(root string) string {
	return filepath.Join(root, deploymentConfigSourceName)
}

// sealDeployment strips every write bit from the artifact's regular files.
//
// Clearing bits rather than assigning a fixed mode preserves the two
// distinctions the render already made: an executable stays executable
// (0755 -> 0555) and an owner-only file stays owner-only (0600 -> 0400), so
// rendered .env files and the manifest do not become world-readable in the name
// of becoming read-only. Everything else lands on 0444, which keeps the
// config assets bind-mounted into containers readable by the in-image service
// user.
func sealDeployment(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sealed := info.Mode().Perm() &^ 0222
		// A file the owner cannot read is not a useful artifact, and a mode of
		// 0000 would survive the mask above unchanged.
		sealed |= 0400
		if sealed == info.Mode().Perm() {
			return nil
		}
		if err := os.Chmod(path, sealed); err != nil {
			return fmt.Errorf("seal %s: %w", path, err)
		}
		return nil
	})
}
