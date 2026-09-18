//go:build !linux && !darwin

package computeingressruntime

import (
	"context"
	"fmt"
	"os"
)

func readPrivateArtifact(context.Context, string) ([]byte, os.FileInfo, os.FileInfo, error) {
	return nil, nil, nil, fmt.Errorf("HTTP private credential delivery requires Linux or macOS")
}

func publishPrivateArtifact(context.Context, string, []byte, func(context.Context) error) error {
	return fmt.Errorf("HTTP private credential delivery requires Linux or macOS")
}
