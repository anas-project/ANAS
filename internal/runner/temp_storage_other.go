//go:build !linux

package runner

import (
	"errors"
	"os"
	"reflect"
	"strconv"
)

func openTemporaryRoot(path string) (*os.Root, *os.File, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, err
	}
	file, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, nil, err
	}
	return root, file, nil
}

func temporaryFileIdentity(info os.FileInfo) string {
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.Kind() == reflect.Struct {
		if inode := value.FieldByName("Ino"); inode.IsValid() && inode.CanUint() {
			return strconv.FormatUint(inode.Uint(), 10)
		}
	}
	return ""
}

func temporaryFileOwned(info os.FileInfo) bool {
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return false
	}
	uid, links := value.FieldByName("Uid"), value.FieldByName("Nlink")
	return uid.IsValid() && uid.CanUint() && links.IsValid() && links.CanUint() && int(uid.Uint()) == os.Geteuid() && links.Uint() == 1
}

func inspectTemporaryFilesystem(string) (TemporaryFilesystem, uint64, uint64, error) {
	return TemporaryFilesystem{}, 0, 0, errors.New("managed temporary storage requires a local Linux filesystem")
}

func checkTemporaryFeatures(_ string, features []string) error {
	if len(features) == 0 {
		return nil
	}
	return errors.New("temporary filesystem feature checks require Linux")
}
func removeTemporaryLeaseDirectory(*TemporaryLease) error {
	return errors.New("safe temporary garbage collection requires Linux")
}

func confirmTemporaryDirectoryAbsent(*TemporaryLease) (bool, error) {
	return false, errors.New("safe temporary absence reconciliation requires Linux")
}
