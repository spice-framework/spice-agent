package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestRemoteToolStreamsProgressAndTerminalOutcomesWithoutAliasing(t *testing.T) {
	definition := remoteDefinition(t, tool.EffectReadOnly)
	sessionID := bytes.Repeat([]byte{0x31}, pluginv1.SessionIDBytes)
	limits := remoteLimits(2)
	var observed *pluginv1.ExecuteRequest
	client := &fakePluginClient{execute: func(
		ctx context.Context,
		request *pluginv1.ExecuteRequest,
	) (pluginv1.PluginService_ExecuteClient, error) {
		observed, _ = proto.Clone(request).(*pluginv1.ExecuteRequest)
		return newFakeExecuteStream(
			ctx,
			remoteProgress("call-1", 1, "working"),
			remoteResult("call-1", 2, []byte(`{"ok":true}`), ""),
		), nil
	}}
	session, err := newRemoteSession(client, sessionID, limits, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	implementation, err := newRemoteTool(definition, session)
	if err != nil {
		t.Fatal(err)
	}

	sessionID[0] = 0xff
	limits.MaxConcurrentCalls = 99
	definitionSchema := definition.InputSchema()
	if len(definitionSchema) == 0 {
		t.Fatal("fixture definition has no input schema")
	}
	definitionSchema[0] = 'x'
	call, _ := tool.NewCall("call-1", "remote.read", []byte(`{"path":"README.md"}`))
	reporter := &recordingReporter{}
	result, err := implementation.Execute(t.Context(), call, reporter)
	if err != nil {
		t.Fatal(err)
	}
	if result.CallID() != call.ID() || string(result.Content()) != `{"ok":true}` {
		t.Fatalf("result = %#v", result)
	}
	if got := reporter.messages(); len(got) != 1 || got[0] != "working" {
		t.Fatalf("progress = %v", got)
	}
	if observed == nil {
		t.Fatal("remote request was not observed")
	}
	observedSession := observed.GetSessionId()
	if len(observedSession) != pluginv1.SessionIDBytes || observedSession[0] != 0x31 ||
		string(observed.GetArgumentsJson()) != `{"path":"README.md"}` {
		t.Fatalf("request was aliased or malformed: %v", observed)
	}
	if implementation.Definition().Fingerprint() != remoteDefinition(t, tool.EffectReadOnly).Fingerprint() ||
		cap(session.admission) != 2 {
		t.Fatal("remote session or definition was aliased")
	}
}

func TestRemoteToolPreservesValidatedTerminalFailure(t *testing.T) {
	client := &fakePluginClient{execute: streamFactory(
		remoteFailure("call-1", 1, pluginv1.ExecutionState_EXECUTION_STATE_UNCERTAIN,
			pluginv1.RetryDisposition_RETRY_DISPOSITION_NEVER, "commit acknowledgement was lost"),
	)}
	implementation := mustRemoteTool(t, client, tool.EffectMutating, 1, time.Second)
	call, _ := tool.NewCall("call-1", "remote.write", []byte(`{"value":1}`))
	result, err := implementation.Execute(t.Context(), call, nil)
	var failure *tool.ExecutionError
	if !result.IsZero() || !errors.As(err, &failure) ||
		failure.State() != tool.ExecutionUncertain ||
		failure.RetryDisposition() != tool.RetryNever ||
		failure.Error() != "commit acknowledgement was lost" {
		t.Fatalf("terminal failure = (%#v, %v)", result, err)
	}
	if client.calls.Load() != 1 {
		t.Fatalf("Execute calls = %d", client.calls.Load())
	}
}

func TestRemoteToolPreservesModelVisibleProblemResult(t *testing.T) {
	client := &fakePluginClient{execute: streamFactory(
		remoteResult("call-1", 1, []byte(`{"accepted":false}`), "request denied"),
	)}
	implementation := mustRemoteTool(t, client, tool.EffectReadOnly, 1, time.Second)
	call, _ := tool.NewCall("call-1", "remote.read", []byte(`{}`))
	result, err := implementation.Execute(t.Context(), call, nil)
	problem, failed := result.Problem()
	if err != nil || !failed || problem != "request denied" || string(result.Content()) != `{"accepted":false}` {
		t.Fatalf("problem result = (%#v, %v)", result, err)
	}
}

func TestRemoteToolClassifiesMalformedAndInterruptedStreamsByEffect(t *testing.T) {
	tests := []struct {
		name   string
		effect tool.Effect
		stream func(context.Context) pluginv1.PluginService_ExecuteClient
		state  tool.ExecutionState
	}{
		{"read-only missing terminal", tool.EffectReadOnly, func(ctx context.Context) pluginv1.PluginService_ExecuteClient {
			return newFakeExecuteStream(ctx)
		}, tool.ExecutionDefinitive},
		{"mutating missing terminal", tool.EffectMutating, func(ctx context.Context) pluginv1.PluginService_ExecuteClient {
			return newFakeExecuteStream(ctx)
		}, tool.ExecutionUncertain},
		{"wrong correlation", tool.EffectMutating, func(ctx context.Context) pluginv1.PluginService_ExecuteClient {
			return newFakeExecuteStream(ctx, remoteProgress("other", 1, "working"))
		}, tool.ExecutionUncertain},
		{"noncontiguous sequence", tool.EffectReadOnly, func(ctx context.Context) pluginv1.PluginService_ExecuteClient {
			return newFakeExecuteStream(ctx, remoteProgress("call-1", 2, "working"))
		}, tool.ExecutionDefinitive},
		{"post terminal", tool.EffectMutating, func(ctx context.Context) pluginv1.PluginService_ExecuteClient {
			return newFakeExecuteStream(
				ctx,
				remoteResult("call-1", 1, []byte(`{}`), ""),
				remoteProgress("call-1", 2, "late"),
			)
		}, tool.ExecutionUncertain},
		{"transport loss", tool.EffectMutating, func(ctx context.Context) pluginv1.PluginService_ExecuteClient {
			return &fakeExecuteStream{currentContext: func() context.Context { return ctx }, receive: func() (*pluginv1.ExecuteResponse, error) {
				return nil, status.Error(codes.Unavailable, "private transport detail")
			}}
		}, tool.ExecutionUncertain},
		{"first receive invalid argument", tool.EffectMutating, func(ctx context.Context) pluginv1.PluginService_ExecuteClient {
			return &fakeExecuteStream{currentContext: func() context.Context { return ctx }, receive: func() (*pluginv1.ExecuteResponse, error) {
				return nil, status.Error(codes.InvalidArgument, "private rejection")
			}}
		}, tool.ExecutionUncertain},
		{"first receive resource exhaustion", tool.EffectMutating, func(ctx context.Context) pluginv1.PluginService_ExecuteClient {
			return &fakeExecuteStream{currentContext: func() context.Context { return ctx }, receive: func() (*pluginv1.ExecuteResponse, error) {
				return nil, status.Error(codes.ResourceExhausted, "private rejection")
			}}
		}, tool.ExecutionUncertain},
		{"post-progress rejection is uncertain", tool.EffectMutating, func(ctx context.Context) pluginv1.PluginService_ExecuteClient {
			responses := 0
			return &fakeExecuteStream{currentContext: func() context.Context { return ctx }, receive: func() (*pluginv1.ExecuteResponse, error) {
				responses++
				if responses == 1 {
					return remoteProgress("call-1", 1, "admitted"), nil
				}
				return nil, status.Error(codes.InvalidArgument, "private late rejection")
			}}
		}, tool.ExecutionUncertain},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePluginClient{execute: func(
				ctx context.Context,
				_ *pluginv1.ExecuteRequest,
			) (pluginv1.PluginService_ExecuteClient, error) {
				return test.stream(ctx), nil
			}}
			implementation := mustRemoteTool(t, client, test.effect, 1, time.Second)
			call, _ := tool.NewCall("call-1", implementation.Definition().Name(), []byte(`{}`))
			_, err := implementation.Execute(t.Context(), call, nil)
			assertExecutionFailure(t, err, test.state)
			if strings.Contains(err.Error(), "private") {
				t.Fatalf("error leaks transport text: %v", err)
			}
			if client.calls.Load() != 1 {
				t.Fatalf("Execute calls = %d", client.calls.Load())
			}
		})
	}
}

