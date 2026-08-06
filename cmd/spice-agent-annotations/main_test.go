package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/spice-framework/spice-agent/annotation/agenttool"
	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/spice/annotation/sdk/protocol"
)

func TestRunServesOnlyFramedProtocolOnStdout(t *testing.T) {
	requests := new(bytes.Buffer)
	writeRequest(t, requests, 1, "initialize", protocol.InitializeParams{
		Protocol: sdk.ProtocolV1Alpha2,
		ToolPath: agenttool.Path,
	})
	writeRequest(t, requests, 2, "describe", protocol.DescribeParams{})
	name, _ := json.Marshal("read")
	writeRequest(t, requests, 3, "analyze", protocol.AnalyzeParams{
		Descriptor: sdk.Symbol{Package: "github.com/spice-framework/spice-agent/annotation/agent", Name: "Tool"},
		Invocation: sdk.Invocation{
			DescriptorPackage: "github.com/spice-framework/spice-agent/annotation/agent",
			DescriptorSymbol:  "Tool",
			CanonicalName:     "agent.Tool",
			Arguments:         []sdk.InvocationArgument{{Name: "name", Kind: sdk.KindString, Value: name}},
			Declaration: sdk.Declaration{
				Target: sdk.TargetFunction, SymbolID: "example.com/app.NewRead", Name: "NewRead",
				PackagePath: "example.com/app", TypeID: "func() github.com/spice-framework/spice-agent/tool.Tool",
			},
			Facts: map[string]string{"symbol_kind": "function"},
		},
	})
	writeRequest(t, requests, 4, "shutdown", protocol.ShutdownParams{})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	if code := run([]string{"spice-agent-annotations", "--spice-stdio"}, requests, stdout, stderr); code != 0 {
		t.Fatalf("run code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("successful stderr = %q", stderr.String())
	}
	reader := bufio.NewReader(stdout)
	for id := uint64(1); id <= 4; id++ {
		var response protocol.Response
		if err := protocol.ReadMessage(reader, &response); err != nil {
			t.Fatal(err)
		}
		if response.JSONRPC != "2.0" || response.ID != id || response.Error != nil || len(response.Result) == 0 {
			t.Fatalf("response %d = %#v", id, response)
		}
	}
	if trailing, err := io.ReadAll(reader); err != nil || len(trailing) != 0 {
		t.Fatalf("unframed stdout = %q, %v", trailing, err)
	}
}

func TestRunRejectsWrongModeAndProtocolFailureWithoutStdoutNoise(t *testing.T) {
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	if code := run([]string{"spice-agent-annotations"}, bytes.NewReader(nil), stdout, stderr); code != 2 {
		t.Fatalf("wrong-mode code = %d", code)
	}
	if stdout.Len() != 0 || !bytes.Contains(stderr.Bytes(), []byte("requires --spice-stdio")) {
		t.Fatalf("wrong-mode stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"spice-agent-annotations", "--spice-stdio"}, bytes.NewBufferString("Content-Length: 1\r\n\r\n{"), stdout, stderr); code != 1 {
		t.Fatalf("invalid-protocol code = %d", code)
	}
	if stdout.Len() != 0 || !bytes.Contains(stderr.Bytes(), []byte("spice-agent-annotations:")) {
		t.Fatalf("invalid-protocol stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if code := run([]string{}, nil, io.Discard, errorWriter{}); code != 1 {
		t.Fatalf("stderr failure code = %d", code)
	}
}

func writeRequest(t *testing.T, destination io.Writer, id uint64, method string, params any) {
	t.Helper()
	content, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if err = protocol.WriteMessage(destination, protocol.Request{
		JSONRPC: "2.0", ID: id, Method: method, Params: content,
	}); err != nil {
		t.Fatal(err)
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
