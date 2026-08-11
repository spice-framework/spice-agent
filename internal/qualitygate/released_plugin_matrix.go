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

type releasedPluginMatrix struct {
	direction releasedGenerationDirection
	fixture   releasedGenerationBuild
	client    releasedGenerationBuild
	output    io.Writer
}

func newReleasedPluginMatrix(
	direction releasedGenerationDirection,
	fixture releasedGenerationBuild,
	client releasedGenerationBuild,
	output io.Writer,
) (*releasedPluginMatrix, error) {
	if direction.Peer != fixture.Generation.Role || direction.Client != client.Generation.Role ||
		fixture.Fixture == "" || client.Peer == "" {
		return nil, errors.New("released plugin matrix direction is inconsistent")
	}
	if output == nil {
		return nil, errors.New("released plugin matrix output is required")
	}
	return &releasedPluginMatrix{
		direction: direction, fixture: fixture, client: client, output: output,
	}, nil
}

func (matrix *releasedPluginMatrix) Run(ctx context.Context) error {
	scope, err := newReleasedProcessScope("plugin")
	if err != nil {
		return err
	}
	defer scope.Close() //nolint:errcheck // cleanup cannot change a proved process result.
	secret, err := scope.Secret()
	if err != nil {
		return err
	}
	fixture, err := newReleasedPluginFixtureProcess(ctx, matrix.fixture.Fixture)
	if err != nil {
		return err
	}
	defer fixture.Close() //nolint:errcheck // cleanup cannot change a proved process result.
	if err = fixture.Configure(ctx, scope, secret); err != nil {
		return err
	}
	clientOutput, clientError, err := matrix.runClient(ctx, scope, secret)
	if err != nil {
		return err
	}
	if err = matrix.validateClientResult(clientOutput); err != nil {
		return err
	}
	remaining, err := fixture.Stop()
	if err != nil {
		return err
	}
	if len(remaining) != 0 || len(clientError) != 0 || fixture.Contaminated(secret) {
		return errors.New("released plugin peers contaminated output or exposed the handshake secret")
	}
	if _, err = fmt.Fprintf(
		matrix.output,
		"    plugin %s: client %s -> fixture %s (%s)\n",
		matrix.direction.ID,
		matrix.client.Generation.Version,
		matrix.fixture.Generation.Version,
		"1.0.0",
	); err != nil {
		return fmt.Errorf("write released plugin matrix status: %w", err)
	}
	return nil
}

func (matrix *releasedPluginMatrix) runClient(
	ctx context.Context,
	scope *releasedProcessScope,
	secret string,
) ([]byte, []byte, error) {
	clientValue := exec.CommandContext(ctx, matrix.client.Peer, "plugin-client") // #nosec G204 -- exact digest-verified public-release build path.
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
		"address": scope.Address(), "secret": secret,
	}); err != nil {
		return nil, nil, err
	}
	if err = clientInput.Close(); err != nil {
		return nil, nil, err
	}
	if err = clientValue.Wait(); err != nil {
		return nil, nil, fmt.Errorf("released plugin client failed: %w; stderr=%q", err, clientError.String())
	}
	if strings.Contains(clientError.String(), secret) {
		return nil, nil, errors.New("released plugin client exposed the handshake secret")
	}
	return clientOutput.Bytes(), clientError.Bytes(), nil
}

func (matrix *releasedPluginMatrix) validateClientResult(encoded []byte) error {
	var result struct {
		Protocol   string `json:"protocol"`
		Conformant bool   `json:"conformant"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return fmt.Errorf("decode released plugin result: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("released plugin result contains trailing JSON")
	}
	if result.Protocol != "1.0.0" || !result.Conformant {
		return errors.New("released plugin result differs from the required contract")
	}
	return nil
}
