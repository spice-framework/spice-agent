package pluginv1_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"github.com/spice-framework/spice-agent/tool"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestInitializeHandshakeAuthenticatesCompleteContract(t *testing.T) {
	t.Parallel()
	request := validInitializeRequest()
	response := validInitializeResponse(t)
	signed, err := pluginv1.SignInitializeResponse(request, response, handshakeSecret())
	if err != nil {
		t.Fatal(err)
	}
	if err = pluginv1.ValidateInitializeResponseForRequest(request, signed, handshakeSecret()); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*pluginv1.InitializeResponse){
		"launch":     func(value *pluginv1.InitializeResponse) { value.LaunchId[0]++ },
		"challenge":  func(value *pluginv1.InitializeResponse) { value.HandshakeChallenge[0]++ },
		"protocol":   func(value *pluginv1.InitializeResponse) { value.Protocol.Minor++ },
		"manifest":   func(value *pluginv1.InitializeResponse) { value.Manifest.Name = "changed" },
		"proof":      func(value *pluginv1.InitializeResponse) { value.HandshakeProof[0]++ },
		"unknown":    func(value *pluginv1.InitializeResponse) { value.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01}) },
		"capability": func(value *pluginv1.InitializeResponse) { value.Capabilities.Names = []string{"other"} },
	} {
		candidate := cloneMessage(signed)
		mutate(candidate)
		if err = pluginv1.ValidateInitializeResponseForRequest(request, candidate, handshakeSecret()); err == nil {
			t.Errorf("tampered %s response succeeded", name)
		}
	}
	wrongSecret := bytes.Repeat([]byte{0x99}, pluginv1.HandshakeSecretBytes)
	if err = pluginv1.ValidateInitializeResponseForRequest(request, signed, wrongSecret); err == nil {
		t.Fatal("wrong launch secret succeeded")
	}
}

func TestInitializeFailureIsAuthenticatedAndContainsNoSuccessContract(t *testing.T) {
	t.Parallel()
	request := validInitializeRequest()
	failure := &pluginv1.InitializeResponse{
		Status: &commonv1.Status{
			Code:    commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
			Message: "plugin initialization was rejected",
		},
		Plugin:             validBuild("fixture"),
		LaunchId:           slices.Clone(request.GetLaunchId()),
		HandshakeChallenge: slices.Clone(request.GetHandshakeChallenge()),
	}
	signed, err := pluginv1.SignInitializeResponse(request, failure, handshakeSecret())
	if err != nil {
		t.Fatal(err)
	}
	err = pluginv1.ValidateInitializeResponseForRequest(request, signed, handshakeSecret())
	var statusErr *commonv1.StatusError
	if !errors.As(err, &statusErr) || statusErr.Status().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("failure = %T %v", err, err)
	}

	signed.Protocol = &commonv1.ProtocolVersion{Major: 1}
	resigned, err := pluginv1.SignInitializeResponse(request, signed, handshakeSecret())
	if err != nil {
		t.Fatal(err)
	}
	if err = pluginv1.ValidateInitializeResponseForRequest(request, resigned, handshakeSecret()); err == nil {
		t.Fatal("failed response with negotiated fields succeeded")
	}
}

func TestInitializeRejectsAuthenticatedNegotiationMismatch(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*pluginv1.InitializeRequest, *pluginv1.InitializeResponse){
		"new major": func(_ *pluginv1.InitializeRequest, response *pluginv1.InitializeResponse) {
			response.Protocol.Major = 2
		},
		"new patch": func(_ *pluginv1.InitializeRequest, response *pluginv1.InitializeResponse) {
			response.Protocol.Patch = 1
		},
		"old host range": func(request *pluginv1.InitializeRequest, _ *pluginv1.InitializeResponse) {
			request.Protocol = &commonv1.ProtocolRange{
				Minimum: &commonv1.ProtocolVersion{Major: 2},
				Maximum: &commonv1.ProtocolVersion{Major: 2},
			}
		},
		"unrequested capability": func(_ *pluginv1.InitializeRequest, response *pluginv1.InitializeResponse) {
			response.Capabilities.Names = []string{"other"}
		},
		"larger limits": func(_ *pluginv1.InitializeRequest, response *pluginv1.InitializeResponse) {
			response.Limits.MaxConcurrentCalls++
		},
		"duplicate tool": func(_ *pluginv1.InitializeRequest, response *pluginv1.InitializeResponse) {
			response.Manifest.Tools = append(response.Manifest.Tools, cloneMessage(response.Manifest.Tools[0]))
		},
	} {
		request := validInitializeRequest()
		response := validInitializeResponse(t)
		mutate(request, response)
		signed, err := pluginv1.SignInitializeResponse(request, response, handshakeSecret())
		if err != nil {
			t.Fatal(err)
		}
		if err = pluginv1.ValidateInitializeResponseForRequest(request, signed, handshakeSecret()); err == nil {
			t.Errorf("authenticated %s mismatch succeeded", name)
		}
	}
}

