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

	"github.com/spice-framework/spice-agent/plugin/conformance"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const pythonConformanceEnvironment = "SPICE_AGENT_PYTHON_CONFORMANCE"

func TestIndependentPythonFixturePassesPublicConformance(t *testing.T) {
	if os.Getenv(pythonConformanceEnvironment) != "1" {
		t.Skip("set " + pythonConformanceEnvironment + "=1 to run the Python runtime-plugin acceptance")
	}
	root := repositoryRoot(t)
	fixture := filepath.Join(root, "testdata", "runtimeplugin", "python")
	uv, err := exec.LookPath("uv")
	if err != nil {
		t.Fatal("Python runtime-plugin acceptance requires uv on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), acceptanceTimeout)
	defer cancel()
	address := pythonFixtureAddress(t)
	secret := bytes.Repeat([]byte{0x79}, pluginv1.HandshakeSecretBytes)
	command := exec.CommandContext( // #nosec G204 -- exact uv executable and fixed fixture command.
		ctx,
		uv,
		"run",
		"--frozen",
		"--offline",
		"--directory",
		fixture,
		"python",
		"-m",
		"spice_agent_python_fixture.main",
	)
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
			t.Fatalf("Python fixture readiness = %q, %v", result.line, result.err)
		}
	}
	tailResult := captureOutput(reader, 1024)

	connection, err := grpc.NewClient(
		"passthrough:///spice-agent-python-plugin-conformance",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(dialContext context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(dialContext, "unix", address)
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
	err = conformance.Run(ctx, pluginv1.NewPluginServiceClient(connection), conformance.Config{
		HostBuild: &pluginv1.BuildIdentity{
			Component: "spice-agent-python-conformance-host",
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
		t.Fatalf("Python fixture exit: %v; stderr=%q", err, stderr.String())
	}
	waited = true
	tail := <-tailResult
	if tail.err != nil && !errors.Is(tail.err, os.ErrClosed) {
		t.Fatal(tail.err)
	}
	if len(tail.value) != 0 {
		t.Fatalf("Python fixture contaminated stdout after readiness: %q", tail.value)
	}
	encodedSecret := base64.RawURLEncoding.EncodeToString(secret)
	if strings.Contains(stderr.String(), encodedSecret) || strings.Contains(strings.Join(command.Args, "\x00"), encodedSecret) {
		t.Fatal("Python fixture exposed the launch secret outside its private stdin bootstrap")
	}
}

func pythonFixtureAddress(t *testing.T) string {
	t.Helper()
	random := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, random); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp("", "spice-py-")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
			t.Errorf("remove Python fixture directory: %v", cleanupErr)
		}
	})
	realDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	address := filepath.Join(realDirectory, "plugin-"+hex.EncodeToString(random)+".sock")
	if !filepath.IsAbs(address) {
		t.Fatal("Python fixture socket path is not absolute")
	}
	return address
}
