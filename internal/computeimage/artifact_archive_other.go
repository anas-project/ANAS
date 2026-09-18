//go:build !linux && !darwin

package computeimage

import "os"

func artifactArchiveSupported() bool {
	return false
}

func artifactNoFollowFlags() int {
	return 0
}

func artifactPrivateDirectory(os.FileInfo) bool {
	return false
}

func artifactOwnedFile(os.FileInfo, os.FileMode, bool) bool {
	return false
}

func artifactTryLock(*os.File) error {
	return ErrArtifactUnavailable
}

func artifactUnlock(*os.File) error {
	return ErrArtifactUnavailable
}
