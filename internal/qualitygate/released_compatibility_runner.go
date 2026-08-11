package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type releasedCompatibilityRunner struct {
	repository    string
	compatibility releasedGenerationCompatibility
	output        io.Writer
}

func newReleasedCompatibilityRunner(
	repository string,
	compatibility releasedGenerationCompatibility,
	output io.Writer,
) (*releasedCompatibilityRunner, error) {
	if !filepath.IsAbs(repository) {
		return nil, errors.New("released compatibility repository path must be absolute")
	}
	if output == nil {
		return nil, errors.New("released compatibility output is required")
	}
	if err := compatibility.ValidateCandidate(); err != nil {
		return nil, err
	}
	if err := compatibility.ValidateSource(repository); err != nil {
		return nil, err
	}
	return &releasedCompatibilityRunner{
		repository: repository, compatibility: compatibility, output: output,
	}, nil
}

func (runner *releasedCompatibilityRunner) Run(ctx context.Context) (runErr error) {
	workspace, err := os.MkdirTemp("", "spice-agent-released-compatibility-")
	if err != nil {
		return fmt.Errorf("create released compatibility workspace: %w", err)
	}
	ownedWorkspace, err := newReleasedWorkspace(workspace)
	if err != nil {
		_ = os.RemoveAll(workspace)
		return err
	}
	defer func() {
		if cleanupErr := ownedWorkspace.Close(); runErr == nil && cleanupErr != nil {
			runErr = fmt.Errorf("remove released compatibility workspace: %w", cleanupErr)
		}
	}()
	builder, err := newReleasedGenerationBuilder(runner.repository, ownedWorkspace.Path())
	if err != nil {
		return err
	}
	builds := make(map[string]releasedGenerationBuild, len(runner.compatibility.Generations))
	for _, generation := range runner.compatibility.Generations {
		build, buildErr := builder.Build(ctx, generation)
		if buildErr != nil {
			return fmt.Errorf("build released generation %s: %w", generation.Version, buildErr)
		}
		builds[generation.Role] = build
		if _, writeErr := fmt.Fprintf(
			runner.output,
			"    built %s from public proxy/SumDB with fresh caches\n",
			generation.Version,
		); writeErr != nil {
			return fmt.Errorf("write released generation build status: %w", writeErr)
		}
	}
	for _, direction := range runner.compatibility.Engine.Directions {
		matrix, constructErr := newReleasedEngineMatrix(
			direction,
			builds[direction.Peer],
			builds[direction.Client],
			runner.output,
		)
		if constructErr != nil {
			return constructErr
		}
		if directionErr := matrix.Run(ctx); directionErr != nil {
			return fmt.Errorf("run released engine direction %s: %w", direction.ID, directionErr)
		}
	}
	for _, direction := range runner.compatibility.Plugin.Directions {
		matrix, constructErr := newReleasedPluginMatrix(
			direction,
			builds[direction.Peer],
			builds[direction.Client],
			runner.output,
		)
		if constructErr != nil {
			return constructErr
		}
		if directionErr := matrix.Run(ctx); directionErr != nil {
			return fmt.Errorf("run released plugin direction %s: %w", direction.ID, directionErr)
		}
	}
	return nil
}
