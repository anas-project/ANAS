//go:build !unix

package consoleauth

import (
	"errors"
	"io/fs"
)

func effectiveUID() uint32 { return 0 }

func ownerUID(fs.FileInfo) (uint32, error) {
	return 0, errors.New("Unix owner metadata is unavailable")
}
