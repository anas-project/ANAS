package config

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Typed scalars for settings that have to distinguish "not set" from a value.
//
// They are strings because that is what a setting actually is by the time
// anything reads it: the config becomes environment variables, and every module
// compares against "true" or "false". Carrying a bool through and converting at
// the edge only moves the conversion somewhere less visible.
//
// They are not plain strings because a plain string accepts anything. `ipv6:
// flase` would be stored verbatim, and a module testing `!= "false"` would read
// it as true -- the setting written, the command silent, the behaviour the
// opposite of what was asked. Parsing at load time turns that into an error
// naming the line.
//
// The zero value is the empty string, which means unset, which is what lets a
// schema default apply. That distinction is the whole reason these exist: the
// defaults for ipv4 and ipv6 are "true", and a type that could not express
// "absent" would let the default overwrite a deliberate false.

// Bool is an optional boolean setting: "", "true" or "false".
type Bool string

const (
	BoolUnset Bool = ""
	BoolTrue  Bool = "true"
	BoolFalse Bool = "false"
)

// UnmarshalYAML accepts a YAML boolean or the strings "true"/"false", and
// rejects everything else. YAML 1.1 spellings such as `yes` and `on` are
// deliberately not accepted: they mean a boolean to some readers and a string
// to others, and a setting whose meaning depends on the parser is worse than
// one that has to be spelled out.
func (b *Bool) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("line %d: %s must be true or false", node.Line, node.Value)
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		*b = BoolUnset
	case "true":
		*b = BoolTrue
	case "false":
		*b = BoolFalse
	default:
		return fmt.Errorf("line %d: %q is not a boolean; write true or false", node.Line, raw)
	}
	return nil
}

// Set reports whether the setting was written at all.
func (b Bool) Set() bool { return b != BoolUnset }

// True reports whether the setting is present and affirmative. An unset value
// is not true; callers that need a default apply it themselves rather than
// having one implied here, because the defaults live in the schema.
func (b Bool) True() bool { return b == BoolTrue }

func (b Bool) String() string { return string(b) }

// Int is an optional integer setting, with the same "" means unset rule. It
// exists for the same reason as Bool: `keep_auto: 0` and an absent keep_auto
// are different instructions, and "keep none" must not be read as "use the
// default of 5".
type Int string

func (i *Int) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("line %d: %s must be a whole number", node.Line, node.Value)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		*i = ""
		return nil
	}
	if _, err := strconv.Atoi(raw); err != nil {
		return fmt.Errorf("line %d: %q is not a whole number", node.Line, raw)
	}
	*i = Int(raw)
	return nil
}

func (i Int) Set() bool { return i != "" }

// Value returns the number and whether one was written.
func (i Int) Value() (int, bool) {
	if !i.Set() {
		return 0, false
	}
	n, err := strconv.Atoi(string(i))
	return n, err == nil
}

func (i Int) String() string { return string(i) }
