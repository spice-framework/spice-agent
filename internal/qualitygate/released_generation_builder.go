package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type releasedGenerationBuilder struct {
	repository string
	workspace  string
}

func newReleasedGenerationBuilder(repository, workspace string) (*releasedGenerationBuilder, error) {
	if !filepath.IsAbs(repository) || !filepath.IsAbs(workspace) {
		return nil, errors.New("released generation builder paths must be absolute")
	}
	return &releasedGenerationBuilder{repository: repository, workspace: workspace}, nil
}

func (builder *releasedGenerationBuilder) Build(
	ctx context.Context,
	generation releasedGeneration,
) (releasedGenerationBuild, error) {
	moduleDirectory := filepath.Join(builder.workspace, "peer-"+generation.Role)
	if err := os.Mkdir(moduleDirectory, 0o700); err != nil {
		return releasedGenerationBuild{}, fmt.Errorf("create released peer module: %w", err)
	}
	moduleRoot, openErr := os.OpenRoot(moduleDirectory)
	if openErr != nil {
		return releasedGenerationBuild{}, fmt.Errorf("open released peer module: %w", openErr)
	}
	defer moduleRoot.Close() //nolint:errcheck // the temporary workspace cleanup owns final removal.
	if err := builder.copyPeerSource(moduleDirectory); err != nil {
		return releasedGenerationBuild{}, err
	}
	goMod := fmt.Sprintf(
		"module example.com/spice-agent-released-peer-%s\n\ngo 1.26.0\n\ntoolchain go1.26.5\n\nrequire %s %s\n",
		generation.Role,
		modulePath,
		generation.Version,
	)
	if err := moduleRoot.WriteFile("go.mod", []byte(goMod), 0o600); err != nil {
		return releasedGenerationBuild{}, fmt.Errorf("write released peer go.mod: %w", err)
	}
	moduleCache := filepath.Join(builder.workspace, "gomodcache-"+generation.Role)
	buildCache := filepath.Join(builder.workspace, "gocache-"+generation.Role)
	if err := os.Mkdir(moduleCache, 0o700); err != nil {
		return releasedGenerationBuild{}, fmt.Errorf("create released peer module cache: %w", err)
	}
	if err := os.Mkdir(buildCache, 0o700); err != nil {
		return releasedGenerationBuild{}, fmt.Errorf("create released peer build cache: %w", err)
	}
	networkEnvironment := map[string]string{
		"GOCACHE": buildCache, "GOMODCACHE": moduleCache, "GOPROXY": "https://proxy.golang.org",
		"GOSUMDB": "sum.golang.org", "GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": "",
		"GONOSUMDB": "", "GONOPROXY": "", "GOPRIVATE": "", "GOVCS": "public:git|https,private:off",
	}
	downloadOutput, err := capture(
		ctx,
		moduleDirectory,
		networkEnvironment,
		"go",
		"mod",
		"download",
		"-json",
		modulePath+"@"+generation.Version,
	)
	if err != nil {
		return releasedGenerationBuild{}, err
	}
	if err = builder.validateDownload(downloadOutput, generation); err != nil {
		return releasedGenerationBuild{}, err
	}
	if err = command(ctx, moduleDirectory, networkEnvironment, "go", "mod", "tidy"); err != nil {
		return releasedGenerationBuild{}, err
	}
	if err = command(ctx, moduleDirectory, networkEnvironment, "go", "mod", "vendor"); err != nil {
		return releasedGenerationBuild{}, err
	}
	materializedGoMod, err := moduleRoot.ReadFile("go.mod")
	if err != nil {
		return releasedGenerationBuild{}, fmt.Errorf("read released peer go.mod: %w", err)
	}
	for _, forbidden := range []string{"replace ", "exclude ", "retract "} {
		if strings.Contains(string(materializedGoMod), forbidden) {
			return releasedGenerationBuild{}, fmt.Errorf("released peer go.mod contains forbidden %q", strings.TrimSpace(forbidden))
		}
	}
	peer := filepath.Join(builder.workspace, "peer-"+generation.Role+builder.executableSuffix())
	fixture := filepath.Join(builder.workspace, "plugin-fixture-"+generation.Role+builder.executableSuffix())
	offlineEnvironment := map[string]string{
		"GOCACHE": buildCache, "GOMODCACHE": moduleCache, "GOPROXY": "off",
		"GOSUMDB": "sum.golang.org", "GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": "",
		"GONOSUMDB": "", "GONOPROXY": "", "GOPRIVATE": "", "GOVCS": "public:git|https,private:off",
	}
	if err = command(ctx, moduleDirectory, offlineEnvironment, "go", "mod", "tidy", "-diff"); err != nil {
		return releasedGenerationBuild{}, err
	}
	if err = command(ctx, moduleDirectory, offlineEnvironment, "go", "build", "-trimpath", "-mod=vendor", "-o", peer, "."); err != nil {
		return releasedGenerationBuild{}, err
	}
	if err = command(
		ctx,
		moduleDirectory,
		offlineEnvironment,
		"go",
		"build",
		"-trimpath",
		"-mod=mod",
		"-o",
		fixture,
		modulePath+"/cmd/spice-agent-plugin-fixture",
	); err != nil {
		return releasedGenerationBuild{}, err
	}
	return releasedGenerationBuild{Generation: generation, Peer: peer, Fixture: fixture}, nil
}

func (builder *releasedGenerationBuilder) copyPeerSource(destination string) error {
	source := filepath.Join(builder.repository, "internal", "releasedcompatibility", "testdata", "peer")
	sourceRoot, err := os.OpenRoot(source)
	if err != nil {
		return fmt.Errorf("open released peer source: %w", err)
	}
	defer sourceRoot.Close() //nolint:errcheck // repository source ownership outlives this bounded copy.
	destinationRoot, err := os.OpenRoot(destination)
	if err != nil {
		return fmt.Errorf("open released peer destination: %w", err)
	}
	defer destinationRoot.Close() //nolint:errcheck // temporary workspace cleanup owns final removal.
	entries, err := os.ReadDir(source)
	if err != nil {
		return fmt.Errorf("read released peer source: %w", err)
	}
	if len(entries) != 11 {
		return errors.New("released peer source file count is invalid")
	}
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			return fmt.Errorf("inspect released peer source: %w", infoErr)
		}
		if entry.IsDir() || info.Mode()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".go") {
			return errors.New("released peer source contains an unsupported entry")
		}
		content, readErr := sourceRoot.ReadFile(entry.Name())
		if readErr != nil {
			return fmt.Errorf("read released peer source: %w", readErr)
		}
		if writeErr := destinationRoot.WriteFile(entry.Name(), content, 0o600); writeErr != nil {
			return fmt.Errorf("write released peer source: %w", writeErr)
		}
	}
	return nil
}

func (builder *releasedGenerationBuilder) validateDownload(
	encoded string,
	generation releasedGeneration,
) error {
	var download releasedModuleDownload
	decoder := json.NewDecoder(bytes.NewBufferString(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&download); err != nil {
		return fmt.Errorf("decode released module download: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("released module download contains trailing JSON")
	}
	if download.Path != modulePath || download.Version != generation.Version || download.Sum != generation.ModuleSum ||
		download.GoModSum != generation.GoModSum || download.Origin.VCS != "git" ||
		download.Origin.URL != "https://github.com/spice-framework/spice-agent" || download.Origin.Hash != generation.Commit ||
		download.Origin.Ref != "refs/tags/"+generation.Version {
		return errors.New("released module download identity differs from the manifest")
	}
	return nil
}

func (builder *releasedGenerationBuilder) executableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
