package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/deployment"
)

type fixturePrepareConfig struct {
	Workspace          string `json:"workspace"`
	RequestRegistry    string `json:"request_registry"`
	ReaderCredentials  string `json:"reader_credentials"`
	StateDirectory     string `json:"state_directory"`
	RouteDirectory     string `json:"route_directory"`
	RendererEntrypoint string `json:"renderer_entrypoint"`
}

// prepareLabFixtures prepares responses and an expectation registry only. It
// uses the same held journal/source as publication, but requires no outstanding
// publications and never calls HostActions, a probe, or the route publisher.
func prepareLabFixtures(configPath, destination string) error {
	var config fixturePrepareConfig
	if err := readInput(configPath, &config); err != nil {
		return err
	}
	for _, path := range []string{destination, config.Workspace, config.RequestRegistry, config.ReaderCredentials, config.StateDirectory, config.RouteDirectory, config.RendererEntrypoint} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("fixture preparation requires absolute clean installed paths")
		}
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("fixture preparation requires a new output directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	snapshot, err := deployment.NewReader(config.Workspace).HTTPAuthorizations(ctx)
	if err != nil {
		return err
	}
	for _, grant := range snapshot.Authorizations {
		if grant.BaseDomain != "example.test" {
			return fmt.Errorf("fixture preparation refuses production authorization")
		}
	}
	readers, err := computeingressruntime.OpenWorkspaceReaders(ctx, config.Workspace, config.RequestRegistry, config.ReaderCredentials, computeingressruntime.FileRouteRenderer{Directory: config.RouteDirectory, Entrypoint: config.RendererEntrypoint})
	if err != nil {
		return err
	}
	defer readers.Close()
	return (computeingressruntime.FileStateStore{Directory: config.StateDirectory}).WithExclusive(ctx, func(journal computeingressruntime.Journal) error {
		state, err := journal.Load(ctx)
		if err != nil {
			return err
		}
		if len(state.Publications) != 0 {
			return fmt.Errorf("retire outstanding lab publications before preparing fixture responses")
		}
		desired, err := readers.Source.ReadDesired(ctx, journal)
		if err != nil {
			return err
		}
		defer readers.Source.AfterRetirement()
		if desired.Epoch != snapshot.Epoch || len(desired.Targets) == 0 {
			return fmt.Errorf("fixture preparation requires unchanged active lab requests")
		}
		type fileRecord struct {
			File        string                                       `json:"file"`
			Expectation computeingressruntime.FixtureHTTPExpectation `json:"expectation"`
		}
		plan := struct {
			Schema    string       `json:"schema"`
			Status    string       `json:"status"`
			Registry  string       `json:"registry"`
			Responses []fileRecord `json:"responses"`
		}{Schema: "anas.compute-http-lab-fixture-plan/v1", Status: "prepared-only", Registry: "fixture-registry.json"}
		files := make(map[string][]byte)
		fixtures := make([]computeingressruntime.FixtureHTTPExpectation, 0, len(desired.Targets))
		for _, target := range desired.Targets {
			fixture, body, err := computeingressruntime.PrepareFixtureResponse(target, "/anas-incus-http-fixture")
			if err != nil {
				return err
			}
			name := fmt.Sprintf("response-%x.bin", sha256.Sum256([]byte(target.Publication.Host)))
			if _, exists := files[name]; exists {
				return fmt.Errorf("duplicate fixture response output")
			}
			files[name] = body
			// The plan is installation guidance, never a replayable reservation.
			binding := fixture
			binding.Target.Publication.Reservation = ""
			plan.Responses = append(plan.Responses, fileRecord{File: name, Expectation: binding})
			fixtures = append(fixtures, fixture)
		}
		planJSON, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return fmt.Errorf("cannot encode fixture preparation plan")
		}
		files["fixture-plan.json"] = append(planJSON, '\n')
		if err := journal.Check(ctx); err != nil {
			return err
		}
		if err := writeArtifacts(destination, files); err != nil {
			return err
		}
		// Publication of the read-only registry is last. If it fails, prepared
		// response files remain private for inspection; they authorize nothing.
		if err := computeingressruntime.RegisterHTTPFixtures(ctx, config.Workspace, filepath.Join(destination, "fixture-registry.json"), fixtures, readers.Source, readers.Source); err != nil {
			return fmt.Errorf("fixture registration failed; private prepared files remain for inspection: %w", err)
		}
		return journal.Check(ctx)
	})
}
