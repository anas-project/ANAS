//go:build linux || darwin

package main

import (
	"os"

	"github.com/anas-project/ANAS/internal/computeimage"
	"golang.org/x/sys/unix"
)

func openArtifactInput(path string, limit int64) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > limit {
		return nil, nil, computeimage.ErrArtifactUnavailable
	}
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, computeimage.ErrArtifactUnavailable
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || before.Size() != after.Size() ||
		before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		_ = file.Close()
		return nil, nil, computeimage.ErrArtifactUnavailable
	}
	return file, after, nil
}
