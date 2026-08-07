package enginev1_test

import (
	"bytes"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/protobuf/proto"
)

func TestProtocolMinorThreeInitializationAttemptRoundTrip(t *testing.T) {
	t.Parallel()
	for _, reconnect := range []bool{false, true} {
		request := minorThreeInitializeRequest()
		clientID, epoch := "client-1", uint64(1)
		if reconnect {
			request.ReconnectClaim = &enginev1.ReconnectClaim{
				ClientId: "client-1", ExpectedOwnershipEpoch: 7,
			}
			epoch = 8
		}
		negotiation, failure := enginev1.PreflightInitialize(
			request, commonv1.SupportedProtocolRange(), build("spice-agentd"),
			capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
			protocolLimits(), health(), definitionSet(),
		)
		if failure != nil {
			t.Fatalf("reconnect=%t preflight failed: %#v", reconnect, failure)
		}
		captured := negotiation.InitializationAttemptID()
		if len(captured) != enginev1.InitializationAttemptIDBytes ||
			!bytes.Equal(captured, request.GetInitializationAttemptId()) {
			t.Fatalf("reconnect=%t captured attempt = %x", reconnect, captured)
		}
		captured[0] ^= 0xff
		if bytes.Equal(captured, negotiation.InitializationAttemptID()) {
			t.Fatalf("reconnect=%t negotiation exposed mutable attempt bytes", reconnect)
		}
		response := enginev1.CompleteInitialize(negotiation, clientID, epoch)
		if err := enginev1.ValidateInitializeResponseForRequest(request, response); err != nil {
			t.Fatalf("reconnect=%t response failed: %v", reconnect, err)
		}
		if !bytes.Equal(response.GetInitializationAttemptId(), request.GetInitializationAttemptId()) {
			t.Fatalf("reconnect=%t response did not echo attempt", reconnect)
		}
	}
}

func TestInitializationAttemptRejectsSemanticBoundaryAndMalformedIDs(t *testing.T) {
	t.Parallel()
	validID := initializationAttemptID()
	for _, test := range []struct {
		name     string
		protocol *commonv1.ProtocolRange
		attempt  []byte
	}{
		{
			name:     "legacy identity",
			protocol: protocolMinorRange(0, 2),
			attempt:  validID,
		},
		{
			name:     "crossing without identity",
			protocol: protocolMinorRange(0, 3),
		},
		{
			name:     "crossing with identity",
			protocol: protocolMinorRange(2, 3),
			attempt:  validID,
		},
		{
			name:     "minor three missing identity",
			protocol: protocolMinorRange(3, 3),
		},
		{
			name:     "minor three short identity",
			protocol: protocolMinorRange(3, 3),
			attempt:  validID[:enginev1.InitializationAttemptIDBytes-1],
		},
		{
			name:     "minor three long identity",
			protocol: protocolMinorRange(3, 3),
			attempt:  append(bytes.Clone(validID), 0x11),
		},
		{
			name:     "minor three zero identity",
			protocol: protocolMinorRange(3, 3),
			attempt:  make([]byte, enginev1.InitializationAttemptIDBytes),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := validInitializeRequest()
			request.Protocol = test.protocol
			request.InitializationAttemptId = test.attempt
			if err := enginev1.ValidateInitializeRequest(request); err == nil {
				t.Fatal("invalid initialization attempt succeeded")
			}
		})
	}
}

func TestInitializationAttemptResponseMustEchoRequestExactly(t *testing.T) {
	t.Parallel()
	request := minorThreeInitializeRequest()
	response := enginev1.NegotiateInitialize(
		request, commonv1.SupportedProtocolRange(), build("spice-agentd"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), health(), definitionSet(), "client-1", 1,
	)
	if err := enginev1.ValidateInitializeResponseForRequest(request, response); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*enginev1.InitializeResponse){
		func(candidate *enginev1.InitializeResponse) { candidate.InitializationAttemptId = nil },
		func(candidate *enginev1.InitializeResponse) { candidate.InitializationAttemptId[0] ^= 0xff },
	} {
		candidate, ok := proto.Clone(response).(*enginev1.InitializeResponse)
		if !ok {
			t.Fatal("initialize response clone changed type")
		}
		mutate(candidate)
		if err := enginev1.ValidateInitializeResponseForRequest(request, candidate); err == nil {
			t.Fatal("mismatched initialization attempt response succeeded")
		}
	}

	legacy := validInitializeRequest()
	legacyResponse := enginev1.NegotiateInitialize(
		legacy, commonv1.SupportedProtocolRange(), build("spice-agentd"),
		capabilities("events", enginev1.CapabilitySnapshotAuthorityV1, "snapshots"),
		protocolLimits(), health(), definitionSet(), "client-1", 1,
	)
	legacyResponse.InitializationAttemptId = initializationAttemptID()
	if err := enginev1.ValidateInitializeResponse(legacyResponse); err == nil {
		t.Fatal("legacy response with attempt identity succeeded")
	}
}

func TestInitializationAttemptSupportsFutureRangeOnReplaySide(t *testing.T) {
	t.Parallel()
	request := minorThreeInitializeRequest()
	request.Protocol = protocolMinorRange(3, 4)
	if err := enginev1.ValidateInitializeRequest(request); err != nil {
		t.Fatalf("non-crossing replay-side range failed: %v", err)
	}
}

func minorThreeInitializeRequest() *enginev1.InitializeRequest {
	request := validInitializeRequest()
	request.Protocol = protocolMinorRange(3, 3)
	request.InitializationAttemptId = initializationAttemptID()
	return request
}

func protocolMinorRange(minimum, maximum uint32) *commonv1.ProtocolRange {
	return &commonv1.ProtocolRange{
		Minimum: &commonv1.ProtocolVersion{Major: 1, Minor: minimum},
		Maximum: &commonv1.ProtocolVersion{Major: 1, Minor: maximum},
	}
}

func initializationAttemptID() []byte {
	return bytes.Repeat([]byte{0x5a}, enginev1.InitializationAttemptIDBytes)
}
