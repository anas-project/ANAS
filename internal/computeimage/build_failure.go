package computeimage

import "errors"

// BuildStage is a diagnostic observation, never proof that a stage completed.
// No builder-provided text may be carried across the archive error boundary.
type BuildStage uint8

const (
	BuildStageUnknown BuildStage = iota
	BuildStageDownload
	BuildStageRepositories
	BuildStagePackages
	BuildStageFiles
	BuildStageHooks
	BuildStagePacking
)

type buildFailure struct {
	stage            BuildStage
	unverifiedSource bool
}

func (e *buildFailure) Error() string {
	if e.unverifiedSource {
		return ErrArtifactBuildIncomplete.Error() + "; bootstrap Release signatures were not verified"
	}
	names := [...]string{"unknown", "download", "repositories", "packages", "files", "hooks", "packing"}
	stage := e.stage
	if int(stage) >= len(names) {
		stage = BuildStageUnknown
	}
	return ErrArtifactBuildIncomplete.Error() + "; last observed builder stage: " + names[stage]
}

func (*buildFailure) Unwrap() error { return ErrArtifactBuildIncomplete }

// BuildFailureAt keeps only a fixed stage label, not tool output, argv or paths.
func BuildFailureAt(stage BuildStage) error {
	if stage > BuildStagePacking {
		stage = BuildStageUnknown
	}
	return &buildFailure{stage: stage}
}

// A successful builder process cannot authorize publication when its trusted
// bootstrapper reported that it skipped Release signature verification.
// This carries only a closed reason, never the keyring path or raw tool output.
func BuildUnverifiedSourceFailure() error {
	return &buildFailure{stage: BuildStageDownload, unverifiedSource: true}
}

func sanitizeBuildFailure(err error) error {
	var observed *buildFailure
	if errors.As(err, &observed) && observed != nil {
		if observed.unverifiedSource {
			return BuildUnverifiedSourceFailure()
		}
		return BuildFailureAt(observed.stage)
	}
	return ErrArtifactBuildIncomplete
}