func TestRemoteToolRejectsDefinitionIncompatibleTerminalFailures(t *testing.T) {
	tests := []struct {
		name      string
		effect    tool.Effect
		state     pluginv1.ExecutionState
		retry     pluginv1.RetryDisposition
		wantState tool.ExecutionState
	}{
		{
			name: "read-only uncertainty", effect: tool.EffectReadOnly,
			state:     pluginv1.ExecutionState_EXECUTION_STATE_UNCERTAIN,
			retry:     pluginv1.RetryDisposition_RETRY_DISPOSITION_NEVER,
			wantState: tool.ExecutionDefinitive,
		},
		{
			name: "replay-unsafe retry", effect: tool.EffectMutating,
			state:     pluginv1.ExecutionState_EXECUTION_STATE_DEFINITIVE,
			retry:     pluginv1.RetryDisposition_RETRY_DISPOSITION_ALLOWED,
			wantState: tool.ExecutionUncertain,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePluginClient{execute: streamFactory(remoteFailure(
				"call-1", 1, test.state, test.retry, "plugin claim",
			))}
			implementation := mustRemoteTool(t, client, test.effect, 1, time.Second)
			call, _ := tool.NewCall("call-1", implementation.Definition().Name(), []byte(`{}`))
			_, err := implementation.Execute(t.Context(), call, nil)
			assertExecutionFailure(t, err, test.wantState)
			if err.Error() != string(remoteProtocolFailure) || strings.Contains(err.Error(), "plugin claim") {
				t.Fatalf("incompatible failure was not normalized: %v", err)
			}
		})
	}
}

