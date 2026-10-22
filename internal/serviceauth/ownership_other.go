//go:build !unix

package serviceauth

import (
	"errors"
	"io/fs"
)

func fileOwner(fs.FileInfo) (int, int, error) {
	return 0, 0, errors.New("service credential delivery requires Unix ownership semantics")
}
