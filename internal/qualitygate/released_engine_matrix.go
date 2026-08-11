package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type releasedEngineMatrix struct {
	direction releasedGenerationDirection
	server    releasedGenerationBuild
	client    releasedGenerationBuild
	output    io.Writer
}

func newReleasedEngineMatrix(
	direction releasedGenerationDirection,
	server releasedGenerationBuild,
	client releasedGenerationBuild,
	output io.Writer,
) (*releasedEngineMatrix, error) {
	if direction.Peer != server.Generation.Role || direction.Client != client.Generation.Role ||
		server.Peer == "" || client.Peer == "" {
		return nil, errors.New("released engine matrix direction is inconsistent")
	}
	if output == nil {
		return nil, errors.New("released engine matrix output is required")
	}
	return &releasedEngineMatrix{
		direction: direction, server: server, client: client, output: output,
	}, nil
}

func (matrix *releasedEngineMatrix) Run(ctx context.Context) error {
	scope, err := newReleasedProcessScope("engine")
	if err != nil {
		return err
	}
	defer scope.Close() //nolint:errcheck // cleanup cannot change a proved process result.
	authorization, err := scope.Authorization()
	if err != nil {
		return err
	}
	server, err := newReleasedEngineServerProcess(ctx, matrix.server.Peer)
	if err != nil {
		return err
	}
	defer server.Close() //nolint:errcheck // cleanup cannot change a proved process result.
	if err = server.Configure(ctx, scope, authorization); err != nil {
		return err
	}
	clientOutput, clientError, err := matrix.runClient(ctx, scope, authorization)
	if err != nil {
		return err
	}
	if err = matrix.validateClientResult(clientOutput); err != nil {
		return err
	}
	remaining, err := server.Stop()
	if err != nil {
		return err
	}
	if len(remaining) != 0 || len(clientError) != 0 || server.Contaminated(authorization) {
		return errors.New("released engine peers contaminated output or exposed authorization")
	}
	if _, err = fmt.Fprintf(
		matrix.output,
		"    engine %s: client %s -> server %s (%s)\n",
		matrix.direction.ID,
		matrix.client.Generation.Version,
		matrix.server.Generation.Version,
		"1.3.0",
	); err != nil {
		return fmt.Errorf("write released engine matrix status: %w", err)
	}
	return nil
}

func (matrix *releasedEngineMatrix) runClient(
	ctx context.Context,
	scope *releasedProcessScope,
	authorization string,
) ([]byte, []byte, error) {
	clientValue := exec.CommandContext(ctx, matrix.client.Peer, "engine-client") // #nosec G204 -- exact digest-verified public-release build path.
	clientInput, err := clientValue.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	var clientOutput bytes.Buffer
	var clientError bytes.Buffer
	clientValue.Stdout = &clientOutput
	clientValue.Stderr = &clientError
	if err = clientValue.Start(); err != nil {
		return nil, nil, err
	}
	if err = json.NewEncoder(clientInput).Encode(map[string]string{
		"address": scope.Address(), "authorization": authorization,
	}); err != nil {
		return nil, nil, err
	}
	if err = clientInput.Close(); err != nil {
		return nil, nil, err
	}
	if err = clientValue.Wait(); err != nil {
		return nil, nil, fmt.Errorf("released engine client failed: %w; stderr=%q", err, clientError.String())
	}
	if strings.Contains(clientError.String(), authorization) {
		return nil, nil, errors.New("released engine client exposed authorization")
	}
	return clientOutput.Bytes(), clientError.Bytes(), nil
}

func (matrix *releasedEngineMatrix) validateClientResult(encoded []byte) error {
	var result struct {
		Protocol             string `json:"protocol"`
		WrongTokenRejected   bool   `json:"wrong_token_rejected"`
		CompletedText        string `json:"completed_text"`
		CancellationTerminal string `json:"cancellation_terminal"`
		ActiveRuns           uint64 `json:"active_runs"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("decode released engine result: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("released engine result contains trailing JSON")
	}
	if result.Protocol != "1.3.0" || !result.WrongTokenRejected ||
		result.CompletedText != "released peer handled: hello" || result.CancellationTerminal != "cancelled" ||
		result.ActiveRuns != 0 {
		return errors.New("released engine result differs from the required contract")
	}
	return nil
}