func TestRemoteToolRemoteStatusesRemainUncertainForMutatingCalls(t *testing.T) {
	for _, code := range []codes.Code{codes.InvalidArgument, codes.ResourceExhausted} {
		t.Run(code.String()+"/invoke", func(t *testing.T) {
			client := &fakePluginClient{execute: func(
				context.Context,
				*pluginv1.ExecuteRequest,
			) (pluginv1.PluginService_ExecuteClient, error) {
				return nil, status.Error(code, "private rejection")
			}}
			implementation := mustRemoteTool(t, client, tool.EffectMutating, 1, time.Second)
			call, _ := tool.NewCall("call-1", "remote.write", []byte(`{}`))
			_, err := implementation.Execute(t.Context(), call, nil)
			assertExecutionFailure(t, err, tool.ExecutionUncertain)
		})
		t.Run(code.String()+"/first-recv", func(t *testing.T) {
			client := &fakePluginClient{execute: func(
				ctx context.Context,
				_ *pluginv1.ExecuteRequest,
			) (pluginv1.PluginService_ExecuteClient, error) {
				return &fakeExecuteStream{
					currentContext: func() context.Context { return ctx },
					receive: func() (*pluginv1.ExecuteResponse, error) {
						return nil, status.Error(code, "private post-mutation status")
					},
				}, nil
			}}
			implementation := mustRemoteTool(t, client, tool.EffectMutating, 1, time.Second)
			call, _ := tool.NewCall("call-1", "remote.write", []byte(`{}`))
			_, err := implementation.Execute(t.Context(), call, nil)
			assertExecutionFailure(t, err, tool.ExecutionUncertain)
		})
	}
	t.Run("other transport is uncertain after invocation", func(t *testing.T) {
		client := &fakePluginClient{execute: func(
			context.Context,
			*pluginv1.ExecuteRequest,
		) (pluginv1.PluginService_ExecuteClient, error) {
			return nil, status.Error(codes.Unavailable, "private rejection")
		}}
		implementation := mustRemoteTool(t, client, tool.EffectMutating, 1, time.Second)
		call, _ := tool.NewCall("call-1", "remote.write", []byte(`{}`))
		_, err := implementation.Execute(t.Context(), call, nil)
		assertExecutionFailure(t, err, tool.ExecutionUncertain)
	})

	client := &fakePluginClient{execute: streamFactory(remoteResult("call-1", 1, []byte(`{}`), ""))}
	implementation := mustRemoteTool(t, client, tool.EffectMutating, 1, time.Second)
	call, _ := tool.NewCall("call-1", "remote.write", []byte(`{}`))
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := implementation.Execute(canceled, call, nil)
	assertExecutionFailure(t, err, tool.ExecutionDefinitive)
	if !errors.Is(err, context.Canceled) || client.calls.Load() != 0 {
		t.Fatalf("pre-admission cancellation = %v, calls = %d", err, client.calls.Load())
	}
}

