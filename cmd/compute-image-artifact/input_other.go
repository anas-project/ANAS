//go:build !linux && !darwin

package main

import (
	"errors"
	"os"
)

func openArtifactInput(string, int64) (*os.File, os.FileInfo, error) {
	return nil, nil, errors.New("local compute image artifact inspection requires Linux or macOS")
}
