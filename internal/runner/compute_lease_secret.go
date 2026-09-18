package runner

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

// A lease secret is a stable naming key, separate from authentication and the
// credential rotation inventory. Never derive it from a deployment or keypair.
func computeLeaseSecretKey(consumer, id string) string {
	return computeResourcePrefix(consumer, id) + "LEASE_SECRET"
}

func validateComputeLeaseSecret(value string) error {
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != 32 || base64.StdEncoding.EncodeToString(decoded) != value {
		// Do not include the malformed value (or the decoder's input) in errors.
		return fmt.Errorf("compute lease secret must be canonical base64 encoding of 32 bytes; restore the original Secret Store entry")
	}
	return nil
}

func computeLeaseSecretMetadata(consumer string) secretMetadata {
	return secretMetadata{Owner: consumer, Kind: "compute_lease_secret", Provenance: "generated-resource"}
}

func (a *app) ensureComputeLeaseSecret(consumer, id string) (string, string, error) {
	key := computeLeaseSecretKey(consumer, id)
	if _, present := a.secrets.values[key]; present {
		value, err := a.readComputeLeaseSecret(consumer, id, key)
		return key, value, err
	}
	// A deleted entry from an already issued lease is data loss, not a legacy
	// upgrade. Re-minting it would silently change every derived URL.
	if err := a.requireUnissuedComputeLeaseSecret(consumer, id); err != nil {
		return "", "", err
	}
	value, err := a.secrets.Ensure(key, func() (string, error) {
		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(bytes), nil
	})
	if err != nil {
		return "", "", err
	}
	a.secrets.SetWithMetadata(key, value, computeLeaseSecretMetadata(consumer))
	return key, value, nil
}

// Frozen deployments resolve only the reference they carry. Legacy manifests
// without a reference remain readable and do not mint a key during replay.
func (a *app) readComputeLeaseSecret(consumer, id, key string) (string, error) {
	if key != computeLeaseSecretKey(consumer, id) {
		return "", fmt.Errorf("resource %s.%s has an invalid compute lease secret reference", consumer, id)
	}
	value, present := a.secrets.values[key]
	if !present {
		return "", fmt.Errorf("resource %s.%s compute lease secret is missing; restore the original Secret Store entry", consumer, id)
	}
	if err := validateComputeLeaseSecret(value); err != nil {
		return "", fmt.Errorf("resource %s.%s: %w", consumer, id, err)
	}
	if a.secrets.metadata[key] != computeLeaseSecretMetadata(consumer) {
		return "", fmt.Errorf("resource %s.%s compute lease secret ownership or lifecycle metadata is invalid", consumer, id)
	}
	return value, nil
}

func (a *app) requireUnissuedComputeLeaseSecret(consumer, id string) error {
	if a.base == "" {
		return nil
	}
	missing := func() error {
		return fmt.Errorf("resource %s.%s has a recorded compute lease secret reference but no Secret Store entry; restore the original entry", consumer, id)
	}
	var state resourceState
	path := filepath.Join(a.base, "state", "resources", consumer+"."+id+".yml")
	if err := readYAML(path, &state); err != nil && !os.IsNotExist(err) {
		return err
	}
	if state.Actual.LeaseSecret != "" {
		return missing()
	}
	paths, err := filepath.Glob(filepath.Join(a.base, "deployments", "*", "deployment.yml"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		manifest, err := loadDeploymentManifest(filepath.Dir(path))
		if err != nil {
			return err
		}
		for _, resource := range manifest.Resources {
			if resource.Contract == "compute" && resource.Consumer == consumer && resource.ID == id && resource.LeaseSecretKey != "" {
				return missing()
			}
		}
	}
	return nil
}