func TestRemoteToolReporterFailureIsPreservedByDispatcher(t *testing.T) {
	client := &fakePluginClient{execute: streamFactory(
		remoteProgress("call-1", 1, "working"),
		remoteResult("call-1", 2, []byte(`{}`), ""),
	)}
	implementation := mustRemoteTool(t, client, tool.EffectMutating, 1, time.Second)
	dispatcher, err := stage.NewDispatcher(map[string]tool.Tool{"remote.write": implementation})
	if err != nil {
		t.Fatal(err)
	}
	call, _ := tool.NewCall("call-1", "remote.write", []byte(`{}`))
	reporterErr := errors.New("reporter stopped")
	_, err = dispatcher.Dispatch(t.Context(), call, rejectingReporter{err: reporterErr})
	var (
		combined  *stage.DispatchFailure
		execution *tool.ExecutionError
	)
	if !errors.As(err, &combined) || !errors.As(err, &execution) ||
		!errors.Is(combined.ReporterFailure(), reporterErr) ||
		execution.State() != tool.ExecutionUncertain ||
		execution.RetryDisposition() != tool.RetryNever {
		t.Fatalf("dispatch reporter failure = %v", err)
	}
}

func TestRemoteToolTimeoutAfterInvocationNeverRetries(t *testing.T) {
	for _, test := range []struct {
		name    string
		timeout time.Duration
		cancel  bool
		want    error
	}{
		{"timeout", 20 * time.Millisecond, false, context.DeadlineExceeded},
		{"caller cancellation", time.Second, true, context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			invoked := make(chan struct{})
			client := &fakePluginClient{execute: func(
				ctx context.Context,
				_ *pluginv1.ExecuteRequest,
			) (pluginv1.PluginService_ExecuteClient, error) {
				close(invoked)
				return &fakeExecuteStream{currentContext: func() context.Context { return ctx }, receive: func() (*pluginv1.ExecuteResponse, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				}}, nil
			}}
			implementation := mustRemoteTool(t, client, tool.EffectMutating, 1, test.timeout)
			call, _ := tool.NewCall("call-1", "remote.write", []byte(`{}`))
			ctx, cancel := context.WithCancel(t.Context())
			if test.cancel {
				go func() {
					<-invoked
					cancel()
				}()
			}
			defer cancel()
			_, err := implementation.Execute(ctx, call, nil)
			assertExecutionFailure(t, err, tool.ExecutionUncertain)
			if !errors.Is(err, test.want) || client.calls.Load() != 1 {
				t.Fatalf("interruption = %v, calls = %d", err, client.calls.Load())
			}
		})
	}
}

func TestRemoteToolAdmissionEnforcesNegotiatedConcurrency(t *testing.T) {
	release := make(chan struct{})
	var active, maximum atomic.Int32
	client := &fakePluginClient{execute: func(
		ctx context.Context,
		request *pluginv1.ExecuteRequest,
	) (pluginv1.PluginService_ExecuteClient, error) {
		current := active.Add(1)
		for observed := maximum.Load(); current > observed && !maximum.CompareAndSwap(observed, current); {
			observed = maximum.Load()
		}
		frames := []*pluginv1.ExecuteResponse{remoteResult(request.GetCallId(), 1, []byte(`{}`), "")}
		return &fakeExecuteStream{
			currentContext: func() context.Context { return ctx },
			receive: func() (*pluginv1.ExecuteResponse, error) {
				<-release
				if len(frames) == 0 {
					active.Add(-1)
					return nil, io.EOF
				}
				frame := frames[0]
				frames = frames[1:]
				return frame, nil
			},
		}, nil
	}}
	implementation := mustRemoteTool(t, client, tool.EffectReadOnly, 2, time.Second)
	const calls = 8
	results := make(chan error, calls)
	for index := range calls {
		go func() {
			call, _ := tool.NewCall(tool.CallID("call-"+strconv.Itoa(index)), "remote.read", []byte(`{}`))
			_, err := implementation.Execute(t.Context(), call, nil)
			results <- err
		}()
	}
	deadline := time.Now().Add(time.Second)
	for maximum.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if maximum.Load() != 2 || client.calls.Load() != 2 {
		t.Fatalf("before release: maximum = %d, RPC calls = %d", maximum.Load(), client.calls.Load())
	}
	close(release)
	for range calls {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if maximum.Load() != 2 || client.calls.Load() != calls {
		t.Fatalf("maximum = %d, RPC calls = %d", maximum.Load(), client.calls.Load())
	}
}

func TestRemoteToolAcceptedTerminalWinsLateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	responses := 0
	client := &fakePluginClient{execute: func(
		operation context.Context,
		_ *pluginv1.ExecuteRequest,
	) (pluginv1.PluginService_ExecuteClient, error) {
		return &fakeExecuteStream{currentContext: func() context.Context { return operation }, receive: func() (*pluginv1.ExecuteResponse, error) {
			responses++
			if responses == 1 {
				cancel()
				return remoteResult("call-1", 1, []byte(`{"accepted":true}`), ""), nil
			}
			return nil, context.Canceled
		}}, nil
	}}
	implementation := mustRemoteTool(t, client, tool.EffectMutating, 1, time.Second)
	call, _ := tool.NewCall("call-1", "remote.write", []byte(`{}`))
	result, err := implementation.Execute(ctx, call, nil)
	if err != nil || string(result.Content()) != `{"accepted":true}` {
		t.Fatalf("terminal-v-cancel = (%s, %v)", result.Content(), err)
	}
}