func TestInitializeValidationFailsClosedAndDoesNotReflectSecrets(t *testing.T) {
	t.Parallel()
	secretMarker := "SECRET-MARKER-DO-NOT-RETURN"
	request := validInitializeRequest()
	request.Host.Component = secretMarker + "\n"
	err := pluginv1.ValidateInitializeRequest(request)
	if err == nil || strings.Contains(err.Error(), secretMarker) {
		t.Fatalf("unsafe request error = %v", err)
	}

	for name, mutate := range map[string]func(*pluginv1.InitializeRequest){
		"nil protocol": func(value *pluginv1.InitializeRequest) { value.Protocol = nil },
		"build":        func(value *pluginv1.InitializeRequest) { value.Host = nil },
		"capabilities": func(value *pluginv1.InitializeRequest) { value.RequiredCapabilities.Names = []string{"z"} },
		"limits":       func(value *pluginv1.InitializeRequest) { value.RequestedLimits.MaxTools = 0 },
		"launch":       func(value *pluginv1.InitializeRequest) { value.LaunchId = nil },
		"challenge":    func(value *pluginv1.InitializeRequest) { value.HandshakeChallenge = nil },
	} {
		candidate := validInitializeRequest()
		mutate(candidate)
		if err = pluginv1.ValidateInitializeRequest(candidate); err == nil {
			t.Errorf("invalid %s request succeeded", name)
		}
	}
	if _, err = pluginv1.SignInitializeResponse(validInitializeRequest(), validInitializeResponse(t), []byte("short")); err == nil {
		t.Fatal("short handshake secret succeeded")
	}
}

func TestInitializeResponseRejectsIncompleteSuccessShapes(t *testing.T) {
	t.Parallel()
	request := validInitializeRequest()
	if err := pluginv1.ValidateInitializeResponseForRequest(request, nil, handshakeSecret()); err == nil {
		t.Fatal("nil initialize response succeeded")
	}
	for name, mutate := range map[string]func(*pluginv1.InitializeResponse){
		"status":       func(value *pluginv1.InitializeResponse) { value.Status = nil },
		"build":        func(value *pluginv1.InitializeResponse) { value.Plugin = nil },
		"capabilities": func(value *pluginv1.InitializeResponse) { value.Capabilities = nil },
		"limits":       func(value *pluginv1.InitializeResponse) { value.Limits = nil },
		"manifest":     func(value *pluginv1.InitializeResponse) { value.Manifest = nil },
		"session":      func(value *pluginv1.InitializeResponse) { value.SessionId = nil },
		"proof":        func(value *pluginv1.InitializeResponse) { value.HandshakeProof = nil },
	} {
		response := validInitializeResponse(t)
		signed, err := pluginv1.SignInitializeResponse(request, response, handshakeSecret())
		if err != nil {
			t.Fatal(err)
		}
		mutate(signed)
		if err = pluginv1.ValidateInitializeResponseForRequest(request, signed, handshakeSecret()); err == nil {
			t.Errorf("incomplete %s response succeeded", name)
		}
	}
}

