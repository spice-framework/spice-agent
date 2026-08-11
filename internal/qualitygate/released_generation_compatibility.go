package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type releasedGenerationCompatibility struct {
	Schema             string                           `json:"schema"`
	Module             string                           `json:"module"`
	Status             string                           `json:"status"`
	Go                 string                           `json:"go"`
	BuildSource        string                           `json:"build_source"`
	Isolation          releasedGenerationIsolation      `json:"isolation"`
	Generations        []releasedGeneration             `json:"generations"`
	Platforms          []string                         `json:"platforms"`
	Engine             releasedProtocolGenerationMatrix `json:"engine"`
	Plugin             releasedProtocolGenerationMatrix `json:"plugin"`
	RunnerSource       string                           `json:"runner_source"`
	RunnerSourceSHA256 string                           `json:"runner_source_sha256"`
	Evidence           releasedGenerationEvidence       `json:"evidence"`
	Proven             bool                             `json:"proven"`
}

func (compatibility releasedGenerationCompatibility) ValidateProven() error {
	wantGenerations := []releasedGeneration{
		{
			Role: "previous", Version: "v0.1.0-preview.5", Commit: "3e8fe6406171a7e7f1765311a4fa7fc3b878e425",
			ModuleSum: "h1:rGND9DYx3pssliD1tZQOvPDOZ5GVfQLDc7VJQI3HLOM=", GoModSum: "h1:pbhYOeNgn4pCIhEmcdbjnFjJijY4ZSLM8ZHxaF2dxz0=",
		},
		{
			Role: "current", Version: "v0.1.0-preview.6", Commit: "f771caa3b150d87845417c4e26938e2a889441a6",
			ModuleSum: "h1:XJKJge+xWP/FLNoL1/rXq8z8tdu/5iEkKfmu1dTgFms=", GoModSum: "h1:pbhYOeNgn4pCIhEmcdbjnFjJijY4ZSLM8ZHxaF2dxz0=",
		},
	}
	wantIsolation := releasedGenerationIsolation{
		GOWorkOff: true, FreshModuleCaches: true, NoReplace: true, PublicProxy: true, PublicSumDB: true,
		PeerVendorOfflineBuild: true, FixtureModuleCacheOffline: true,
	}
	wantEngine := releasedProtocolGenerationMatrix{
		Protocol: "1.3.0", PeerKind: "engine-server",
		Directions: []releasedGenerationDirection{
			{ID: "preview5-client-to-preview6-server", Client: "previous", Peer: "current"},
			{ID: "preview6-client-to-preview5-server", Client: "current", Peer: "previous"},
		},
		RequiredCases: []string{"active-run-drain", "authenticated-initialize", "cancellation-terminal", "model-delta-and-run-completion", "process-cleanup", "wrong-token-refusal"},
	}
	wantPlugin := releasedProtocolGenerationMatrix{
		Protocol: "1.0.0", PeerKind: "plugin-fixture",
		Directions: []releasedGenerationDirection{
			{ID: "preview5-client-to-preview6-fixture", Client: "previous", Peer: "current"},
			{ID: "preview6-client-to-preview5-fixture", Client: "current", Peer: "previous"},
		},
		RequiredCases: []string{"authenticated-transcript", "cancellation-and-drain", "exact-manifest", "malformed-and-oversized-refusal", "process-cleanup", "shutdown", "typed-failure"},
	}
	if compatibility.Schema != "spice.agent.released-generation.compatibility/v1alpha1" ||
		compatibility.Module != modulePath || compatibility.Status != "hosted-linux-windows-proven" ||
		compatibility.Go != requiredGoVersion || compatibility.BuildSource != "public-proxy-and-sumdb" ||
		compatibility.Isolation != wantIsolation || !slices.Equal(compatibility.Generations, wantGenerations) ||
		!slices.Equal(compatibility.Platforms, []string{"linux/amd64", "windows/amd64"}) ||
		!compatibility.Engine.Equal(wantEngine) || !compatibility.Plugin.Equal(wantPlugin) ||
		compatibility.RunnerSource != "internal/releasedcompatibility/testdata/peer" || len(compatibility.RunnerSourceSHA256) != 64 ||
		compatibility.Evidence != (releasedGenerationEvidence{
			Workflow: "https://github.com/spice-framework/spice-agent/actions/workflows/released-compatibility.yml",
			Run:      31454312077,
			Commit:   "609f74f0abc7e3eba9f8a9ceab3c68ac17208ca2",
		}) || !compatibility.Proven {
		return errors.New("released-generation compatibility manifest differs from the reviewed hosted proof")
	}
	return nil
}

func (compatibility releasedGenerationCompatibility) ValidateSource(root string) error {
	directory := filepath.Join(root, filepath.FromSlash(compatibility.RunnerSource))
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read released compatibility peer source: %w", err)
	}
	digest := sha256.New()
	files := 0
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			return fmt.Errorf("inspect released compatibility peer source: %w", infoErr)
		}
		if entry.IsDir() || info.Mode()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".go") {
			return errors.New("released compatibility peer source contains an unsupported entry")
		}
		content, readErr := os.ReadFile(filepath.Join(directory, entry.Name())) // #nosec G304 -- validated repository-owned peer filename.
		if readErr != nil {
			return fmt.Errorf("read released compatibility peer file: %w", readErr)
		}
		_, _ = io.WriteString(digest, entry.Name())
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(content)
		_, _ = digest.Write([]byte{0})
		files++
	}
	if files != 11 || hex.EncodeToString(digest.Sum(nil)) != compatibility.RunnerSourceSHA256 {
		return errors.New("released compatibility peer source digest differs from the manifest")
	}
	return nil
}
