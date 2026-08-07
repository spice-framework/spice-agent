package enginev1_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestInitializePreflightPrecedesOwnershipAndIsDeterministic(t *testing.T) {
	t.Parallel()
	request := validInitializeRequest()
	negotiation, failure := enginev1.PreflightInitialize(
		request, commonv1.SupportedProtocolRange(), build("spice-agentd"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), health(), definitionSet(),
	)
	if failure != nil || negotiation == nil {
		t.Fatalf("preflight = %#v, failure=%#v", negotiation, failure)
	}

	// Mutation after preflight cannot change the completed contract.
	request.Client.Component = "mutated"
	response := enginev1.CompleteInitialize(negotiation, "client-1", 1)
	if err := enginev1.ValidateInitializeResponse(response); err != nil {
		t.Fatal(err)
	}
	if response.GetServer().GetComponent() != "spice-agentd" {
		t.Fatal("preflight retained caller-owned server identity")
	}
	first, err := (proto.MarshalOptions{Deterministic: true}).Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	second, err := (proto.MarshalOptions{Deterministic: true}).Marshal(
		enginev1.CompleteInitialize(negotiation, "client-1", 1),
	)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("initialize completion is nondeterministic: %v", err)
	}

	sessionOwnershipAllocations := 0
	invalid := validInitializeRequest()
	invalid.RequiredCapabilities = capabilities("unavailable")
	invalid.SupportedCapabilities = capabilities("events", "unavailable")
	prepared, rejection := enginev1.PreflightInitialize(
		invalid, commonv1.SupportedProtocolRange(), build("spice-agentd"),
		capabilities("events"), protocolLimits(), health(), definitionSet(),
	)
	if rejection == nil {
		t.Fatalf("invalid preflight returned no rejection: %#v", prepared)
	}
	if prepared != nil {
		sessionOwnershipAllocations++
	}
	if sessionOwnershipAllocations != 0 {
		t.Fatal("invalid negotiation allocated ownership")
	}
	if err := enginev1.ValidateInitializeResponse(rejection); err == nil {
		t.Fatal("negotiation rejection did not surface its typed status")
	}
	rejection.ClientId = "must-not-be-active"
	if err := enginev1.ValidateInitializeResponse(rejection); err == nil || !strings.Contains(err.Error(), "negotiated fields") {
		t.Fatalf("failed initialize response accepted active fields: %v", err)
	}
}

