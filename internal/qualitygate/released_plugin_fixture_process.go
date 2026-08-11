package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type releasedPluginFixtureProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
	reader  *bufio.Reader
	stderr  bytes.Buffer
	waited  bool
}

func newReleasedPluginFixtureProcess(
	ctx context.Context,
	executable string,
) (*releasedPluginFixtureProcess, error) {
	process := &releasedPluginFixtureProcess{}
	process.command = exec.CommandContext(ctx, executable) // #nosec G204 -- exact SumDB-verified public-release fixture path.
	input, err := process.command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := process.command.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	process.input = input
	process.reader = bufio.NewReader(output)
	process.command.Stderr = &process.stderr
	if err = process.command.Start(); err != nil {
		_ = input.Close()
		return nil, err
	}
	return process, nil
}

func (process *releasedPluginFixtureProcess) Configure(
	ctx context.Context,
	scope *releasedProcessScope,
	secret string,
) error {
	if err := json.NewEncoder(process.input).Encode(map[string]string{
		"address": scope.Address(), "secret": secret,
	}); err != nil {
		return err
	}
	if err := process.input.Close(); err != nil {
		return err
	}
	ready, err := process.readLine(ctx)
	if err != nil {
		return fmt.Errorf(
			"read released plugin fixture readiness: %w; stderr=%q",
			err,
			process.redactedStderr(secret),
		)
	}
	if string(ready) != "{\"ready\":true}\n" {
		return errors.New("released plugin fixture readiness is invalid")
	}
	return nil
}

func (process *releasedPluginFixtureProcess) redactedStderr(secret string) string {
	return strings.ReplaceAll(process.stderr.String(), secret, "[redacted]")
}

func (process *releasedPluginFixtureProcess) Stop() ([]byte, error) {
	remaining, err := io.ReadAll(process.reader)
	if err != nil {
		return nil, err
	}
	if err = process.command.Wait(); err != nil {
		return nil, fmt.Errorf("released plugin fixture failed: %w; stderr=%q", err, process.stderr.String())
	}
	process.waited = true
	return remaining, nil
}

func (process *releasedPluginFixtureProcess) Contaminated(secret string) bool {
	return process.stderr.Len() != 0 ||
		strings.Contains(strings.Join(process.command.Args, "\x00"), secret) ||
		strings.Contains(process.stderr.String(), secret)
}

func (process *releasedPluginFixtureProcess) Close() error {
	if process.waited || process.command.Process == nil {
		return nil
	}
	_ = process.input.Close()
	killErr := process.command.Process.Kill()
	waitErr := process.command.Wait()
	process.waited = true
	return errors.Join(killErr, waitErr)
}

func (process *releasedPluginFixtureProcess) readLine(ctx context.Context) ([]byte, error) {
	type lineResult struct {
		line []byte
		err  error
	}
	result := make(chan lineResult, 1)
	go func() {
		line, err := process.reader.ReadBytes('\n')
		result <- lineResult{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case observed := <-result:
		return observed.line, observed.err
	}
}
