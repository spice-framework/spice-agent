package conformance

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"github.com/spice-framework/spice-agent/tool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

const (
	// EchoToolName is the required deterministic conformance echo tool.
	EchoToolName = "conformance.echo"
	// FailToolName is the required typed-failure conformance tool.
	FailToolName = "conformance.fail"
	// WaitToolName is the required cancellation conformance tool.
	WaitToolName = "conformance.wait"

	defaultOperationTimeout = 2 * time.Second
	minimumOperationTimeout = 100 * time.Millisecond
	maximumOperationTimeout = 30 * time.Second
)

var futureUnknownField = func() []byte {
	value := protowire.AppendTag(nil, 127, protowire.BytesType)
	return protowire.AppendBytes(value, []byte("future-compatible"))
}()

// Config supplies caller-owned handshake material and negotiated bounds. Run
// copies every field before use and never includes Secret in an error.
type Config struct {
	HostBuild        *pluginv1.BuildIdentity
	Limits           *pluginv1.Limits
	Secret           []byte
	OperationTimeout time.Duration
}

// Run exercises the complete initial runtime-tool profile through client. It
// is destructive to that one fixture session: the final steps drain and shut
// it down. A fresh fixture process is required for each call. Client must have
// the cancellation behavior of the generated gRPC client: an outstanding Recv
// must return when its stream context is canceled.
func Run(ctx context.Context, client pluginv1.PluginServiceClient, config Config) error {
	if ctx == nil || client == nil {
		return errors.New("plugin conformance context and client are required")
	}
	timeout := config.OperationTimeout
	if timeout == 0 {
		timeout = defaultOperationTimeout
	}
	if timeout < minimumOperationTimeout || timeout > maximumOperationTimeout {
		return errors.New("plugin conformance operation timeout is outside supported bounds")
	}
	if config.HostBuild == nil || config.Limits == nil {
		return errors.New("plugin conformance build and limits are required")
	}
	if len(config.Secret) != pluginv1.HandshakeSecretBytes {
		return errors.New("plugin conformance handshake secret has an invalid size")
	}
	secret := slices.Clone(config.Secret)
	defer clear(secret)
	launchID, err := randomBytes(pluginv1.LaunchIDBytes)
	if err != nil {
		return err
	}
	challenge, err := randomBytes(pluginv1.HandshakeChallengeBytes)
	if err != nil {
		return err
	}
	request := &pluginv1.InitializeRequest{
		Protocol:              pluginv1.SupportedProtocolRange(),
		Host:                  clone(config.HostBuild),
		SupportedCapabilities: &commonv1.CapabilitySet{Names: []string{pluginv1.CapabilityRuntimeToolsV1}},
		RequiredCapabilities:  &commonv1.CapabilitySet{Names: []string{pluginv1.CapabilityRuntimeToolsV1}},
		RequestedLimits:       clone(config.Limits),
		LaunchId:              launchID,
		HandshakeChallenge:    challenge,
	}
	request.ProtoReflect().SetUnknown(slices.Clone(futureUnknownField))
	if err = pluginv1.ValidateInitializeRequest(request); err != nil {
		return fmt.Errorf("plugin conformance initialize request: %w", err)
	}
	response, err := initialize(ctx, client, request, timeout)
	if err != nil {
		return err
	}
	if err = pluginv1.ValidateInitializeResponseForRequest(request, response, secret); err != nil {
		return fmt.Errorf("plugin conformance initialize response: %w", err)
	}
	if err = rejectReinitialize(ctx, client, request, secret, timeout); err != nil {
		return err
	}
	if err = validateManifest(response.GetManifest(), response.GetLimits()); err != nil {
		return err
	}
	sessionID := slices.Clone(response.GetSessionId())
	if err = runEcho(ctx, client, sessionID, response.GetLimits(), timeout); err != nil {
		return err
	}
	if err = runFailure(ctx, client, sessionID, response.GetLimits(), timeout); err != nil {
		return err
	}
	if err = rejectMalformedCalls(ctx, client, sessionID, response.GetLimits(), timeout); err != nil {
		return err
	}
	if err = cancellationAndDrain(ctx, client, sessionID, response.GetLimits(), timeout); err != nil {
		return err
	}
	return shutdown(ctx, client, sessionID, response.GetLimits(), timeout)
}

