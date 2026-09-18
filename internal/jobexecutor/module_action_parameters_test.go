package jobexecutor

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/consolejobs"
)

func TestModuleActionNormalizationCanonicalizesObjectsWithoutRounding(t *testing.T) {
	bodies := []string{
		`{"z":18446744073709551615,"nested":{"b":2,"a":1},"a":true}`,
		`{ "a":true, "nested":{ "a":1, "b":2 }, "z":18446744073709551615 }`,
	}
	var first json.RawMessage
	for _, body := range bodies {
		owned := json.RawMessage(body)
		definition := ModuleActionDefinition{Name: "module.fixture.status", Normalize: func(json.RawMessage) (json.RawMessage, error) { return owned, nil }}
		normalized, err := normalizeModuleAction(definition, json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(normalized, []byte("18446744073709551615")) {
			t.Fatal("counter rounded while canonicalizing parameters")
		}
		if first == nil {
			first = append(json.RawMessage(nil), normalized...)
		} else if !bytes.Equal(first, normalized) {
			t.Fatal("equivalent objects normalized to different digest inputs")
		}
		// A callback retaining its result must not be able to change queued data.
		owned[0] = '!'
		if !json.Valid(normalized) {
			t.Fatal("normalization returned callback-owned memory")
		}
	}
}

func TestModuleActionNormalizationRejectsInvalidOrExpandedOutput(t *testing.T) {
	for name, output := range map[string]string{
		"null":      `null`,
		"array":     `[]`,
		"duplicate": `{"x":1,"x":2}`,
		"trailing":  `{} {}`,
		// Escaping expands this otherwise bounded callback result beyond 32 KiB.
		"expanded": `{"text":"` + strings.Repeat("<", 6000) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			definition := ModuleActionDefinition{Name: "module.fixture.status", Normalize: func(json.RawMessage) (json.RawMessage, error) { return json.RawMessage(output), nil }}
			if _, err := normalizeModuleAction(definition, json.RawMessage(`{}`)); !errors.Is(err, ErrModuleActionParameters) {
				t.Fatalf("invalid normalized output accepted: %v", err)
			}
		})
	}
}

func TestStoredModuleActionCannotGainNewDefaultsOrDeployment(t *testing.T) {
	definition := ModuleActionDefinition{
		Name: "module.fixture.status", Module: "fixture", DeploymentID: "deployment-1", DescriptorDigest: strings.Repeat("a", 64),
		Normalize: func(json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`{"b":2,"a":1}`), nil },
	}
	job := consolejobs.Job{Request: map[string]any{
		"module": "fixture", "deployment_id": "deployment-1", "descriptor_digest": definition.DescriptorDigest,
		"parameters": map[string]any{"a": json.Number("1"), "b": json.Number("2")},
	}}
	if _, err := storedModuleActionParameters(job, definition); err != nil {
		t.Fatalf("same parameters in a different key order rejected: %v", err)
	}
	definition.Normalize = func(json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"a":1,"b":2,"new_default":true}`), nil
	}
	if _, err := storedModuleActionParameters(job, definition); !errors.Is(err, ErrModuleActionParameters) {
		t.Fatal("queued action silently acquired a new default")
	}
	definition.DeploymentID = "deployment-2"
	if _, err := storedModuleActionParameters(job, definition); !errors.Is(err, ErrModuleActionDenied) {
		t.Fatal("queued action silently moved to another deployment")
	}
}
