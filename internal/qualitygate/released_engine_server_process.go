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

type releasedEngineServerProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
	reader  *bufio.Reader
	stderr  bytes.Buffer
	waited  bool
}

func newReleasedEngineServerProcess(
	ctx context.Context,
	executable string,
) (*releasedEngineServerProcess, error) {
	process := &releasedEngineServerProcess{}
	process.command = exec.CommandContext(ctx, executable, "engine-server") // #nosec G204 -- exact digest-verified public-release build path.
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

func (process *releasedEngineServerProcess) Configure(
	ctx context.Context,
	scope *releasedProcessScope,
	authorization string,
) error {
	configuration := map[string]string{
		"address": scope.Address(), "authorization": authorization,
		"authority_directory": scope.AuthorityDirectory(),
	}
	if err := json.NewEncoder(process.input).Encode(configuration); err != nil {
		return err
	}
	ready, err := process.readLine(ctx)
	if err != nil {
		return fmt.Errorf(
			"read released engine server readiness: %w; stderr=%q",
			err,
			process.redactedStderr(authorization),
		)
	}
	if string(ready) != "{\"ready\":true}\n" {
		return errors.New("released engine server readiness is invalid")
	}
	return nil
}

func (process *releasedEngineServerProcess) redactedStderr(secret string) string {
	return strings.ReplaceAll(process.stderr.String(), secret, "[redacted]")
}

func (process *releasedEngineServerProcess) Stop() ([]byte, error) {
	if _, err := io.WriteString(process.input, "STOP\n"); err != nil {
		return nil, err
	}
	if err := process.input.Close(); err != nil {
		return nil, err
	}
	remaining, err := io.ReadAll(process.reader)
	if err != nil {
		return nil, err
	}
	if err = process.command.Wait(); err != nil {
		return nil, fmt.Errorf("released engine server failed: %w; stderr=%q", err, process.stderr.String())
	}
	process.waited = true
	return remaining, nil
}

func (process *releasedEngineServerProcess) Contaminated(authorization string) bool {
	return process.stderr.Len() != 0 ||
		strings.Contains(strings.Join(process.command.Args, "\x00"), authorization) ||
		strings.Contains(process.stderr.String(), authorization)
}

func (process *releasedEngineServerProcess) Close() error {
	if process.waited || process.command.Process == nil {
		return nil
	}
	_ = process.input.Close()
	killErr := process.command.Process.Kill()
	waitErr := process.command.Wait()
	process.waited = true
	return errors.Join(killErr, waitErr)
}

func (process *releasedEngineServerProcess) readLine(ctx context.Context) ([]byte, error) {
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
