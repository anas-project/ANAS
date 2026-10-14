//go:build !unix

package appcatalog

import (
	"fmt"
	"os"
)

func openAssetFile(_ *os.Root, _ string) (*os.File, error) {
	return nil, fmt.Errorf("this platform lacks the required no-follow asset reader")
}