func rejectReinitialize(
	ctx context.Context,
	client pluginv1.PluginServiceClient,
	request *pluginv1.InitializeRequest,
	secret []byte,
	timeout time.Duration,
) error {
	response, err := initialize(ctx, client, request, timeout)
	if err != nil {
		return err
	}
	err = pluginv1.ValidateInitializeResponseForRequest(request, response, secret)
	var statusErr *commonv1.StatusError
	if !errors.As(err, &statusErr) ||
		statusErr.Status().GetCode() != commonv1.ErrorCode_ERROR_CODE_CONFLICT {
		return errors.New("plugin conformance duplicate initialization did not return an authenticated conflict")
	}
	return nil
}

func initialize(
	ctx context.Context,
	client pluginv1.PluginServiceClient,
	request *pluginv1.InitializeRequest,
	timeout time.Duration,
) (*pluginv1.InitializeResponse, error) {
	operation, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := client.Initialize(operation, request)
	if err != nil {
		return nil, fmt.Errorf("plugin conformance initialize: %w", err)
	}
	return response, nil
}

func validateManifest(value *pluginv1.Manifest, limits *pluginv1.Limits) error {
	catalog, err := pluginv1.DecodeManifest(value, limits)
	if err != nil {
		return fmt.Errorf("plugin conformance manifest: %w", err)
	}
	definitions := catalog.Definitions()
	if len(definitions) != 3 || definitions[0].Name() != EchoToolName ||
		definitions[1].Name() != FailToolName || definitions[2].Name() != WaitToolName {
		return errors.New("plugin conformance manifest does not contain the canonical tools")
	}
	for _, definition := range definitions {
		if definition.Effect() != tool.EffectReadOnly || definition.ReplaySafety() != tool.ReplaySafe ||
			len(definition.Capabilities()) != 0 {
			return errors.New("plugin conformance tool metadata is inconsistent")
		}
	}
	return nil
}

func runFailure(
	ctx context.Context,
	client pluginv1.PluginServiceClient,
	sessionID []byte,
	limits *pluginv1.Limits,
	timeout time.Duration,
) error {
	request := &pluginv1.ExecuteRequest{
		SessionId: slices.Clone(sessionID), CallId: "conformance-failure", ToolName: FailToolName,
		ArgumentsJson: []byte(`{}`),
	}
	validator, err := pluginv1.NewStreamValidator(request, sessionID, limits)
	if err != nil {
		return err
	}
	operation, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stream, err := client.Execute(operation, request)
	if err != nil {
		return fmt.Errorf("plugin conformance typed failure: %w", err)
	}
	terminal, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("plugin conformance typed failure terminal: %w", err)
	}
	frame, err := validator.Accept(terminal)
	if err != nil {
		return err
	}
	failure, ok := frame.Failure()
	if !ok || failure.State() != tool.ExecutionDefinitive ||
		failure.RetryDisposition() != tool.RetryNever || failure.Error() != "fixture failure" {
		return errors.New("plugin conformance typed failure is incorrect")
	}
	if _, err = stream.Recv(); !errors.Is(err, io.EOF) {
		return errors.New("plugin conformance typed failure contains post-terminal traffic")
	}
	return validator.Finish()
}

