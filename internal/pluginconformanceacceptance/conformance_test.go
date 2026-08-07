package pluginconformanceacceptance_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/daemon/localipc"
	"github.com/spice-framework/spice-agent/plugin/conformance"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"github.com/spice-framework/spice-agent/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const acceptanceTimeout = 30 * time.Second

func TestIndependentGoFixturePassesPublicConformance(t *testing.T) {
	root := repositoryRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), acceptanceTimeout)
	defer cancel()
	executable := buildFixture(t, ctx, root)
	address := fixtureAddress(t)
	secret := bytes.Repeat([]byte{0x73}, pluginv1.HandshakeSecretBytes)

	command := exec.CommandContext(ctx, executable) // #nosec G204 -- exact repository-built fixture.
	command.Dir = root
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	bootstrap := map[string]string{
		"address": address,
		"secret":  base64.RawURLEncoding.EncodeToString(secret),
	}
	if err = json.NewEncoder(stdin).Encode(bootstrap); err != nil {
		t.Fatal(err)
	}
	if err = stdin.Close(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	ready := make(chan struct {
		line string
		err  error
	}, 1)
	go func() {
		line, readErr := reader.ReadString('\n')
		ready <- struct {
			line string
			err  error
		}{line: line, err: readErr}
	}()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case result := <-ready:
		if result.err != nil || result.line != "{\"ready\":true}\n" {
			t.Fatalf("fixture readiness = %q, %v; stderr=%q", result.line, result.err, stderr.String())
		}
	}
	tailResult := captureOutput(reader, 1024)

	connection, err := grpc.NewClient(
		"passthrough:///spice-agent-plugin-conformance",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(dialContext context.Context, _ string) (net.Conn, error) {
			return localipc.Dial(dialContext, address)
		}),
		grpc.WithNoProxy(),
		grpc.WithDisableRetry(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(pluginv1.InitializeBootstrapMaximumBytes),
			grpc.MaxCallSendMsgSize(pluginv1.InitializeBootstrapMaximumBytes),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	client := pluginv1.NewPluginServiceClient(connection)
	err = conformance.Run(ctx, client, conformance.Config{
		HostBuild: &pluginv1.BuildIdentity{
			Component: "spice-agent-conformance-host",
			Version:   "v1",
			Commit:    "fixture-test",
			Runtime:   runtime.Version(),
		},
		Limits:           conformanceLimits(),
		Secret:           secret,
		OperationTimeout: 3 * time.Second,
	})
	closeErr := connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if err = command.Wait(); err != nil {
		t.Fatalf("fixture exit: %v; stderr=%q", err, stderr.String())
	}
	waited = true
	tail := <-tailResult
	if tail.err != nil && !errors.Is(tail.err, os.ErrClosed) {
		t.Fatal(tail.err)
	}
	if len(tail.value) != 0 {
		t.Fatalf("fixture contaminated stdout after readiness: %q", tail.value)
	}
	encodedSecret := base64.RawURLEncoding.EncodeToString(secret)
	if strings.Contains(stderr.String(), encodedSecret) || strings.Contains(strings.Join(command.Args, "\x00"), encodedSecret) {
		t.Fatal("fixture exposed the launch secret outside its private stdin bootstrap")
	}
}

type outputCapture struct {
	value []byte
	err   error
}

func captureOutput(reader io.Reader, limit int) <-chan outputCapture {
	result := make(chan outputCapture, 1)
	go func() {
		captured := make([]byte, 0, limit+1)
		buffer := make([]byte, 4096)
		for {
			read, err := reader.Read(buffer)
			if read > 0 && len(captured) <= limit {
				remaining := limit + 1 - len(captured)
				captured = append(captured, buffer[:min(read, remaining)]...)
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					err = nil
				}
				result <- outputCapture{value: captured, err: err}
				return
			}
		}
	}()
	return result
}

func buildFixture(t *testing.T, ctx context.Context, root string) string {
	t.Helper()
	name := "spice-agent-plugin-fixture"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	command := exec.CommandContext(
		ctx,
		"go",
		"build",
		"-trimpath",
		"-o",
		path,
		"./cmd/spice-agent-plugin-fixture",
	) // #nosec G204 -- fixed Go build of the repository fixture.
	command.Dir = root
	command.Env = append(
		os.Environ(),
		"GOPROXY=off",
		"GOWORK=off",
		"GOTOOLCHAIN=local",
		"GOFLAGS=-mod=vendor",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
	}
	return path
}

func fixtureAddress(t *testing.T) string {
	t.Helper()
	random := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, random); err != nil {
		t.Fatal(err)
	}
	name := "spice-agent-plugin-fixture-" + hex.EncodeToString(random)
	if runtime.GOOS == "windows" {
		return `\\.\pipe\` + name
	}
	directory, err := os.MkdirTemp("", "spice-pf-")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
			t.Errorf("remove fixture directory: %v", cleanupErr)
		}
	})
	return filepath.Join(directory, name+".sock")
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		content, readErr := os.ReadFile(filepath.Join(current, "go.mod")) // #nosec G304 -- bounded ancestors.
		if readErr == nil && bytes.Contains(content, []byte("module github.com/spice-framework/spice-agent")) {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatal("repository root not found")
		}
		current = parent
	}
}

func conformanceLimits() *pluginv1.Limits {
	return &pluginv1.Limits{
		MaxMessageBytes:      pluginv1.InitializeBootstrapMaximumBytes,
		MaxTools:             16,
		MaxSchemaBytes:       64 << 10,
		MaxCallArgumentBytes: 64 << 10,
		MaxResultBytes:       pluginv1.InitializeBootstrapMaximumBytes,
		MaxProgressBytes:     tool.MaximumProgressBytes,
		MaxConcurrentCalls:   8,
	}
}