func TestRemoteSessionAndToolRejectInvalidConstruction(t *testing.T) {
	client := &fakePluginClient{}
	limits := remoteLimits(1)
	validSession := bytes.Repeat([]byte{1}, pluginv1.SessionIDBytes)
	if _, err := newRemoteSession(nil, validSession, limits, time.Second); err == nil {
		t.Fatal("nil client succeeded")
	}
	if _, err := newRemoteSession(client, []byte{1}, limits, time.Second); err == nil {
		t.Fatal("short session identity succeeded")
	}
	if _, err := newRemoteSession(client, validSession, &pluginv1.Limits{}, time.Second); err == nil {
		t.Fatal("invalid limits succeeded")
	}
	if _, err := newRemoteSession(client, validSession, limits, 0); err == nil {
		t.Fatal("zero timeout succeeded")
	}
	session, _ := newRemoteSession(client, validSession, limits, time.Second)
	if _, err := newRemoteTool(tool.Definition{}, session); err == nil {
		t.Fatal("invalid definition succeeded")
	}
	if _, err := newRemoteTool(remoteDefinition(t, tool.EffectReadOnly), nil); err == nil {
		t.Fatal("nil session succeeded")
	}
}

type fakePluginClient struct {
	execute func(context.Context, *pluginv1.ExecuteRequest) (pluginv1.PluginService_ExecuteClient, error)
	calls   atomic.Int32
}

func (client *fakePluginClient) Initialize(
	context.Context,
	*pluginv1.InitializeRequest,
	...grpc.CallOption,
) (*pluginv1.InitializeResponse, error) {
	return nil, errors.New("unexpected initialize")
}

func (client *fakePluginClient) Execute(
	ctx context.Context,
	request *pluginv1.ExecuteRequest,
	_ ...grpc.CallOption,
) (pluginv1.PluginService_ExecuteClient, error) {
	client.calls.Add(1)
	if client.execute == nil {
		return nil, errors.New("unexpected execute")
	}
	return client.execute(ctx, request)
}

func (client *fakePluginClient) Drain(
	context.Context,
	*pluginv1.DrainRequest,
	...grpc.CallOption,
) (*pluginv1.DrainResponse, error) {
	return nil, errors.New("unexpected drain")
}

func (client *fakePluginClient) Shutdown(
	context.Context,
	*pluginv1.ShutdownRequest,
	...grpc.CallOption,
) (*pluginv1.ShutdownResponse, error) {
	return nil, errors.New("unexpected shutdown")
}

type fakeExecuteStream struct {
	currentContext func() context.Context
	receive        func() (*pluginv1.ExecuteResponse, error)
}

func newFakeExecuteStream(
	ctx context.Context,
	frames ...*pluginv1.ExecuteResponse,
) *fakeExecuteStream {
	index := 0
	return &fakeExecuteStream{currentContext: func() context.Context { return ctx }, receive: func() (*pluginv1.ExecuteResponse, error) {
		if index >= len(frames) {
			return nil, io.EOF
		}
		frame := frames[index]
		index++
		return frame, nil
	}}
}

func (stream *fakeExecuteStream) Recv() (*pluginv1.ExecuteResponse, error) {
	return stream.receive()
}

func (*fakeExecuteStream) Header() (metadata.MD, error) { return metadata.MD{}, nil }
func (*fakeExecuteStream) Trailer() metadata.MD         { return nil }
func (*fakeExecuteStream) CloseSend() error             { return nil }
func (stream *fakeExecuteStream) Context() context.Context {
	return stream.currentContext()
}
func (*fakeExecuteStream) SendMsg(any) error { return nil }
func (*fakeExecuteStream) RecvMsg(any) error { return nil }