func runEcho(
	ctx context.Context,
	client pluginv1.PluginServiceClient,
	sessionID []byte,
	limits *pluginv1.Limits,
	timeout time.Duration,
) error {
	request := &pluginv1.ExecuteRequest{
		SessionId:     slices.Clone(sessionID),
		CallId:        "conformance-echo",
		ToolName:      EchoToolName,
		ArgumentsJson: []byte(`{"value":"hello"}`),
	}
	request.ProtoReflect().SetUnknown(slices.Clone(futureUnknownField))
	validator, err := pluginv1.NewStreamValidator(request, sessionID, limits)
	if err != nil {
		return err
	}
	operation, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stream, err := client.Execute(operation, request)
	if err != nil {
		return fmt.Errorf("plugin conformance echo: %w", err)
	}
	progress, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("plugin conformance echo progress: %w", err)
	}
	if len(progress.ProtoReflect().GetUnknown()) == 0 {
		return errors.New("plugin conformance echo progress omitted the compatibility field")
	}
	frame, err := validator.Accept(progress)
	if err != nil {
		return err
	}
	value, ok := frame.Progress()
	if !ok || value.Message() != "echo accepted" {
		return errors.New("plugin conformance echo progress is incorrect")
	}
	terminal, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("plugin conformance echo result: %w", err)
	}
	frame, err = validator.Accept(terminal)
	if err != nil {
		return err
	}
	result, ok := frame.Result()
	if !ok || string(result.Content()) != `{"value":"hello"}` {
		return errors.New("plugin conformance echo result is incorrect")
	}
	if _, err = stream.Recv(); !errors.Is(err, io.EOF) {
		return errors.New("plugin conformance echo stream contains post-terminal traffic")
	}
	return validator.Finish()
}

func rejectMalformedCalls(
	ctx context.Context,
	client pluginv1.PluginServiceClient,
	sessionID []byte,
	limits *pluginv1.Limits,
	timeout time.Duration,
) error {
	invalid := []struct {
		name      string
		callID    string
		toolName  string
		arguments []byte
	}{
		{name: "truncated JSON", callID: "malformed-json", toolName: EchoToolName, arguments: []byte("{")},
		{name: "NaN", callID: "malformed-nan", toolName: EchoToolName, arguments: []byte("NaN")},
		{name: "infinity", callID: "malformed-infinity", toolName: EchoToolName, arguments: []byte("Infinity")},
		{name: "invalid UTF-8 JSON", callID: "malformed-utf8", toolName: EchoToolName, arguments: []byte{'"', 0xff, '"'}},
		{name: "controlled call identity", callID: "malformed\nidentity", toolName: EchoToolName, arguments: []byte(`{}`)},
		{name: "controlled tool name", callID: "malformed-tool", toolName: "conformance.\techo", arguments: []byte(`{}`)},
		{name: "oversized call identity", callID: strings.Repeat("c", 129), toolName: EchoToolName, arguments: []byte(`{}`)},
		{name: "oversized tool name", callID: "malformed-tool-size", toolName: strings.Repeat("t", 129), arguments: []byte(`{}`)},
	}
	for _, test := range invalid {
		request := &pluginv1.ExecuteRequest{
			SessionId:     slices.Clone(sessionID),
			CallId:        test.callID,
			ToolName:      test.toolName,
			ArgumentsJson: slices.Clone(test.arguments),
		}
		if err := expectExecuteCode(ctx, client, request, timeout, codes.InvalidArgument); err != nil {
			return fmt.Errorf("plugin conformance %s: %w", test.name, err)
		}
	}
	oversizedArguments := []byte{'"'}
	for range limits.GetMaxCallArgumentBytes() {
		oversizedArguments = append(oversizedArguments, 'x')
	}
	oversizedArguments = append(oversizedArguments, '"')
	oversized := &pluginv1.ExecuteRequest{
		SessionId: slices.Clone(sessionID), CallId: "oversized", ToolName: EchoToolName,
		ArgumentsJson: oversizedArguments,
	}
	return expectExecuteCode(ctx, client, oversized, timeout, codes.ResourceExhausted)
}

func expectExecuteCode(
	ctx context.Context,
	client pluginv1.PluginServiceClient,
	request *pluginv1.ExecuteRequest,
	timeout time.Duration,
	want codes.Code,
) error {
	operation, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stream, err := client.Execute(operation, request)
	if err == nil {
		_, err = stream.Recv()
	}
	if status.Code(err) != want {
		return fmt.Errorf("plugin conformance invalid call status is %s, want %s", status.Code(err), want)
	}
	return nil
}

