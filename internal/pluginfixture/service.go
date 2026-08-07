package pluginfixture

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"slices"
	"sync"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"github.com/spice-framework/spice-agent/tool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	maximumFixtureMessageBytes = pluginv1.InitializeBootstrapMaximumBytes
	echoToolName               = "conformance.echo"
	failToolName               = "conformance.fail"
	waitToolName               = "conformance.wait"
)

var fixtureUnknownField = func() []byte {
	value := protowire.AppendTag(nil, 127, protowire.BytesType)
	return protowire.AppendBytes(value, []byte("fixture-compatible"))
}()

type service struct {
	pluginv1.UnimplementedPluginServiceServer

	secret   []byte
	limits   *pluginv1.Limits
	manifest *pluginv1.Manifest
	shutdown func()

	mu          sync.Mutex
	initialized bool
	draining    bool
	closed      bool
	sessionID   []byte
	active      uint32
	zero        chan struct{}
}

// NewService constructs the independent Go conformance fixture service. It is
// internal test infrastructure, not the production runtime-plugin host.
func NewService(secret []byte, shutdown func()) (pluginv1.PluginServiceServer, error) {
	if len(secret) != pluginv1.HandshakeSecretBytes || shutdown == nil {
		return nil, errors.New("fixture secret and shutdown callback are required")
	}
	manifest, err := fixtureManifest()
	if err != nil {
		return nil, err
	}
	zero := make(chan struct{})
	close(zero)
	return &service{
		secret:   slices.Clone(secret),
		limits:   fixtureLimits(),
		manifest: manifest,
		shutdown: shutdown,
		zero:     zero,
	}, nil
}

