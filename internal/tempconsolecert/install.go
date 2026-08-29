package tempconsolecert

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type existingPair struct {
	exists      bool
	certificate []byte
	privateKey  []byte
}

type pairInstall struct {
	directory       string
	certificatePath string
	privateKeyPath  string
	certificateTemp string
	privateKeyTemp  string
	previous        existingPair
}

func ensureSecureDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("secure directory: %w", err)
		}
		info, err = os.Lstat(directory)
	}
	if err != nil {
		return fmt.Errorf("inspect directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("directory must be a non-symlink directory")
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("directory mode is %04o, want 0700", info.Mode().Perm())
	}
	if err := validateCurrentOwner(info); err != nil {
		return fmt.Errorf("directory owner: %w", err)
	}
	return nil
}

func readExistingPair(certificatePath, privateKeyPath string, read func(string) ([]byte, error)) (existingPair, error) {
	certificateExists, err := validateExistingTarget(certificatePath)
	if err != nil {
		return existingPair{}, err
	}
	privateKeyExists, err := validateExistingTarget(privateKeyPath)
	if err != nil {
		return existingPair{}, err
	}
	if certificateExists != privateKeyExists {
		return existingPair{}, fmt.Errorf("certificate and private key targets must either both exist or both be absent")
	}
	if !certificateExists {
		return existingPair{}, nil
	}
	certificate, err := read(certificatePath)
	if err != nil {
		return existingPair{}, fmt.Errorf("read existing certificate: %w", err)
	}
	privateKey, err := read(privateKeyPath)
	if err != nil {
		return existingPair{}, fmt.Errorf("read existing private key: %w", err)
	}
	return existingPair{exists: true, certificate: certificate, privateKey: privateKey}, nil
}

func validateExistingTarget(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect existing %s: %w", filepath.Base(path), err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("existing %s must be a non-symlink regular file", filepath.Base(path))
	}
	if info.Mode().Perm() != 0o600 {
		return false, fmt.Errorf("existing %s mode is %04o, want 0600", filepath.Base(path), info.Mode().Perm())
	}
	if err := validateCurrentOwner(info); err != nil {
		return false, fmt.Errorf("existing %s owner: %w", filepath.Base(path), err)
	}
	return true, nil
}

func writeTemporaryFile(directory, pattern string, body []byte) (path string, err error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path = file.Name()
	keep := false
	closed := false
	defer func() {
		if !closed {
			if closeErr := file.Close(); err == nil && closeErr != nil {
				err = closeErr
			}
		}
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if err = file.Chmod(0o600); err != nil {
		return "", err
	}
	if err = writeFull(file, body); err != nil {
		return "", err
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	closed = true
	keep = true
	return path, nil
}

func writeFull(writer io.Writer, body []byte) error {
	for len(body) != 0 {
		written, err := writer.Write(body)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(body) {
			return io.ErrShortWrite
		}
		body = body[written:]
	}
	return nil
}

func installPair(install pairInstall, deps dependencies) error {
	var rollbackCertificate, rollbackPrivateKey string
	if install.previous.exists {
		var err error
		rollbackCertificate, err = deps.writeTemp(install.directory, ".rollback-console-cert-*", install.previous.certificate)
		if err != nil {
			return fmt.Errorf("prepare certificate rollback: %w", err)
		}
		defer deps.remove(rollbackCertificate)
		rollbackPrivateKey, err = deps.writeTemp(install.directory, ".rollback-console-key-*", install.previous.privateKey)
		if err != nil {
			return fmt.Errorf("prepare private key rollback: %w", err)
		}
		defer deps.remove(rollbackPrivateKey)
	}

	privateKeyInstalled := false
	certificateInstalled := false
	rollback := func(cause error) error {
		if !privateKeyInstalled && !certificateInstalled {
			return cause
		}
		var rollbackErr error
		if install.previous.exists {
			if err := deps.rename(rollbackPrivateKey, install.privateKeyPath); err != nil {
				rollbackErr = fmt.Errorf("restore private key: %w", err)
			} else {
				rollbackPrivateKey = ""
			}
			if err := deps.rename(rollbackCertificate, install.certificatePath); err != nil && rollbackErr == nil {
				rollbackErr = fmt.Errorf("restore certificate: %w", err)
			} else if err == nil {
				rollbackCertificate = ""
			}
		} else {
			if err := removeIfPresent(deps.remove, install.privateKeyPath); err != nil {
				rollbackErr = fmt.Errorf("remove private key: %w", err)
			}
			if err := removeIfPresent(deps.remove, install.certificatePath); err != nil && rollbackErr == nil {
				rollbackErr = fmt.Errorf("remove certificate: %w", err)
			}
		}
		if err := deps.syncDirectory(install.directory); err != nil && rollbackErr == nil {
			rollbackErr = fmt.Errorf("sync rollback: %w", err)
		}
		if rollbackErr != nil {
			return fmt.Errorf("%v; rollback failed: %w", cause, rollbackErr)
		}
		return cause
	}

	if err := deps.rename(install.privateKeyTemp, install.privateKeyPath); err != nil {
		return fmt.Errorf("replace private key: %w", err)
	}
	privateKeyInstalled = true
	install.privateKeyTemp = ""
	if err := deps.rename(install.certificateTemp, install.certificatePath); err != nil {
		return rollback(fmt.Errorf("replace certificate: %w", err))
	}
	certificateInstalled = true
	install.certificateTemp = ""

	certificate, err := deps.readFile(install.certificatePath)
	if err != nil {
		return rollback(fmt.Errorf("read installed certificate: %w", err))
	}
	privateKey, err := deps.readFile(install.privateKeyPath)
	if err != nil {
		return rollback(fmt.Errorf("read installed private key: %w", err))
	}
	if _, err := tls.X509KeyPair(certificate, privateKey); err != nil {
		return rollback(fmt.Errorf("installed certificate and private key do not match"))
	}
	if err := deps.syncDirectory(install.directory); err != nil {
		return rollback(fmt.Errorf("sync installed pair: %w", err))
	}
	return nil
}

func removeIfPresent(remove func(string) error, path string) error {
	if err := remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func renameFile(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }
func removeFile(path string) error             { return os.Remove(path) }
func readFile(path string) ([]byte, error)     { return os.ReadFile(path) }

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
