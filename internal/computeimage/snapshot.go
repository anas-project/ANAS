package computeimage

import (
	"fmt"
	"reflect"
	"sort"
)

// Snapshot retains the trusted catalog as well as the exact ordered results.
// Rollback validates against these bytes, never a newly installed catalog.
type Snapshot struct {
	Catalog  []Entry           `json:"catalog" yaml:"catalog"`
	Images   []Resolution      `json:"images" yaml:"images"`
	Bindings map[string]string `json:"bindings,omitempty" yaml:"bindings,omitempty"`
}

func Freeze(refs []Reference, target Target, entries []Entry, keys []string) (*Snapshot, error) {
	catalog, err := NewCatalog(entries, nil)
	if err != nil {
		return nil, err
	}
	images, err := Resolve(refs, target, catalog)
	if err != nil {
		return nil, err
	}
	s := &Snapshot{Catalog: append([]Entry{}, entries...), Images: images}
	if len(keys) > 0 {
		if len(keys) != len(images) {
			return nil, fmt.Errorf("image binding count differs from image count")
		}
		s.Bindings = map[string]string{}
		for i, key := range keys {
			if key == "" || s.Bindings[key] != "" {
				return nil, fmt.Errorf("image binding keys must be non-empty and unique")
			}
			s.Bindings[key] = images[i].Fingerprint
		}
	}
	return s, nil
}

func (s *Snapshot) Validate(refs []Reference, iface string) error {
	if s == nil || len(s.Images) == 0 {
		return fmt.Errorf("compute images are not frozen; create a new deployment with structured image declarations")
	}
	target := s.Images[0].Target
	if target.Interface != iface {
		return fmt.Errorf("frozen image interface does not match resource")
	}
	keys := make([]string, 0, len(s.Bindings))
	for k := range s.Bindings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	expected, err := Freeze(refs, target, s.Catalog, keys)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(s.Images, expected.Images) || !reflect.DeepEqual(s.Bindings, expected.Bindings) {
		return fmt.Errorf("frozen compute image resolution or bindings do not match declarations and catalog")
	}
	return nil
}

func (s *Snapshot) Clone() *Snapshot {
	if s == nil {
		return nil
	}
	out := &Snapshot{Catalog: append([]Entry{}, s.Catalog...), Images: append([]Resolution{}, s.Images...)}
	if s.Bindings != nil {
		out.Bindings = map[string]string{}
		for k, v := range s.Bindings {
			out.Bindings[k] = v
		}
	}
	return out
}