func TestCatalogConvertsToImmutableKernelDefinitions(t *testing.T) {
	t.Parallel()
	read, err := tool.NewDefinition(
		"read",
		"Read one value.",
		json.RawMessage(`{"type":"object"}`),
		tool.EffectReadOnly,
		tool.ReplaySafe,
		tool.CapabilityFilesystemRead,
	)
	if err != nil {
		t.Fatal(err)
	}
	write, err := tool.NewDefinition(
		"write",
		"Write one value.",
		json.RawMessage(`{"type":"object"}`),
		tool.EffectMutating,
		tool.ReplayUnsafe,
		tool.CapabilityFilesystemWrite,
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := pluginv1.NewCatalog("fixture", "v1", []tool.Definition{write, read}, validLimits())
	if err != nil {
		t.Fatal(err)
	}
	definitions := catalog.Definitions()
	if catalog.Name() != "fixture" || catalog.Version() != "v1" ||
		len(definitions) != 2 || definitions[0].Name() != "read" || definitions[1].Name() != "write" {
		t.Fatalf("catalog = %q %q %#v", catalog.Name(), catalog.Version(), definitions)
	}
	manifest, err := catalog.Manifest()
	if err != nil {
		t.Fatal(err)
	}
	manifest.Name = "mutated"
	manifest.Tools[0].InputSchemaJson[0] = '['
	definitions[0] = write
	if catalog.Name() != "fixture" || catalog.Definitions()[0].Name() != "read" ||
		string(catalog.Definitions()[0].InputSchema()) != `{"type":"object"}` {
		t.Fatal("catalog aliases returned mutable state")
	}
}

func TestCatalogRejectsDuplicateUnknownAndOversizedDefinitions(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*pluginv1.Manifest){
		"duplicate": func(value *pluginv1.Manifest) {
			value.Tools = append(value.Tools, cloneMessage(value.Tools[0]))
		},
		"unsorted": func(value *pluginv1.Manifest) {
			value.Tools[0].Name = "z"
			second := cloneMessage(value.Tools[0])
			second.Name = "a"
			value.Tools = append(value.Tools, second)
		},
		"effect": func(value *pluginv1.Manifest) { value.Tools[0].Effect = pluginv1.ToolEffect(99) },
		"replay": func(value *pluginv1.Manifest) { value.Tools[0].ReplaySafety = pluginv1.ReplaySafety(99) },
		"capability": func(value *pluginv1.Manifest) {
			value.Tools[0].Capabilities.Names = []string{"secret-value"}
		},
		"schema": func(value *pluginv1.Manifest) {
			value.Tools[0].InputSchemaJson = []byte(`"` +
				strings.Repeat("x", int(validLimits().GetMaxSchemaBytes())) + `"`)
		},
	} {
		candidate := validManifest(t)
		mutate(candidate)
		if _, err := pluginv1.DecodeManifest(candidate, validLimits()); err == nil {
			t.Errorf("invalid %s manifest succeeded", name)
		} else if strings.Contains(err.Error(), "secret-value") {
			t.Errorf("%s error reflected input: %v", name, err)
		}
	}
}