func (current *service) Initialize(
	_ context.Context,
	request *pluginv1.InitializeRequest,
) (*pluginv1.InitializeResponse, error) {
	if err := pluginv1.ValidateInitializeRequest(request); err != nil {
		return current.failedInitialize(request, commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			"plugin initialization request is invalid")
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.initialized {
		return current.failedInitialize(request, commonv1.ErrorCode_ERROR_CODE_CONFLICT,
			"plugin session is already initialized")
	}
	protocol, protocolStatus := commonv1.NegotiateProtocol(request.GetProtocol(), pluginv1.SupportedProtocolRange())
	if protocolStatus.GetCode() != commonv1.ErrorCode_ERROR_CODE_OK {
		return current.failedInitialize(request, protocolStatus.GetCode(), protocolStatus.GetMessage())
	}
	capabilities, capabilityStatus := commonv1.NegotiateCapabilities(
		request.GetSupportedCapabilities(),
		request.GetRequiredCapabilities(),
		&commonv1.CapabilitySet{Names: []string{pluginv1.CapabilityRuntimeToolsV1}},
	)
	if capabilityStatus.GetCode() != commonv1.ErrorCode_ERROR_CODE_OK {
		return current.failedInitialize(request, capabilityStatus.GetCode(), capabilityStatus.GetMessage())
	}
	limits, err := pluginv1.NegotiateLimits(request.GetRequestedLimits(), current.limits)
	if err != nil {
		return current.failedInitialize(request, commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			"plugin limit negotiation failed")
	}
	if _, err = pluginv1.DecodeManifest(current.manifest, limits); err != nil {
		return current.failedInitialize(request, commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			"plugin manifest exceeds negotiated limits")
	}
	sessionID := make([]byte, pluginv1.SessionIDBytes)
	if _, err = io.ReadFull(rand.Reader, sessionID); err != nil {
		return nil, status.Error(codes.Internal, "plugin session identity generation failed")
	}
	response := &pluginv1.InitializeResponse{
		Status:       commonv1.OKStatus(),
		Protocol:     protocol,
		Plugin:       fixtureBuild(),
		Capabilities: capabilities,
		Limits:       limits,
		Manifest:     current.manifest,
		LaunchId:     slices.Clone(request.GetLaunchId()),
		SessionId:    sessionID,
		HandshakeChallenge: slices.Clone(
			request.GetHandshakeChallenge(),
		),
	}
	signed, err := pluginv1.SignInitializeResponse(request, response, current.secret)
	if err != nil {
		return nil, status.Error(codes.Internal, "plugin handshake signing failed")
	}
	current.initialized = true
	current.limits = cloneLimits(limits)
	current.sessionID = slices.Clone(sessionID)
	return signed, nil
}

func (current *service) failedInitialize(
	request *pluginv1.InitializeRequest,
	code commonv1.ErrorCode,
	message string,
) (*pluginv1.InitializeResponse, error) {
	if request == nil || len(request.GetLaunchId()) != pluginv1.LaunchIDBytes ||
		len(request.GetHandshakeChallenge()) != pluginv1.HandshakeChallengeBytes {
		return nil, status.Error(codes.InvalidArgument, "plugin initialization request is invalid")
	}
	response := &pluginv1.InitializeResponse{
		Status: &commonv1.Status{Code: code, Message: message},
		Plugin: fixtureBuild(),
		LaunchId: slices.Clone(
			request.GetLaunchId(),
		),
		HandshakeChallenge: slices.Clone(request.GetHandshakeChallenge()),
	}
	signed, err := pluginv1.SignInitializeResponse(request, response, current.secret)
	if err != nil {
		return nil, status.Error(codes.Internal, "plugin handshake signing failed")
	}
	return signed, nil
}

func (current *service) Execute(
	request *pluginv1.ExecuteRequest,
	stream pluginv1.PluginService_ExecuteServer,
) error {
	if request == nil {
		return status.Error(codes.InvalidArgument, "plugin call is invalid")
	}
	current.mu.Lock()
	if !current.initialized || current.closed {
		current.mu.Unlock()
		return status.Error(codes.FailedPrecondition, "plugin session is unavailable")
	}
	if current.draining {
		current.mu.Unlock()
		return status.Error(codes.Unavailable, "plugin is draining")
	}
	if uint64(len(request.GetArgumentsJson())) > current.limits.GetMaxCallArgumentBytes() {
		current.mu.Unlock()
		return status.Error(codes.ResourceExhausted, "plugin call exceeds the negotiated limit")
	}
	call, err := pluginv1.DecodeExecuteRequest(request, current.sessionID, current.limits)
	if err != nil {
		current.mu.Unlock()
		return status.Error(codes.InvalidArgument, "plugin call is invalid")
	}
	if !knownTool(call.Name()) {
		current.mu.Unlock()
		return status.Error(codes.NotFound, "plugin tool is unavailable")
	}
	if current.active >= current.limits.GetMaxConcurrentCalls() {
		current.mu.Unlock()
		return status.Error(codes.ResourceExhausted, "plugin concurrent-call limit is exhausted")
	}
	current.admitLocked()
	current.mu.Unlock()
	defer current.release()

	switch call.Name() {
	case echoToolName:
		return executeEcho(call, stream)
	case failToolName:
		return executeFailure(call, stream)
	case waitToolName:
		return executeWait(call, stream)
	default:
		return status.Error(codes.Internal, "plugin tool dispatch is inconsistent")
	}
}

func (current *service) Drain(
	ctx context.Context,
	request *pluginv1.DrainRequest,
) (*pluginv1.DrainResponse, error) {
	current.mu.Lock()
	if !current.initialized || current.closed {
		current.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "plugin session is unavailable")
	}
	if err := pluginv1.ValidateDrainRequest(request, current.sessionID, current.limits); err != nil {
		current.mu.Unlock()
		return nil, status.Error(codes.InvalidArgument, "plugin drain request is invalid")
	}
	current.draining = true
	zero := current.zero
	current.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	case <-zero:
		return &pluginv1.DrainResponse{Status: commonv1.OKStatus()}, nil
	}
}

func (current *service) Shutdown(
	_ context.Context,
	request *pluginv1.ShutdownRequest,
) (*pluginv1.ShutdownResponse, error) {
	current.mu.Lock()
	if !current.initialized || current.closed {
		current.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "plugin session is unavailable")
	}
	if err := pluginv1.ValidateShutdownRequest(request, current.sessionID, current.limits); err != nil {
		current.mu.Unlock()
		return nil, status.Error(codes.InvalidArgument, "plugin shutdown request is invalid")
	}
	if !current.draining || current.active != 0 {
		current.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "plugin must drain before shutdown")
	}
	current.closed = true
	shutdown := current.shutdown
	current.mu.Unlock()
	go shutdown()
	return &pluginv1.ShutdownResponse{Status: commonv1.OKStatus()}, nil
}

func (current *service) admitLocked() {
	if current.active == 0 {
		current.zero = make(chan struct{})
	}
	current.active++
}

func (current *service) release() {
	current.mu.Lock()
	defer current.mu.Unlock()
	current.active--
	if current.active == 0 {
		close(current.zero)
	}
}