func TestInitializePreflightReconnectAndFailureBoundaries(t *testing.T) {
	t.Parallel()
	request := validInitializeRequest()
	request.ReconnectClaim = &enginev1.ReconnectClaim{ClientId: "client-1", ExpectedOwnershipEpoch: 9}
	negotiation, failure := enginev1.PreflightInitialize(
		request, commonv1.SupportedProtocolRange(), build("spice-agentd"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), health(), definitionSet(),
	)
	if failure != nil {
		t.Fatal(failure)
	}
	if negotiation == nil {
		t.Fatal("successful reconnect preflight returned no negotiation")
		return
	}
	claim := negotiation.ReconnectClaim()
	if claim == nil {
		t.Fatal("reconnect preflight lost its claim")
		return
	}
	claim.ClientId = "mutated"
	repeatedClaim := negotiation.ReconnectClaim()
	if repeatedClaim == nil || repeatedClaim.GetClientId() != "client-1" {
		t.Fatal("negotiation exposed reconnect claim storage")
	}
	if response := enginev1.CompleteInitialize(negotiation, "client-1", 10); enginev1.ValidateInitializeResponseForRequest(request, response) != nil {
		t.Fatalf("valid reconnect completion = %#v", response)
	}
	if response := enginev1.CompleteInitialize(negotiation, "client-1", 11); response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INTERNAL {
		t.Fatalf("invalid reconnect completion = %#v", response)
	}
	if response := enginev1.CompleteInitialize(nil, "client", 1); response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INTERNAL {
		t.Fatalf("nil completion = %#v", response)
	}

	for name, invoke := range map[string]func() (*enginev1.InitializeNegotiation, *enginev1.InitializeResponse){
		"nil request": func() (*enginev1.InitializeNegotiation, *enginev1.InitializeResponse) {
			return enginev1.PreflightInitialize(nil, commonv1.SupportedProtocolRange(), build("server"), capabilities("events"), protocolLimits(), health(), definitionSet())
		},
		"invalid server range": func() (*enginev1.InitializeNegotiation, *enginev1.InitializeResponse) {
			return enginev1.PreflightInitialize(validInitializeRequest(), nil, build("server"), capabilities("events"), protocolLimits(), health(), definitionSet())
		},
		"invalid server capabilities": func() (*enginev1.InitializeNegotiation, *enginev1.InitializeResponse) {
			return enginev1.PreflightInitialize(validInitializeRequest(), commonv1.SupportedProtocolRange(), build("server"), nil, protocolLimits(), health(), definitionSet())
		},
	} {
		prepared, response := invoke()
		if prepared != nil || response == nil || response.GetStatus().GetCode() == commonv1.ErrorCode_ERROR_CODE_OK {
			t.Errorf("%s preflight = %#v, %#v", name, prepared, response)
		}
	}

	tooSmall := validInitializeRequest()
	tooSmall.RequestedLimits.MaxMessageBytes = 1
	if prepared, response := enginev1.PreflightInitialize(
		tooSmall, commonv1.SupportedProtocolRange(), build("server"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), health(), definitionSet(),
	); prepared != nil || response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("oversized initialize request = %#v, %#v", prepared, response)
	}

	requestBoundary := validInitializeRequest()
	for range 4 {
		requestBoundary.RequestedLimits.MaxMessageBytes = uint64(proto.Size(requestBoundary))
	}
	if uint64(proto.Size(requestBoundary)) != requestBoundary.GetRequestedLimits().GetMaxMessageBytes() {
		t.Fatal("initialize request size boundary did not converge")
	}
	prepared, response := enginev1.PreflightInitialize(
		requestBoundary, commonv1.SupportedProtocolRange(), build("server"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), health(), definitionSet(),
	)
	if prepared != nil || response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT ||
		response.GetClientId() != "" || response.GetDefinitions() != nil {
		t.Fatalf("preflight accepted a request whose success response cannot fit = %#v, %#v", prepared, response)
	}

	worstRequest := validInitializeRequest()
	worstRequest.ReconnectClaim = &enginev1.ReconnectClaim{
		ClientId: strings.Repeat("c", 128), ExpectedOwnershipEpoch: math.MaxUint64 - 1,
	}
	worstNegotiation, failure := enginev1.PreflightInitialize(
		worstRequest, commonv1.SupportedProtocolRange(), build("server"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), health(), definitionSet(),
	)
	if failure != nil || worstNegotiation == nil {
		t.Fatalf("worst-case sizing preflight = %#v, %#v", worstNegotiation, failure)
	}
	worstResponse := enginev1.CompleteInitialize(worstNegotiation, strings.Repeat("c", 128), math.MaxUint64)
	for range 4 {
		worstResponse.Limits.MaxMessageBytes = uint64(proto.Size(worstResponse))
	}
	exactSuccessBytes := uint64(proto.Size(worstResponse))
	if worstResponse.GetLimits().GetMaxMessageBytes() != exactSuccessBytes {
		t.Fatal("initialize success response size boundary did not converge")
	}
	exactSuccess := validInitializeRequest()
	exactSuccess.ReconnectClaim = &enginev1.ReconnectClaim{
		ClientId: strings.Repeat("c", 128), ExpectedOwnershipEpoch: math.MaxUint64 - 1,
	}
	exactSuccess.RequestedLimits.MaxMessageBytes = exactSuccessBytes
	prepared, response = enginev1.PreflightInitialize(
		exactSuccess, commonv1.SupportedProtocolRange(), build("server"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), health(), definitionSet(),
	)
	if response != nil || prepared == nil {
		t.Fatalf("exact-size initialize success preflight = %#v, %#v", prepared, response)
	}
	exactResponse := enginev1.CompleteInitialize(prepared, strings.Repeat("c", 128), math.MaxUint64)
	if uint64(proto.Size(exactResponse)) != exactSuccessBytes ||
		exactResponse.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_OK {
		t.Fatalf("exact-size initialize success = size %d, response %#v", proto.Size(exactResponse), exactResponse)
	}
	oneByteShort := validInitializeRequest()
	oneByteShort.ReconnectClaim = &enginev1.ReconnectClaim{
		ClientId: strings.Repeat("c", 128), ExpectedOwnershipEpoch: math.MaxUint64 - 1,
	}
	oneByteShort.RequestedLimits.MaxMessageBytes = exactSuccessBytes - 1
	if prepared, response = enginev1.PreflightInitialize(
		oneByteShort, commonv1.SupportedProtocolRange(), build("server"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), health(), definitionSet(),
	); prepared != nil || response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("one-byte undersized initialize success bound = %#v, %#v", prepared, response)
	}

	unsupported := validInitializeRequest()
	unsupported.Protocol = versionRange(2)
	if prepared, response = enginev1.PreflightInitialize(
		unsupported, versionRange(2), build("server"), capabilities("events"),
		protocolLimits(), health(), definitionSet(),
	); prepared != nil || response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INTERNAL {
		t.Fatalf("unsupported local protocol selection = %#v, %#v", prepared, response)
	}

	capabilityBound := validInitializeRequest()
	capabilityBound.RequestedLimits.MaxCollectionItems = 1
	if prepared, response = enginev1.PreflightInitialize(
		capabilityBound, commonv1.SupportedProtocolRange(), build("server"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), health(), definitionSet(),
	); prepared != nil || response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("capabilities escaped negotiated collection bound: %#v, %#v", prepared, response)
	}

	definitionBound := validInitializeRequest()
	definitionBound.SupportedCapabilities = capabilities("events")
	definitionBound.RequiredCapabilities = capabilities("events")
	definitionBound.RequestedLimits.MaxCollectionItems = 1
	definitions := definitionSet()
	definitions.Definitions = append(definitions.Definitions, &enginev1.Definition{
		Id: "review", Revision: "v1", Model: "reasoning", MaxTurns: 2,
	})
	if prepared, response = enginev1.PreflightInitialize(
		definitionBound, commonv1.SupportedProtocolRange(), build("server"),
		capabilities("events"), protocolLimits(), health(), definitions,
	); prepared != nil || response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("definitions escaped negotiated collection bound: %#v, %#v", prepared, response)
	}
	healthBound := validInitializeRequest()
	healthBound.SupportedCapabilities = capabilities("events")
	healthBound.RequiredCapabilities = capabilities("events")
	healthBound.RequestedLimits.MaxCollectionItems = 1
	degraded := health()
	degraded.State = commonv1.HealthState_HEALTH_STATE_DEGRADED
	degraded.DegradedReasons = []string{"dependency-a", "dependency-b"}
	if prepared, response = enginev1.PreflightInitialize(
		healthBound, commonv1.SupportedProtocolRange(), build("server"),
		capabilities("events"), protocolLimits(), degraded, definitionSet(),
	); prepared != nil || response.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT {
		t.Fatalf("health reasons escaped negotiated collection bound: %#v, %#v", prepared, response)
	}
}