func cancellationAndDrain(
	ctx context.Context,
	client pluginv1.PluginServiceClient,
	sessionID []byte,
	limits *pluginv1.Limits,
	timeout time.Duration,
) error {
	waitContext, cancelWait := context.WithCancel(ctx)
	waitRequest := &pluginv1.ExecuteRequest{
		SessionId: slices.Clone(sessionID), CallId: "conformance-wait", ToolName: WaitToolName,
		ArgumentsJson: []byte(`{}`),
	}
	validator, err := pluginv1.NewStreamValidator(waitRequest, sessionID, limits)
	if err != nil {
		cancelWait()
		return err
	}
	stream, err := client.Execute(waitContext, waitRequest)
	if err != nil {
		cancelWait()
		return fmt.Errorf("plugin conformance wait: %w", err)
	}
	admission, err := receiveWithin(waitContext, timeout, cancelWait, stream.Recv)
	if err != nil {
		cancelWait()
		return fmt.Errorf("plugin conformance wait admission: %w", err)
	}
	frame, err := validator.Accept(admission)
	if err != nil {
		cancelWait()
		return fmt.Errorf("plugin conformance wait admission: %w", err)
	}
	progress, ok := frame.Progress()
	if !ok || progress.Message() != "waiting" {
		cancelWait()
		return errors.New("plugin conformance wait admission is incorrect")
	}
	drainContext, cancelDrain := context.WithTimeout(ctx, minimumOperationTimeout)
	_, drainErr := client.Drain(drainContext, &pluginv1.DrainRequest{SessionId: slices.Clone(sessionID)})
	cancelDrain()
	if status.Code(drainErr) != codes.DeadlineExceeded {
		cancelWait()
		return errors.New("plugin conformance drain did not wait for the active call")
	}
	cancelWait()
	if _, err = stream.Recv(); status.Code(err) != codes.Canceled {
		return errors.New("plugin conformance call cancellation was not propagated")
	}

	operation, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	drained, err := client.Drain(operation, &pluginv1.DrainRequest{SessionId: slices.Clone(sessionID)})
	if err != nil {
		return fmt.Errorf("plugin conformance drain: %w", err)
	}
	if err = pluginv1.ValidateDrainResponse(drained, limits); err != nil {
		return err
	}
	postDrain := &pluginv1.ExecuteRequest{
		SessionId: slices.Clone(sessionID), CallId: "post-drain", ToolName: EchoToolName,
		ArgumentsJson: []byte(`{}`),
	}
	return expectExecuteCode(ctx, client, postDrain, timeout, codes.Unavailable)
}

type receiveOutcome struct {
	response *pluginv1.ExecuteResponse
	err      error
}

func receiveWithin(
	ctx context.Context,
	timeout time.Duration,
	cancel context.CancelFunc,
	receive func() (*pluginv1.ExecuteResponse, error),
) (*pluginv1.ExecuteResponse, error) {
	result := make(chan receiveOutcome, 1)
	go func() {
		response, err := receive()
		result <- receiveOutcome{response: response, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		cancel()
		joinReceive(result, timeout)
		return nil, ctx.Err()
	case <-timer.C:
		cancel()
		joinReceive(result, timeout)
		return nil, context.DeadlineExceeded
	case received := <-result:
		return received.response, received.err
	}
}

func joinReceive(result <-chan receiveOutcome, timeout time.Duration) {
	timer := time.NewTimer(min(timeout, minimumOperationTimeout))
	defer timer.Stop()
	select {
	case <-result:
	case <-timer.C:
	}
}

func shutdown(
	ctx context.Context,
	client pluginv1.PluginServiceClient,
	sessionID []byte,
	limits *pluginv1.Limits,
	timeout time.Duration,
) error {
	operation, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := client.Shutdown(operation, &pluginv1.ShutdownRequest{SessionId: slices.Clone(sessionID)})
	if err != nil {
		return fmt.Errorf("plugin conformance shutdown: %w", err)
	}
	return pluginv1.ValidateShutdownResponse(response, limits)
}

func randomBytes(size int) ([]byte, error) {
	result := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, result); err != nil {
		return nil, errors.New("generate plugin conformance handshake material")
	}
	return result, nil
}

func clone[T proto.Message](value T) T {
	result, ok := proto.Clone(value).(T)
	if !ok {
		panic("protobuf clone changed concrete conformance message type")
	}
	return result
}