func executeEcho(call tool.Call, stream pluginv1.PluginService_ExecuteServer) error {
	var input struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(call.Arguments(), &input); err != nil || input.Value == "" {
		return status.Error(codes.InvalidArgument, "plugin echo arguments are invalid")
	}
	progress := &pluginv1.ExecuteResponse{
		CallId: string(call.ID()), Sequence: 1,
		Frame: &pluginv1.ExecuteResponse_Progress{Progress: &pluginv1.Progress{Message: "echo accepted"}},
	}
	progress.ProtoReflect().SetUnknown(slices.Clone(fixtureUnknownField))
	if err := stream.Send(progress); err != nil {
		return err
	}
	content, err := json.Marshal(input)
	if err != nil {
		return status.Error(codes.Internal, "plugin echo encoding failed")
	}
	return stream.Send(&pluginv1.ExecuteResponse{
		CallId: string(call.ID()), Sequence: 2,
		Frame: &pluginv1.ExecuteResponse_Result{Result: &pluginv1.Result{ContentJson: content}},
	})
}

func executeFailure(call tool.Call, stream pluginv1.PluginService_ExecuteServer) error {
	if !bytes.Equal(call.Arguments(), []byte(`{}`)) {
		return status.Error(codes.InvalidArgument, "plugin failure arguments are invalid")
	}
	return stream.Send(&pluginv1.ExecuteResponse{
		CallId: string(call.ID()), Sequence: 1,
		Frame: &pluginv1.ExecuteResponse_Failure{Failure: &pluginv1.ExecutionFailure{
			State:       pluginv1.ExecutionState_EXECUTION_STATE_DEFINITIVE,
			Retry:       pluginv1.RetryDisposition_RETRY_DISPOSITION_NEVER,
			SafeMessage: "fixture failure",
		}},
	})
}

func executeWait(call tool.Call, stream pluginv1.PluginService_ExecuteServer) error {
	if !bytes.Equal(call.Arguments(), []byte(`{}`)) {
		return status.Error(codes.InvalidArgument, "plugin wait arguments are invalid")
	}
	if err := stream.Send(&pluginv1.ExecuteResponse{
		CallId: string(call.ID()), Sequence: 1,
		Frame: &pluginv1.ExecuteResponse_Progress{Progress: &pluginv1.Progress{Message: "waiting"}},
	}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return status.FromContextError(stream.Context().Err()).Err()
}

func fixtureManifest() (*pluginv1.Manifest, error) {
	definitions := make([]tool.Definition, 0, 3)
	for _, item := range []struct {
		name        string
		description string
		schema      string
	}{
		{echoToolName, "Echo one string value.", `{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`},
		{failToolName, "Return one definitive fixture failure.", `{"type":"object"}`},
		{waitToolName, "Wait until the call is cancelled.", `{"type":"object"}`},
	} {
		definition, err := tool.NewDefinition(
			item.name,
			item.description,
			json.RawMessage(item.schema),
			tool.EffectReadOnly,
			tool.ReplaySafe,
		)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	catalog, err := pluginv1.NewCatalog("spice-agent-go-conformance", "v1", definitions, fixtureLimits())
	if err != nil {
		return nil, err
	}
	return catalog.Manifest()
}

func fixtureLimits() *pluginv1.Limits {
	return &pluginv1.Limits{
		MaxMessageBytes:      maximumFixtureMessageBytes,
		MaxTools:             16,
		MaxSchemaBytes:       64 << 10,
		MaxCallArgumentBytes: 64 << 10,
		MaxResultBytes:       maximumFixtureMessageBytes,
		MaxProgressBytes:     tool.MaximumProgressBytes,
		MaxConcurrentCalls:   8,
	}
}

func fixtureBuild() *pluginv1.BuildIdentity {
	return &pluginv1.BuildIdentity{
		Component: "spice-agent-go-conformance",
		Version:   "v1",
		Commit:    "fixture",
		Runtime:   runtime.Version(),
	}
}

func knownTool(name string) bool {
	return name == echoToolName || name == failToolName || name == waitToolName
}

func cloneLimits(value *pluginv1.Limits) *pluginv1.Limits {
	return &pluginv1.Limits{
		MaxMessageBytes:      value.GetMaxMessageBytes(),
		MaxTools:             value.GetMaxTools(),
		MaxSchemaBytes:       value.GetMaxSchemaBytes(),
		MaxCallArgumentBytes: value.GetMaxCallArgumentBytes(),
		MaxResultBytes:       value.GetMaxResultBytes(),
		MaxProgressBytes:     value.GetMaxProgressBytes(),
		MaxConcurrentCalls:   value.GetMaxConcurrentCalls(),
	}
}