func TestInitializeBootstrapBoundIsFixedBeforeNegotiation(t *testing.T) {
	t.Parallel()
	exactRequest := padProtocolMessage(t, validInitializeRequest(), enginev1.InitializeBootstrapMaximumBytes)
	if err := enginev1.ValidateInitializeRequest(exactRequest); err != nil {
		t.Fatalf("exact bootstrap request: %v", err)
	}
	oversizedRequest := padProtocolMessage(t, validInitializeRequest(), enginev1.InitializeBootstrapMaximumBytes+1)
	if err := enginev1.ValidateInitializeRequest(oversizedRequest); err == nil {
		t.Fatal("one-byte oversized bootstrap request succeeded")
	}

	errorResponse := &enginev1.InitializeResponse{
		Status: &commonv1.Status{Code: commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT, Message: "invalid initialize"},
	}
	exactResponse := padProtocolMessage(t, errorResponse, enginev1.InitializeBootstrapMaximumBytes)
	var statusErr *commonv1.StatusError
	if err := enginev1.ValidateInitializeResponse(exactResponse); !errors.As(err, &statusErr) {
		t.Fatalf("exact bootstrap error response = %T %v", err, err)
	}
	oversizedResponse := padProtocolMessage(t, errorResponse, enginev1.InitializeBootstrapMaximumBytes+1)
	if err := enginev1.ValidateInitializeResponse(oversizedResponse); err == nil || errors.As(err, &statusErr) {
		t.Fatalf("one-byte oversized bootstrap error response = %T %v", err, err)
	}

	invalid := validInitializeRequest()
	invalid.Protocol = versionRange(999)
	negotiation, failure := enginev1.PreflightInitialize(
		invalid, commonv1.SupportedProtocolRange(), build("server"), capabilities("events"),
		protocolLimits(), health(), definitionSet(),
	)
	if negotiation != nil || failure == nil || proto.Size(failure) > enginev1.InitializeBootstrapMaximumBytes {
		t.Fatalf("bootstrap failure = %#v, size=%d", negotiation, proto.Size(failure))
	}
}

