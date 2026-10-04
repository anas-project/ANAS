package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/anas-project/ANAS/internal/computeimage"
)

func (a *app) resolveComputeImages(consumer, id, provider, iface string, spec map[string]any, keys []string) (*computeimage.Snapshot, error) {
	refs, err := computeimage.Parse(spec["image_allowlist"])
	if err != nil {
		return nil, fmt.Errorf("resource %s.%s: %w", consumer, id, err)
	}
	mod := a.reg[provider]
	if mod.EnvPrefix == "" {
		mod.EnvPrefix = defaultEnvPrefix(provider)
	}
	arch := a.env[moduleParamEnvKey(provider, mod.EnvPrefix, mod.Exports, "image_architecture")]
	// This is the daemon's configured target, never the architecture of the CLI.
	target := computeimage.Target{Architecture: arch, Interface: iface}
	entries := []computeimage.Entry{}
	if mod.SourceDir != "" {
		path := filepath.Join(mod.SourceDir, "images", "catalog.json")
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			if !info.Mode().IsRegular() || info.Size() > 4<<20 {
				return nil, fmt.Errorf("compute image catalog must be a regular file of at most 4 MiB")
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&entries); err != nil || entries == nil {
				return nil, fmt.Errorf("invalid compute image catalog in provider bundle")
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				return nil, fmt.Errorf("compute image catalog contains trailing data")
			}
		}
	}
	_, err = computeimage.NewCatalog(entries, nil)
	if err != nil {
		return nil, err
	}
	// Historical deployment snapshots are retained version claims. Refuse an
	// altered/removed published key even when it is no longer actively selected.
	if a.base != "" && len(entries) > 0 {
		previous, err := a.loadComputeImageHistory()
		if err != nil {
			return nil, err
		}
		prior, err := computeimage.NewCatalog(previous, nil)
		if err != nil {
			return nil, err
		}
		if _, err := computeimage.NewCatalog(entries, prior); err != nil {
			return nil, err
		}
		paths, err := filepath.Glob(filepath.Join(a.base, "deployments", "*", "deployment.yml"))
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			manifest, err := loadDeploymentManifest(filepath.Dir(path))
			if err != nil {
				return nil, err
			}
			for _, resource := range manifest.Resources {
				if resource.Contract != "compute" || resource.ComputeImages == nil {
					continue
				}
				previous, err := computeimage.NewCatalog(resource.ComputeImages.Catalog, nil)
				if err != nil {
					return nil, err
				}
				if _, err := computeimage.NewCatalog(entries, previous); err != nil {
					return nil, fmt.Errorf("compute image catalog conflicts with deployment %s: %w", manifest.ID, err)
				}
			}
		}
	}
	snapshot, err := computeimage.Freeze(refs, target, entries, keys)
	if err != nil {
		return nil, fmt.Errorf("resource %s.%s: %w", consumer, id, err)
	}
	return snapshot, nil
}

func validateFrozenComputeImages(spec map[string]any, iface string, images *computeimage.Snapshot) error {
	refs, err := computeimage.Parse(spec["image_allowlist"])
	if err != nil {
		return err
	}
	return images.Validate(refs, iface)
}

func validateComputeRequest(request ResourceRequest) (computeQuota, []string, error) {
	quota, _, err := validateComputeSpec(request.Consumer, request.ID, request.Spec)
	if err != nil {
		return computeQuota{}, nil, err
	}
	if err := validateFrozenComputeImages(request.Spec, request.Interface, request.ComputeImages); err != nil {
		return computeQuota{}, nil, err
	}
	pins := make([]string, 0, len(request.ComputeImages.Images))
	for _, image := range request.ComputeImages.Images {
		pins = append(pins, image.Fingerprint)
	}
	return quota, pins, nil
}

func (a *app) loadComputeImageHistory() ([]computeimage.Entry, error) {
	path := filepath.Join(a.base, "state", "compute-image-history.yml")
	var entries []computeimage.Entry
	if err := readYAML(path, &entries); err != nil {
		if os.IsNotExist(err) {
			return []computeimage.Entry{}, nil
		}
		return nil, err
	}
	return entries, nil
}

func (a *app) recordComputeImageHistory() error {
	entries, err := a.loadComputeImageHistory()
	if err != nil {
		return err
	}
	for _, request := range a.resourceRequests {
		if request.ComputeImages == nil || len(request.ComputeImages.Catalog) == 0 {
			continue
		}
		previous, err := computeimage.NewCatalog(entries, nil)
		if err != nil {
			return err
		}
		if _, err := computeimage.NewCatalog(request.ComputeImages.Catalog, previous); err != nil {
			return err
		}
		entries = request.ComputeImages.Catalog
	}
	if len(entries) == 0 {
		return nil
	}
	path := filepath.Join(a.base, "state", "compute-image-history.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return writeYAMLAtomic(path, entries, 0600)
}

// Loading old artifact metadata remains possible for status, stop and replacing
// a deployment. Only an executable target must satisfy the new image contract.
func (a *app) validateComputeImagesFor(selection []string) error {
	for _, request := range a.resourceRequests {
		if request.Contract != "compute" || !contains(selection, request.Consumer) {
			continue
		}
		if _, _, err := validateComputeRequest(request); err != nil {
			return fmt.Errorf("resource %s.%s: %w", request.Consumer, request.ID, err)
		}
	}
	return nil
}