func TestExecuteStreamConvertsProgressResultAndRejectsPostTerminal(t *testing.T) {
	t.Parallel()
	request := validExecuteRequest()
	validator, err := pluginv1.NewStreamValidator(request, sessionID(), validLimits())
	if err != nil {
		t.Fatal(err)
	}
	progressFrame, err := validator.Accept(&pluginv1.ExecuteResponse{
		CallId:   request.GetCallId(),
		Sequence: 1,
		Frame:    &pluginv1.ExecuteResponse_Progress{Progress: &pluginv1.Progress{Message: "working"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	progress, ok := progressFrame.Progress()
	if progressFrame.Kind() != pluginv1.FrameProgress || !ok ||
		progress.CallID() != tool.CallID(request.GetCallId()) || progress.Message() != "working" {
		t.Fatalf("progress = %#v %t", progress, ok)
	}
	resultFrame, err := validator.Accept(&pluginv1.ExecuteResponse{
		CallId:   request.GetCallId(),
		Sequence: 2,
		Frame:    &pluginv1.ExecuteResponse_Result{Result: &pluginv1.Result{ContentJson: []byte(`{"ok":true}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := resultFrame.Result()
	if resultFrame.Kind() != pluginv1.FrameResult || !ok ||
		result.CallID() != tool.CallID(request.GetCallId()) || string(result.Content()) != `{"ok":true}` {
		t.Fatalf("result = %#v %t", result, ok)
	}
	if err = validator.Finish(); err != nil {
		t.Fatal(err)
	}
	if _, err = validator.Accept(&pluginv1.ExecuteResponse{
		CallId: request.GetCallId(), Sequence: 3,
		Frame: &pluginv1.ExecuteResponse_Progress{Progress: &pluginv1.Progress{Message: "late"}},
	}); err == nil {
		t.Fatal("post-terminal progress succeeded")
	}
}

func TestExecuteStreamConvertsTypedFailure(t *testing.T) {
	t.Parallel()
	request := validExecuteRequest()
	validator, err := pluginv1.NewStreamValidator(request, sessionID(), validLimits())
	if err != nil {
		t.Fatal(err)
	}
	frame, err := validator.Accept(&pluginv1.ExecuteResponse{
		CallId:   request.GetCallId(),
		Sequence: 1,
		Frame: &pluginv1.ExecuteResponse_Failure{Failure: &pluginv1.ExecutionFailure{
			State:       pluginv1.ExecutionState_EXECUTION_STATE_UNCERTAIN,
			Retry:       pluginv1.RetryDisposition_RETRY_DISPOSITION_NEVER,
			SafeMessage: "completion could not be confirmed",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	failure, ok := frame.Failure()
	if frame.Kind() != pluginv1.FrameFailure || !ok || failure.CallID() != tool.CallID(request.GetCallId()) ||
		failure.State() != tool.ExecutionUncertain || failure.RetryDisposition() != tool.RetryNever {
		t.Fatalf("failure = %#v %t", failure, ok)
	}
	if err = validator.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteStreamFailsClosedOnMalformedShapes(t *testing.T) {
	t.Parallel()
	request := validExecuteRequest()
	for name, frame := range map[string]*pluginv1.ExecuteResponse{
		"nil":        nil,
		"call":       {CallId: "other", Sequence: 1, Frame: validProgressFrame()},
		"sequence":   {CallId: request.GetCallId(), Sequence: 2, Frame: validProgressFrame()},
		"payload":    {CallId: request.GetCallId(), Sequence: 1},
		"progress":   {CallId: request.GetCallId(), Sequence: 1, Frame: &pluginv1.ExecuteResponse_Progress{}},
		"empty text": {CallId: request.GetCallId(), Sequence: 1, Frame: &pluginv1.ExecuteResponse_Progress{Progress: &pluginv1.Progress{}}},
		"large progress": {CallId: request.GetCallId(), Sequence: 1, Frame: &pluginv1.ExecuteResponse_Progress{
			Progress: &pluginv1.Progress{Message: strings.Repeat("x", tool.MaximumProgressBytes+1)},
		}},
		"result": {CallId: request.GetCallId(), Sequence: 1, Frame: &pluginv1.ExecuteResponse_Result{
			Result: &pluginv1.Result{ContentJson: []byte("bad")},
		}},
		"uncertain retry": {CallId: request.GetCallId(), Sequence: 1, Frame: &pluginv1.ExecuteResponse_Failure{
			Failure: &pluginv1.ExecutionFailure{
				State: pluginv1.ExecutionState_EXECUTION_STATE_UNCERTAIN,
				Retry: pluginv1.RetryDisposition_RETRY_DISPOSITION_ALLOWED, SafeMessage: "failed",
			},
		}},
	} {
		validator, err := pluginv1.NewStreamValidator(request, sessionID(), validLimits())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = validator.Accept(frame); err == nil {
			t.Errorf("invalid %s frame succeeded", name)
		}
	}
	validator, err := pluginv1.NewStreamValidator(request, sessionID(), validLimits())
	if err != nil {
		t.Fatal(err)
	}
	if err = validator.Finish(); err == nil {
		t.Fatal("unterminated stream succeeded")
	}
	if _, err = validator.Accept(&pluginv1.ExecuteResponse{
		CallId: request.GetCallId(), Sequence: 1, Frame: validProgressFrame(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = validator.Accept(&pluginv1.ExecuteResponse{
		CallId: request.GetCallId(), Sequence: 1, Frame: validProgressFrame(),
	}); err == nil {
		t.Fatal("duplicate progress sequence succeeded")
	}
	secretMarker := "SECRET-MARKER-DO-NOT-RETURN"
	validator, err = pluginv1.NewStreamValidator(request, sessionID(), validLimits())
	if err != nil {
		t.Fatal(err)
	}
	_, err = validator.Accept(&pluginv1.ExecuteResponse{
		CallId: request.GetCallId(), Sequence: 1,
		Frame: &pluginv1.ExecuteResponse_Failure{Failure: &pluginv1.ExecutionFailure{
			State:       pluginv1.ExecutionState_EXECUTION_STATE_DEFINITIVE,
			Retry:       pluginv1.RetryDisposition_RETRY_DISPOSITION_NEVER,
			SafeMessage: secretMarker + "\n",
		}},
	})
	if err == nil || strings.Contains(err.Error(), secretMarker) {
		t.Fatalf("unsafe failure validation error = %v", err)
	}
}

func TestExecuteUnknownFieldsCountTowardNegotiatedSize(t *testing.T) {
	t.Parallel()
	limits := &pluginv1.Limits{
		MaxMessageBytes:      128,
		MaxTools:             1,
		MaxSchemaBytes:       64,
		MaxCallArgumentBytes: 64,
		MaxResultBytes:       64,
		MaxProgressBytes:     64,
		MaxConcurrentCalls:   1,
	}
	validator, err := pluginv1.NewStreamValidator(validExecuteRequest(), sessionID(), limits)
	if err != nil {
		t.Fatal(err)
	}
	frame := &pluginv1.ExecuteResponse{
		CallId: "call-1", Sequence: 1, Frame: validProgressFrame(),
	}
	unknown := protowire.AppendTag(nil, 127, protowire.BytesType)
	unknown = protowire.AppendBytes(unknown, bytes.Repeat([]byte{'x'}, 128))
	frame.ProtoReflect().SetUnknown(unknown)
	if _, err = validator.Accept(frame); err == nil {
		t.Fatal("oversized unknown fields succeeded")
	}
}

func TestExecuteRequestAndLifecycleAreSessionBounded(t *testing.T) {
	t.Parallel()
	request := validExecuteRequest()
	call, err := pluginv1.DecodeExecuteRequest(request, sessionID(), validLimits())
	if err != nil || call.Name() != "echo" {
		t.Fatalf("call = %#v, %v", call, err)
	}
	wrong := slices.Clone(sessionID())
	wrong[0]++
	if _, err = pluginv1.DecodeExecuteRequest(request, wrong, validLimits()); err == nil {
		t.Fatal("wrong execute session succeeded")
	}
	for name, mutate := range map[string]func(*pluginv1.ExecuteRequest){
		"call control": func(value *pluginv1.ExecuteRequest) { value.CallId = "bad\ncall" },
		"call UTF-8": func(value *pluginv1.ExecuteRequest) {
			value.CallId = string([]byte{'b', 0xff})
		},
		"tool control": func(value *pluginv1.ExecuteRequest) { value.ToolName = "bad\ttool" },
		"tool UTF-8": func(value *pluginv1.ExecuteRequest) {
			value.ToolName = string([]byte{'b', 0xff})
		},
	} {
		invalid := validExecuteRequest()
		mutate(invalid)
		if _, err = pluginv1.DecodeExecuteRequest(invalid, sessionID(), validLimits()); err == nil {
			t.Errorf("invalid %s succeeded", name)
		}
	}
	if err = pluginv1.ValidateDrainRequest(&pluginv1.DrainRequest{SessionId: sessionID()}, sessionID(), validLimits()); err != nil {
		t.Fatal(err)
	}
	if err = pluginv1.ValidateDrainResponse(&pluginv1.DrainResponse{Status: commonv1.OKStatus()}, validLimits()); err != nil {
		t.Fatal(err)
	}
	if err = pluginv1.ValidateDrainResponse(&pluginv1.DrainResponse{Status: commonv1.OKStatus(), ActiveCalls: 1}, validLimits()); err == nil {
		t.Fatal("successful drain with active calls succeeded")
	}
	if err = pluginv1.ValidateShutdownRequest(&pluginv1.ShutdownRequest{SessionId: sessionID()}, sessionID(), validLimits()); err != nil {
		t.Fatal(err)
	}
	if err = pluginv1.ValidateShutdownResponse(&pluginv1.ShutdownResponse{Status: commonv1.OKStatus()}, validLimits()); err != nil {
		t.Fatal(err)
	}
	for name, validate := range map[string]func() error{
		"nil drain request": func() error {
			return pluginv1.ValidateDrainRequest(nil, sessionID(), validLimits())
		},
		"nil drain response": func() error {
			return pluginv1.ValidateDrainResponse(nil, validLimits())
		},
		"invalid drain status": func() error {
			return pluginv1.ValidateDrainResponse(&pluginv1.DrainResponse{}, validLimits())
		},
		"large active drain": func() error {
			return pluginv1.ValidateDrainResponse(&pluginv1.DrainResponse{
				Status:      &commonv1.Status{Code: commonv1.ErrorCode_ERROR_CODE_UNAVAILABLE, Message: "still active"},
				ActiveCalls: validLimits().GetMaxConcurrentCalls() + 1,
			}, validLimits())
		},
		"nil shutdown request": func() error {
			return pluginv1.ValidateShutdownRequest(nil, sessionID(), validLimits())
		},
		"nil shutdown response": func() error {
			return pluginv1.ValidateShutdownResponse(nil, validLimits())
		},
		"invalid shutdown status": func() error {
			return pluginv1.ValidateShutdownResponse(&pluginv1.ShutdownResponse{}, validLimits())
		},
		"nil lifecycle limits": func() error {
			return pluginv1.ValidateShutdownResponse(&pluginv1.ShutdownResponse{Status: commonv1.OKStatus()}, nil)
		},
	} {
		if err = validate(); err == nil {
			t.Errorf("invalid %s succeeded", name)
		}
	}
	if _, err = pluginv1.DecodeExecuteRequest(nil, sessionID(), validLimits()); err == nil {
		t.Fatal("nil execute request succeeded")
	}
	if _, err = pluginv1.NewStreamValidator(nil, sessionID(), validLimits()); err == nil {
		t.Fatal("nil stream request succeeded")
	}
	if _, err = (pluginv1.Catalog{}).Manifest(); err == nil {
		t.Fatal("zero catalog manifest succeeded")
	}
}

func TestUnknownFieldsRoundTripAndServiceShape(t *testing.T) {
	t.Parallel()
	encoded, err := proto.Marshal(validExecuteRequest())
	if err != nil {
		t.Fatal(err)
	}
	unknown := protowire.AppendTag(nil, 127, protowire.BytesType)
	unknown = protowire.AppendBytes(unknown, []byte("future"))
	encoded = append(encoded, unknown...)
	var decoded pluginv1.ExecuteRequest
	if err = proto.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := proto.Marshal(&decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(roundTrip, unknown) {
		t.Fatal("unknown plugin field was not preserved")
	}

	file := (&pluginv1.ExecuteRequest{}).ProtoReflect().Descriptor().ParentFile()
	service := file.Services().ByName("PluginService")
	if service == nil || service.Methods().Len() != 4 {
		t.Fatalf("service = %#v", service)
	}
	execute := service.Methods().ByName("Execute")
	if execute == nil || execute.IsStreamingClient() || !execute.IsStreamingServer() {
		t.Fatalf("execute method = %#v", execute)
	}
}

func TestLimitNegotiationAndFailures(t *testing.T) {
	t.Parallel()
	selected, err := pluginv1.NegotiateLimits(validLimits(), &pluginv1.Limits{
		MaxMessageBytes:      512 << 10,
		MaxTools:             8,
		MaxSchemaBytes:       32 << 10,
		MaxCallArgumentBytes: 16 << 10,
		MaxResultBytes:       64 << 10,
		MaxProgressBytes:     1024,
		MaxConcurrentCalls:   2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if selected.GetMaxMessageBytes() != 512<<10 || selected.GetMaxTools() != 8 ||
		selected.GetMaxProgressBytes() != 1024 || selected.GetMaxConcurrentCalls() != 2 {
		t.Fatalf("selected limits = %#v", selected)
	}
	for name, value := range map[string]*pluginv1.Limits{
		"nil":        nil,
		"zero":       {},
		"tools":      {MaxMessageBytes: 1, MaxTools: 5000, MaxSchemaBytes: 1, MaxCallArgumentBytes: 1, MaxResultBytes: 1, MaxProgressBytes: 1, MaxConcurrentCalls: 1},
		"submessage": {MaxMessageBytes: 1, MaxTools: 1, MaxSchemaBytes: 2, MaxCallArgumentBytes: 1, MaxResultBytes: 1, MaxProgressBytes: 1, MaxConcurrentCalls: 1},
		"message":    {MaxMessageBytes: pluginv1.InitializeBootstrapMaximumBytes + 1, MaxTools: 1, MaxSchemaBytes: 1, MaxCallArgumentBytes: 1, MaxResultBytes: 1, MaxProgressBytes: 1, MaxConcurrentCalls: 1},
	} {
		if err = pluginv1.ValidateLimits(value); err == nil {
			t.Errorf("invalid %s limits succeeded", name)
		}
	}
	if _, err = pluginv1.NegotiateLimits(nil, validLimits()); err == nil {
		t.Fatal("nil requested limits succeeded")
	}
	if _, err = pluginv1.NegotiateLimits(validLimits(), nil); err == nil {
		t.Fatal("nil available limits succeeded")
	}
}

func validInitializeRequest() *pluginv1.InitializeRequest {
	return &pluginv1.InitializeRequest{
		Protocol:              pluginv1.SupportedProtocolRange(),
		Host:                  validBuild("host"),
		SupportedCapabilities: &commonv1.CapabilitySet{Names: []string{pluginv1.CapabilityRuntimeToolsV1}},
		RequiredCapabilities:  &commonv1.CapabilitySet{Names: []string{pluginv1.CapabilityRuntimeToolsV1}},
		RequestedLimits:       validLimits(),
		LaunchId:              bytes.Repeat([]byte{0x11}, pluginv1.LaunchIDBytes),
		HandshakeChallenge:    bytes.Repeat([]byte{0x22}, pluginv1.HandshakeChallengeBytes),
	}
}

func validInitializeResponse(t *testing.T) *pluginv1.InitializeResponse {
	t.Helper()
	request := validInitializeRequest()
	pluginBuild := validBuild("plugin")
	pluginBuild.Runtime = "python3.12"
	return &pluginv1.InitializeResponse{
		Status:             commonv1.OKStatus(),
		Protocol:           &commonv1.ProtocolVersion{Major: 1},
		Plugin:             pluginBuild,
		Capabilities:       &commonv1.CapabilitySet{Names: []string{pluginv1.CapabilityRuntimeToolsV1}},
		Limits:             validLimits(),
		Manifest:           validManifest(t),
		LaunchId:           slices.Clone(request.GetLaunchId()),
		SessionId:          sessionID(),
		HandshakeChallenge: slices.Clone(request.GetHandshakeChallenge()),
	}
}

func validManifest(t *testing.T) *pluginv1.Manifest {
	t.Helper()
	definition, err := tool.NewDefinition(
		"echo",
		"Echo one value.",
		json.RawMessage(`{"type":"object"}`),
		tool.EffectReadOnly,
		tool.ReplaySafe,
	)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := pluginv1.EncodeToolDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	return &pluginv1.Manifest{Name: "fixture", Version: "v1", Tools: []*pluginv1.ToolDefinition{wire}}
}

func validExecuteRequest() *pluginv1.ExecuteRequest {
	return &pluginv1.ExecuteRequest{
		SessionId:     sessionID(),
		CallId:        "call-1",
		ToolName:      "echo",
		ArgumentsJson: []byte(`{"value":"hello"}`),
	}
}

func validProgressFrame() *pluginv1.ExecuteResponse_Progress {
	return &pluginv1.ExecuteResponse_Progress{Progress: &pluginv1.Progress{Message: "working"}}
}

func validBuild(component string) *pluginv1.BuildIdentity {
	return &pluginv1.BuildIdentity{
		Component: component,
		Version:   "v0.1.0",
		Commit:    "0123456789abcdef",
		Runtime:   "go1.26.5",
	}
}

func validLimits() *pluginv1.Limits {
	return &pluginv1.Limits{
		MaxMessageBytes:      1 << 20,
		MaxTools:             32,
		MaxSchemaBytes:       64 << 10,
		MaxCallArgumentBytes: 64 << 10,
		MaxResultBytes:       1 << 20,
		MaxProgressBytes:     tool.MaximumProgressBytes,
		MaxConcurrentCalls:   8,
	}
}

func sessionID() []byte {
	return bytes.Repeat([]byte{0x33}, pluginv1.SessionIDBytes)
}

func handshakeSecret() []byte {
	return bytes.Repeat([]byte{0x44}, pluginv1.HandshakeSecretBytes)
}

func cloneMessage[T proto.Message](value T) T {
	result, ok := proto.Clone(value).(T)
	if !ok {
		panic("protobuf clone changed concrete test message type")
	}
	return result
}