func TestUnaryRequestValidatorsArePureBoundedAndFailClosed(t *testing.T) {
	t.Parallel()
	limits := protocolLimits()
	interaction := &enginev1.RespondInteractionRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "respond-1",
		RunId: "run-1", InteractionId: "interaction-1", ValueJson: []byte(`{"approved":true}`),
	}
	valid := map[string]func(*commonv1.Limits) error{
		"health": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateHealthRequest(&enginev1.HealthRequest{ClientId: "client", OwnershipEpoch: 1}, bounds)
		},
		"interaction": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateRespondInteractionRequest(interaction, bounds)
		},
		"export": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateExportSnapshotRequest(&enginev1.ExportSnapshotRequest{ClientId: "client", OwnershipEpoch: 1, RunId: "run-1"}, bounds)
		},
	}
	for name, validate := range valid {
		if err := validate(limits); err != nil {
			t.Errorf("valid %s request: %v", name, err)
		}
		if err := validate(limitsWithMessageBytes(1)); err == nil {
			t.Errorf("oversized %s request succeeded", name)
		}
	}
	for name, validate := range map[string]func(*commonv1.Limits) error{
		"cancel": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateCancelRunRequest(&enginev1.CancelRunRequest{
				ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "cancel-1", RunId: "run-1",
			}, bounds)
		},
		"suspend": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateSuspendRunRequest(&enginev1.SuspendRunRequest{
				ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "suspend-1", RunId: "run-1",
			}, bounds)
		},
		"resume": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateResumeRunRequest(&enginev1.ResumeRunRequest{
				ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "resume-1", RunId: "run-1",
			}, bounds)
		},
	} {
		if err := validate(limits); err != nil {
			t.Errorf("valid %s mutation: %v", name, err)
		}
		if err := validate(limitsWithMessageBytes(1)); err == nil {
			t.Errorf("oversized %s mutation succeeded", name)
		}
	}

	collectionLimits := protocolLimits()
	collectionLimits.MaxCollectionItems = 2
	start := validStartRunRequest()
	if err := enginev1.ValidateStartRunRequest(start, limitsWithMessageBytes(1)); err == nil {
		t.Fatal("oversized start request succeeded")
	}
	start.Input.Parts = append(start.Input.Parts, &enginev1.ContentPart{Value: &enginev1.ContentPart_Text{Text: "second"}})
	if err := enginev1.ValidateStartRunRequest(start, collectionLimits); err != nil {
		t.Fatalf("message at negotiated part bound: %v", err)
	}
	start.Input.Parts = append(start.Input.Parts, &enginev1.ContentPart{Value: &enginev1.ContentPart_Text{Text: "third"}})
	if err := enginev1.ValidateStartRunRequest(start, collectionLimits); err == nil {
		t.Fatal("message above negotiated part bound succeeded")
	}
	snapshot, err := enginev1.NewSnapshotEnvelope(
		t.Context(), snapshotAuthority(t), "run-import", 1,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED,
		[]byte(`{"run_id":"run-import"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	importRequest := &enginev1.ImportSnapshotRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "import-1", Snapshot: snapshot,
	}
	if err = enginev1.ValidateImportSnapshotRequest(t.Context(), importRequest, snapshotAuthority(t), limits); err != nil {
		t.Fatalf("valid import request: %v", err)
	}
	if err = enginev1.ValidateImportSnapshotRequest(t.Context(), importRequest, snapshotAuthority(t), limitsWithMessageBytes(1)); err == nil {
		t.Fatal("oversized import request succeeded")
	}
	if err := enginev1.ValidateHealthRequest(nil, limits); err == nil {
		t.Fatal("nil health request succeeded")
	}
	if err := enginev1.ValidateRespondInteractionRequest(nil, limits); err == nil {
		t.Fatal("nil interaction request succeeded")
	}
	if err := enginev1.ValidateExportSnapshotRequest(nil, limits); err == nil {
		t.Fatal("nil export request succeeded")
	}
	interaction.ValueJson = []byte("invalid")
	if err := enginev1.ValidateRespondInteractionRequest(interaction, limits); err == nil {
		t.Fatal("invalid interaction JSON succeeded")
	}
	if err := enginev1.ValidateExportSnapshotRequest(&enginev1.ExportSnapshotRequest{ClientId: "client", OwnershipEpoch: 1}, limits); err == nil {
		t.Fatal("export without a run ID succeeded")
	}
	for _, invalidClient := range []string{"client\x00forged", "client\rforged", "client\nforged", "client\tforged", string([]byte{0xff})} {
		if err := enginev1.ValidateHealthRequest(&enginev1.HealthRequest{ClientId: invalidClient, OwnershipEpoch: 1}, limits); err == nil {
			t.Errorf("unsafe client token %q succeeded", invalidClient)
		}
	}
}

func TestMaximumImportSnapshotHonorsNegotiatedMessageLimit(t *testing.T) {
	t.Parallel()
	runID := "run-maximum-import"
	prefix := []byte(`{"run_id":"` + runID + `","padding":"`)
	suffix := []byte(`"}`)
	payload := make([]byte, 0, enginev1.MaximumSnapshotBytes)
	payload = append(payload, prefix...)
	payload = append(payload, bytes.Repeat([]byte{'x'}, enginev1.MaximumSnapshotBytes-len(prefix)-len(suffix))...)
	payload = append(payload, suffix...)
	snapshot, err := enginev1.NewSnapshotEnvelope(
		t.Context(), snapshotAuthority(t), runID, 1,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED, payload,
	)
	if err != nil {
		t.Fatal(err)
	}
	request := &enginev1.ImportSnapshotRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "import-maximum", Snapshot: snapshot,
	}
	if err = enginev1.ValidateImportSnapshotRequest(
		t.Context(), request, snapshotAuthority(t), limitsWithMessageBytes(8<<20),
	); err == nil || !strings.Contains(err.Error(), "encoded message size") {
		t.Fatalf("16 MiB import under 8 MiB negotiated limit = %v", err)
	}
}

func TestUnaryResponseValidatorsEnforceSuccessAndErrorOnlyShapes(t *testing.T) {
	t.Parallel()
	limits := protocolLimits()
	snapshot, err := enginev1.NewSnapshotEnvelope(
		t.Context(), snapshotAuthority(t), "run-1", 7,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED,
		[]byte(`{"run_id":"run-1"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	valid := map[string]func(*commonv1.Limits) error{
		"health": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateHealthResponse(&enginev1.HealthResponse{
				Status: commonv1.OKStatus(), Server: build("server"),
				Protocol: &commonv1.ProtocolVersion{Major: 1, Minor: 1},
				Health:   healthForLimits(bounds),
			}, bounds)
		},
		"start": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateStartRunResponse(&enginev1.StartRunResponse{
				Status: commonv1.OKStatus(), RunId: "run-1", InitialSequence: 1, PlanId: "plan-1",
			}, bounds)
		},
		"cancel requested": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateCancelRunResponse(&enginev1.CancelRunResponse{Status: commonv1.OKStatus(), CancellationRequested: true}, bounds)
		},
		"cancel terminal": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateCancelRunResponse(&enginev1.CancelRunResponse{Status: commonv1.OKStatus(), AlreadyTerminal: true, TerminalSequence: 8}, bounds)
		},
		"interaction": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateRespondInteractionResponse(&enginev1.RespondInteractionResponse{Status: commonv1.OKStatus(), Accepted: true}, bounds)
		},
		"suspend": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateSuspendRunResponse(&enginev1.SuspendRunResponse{Status: commonv1.OKStatus(), Suspended: true, BoundarySequence: 8}, bounds)
		},
		"resume": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateResumeRunResponse(&enginev1.ResumeRunResponse{Status: commonv1.OKStatus(), Resumed: true, NextSequence: 9}, bounds)
		},
		"export": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateExportSnapshotResponse(&enginev1.ExportSnapshotResponse{Status: commonv1.OKStatus(), Snapshot: snapshot}, bounds)
		},
		"import": func(bounds *commonv1.Limits) error {
			return enginev1.ValidateImportSnapshotResponse(&enginev1.ImportSnapshotResponse{Status: commonv1.OKStatus(), RunId: "run-1", NextSequence: 9}, bounds)
		},
	}
	for name, validate := range valid {
		if err := validate(limits); err != nil {
			t.Errorf("valid %s response: %v", name, err)
		}
		if err := validate(limitsWithMessageBytes(1)); err == nil {
			t.Errorf("oversized %s response succeeded", name)
		}
	}

	errorStatus := &commonv1.Status{Code: commonv1.ErrorCode_ERROR_CODE_NOT_FOUND, Message: "run not found"}
	errorOnly := map[string]func() error{
		"health": func() error {
			return enginev1.ValidateHealthResponse(&enginev1.HealthResponse{Status: errorStatus}, limits)
		},
		"start": func() error {
			return enginev1.ValidateStartRunResponse(&enginev1.StartRunResponse{Status: errorStatus}, limits)
		},
		"cancel": func() error {
			return enginev1.ValidateCancelRunResponse(&enginev1.CancelRunResponse{Status: errorStatus}, limits)
		},
		"interaction": func() error {
			return enginev1.ValidateRespondInteractionResponse(&enginev1.RespondInteractionResponse{Status: errorStatus}, limits)
		},
		"suspend": func() error {
			return enginev1.ValidateSuspendRunResponse(&enginev1.SuspendRunResponse{Status: errorStatus}, limits)
		},
		"resume": func() error {
			return enginev1.ValidateResumeRunResponse(&enginev1.ResumeRunResponse{Status: errorStatus}, limits)
		},
		"export": func() error {
			return enginev1.ValidateExportSnapshotResponse(&enginev1.ExportSnapshotResponse{Status: errorStatus}, limits)
		},
		"import": func() error {
			return enginev1.ValidateImportSnapshotResponse(&enginev1.ImportSnapshotResponse{Status: errorStatus}, limits)
		},
	}
	for name, validate := range errorOnly {
		var statusErr *commonv1.StatusError
		if err := validate(); !errors.As(err, &statusErr) {
			t.Errorf("%s error-only response = %T %v", name, err, err)
		}
	}
}

func TestUnaryResponseValidatorsRejectInactiveAndBoundaryViolations(t *testing.T) {
	t.Parallel()
	limits := protocolLimits()
	errorStatus := &commonv1.Status{Code: commonv1.ErrorCode_ERROR_CODE_NOT_FOUND, Message: "not found"}
	malformed := map[string]error{
		"health success omitted":      enginev1.ValidateHealthResponse(&enginev1.HealthResponse{Status: commonv1.OKStatus()}, limits),
		"health error active":         enginev1.ValidateHealthResponse(&enginev1.HealthResponse{Status: errorStatus, Server: build("server")}, limits),
		"start zero sequence":         enginev1.ValidateStartRunResponse(&enginev1.StartRunResponse{Status: commonv1.OKStatus(), RunId: "run", PlanId: "plan"}, limits),
		"start noninitial sequence":   enginev1.ValidateStartRunResponse(&enginev1.StartRunResponse{Status: commonv1.OKStatus(), RunId: "run", InitialSequence: 2, PlanId: "plan"}, limits),
		"start error active":          enginev1.ValidateStartRunResponse(&enginev1.StartRunResponse{Status: errorStatus, RunId: "run"}, limits),
		"cancel no outcome":           enginev1.ValidateCancelRunResponse(&enginev1.CancelRunResponse{Status: commonv1.OKStatus()}, limits),
		"cancel dual outcome":         enginev1.ValidateCancelRunResponse(&enginev1.CancelRunResponse{Status: commonv1.OKStatus(), CancellationRequested: true, AlreadyTerminal: true}, limits),
		"cancel premature sequence":   enginev1.ValidateCancelRunResponse(&enginev1.CancelRunResponse{Status: commonv1.OKStatus(), CancellationRequested: true, TerminalSequence: 1}, limits),
		"cancel terminal no sequence": enginev1.ValidateCancelRunResponse(&enginev1.CancelRunResponse{Status: commonv1.OKStatus(), AlreadyTerminal: true}, limits),
		"interaction not accepted":    enginev1.ValidateRespondInteractionResponse(&enginev1.RespondInteractionResponse{Status: commonv1.OKStatus()}, limits),
		"interaction error active":    enginev1.ValidateRespondInteractionResponse(&enginev1.RespondInteractionResponse{Status: errorStatus, Accepted: true}, limits),
		"suspend zero boundary":       enginev1.ValidateSuspendRunResponse(&enginev1.SuspendRunResponse{Status: commonv1.OKStatus(), Suspended: true}, limits),
		"suspend overflow boundary":   enginev1.ValidateSuspendRunResponse(&enginev1.SuspendRunResponse{Status: commonv1.OKStatus(), Suspended: true, BoundarySequence: math.MaxUint64}, limits),
		"resume zero next":            enginev1.ValidateResumeRunResponse(&enginev1.ResumeRunResponse{Status: commonv1.OKStatus(), Resumed: true}, limits),
		"resume overflow next":        enginev1.ValidateResumeRunResponse(&enginev1.ResumeRunResponse{Status: commonv1.OKStatus(), Resumed: true, NextSequence: math.MaxUint64}, limits),
		"export missing snapshot":     enginev1.ValidateExportSnapshotResponse(&enginev1.ExportSnapshotResponse{Status: commonv1.OKStatus()}, limits),
		"export error active":         enginev1.ValidateExportSnapshotResponse(&enginev1.ExportSnapshotResponse{Status: errorStatus, Snapshot: &enginev1.SnapshotEnvelope{}}, limits),
		"import missing run":          enginev1.ValidateImportSnapshotResponse(&enginev1.ImportSnapshotResponse{Status: commonv1.OKStatus(), NextSequence: 1}, limits),
		"import error active":         enginev1.ValidateImportSnapshotResponse(&enginev1.ImportSnapshotResponse{Status: errorStatus, RunId: "run"}, limits),
	}
	for name, err := range malformed {
		if err == nil {
			t.Errorf("%s succeeded", name)
		}
	}
	for name, err := range map[string]error{
		"health":      enginev1.ValidateHealthResponse(nil, limits),
		"start":       enginev1.ValidateStartRunResponse(nil, limits),
		"cancel":      enginev1.ValidateCancelRunResponse(nil, limits),
		"interaction": enginev1.ValidateRespondInteractionResponse(nil, limits),
		"suspend":     enginev1.ValidateSuspendRunResponse(nil, limits),
		"resume":      enginev1.ValidateResumeRunResponse(nil, limits),
		"export":      enginev1.ValidateExportSnapshotResponse(nil, limits),
		"import":      enginev1.ValidateImportSnapshotResponse(nil, limits),
	} {
		if err == nil {
			t.Errorf("nil %s response succeeded", name)
		}
	}
	collectionOne := protocolLimits()
	collectionOne.MaxCollectionItems = 1
	degraded := healthForLimits(collectionOne)
	degraded.State = commonv1.HealthState_HEALTH_STATE_DEGRADED
	degraded.DegradedReasons = []string{"dependency-a", "dependency-b"}
	if err := enginev1.ValidateHealthResponse(&enginev1.HealthResponse{
		Status: commonv1.OKStatus(), Server: build("server"),
		Protocol: &commonv1.ProtocolVersion{Major: 1, Minor: 1}, Health: degraded,
	}, collectionOne); err == nil {
		t.Fatal("health reasons escaped negotiated collection bound")
	}
	if err := enginev1.ValidateStartRunResponse(&enginev1.StartRunResponse{
		Status: capabilityMismatchStatus("capability-a", "capability-b"),
	}, collectionOne); err == nil || !strings.Contains(err.Error(), "collection limit") {
		t.Fatalf("capability mismatch escaped unary collection bound: %v", err)
	}
}

func TestInitializeClientValidationEnforcesNegotiatedCollections(t *testing.T) {
	t.Parallel()
	request := validInitializeRequest()
	response := enginev1.NegotiateInitialize(
		request, commonv1.SupportedProtocolRange(), build("server"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), health(), definitionSet(), "client", 1,
	)
	response.Limits.MaxCollectionItems = 1
	if err := enginev1.ValidateInitializeResponse(response); err == nil {
		t.Fatal("initialize capabilities escaped negotiated collection bound")
	}

	response.Capabilities = capabilities("events")
	response.Health.State = commonv1.HealthState_HEALTH_STATE_DEGRADED
	response.Health.DegradedReasons = []string{"dependency-a", "dependency-b"}
	if err := enginev1.ValidateInitializeResponse(response); err == nil {
		t.Fatal("initialize health reasons escaped negotiated collection bound")
	}

	errorRequest := validInitializeRequest()
	errorRequest.RequestedLimits.MaxCollectionItems = 1
	errorResponse := &enginev1.InitializeResponse{Status: capabilityMismatchStatus("capability-a", "capability-b")}
	var statusErr *commonv1.StatusError
	if err := enginev1.ValidateInitializeResponseForRequest(errorRequest, errorResponse); !errors.As(err, &statusErr) {
		t.Fatalf("pre-negotiation failure incorrectly used requested collections: %T %v", err, err)
	}
}

func TestInitializeSuccessCorrelatesRequestSelectionAndServerCapacity(t *testing.T) {
	t.Parallel()
	request := validInitializeRequest()
	valid := enginev1.NegotiateInitialize(
		request, commonv1.SupportedProtocolRange(), build("server"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), health(), definitionSet(), "client", 1,
	)
	if err := enginev1.ValidateInitializeResponseForRequest(request, valid); err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*enginev1.InitializeResponse){
		"protocol": func(response *enginev1.InitializeResponse) {
			response.Protocol = &commonv1.ProtocolVersion{Major: 999}
		},
		"unsupported capability": func(response *enginev1.InitializeResponse) {
			response.Capabilities.Names = append(response.Capabilities.Names, "zz-unsupported")
		},
		"missing required capability": func(response *enginev1.InitializeResponse) {
			response.Capabilities.Names = []string{"events", enginev1.CapabilitySnapshotAuthorityV1}
		},
	} {
		candidate := cloneInitializeResponse(t, valid)
		mutate(candidate)
		if err := enginev1.ValidateInitializeResponseForRequest(request, candidate); err == nil {
			t.Errorf("corrupt %s response succeeded", name)
		}
	}

	limitMutations := map[string]func(*commonv1.Limits){
		"message bytes":      func(value *commonv1.Limits) { value.MaxMessageBytes++ },
		"collection items":   func(value *commonv1.Limits) { value.MaxCollectionItems++ },
		"replay events":      func(value *commonv1.Limits) { value.MaxReplayEvents++ },
		"replay bytes":       func(value *commonv1.Limits) { value.MaxReplayBytes++ },
		"concurrent streams": func(value *commonv1.Limits) { value.MaxConcurrentStreams++ },
		"active runs":        func(value *commonv1.Limits) { value.MaxActiveRuns++ },
	}
	for name, mutate := range limitMutations {
		candidate := cloneInitializeResponse(t, valid)
		mutate(candidate.Limits)
		if err := enginev1.ValidateInitializeResponseForRequest(request, candidate); err == nil {
			t.Errorf("response exceeding requested %s succeeded", name)
		}
	}
	capacityMutations := map[string]func(*commonv1.Limits){
		"message bytes":    func(value *commonv1.Limits) { value.MaxMessageBytes = valid.GetLimits().GetMaxMessageBytes() - 1 },
		"collection items": func(value *commonv1.Limits) { value.MaxCollectionItems = valid.GetLimits().GetMaxCollectionItems() - 1 },
		"replay events":    func(value *commonv1.Limits) { value.MaxReplayEvents = valid.GetLimits().GetMaxReplayEvents() - 1 },
		"replay bytes":     func(value *commonv1.Limits) { value.MaxReplayBytes = valid.GetLimits().GetMaxReplayBytes() - 1 },
		"concurrent streams": func(value *commonv1.Limits) {
			value.MaxConcurrentStreams = valid.GetLimits().GetMaxConcurrentStreams() - 1
		},
		"active runs": func(value *commonv1.Limits) { value.MaxActiveRuns = valid.GetLimits().GetMaxActiveRuns() - 1 },
	}
	for name, mutate := range capacityMutations {
		candidate := cloneInitializeResponse(t, valid)
		mutate(candidate.Health.Limits)
		if err := enginev1.ValidateInitializeResponseForRequest(request, candidate); err == nil {
			t.Errorf("response exceeding server %s capacity succeeded", name)
		}
	}
}

func TestInitializeAndHealthPreserveGlobalCapacity(t *testing.T) {
	t.Parallel()
	request := validInitializeRequest()
	request.RequestedLimits.MaxActiveRuns = 8
	serverHealth := health()
	serverHealth.ActiveRuns = 20
	negotiation, failure := enginev1.PreflightInitialize(
		request, commonv1.SupportedProtocolRange(), build("server"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), serverHealth, definitionSet(),
	)
	if failure != nil || negotiation == nil {
		t.Fatalf("global health preflight = %#v, %#v", negotiation, failure)
	}
	response := enginev1.CompleteInitialize(negotiation, "client", 1)
	if err := enginev1.ValidateInitializeResponseForRequest(request, response); err != nil {
		t.Fatalf("global health initialize response: %v", err)
	}
	if response.GetHealth().GetActiveRuns() != 20 || response.GetLimits().GetMaxActiveRuns() != 8 ||
		response.GetHealth().GetLimits().GetMaxActiveRuns() != 32 {
		t.Fatalf("global/connection limits collapsed: %#v", response)
	}
	if err := enginev1.ValidateHealthResponse(&enginev1.HealthResponse{
		Status: commonv1.OKStatus(), Server: build("server"), Protocol: response.GetProtocol(), Health: response.GetHealth(),
	}, response.GetLimits()); err != nil {
		t.Fatalf("global health under lower connection limit: %v", err)
	}

	mismatched := health()
	mismatched.Limits.MaxActiveRuns--
	if negotiation, failure = enginev1.PreflightInitialize(
		request, commonv1.SupportedProtocolRange(), build("server"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), mismatched, definitionSet(),
	); negotiation != nil || failure.GetStatus().GetCode() != commonv1.ErrorCode_ERROR_CODE_INTERNAL {
		t.Fatalf("mismatched server health limits = %#v, %#v", negotiation, failure)
	}
}

func FuzzUnaryProtocolValidators(f *testing.F) {
	f.Add([]byte{0x08, 0x01})
	f.Add([]byte{0xff, 0x01})
	limits := protocolLimits()
	f.Fuzz(func(_ *testing.T, data []byte) {
		var initialize enginev1.InitializeRequest
		if proto.Unmarshal(data, &initialize) == nil {
			_, _ = enginev1.PreflightInitialize(
				&initialize, commonv1.SupportedProtocolRange(), build("server"),
				capabilities("events"), limits, health(), definitionSet(),
			)
		}
		var healthRequest enginev1.HealthRequest
		if proto.Unmarshal(data, &healthRequest) == nil {
			_ = enginev1.ValidateHealthRequest(&healthRequest, limits)
		}
		var startRequest enginev1.StartRunRequest
		if proto.Unmarshal(data, &startRequest) == nil {
			_ = enginev1.ValidateStartRunRequest(&startRequest, limits)
		}
		var cancelRequest enginev1.CancelRunRequest
		if proto.Unmarshal(data, &cancelRequest) == nil {
			_ = enginev1.ValidateCancelRunRequest(&cancelRequest, limits)
		}
		var suspendRequest enginev1.SuspendRunRequest
		if proto.Unmarshal(data, &suspendRequest) == nil {
			_ = enginev1.ValidateSuspendRunRequest(&suspendRequest, limits)
		}
		var resumeRequest enginev1.ResumeRunRequest
		if proto.Unmarshal(data, &resumeRequest) == nil {
			_ = enginev1.ValidateResumeRunRequest(&resumeRequest, limits)
		}
		var start enginev1.StartRunResponse
		if proto.Unmarshal(data, &start) == nil {
			_ = enginev1.ValidateStartRunResponse(&start, limits)
		}
		var healthResponse enginev1.HealthResponse
		if proto.Unmarshal(data, &healthResponse) == nil {
			_ = enginev1.ValidateHealthResponse(&healthResponse, limits)
		}
		var cancel enginev1.CancelRunResponse
		if proto.Unmarshal(data, &cancel) == nil {
			_ = enginev1.ValidateCancelRunResponse(&cancel, limits)
		}
		var interaction enginev1.RespondInteractionRequest
		if proto.Unmarshal(data, &interaction) == nil {
			_ = enginev1.ValidateRespondInteractionRequest(&interaction, limits)
		}
		var interactionResponse enginev1.RespondInteractionResponse
		if proto.Unmarshal(data, &interactionResponse) == nil {
			_ = enginev1.ValidateRespondInteractionResponse(&interactionResponse, limits)
		}
		var suspend enginev1.SuspendRunResponse
		if proto.Unmarshal(data, &suspend) == nil {
			_ = enginev1.ValidateSuspendRunResponse(&suspend, limits)
		}
		var resume enginev1.ResumeRunResponse
		if proto.Unmarshal(data, &resume) == nil {
			_ = enginev1.ValidateResumeRunResponse(&resume, limits)
		}
		var exportRequest enginev1.ExportSnapshotRequest
		if proto.Unmarshal(data, &exportRequest) == nil {
			_ = enginev1.ValidateExportSnapshotRequest(&exportRequest, limits)
		}
		var export enginev1.ExportSnapshotResponse
		if proto.Unmarshal(data, &export) == nil {
			_ = enginev1.ValidateExportSnapshotResponse(&export, limits)
		}
		var imported enginev1.ImportSnapshotResponse
		if proto.Unmarshal(data, &imported) == nil {
			_ = enginev1.ValidateImportSnapshotResponse(&imported, limits)
		}
		var importRequest enginev1.ImportSnapshotRequest
		if proto.Unmarshal(data, &importRequest) == nil {
			_ = enginev1.ValidateImportSnapshotRequest(
				context.Background(), &importRequest, fuzzSnapshotVerifier{}, limits,
			)
		}
	})
}

type fuzzSnapshotVerifier struct{}

func (fuzzSnapshotVerifier) VerifySnapshot(
	context.Context,
	enginev1.SnapshotAuthorityInput,
	*enginev1.SnapshotAuthority,
) error {
	return enginev1.ErrSnapshotAuthorityVerification
}

func limitsWithMessageBytes(messageBytes uint64) *commonv1.Limits {
	return limits(messageBytes, 128, 256, 8<<20, 8, 32)
}

func healthForLimits(bounds *commonv1.Limits) *commonv1.Health {
	return &commonv1.Health{
		State: commonv1.HealthState_HEALTH_STATE_READY,
		Limits: &commonv1.Limits{
			MaxMessageBytes: bounds.GetMaxMessageBytes(), MaxCollectionItems: bounds.GetMaxCollectionItems(),
			MaxReplayEvents: bounds.GetMaxReplayEvents(), MaxReplayBytes: bounds.GetMaxReplayBytes(),
			MaxConcurrentStreams: bounds.GetMaxConcurrentStreams(), MaxActiveRuns: bounds.GetMaxActiveRuns(),
		},
	}
}

func capabilityMismatchStatus(names ...string) *commonv1.Status {
	return &commonv1.Status{
		Code: commonv1.ErrorCode_ERROR_CODE_MISSING_CAPABILITY, Message: "required capabilities are unavailable",
		Detail: &commonv1.Status_CapabilityMismatch{CapabilityMismatch: &commonv1.CapabilityMismatch{
			Required: names, Missing: names,
		}},
	}
}

func cloneInitializeResponse(t *testing.T, value *enginev1.InitializeResponse) *enginev1.InitializeResponse {
	t.Helper()
	result, ok := proto.Clone(value).(*enginev1.InitializeResponse)
	if !ok {
		t.Fatal("initialize response clone changed type")
	}
	return result
}

func padProtocolMessage[T proto.Message](t *testing.T, value T, target int) T {
	t.Helper()
	result, ok := proto.Clone(value).(T)
	if !ok {
		t.Fatal("protocol message clone changed type")
	}
	remaining := target - proto.Size(result)
	if remaining < 0 {
		t.Fatalf("protocol message already exceeds target %d", target)
	}
	for payloadBytes := remaining; payloadBytes >= 0 && payloadBytes >= remaining-16; payloadBytes-- {
		unknown := protowire.AppendTag(nil, 2047, protowire.BytesType)
		unknown = protowire.AppendBytes(unknown, make([]byte, payloadBytes))
		if len(unknown) == remaining {
			result.ProtoReflect().SetUnknown(unknown)
			if proto.Size(result) != target {
				t.Fatalf("padded message size = %d, want %d", proto.Size(result), target)
			}
			return result
		}
	}
	t.Fatalf("cannot pad protocol message to %d bytes", target)
	return result
}
