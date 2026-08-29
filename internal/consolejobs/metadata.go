package consolejobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const maximumLockMetadataBytes = 512

type lockMetadata struct {
	SchemaVersion int    `json:"schema_version"`
	StoreID       string `json:"store_id"`
}

func readLockMetadata(file *os.File) (string, error) {
	if file == nil {
		return "", errors.New("job lock descriptor is unavailable")
	}
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect job lock metadata: %w", err)
	}
	if info.Size() == 0 {
		return "", nil
	}
	if info.Size() > maximumLockMetadataBytes {
		return "", errors.New("job lock metadata is too large")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek job lock metadata: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(file, maximumLockMetadataBytes+1))
	if err != nil {
		return "", fmt.Errorf("read job lock metadata: %w", err)
	}
	if len(body) == 0 || body[len(body)-1] != '\n' || bytes.Count(body, []byte{'\n'}) != 1 {
		return "", errors.New("job lock metadata is incomplete or has trailing records")
	}
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimSuffix(body, []byte{'\n'})))
	decoder.DisallowUnknownFields()
	var metadata lockMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return "", errors.New("job lock metadata is invalid")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return "", errors.New("job lock metadata has trailing JSON data")
	}
	if metadata.SchemaVersion != JournalVersion || !hexDigestPattern.MatchString(metadata.StoreID) {
		return "", errors.New("job lock metadata version or store_id is invalid")
	}
	return metadata.StoreID, nil
}

func writeLockMetadata(file *os.File, directory *os.File, storeID string) error {
	if !hexDigestPattern.MatchString(storeID) {
		return errors.New("refuse to write invalid job lock store_id")
	}
	existing, err := readLockMetadata(file)
	if err != nil {
		return err
	}
	if existing != "" {
		if existing == storeID {
			return nil
		}
		return errors.New("job lock metadata belongs to a different store")
	}
	body, err := json.Marshal(lockMetadata{SchemaVersion: JournalVersion, StoreID: storeID})
	if err != nil {
		return fmt.Errorf("encode job lock metadata: %w", err)
	}
	body = append(body, '\n')
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync job lock before metadata append: %w", err)
	}
	if err := writeAll(file, body); err != nil {
		return fmt.Errorf("append job lock metadata: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync job lock metadata: %w", err)
	}
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync job store directory after lock metadata: %w", err)
	}
	return nil
}
