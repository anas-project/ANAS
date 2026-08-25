package deployment

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// CommandDigest hashes every field that can change command semantics,
// including internal dispatch and input projections. Digest itself is cleared
// to avoid a self-reference. Struct-backed JSON is deterministic here: there
// are no maps in the command descriptor or parameter definitions.
func CommandDigest(command ModuleCommand) (string, error) {
	command.Digest = ""
	body, err := json.Marshal(command)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(body)), nil
}
