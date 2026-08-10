package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

const engineProtocolCompatibilityPath = "engine/v1/compatibility.json"

type engineProtocolCompatibility struct {
	Schema                  string                           `json:"schema"`
	Protocol                string                           `json:"protocol"`
	ProductionRange         protocolCompatibilityRange       `json:"production_range"`
	InitializationModes     []protocolInitializationMode     `json:"initialization_modes"`
	SourceBuiltMatrix       []protocolSourceBuiltMatrixEntry `json:"source_built_matrix"`
	RequiredCases           []string                         `json:"required_cases"`
	ReleasedBinaryMatrix    releasedBinaryMatrix             `json:"released_binary_matrix"`
	PluginBreadthMatrix     pluginBreadthMatrix              `json:"plugin_breadth_matrix"`
	PluginProtocolNextSlice bool                             `json:"plugin_protocol_next_slice"`
}

type protocolCompatibilityRange struct {
	Minimum string `json:"minimum"`
	Maximum string `json:"maximum"`
}

type protocolInitializationMode struct {
	Name                        string `json:"name"`
	Minimum                     string `json:"minimum"`
	Maximum                     string `json:"maximum"`
	AttemptID                   string `json:"attempt_id"`
	AutomaticUnavailableRetries int    `json:"automatic_unavailable_retries"`
	AmbiguousOutcome            string `json:"ambiguous_outcome"`
}

type protocolSourceBuiltMatrixEntry struct {
	Peer        string   `json:"peer"`
	ServerRange string   `json:"server_range"`
	ClientMode  string   `json:"client_mode"`
	Platforms   []string `json:"platforms"`
}

type releasedBinaryMatrix struct {
	Proven bool   `json:"proven"`
	Claim  string `json:"claim"`
}

type pluginBreadthMatrix struct {
	Protocol             string                     `json:"protocol"`
	ProtocolVersion      string                     `json:"protocol_version"`
	Versioning           string                     `json:"versioning"`
	Bridge               string                     `json:"bridge"`
	EngineModes          []string                   `json:"engine_modes"`
	Languages            []string                   `json:"languages"`
	RequiredCases        []string                   `json:"required_cases"`
	ProductionHostLaunch pluginProductionHostLaunch `json:"production_host_launch"`
}

type pluginProductionHostLaunch struct {
	Go     string `json:"go"`
	Python string `json:"python"`
}

func expectedEngineProtocolCompatibility() engineProtocolCompatibility {
	platforms := []string{"linux/amd64", "windows/amd64"}
	return engineProtocolCompatibility{
		Schema:   "spice.agent.engine.compatibility/v1alpha1",
		Protocol: "spice.agent.engine.v1",
		ProductionRange: protocolCompatibilityRange{
			Minimum: "1.0.0", Maximum: "1.3.0",
		},
		InitializationModes: []protocolInitializationMode{
			{
				Name: "legacy", Minimum: "1.0.0", Maximum: "1.2.0", AttemptID: "forbidden",
				AutomaticUnavailableRetries: 0, AmbiguousOutcome: "non-retryable",
			},
			{
				Name: "exact-replay", Minimum: "1.3.0", Maximum: "1.3.0", AttemptID: "required",
				AutomaticUnavailableRetries: 1, AmbiguousOutcome: "same-request-and-attempt-only",
			},
		},
		SourceBuiltMatrix: []protocolSourceBuiltMatrixEntry{
			{
				Peer: "previous-semantics", ServerRange: "1.0.0-1.2.0", ClientMode: "legacy",
				Platforms: append([]string(nil), platforms...),
			},
			{
				Peer: "current", ServerRange: "1.0.0-1.3.0", ClientMode: "exact-replay",
				Platforms: append([]string(nil), platforms...),
			},
		},
		RequiredCases: []string{
			"exact-legacy-1.2",
			"adaptive-current-1.3",
			"explicit-proven-downgrade",
			"authentication-definitive",
			"current-exact-replay-after-response-loss",
			"legacy-ambiguity-never-retries",
			"cancellation-conflict-exact-recovery",
			"process-cleanup",
		},
		ReleasedBinaryMatrix: releasedBinaryMatrix{Proven: false, Claim: "not-claimed"},
		PluginBreadthMatrix: pluginBreadthMatrix{
			Protocol:        "spice.agent.plugin.v1",
			ProtocolVersion: "1.0.0",
			Versioning:      "independent-from-engine",
			Bridge:          "real-process-plugin-v1-to-immutable-run-leased-tool-plan",
			EngineModes:     []string{"1.2.0", "1.3.0"},
			Languages:       []string{"go", "python"},
			RequiredCases: []string{
				"identical-tool-result",
				"cancellation-terminal-events",
				"fixture-process-loss",
				"generation-lease-cleanup",
			},
			ProductionHostLaunch: pluginProductionHostLaunch{
				Go:     "separately-proven",
				Python: "future-pinned-native-artifact-required",
			},
		},
		PluginProtocolNextSlice: false,
	}
}

func checkEngineProtocolCompatibility(root string) error {
	path := filepath.Join(root, filepath.FromSlash(engineProtocolCompatibilityPath))
	content, err := os.ReadFile(path) // #nosec G304 -- fixed repository compatibility path.
	if err != nil {
		return fmt.Errorf("read engine protocol compatibility manifest: %w", err)
	}
	actual, err := decodeEngineProtocolCompatibility(content)
	if err != nil {
		return err
	}
	want := expectedEngineProtocolCompatibility()
	if !reflect.DeepEqual(actual, want) {
		return errors.New("engine protocol compatibility manifest differs from the reviewed contract")
	}
	canonical, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		return fmt.Errorf("encode engine protocol compatibility contract: %w", err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(content, canonical) {
		return errors.New("engine protocol compatibility manifest is not canonical JSON")
	}
	return nil
}

func decodeEngineProtocolCompatibility(content []byte) (engineProtocolCompatibility, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var value engineProtocolCompatibility
	if err := decoder.Decode(&value); err != nil {
		return engineProtocolCompatibility{}, fmt.Errorf("decode engine protocol compatibility manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return engineProtocolCompatibility{}, errors.New("engine protocol compatibility manifest contains multiple JSON values")
		}
		return engineProtocolCompatibility{}, fmt.Errorf("decode engine protocol compatibility trailing data: %w", err)
	}
	return value, nil
}
