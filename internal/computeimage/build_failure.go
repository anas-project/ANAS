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

type buildFailure struct{ stage BuildStage }

func (e *buildFailure) Error() string {
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

func sanitizeBuildFailure(err error) error {
	var observed *buildFailure
	if errors.As(err, &observed) && observed != nil {
		return BuildFailureAt(observed.stage)
	}
	return ErrArtifactBuildIncomplete
}
