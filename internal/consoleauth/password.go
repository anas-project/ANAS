package consoleauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemoryKiB       uint32 = 64 * 1024
	argonIterations      uint32 = 3
	argonParallelism     uint8  = 2
	argonSaltBytes              = 16
	argonKeyBytes               = 32
	maximumPasswordBytes        = 1024
)

// HashPassword creates an Argon2id PHC record using the package's fixed,
// reviewable parameters and crypto/rand salt.
func HashPassword(password string) (string, error) {
	return hashPassword(rand.Reader, password)
}

func hashPassword(random io.Reader, password string) (string, error) {
	if err := validatePasswordInput(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltBytes)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", fmt.Errorf("generate Argon2id salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemoryKiB, argonParallelism, argonKeyBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

// VerifyPassword parses the fixed PHC shape, derives a candidate hash, and uses
// a constant-time comparison. Parameter changes require an intentional package
// update rather than accepting a weaker stored record.
func VerifyPassword(encoded, password string) (bool, error) {
	if len(password) > maximumPasswordBytes {
		return false, errors.New("password is too long")
	}
	salt, expected, err := parsePasswordPHC(encoded)
	if err != nil {
		return false, err
	}
	candidate := argon2.IDKey([]byte(password), salt, argonIterations, argonMemoryKiB, argonParallelism, argonKeyBytes)
	return subtle.ConstantTimeCompare(candidate, expected) == 1, nil
}

func validatePasswordInput(password string) error {
	if password == "" {
		return errors.New("password must not be empty")
	}
	if len(password) > maximumPasswordBytes {
		return errors.New("password is too long")
	}
	return nil
}

func parsePasswordPHC(encoded string) ([]byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return nil, nil, errors.New("owner password hash is not a supported Argon2id PHC record")
	}
	parameters := strings.Split(parts[3], ",")
	if len(parameters) != 3 || !strings.HasPrefix(parameters[0], "m=") || !strings.HasPrefix(parameters[1], "t=") || !strings.HasPrefix(parameters[2], "p=") {
		return nil, nil, errors.New("owner password hash uses unsupported Argon2id parameters")
	}
	memory, memoryErr := strconv.ParseUint(strings.TrimPrefix(parameters[0], "m="), 10, 32)
	iterations, iterationsErr := strconv.ParseUint(strings.TrimPrefix(parameters[1], "t="), 10, 32)
	parallelism, parallelismErr := strconv.ParseUint(strings.TrimPrefix(parameters[2], "p="), 10, 8)
	if memoryErr != nil || iterationsErr != nil || parallelismErr != nil ||
		uint32(memory) != argonMemoryKiB || uint32(iterations) != argonIterations || uint8(parallelism) != argonParallelism {
		return nil, nil, errors.New("owner password hash uses unsupported Argon2id parameters")
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != argonSaltBytes {
		return nil, nil, errors.New("owner password hash has an invalid salt")
	}
	hash, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(hash) != argonKeyBytes {
		return nil, nil, errors.New("owner password hash has an invalid digest")
	}
	return salt, hash, nil
}

func consumeDummyPasswordWork(password string) {
	if len(password) > maximumPasswordBytes {
		password = password[:maximumPasswordBytes]
	}
	salt := make([]byte, argonSaltBytes)
	expected := make([]byte, argonKeyBytes)
	candidate := argon2.IDKey([]byte(password), salt, argonIterations, argonMemoryKiB, argonParallelism, argonKeyBytes)
	_ = subtle.ConstantTimeCompare(candidate, expected)
}