type recordingReporter struct {
	mu       sync.Mutex
	progress []tool.Progress
}

func (reporter *recordingReporter) Report(_ context.Context, progress tool.Progress) error {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	reporter.progress = append(reporter.progress, progress)
	return nil
}

func (reporter *recordingReporter) messages() []string {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	result := make([]string, 0, len(reporter.progress))
	for _, progress := range reporter.progress {
		result = append(result, progress.Message())
	}
	return result
}

type rejectingReporter struct{ err error }

func (reporter rejectingReporter) Report(context.Context, tool.Progress) error { return reporter.err }

func mustRemoteTool(
	t *testing.T,
	client pluginv1.PluginServiceClient,
	effect tool.Effect,
	concurrency uint32,
	timeout time.Duration,
) *remoteTool {
	t.Helper()
	session, err := newRemoteSession(
		client,
		bytes.Repeat([]byte{0x44}, pluginv1.SessionIDBytes),
		remoteLimits(concurrency),
		timeout,
	)
	if err != nil {
		t.Fatal(err)
	}
	implementation, err := newRemoteTool(remoteDefinition(t, effect), session)
	if err != nil {
		t.Fatal(err)
	}
	return implementation
}

func remoteDefinition(t *testing.T, effect tool.Effect) tool.Definition {
	t.Helper()
	name := "remote.read"
	replay := tool.ReplaySafe
	capabilities := []tool.Capability{tool.CapabilityFilesystemRead}
	if effect == tool.EffectMutating {
		name = "remote.write"
		replay = tool.ReplayUnsafe
		capabilities = []tool.Capability{tool.CapabilityFilesystemWrite}
	}
	definition, err := tool.NewDefinition(
		name,
		"Exercises a remote runtime tool.",
		[]byte(`{"type":"object"}`),
		effect,
		replay,
		capabilities...,
	)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func remoteLimits(concurrency uint32) *pluginv1.Limits {
	return &pluginv1.Limits{
		MaxMessageBytes:      1 << 20,
		MaxTools:             16,
		MaxSchemaBytes:       tool.MaximumPayloadBytes,
		MaxCallArgumentBytes: tool.MaximumPayloadBytes,
		MaxResultBytes:       tool.MaximumPayloadBytes,
		MaxProgressBytes:     tool.MaximumProgressBytes,
		MaxConcurrentCalls:   concurrency,
	}
}

func streamFactory(frames ...*pluginv1.ExecuteResponse) func(
	context.Context,
	*pluginv1.ExecuteRequest,
) (pluginv1.PluginService_ExecuteClient, error) {
	return func(ctx context.Context, _ *pluginv1.ExecuteRequest) (pluginv1.PluginService_ExecuteClient, error) {
		return newFakeExecuteStream(ctx, frames...), nil
	}
}

func remoteProgress(callID string, sequence uint64, message string) *pluginv1.ExecuteResponse {
	return &pluginv1.ExecuteResponse{
		CallId: callID, Sequence: sequence,
		Frame: &pluginv1.ExecuteResponse_Progress{Progress: &pluginv1.Progress{Message: message}},
	}
}

func remoteResult(callID string, sequence uint64, content []byte, problem string) *pluginv1.ExecuteResponse {
	return &pluginv1.ExecuteResponse{
		CallId: callID, Sequence: sequence,
		Frame: &pluginv1.ExecuteResponse_Result{Result: &pluginv1.Result{ContentJson: content, Problem: problem}},
	}
}

func remoteFailure(
	callID string,
	sequence uint64,
	state pluginv1.ExecutionState,
	retry pluginv1.RetryDisposition,
	message string,
) *pluginv1.ExecuteResponse {
	return &pluginv1.ExecuteResponse{
		CallId: callID, Sequence: sequence,
		Frame: &pluginv1.ExecuteResponse_Failure{Failure: &pluginv1.ExecutionFailure{
			State: state, Retry: retry, SafeMessage: message,
		}},
	}
}

func assertExecutionFailure(t *testing.T, err error, state tool.ExecutionState) {
	t.Helper()
	var failure *tool.ExecutionError
	if !errors.As(err, &failure) || failure.State() != state ||
		failure.RetryDisposition() != tool.RetryNever {
		t.Fatalf("execution failure = %v, want state %s and retry never", err, state)
	}
}
