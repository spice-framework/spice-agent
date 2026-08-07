package pluginv1_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
	"google.golang.org/protobuf/encoding/protowire"
)

const initializeTranscriptGoldenSHA256 = "c041c506079f6e7a4d8b60fe29c6f050e43007390db433f421508b8acbc59e20"

func TestCanonicalInitializeTranscriptGolden(t *testing.T) {
	t.Parallel()
	request, response := goldenInitializeTranscript()
	transcript, err := pluginv1.CanonicalInitializeTranscript(request, response)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(transcript)
	if observed := hex.EncodeToString(digest[:]); observed != initializeTranscriptGoldenSHA256 {
		t.Fatalf("canonical transcript SHA-256 = %s\ntranscript = %s", observed, transcript)
	}

	response.HandshakeProof = bytes.Repeat([]byte{0xff}, pluginv1.HandshakeProofBytes)
	again, err := pluginv1.CanonicalInitializeTranscript(request, response)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(transcript, again) {
		t.Fatal("handshake proof changed the self-excluding canonical transcript")
	}
}

func TestCanonicalInitializeTranscriptPreservesUnknownOccurrenceOrder(t *testing.T) {
	t.Parallel()
	request, response := goldenInitializeTranscript()
	canonical, err := pluginv1.CanonicalInitializeTranscript(request, response)
	if err != nil {
		t.Fatal(err)
	}
	// Preserve occurrence order but use a non-minimal representation of the
	// same maximum uint64. Wire spelling is not semantic and must canonicalize.
	request.ProtoReflect().SetUnknown(bytes.Join([][]byte{
		unknownVarint(6, 42), // known field number with the wrong wire type
		unknownBytes(127, []byte("future-compatible")),
		unknownBytes(127, []byte("future-compatible-2")),
		append(protowire.AppendTag(nil, 126, protowire.VarintType),
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01),
		unknownFixed64(125, 0x0102030405060708),
		unknownFixed32(124, 0x01020304),
	}, nil))
	normalized, err := pluginv1.CanonicalInitializeTranscript(request, response)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, normalized) {
		t.Fatalf("equivalent unknown wire spelling changed transcript\nfirst  = %s\nsecond = %s", canonical, normalized)
	}

	// Future repeated fields and singular last-wins fields make occurrence
	// order semantic. Reordering unequal atoms must therefore change the proof.
	request.ProtoReflect().SetUnknown(bytes.Join([][]byte{
		unknownVarint(6, 42),
		unknownBytes(127, []byte("future-compatible-2")),
		unknownBytes(127, []byte("future-compatible")),
		unknownVarint(126, ^uint64(0)),
		unknownFixed64(125, 0x0102030405060708),
		unknownFixed32(124, 0x01020304),
	}, nil))
	reordered, err := pluginv1.CanonicalInitializeTranscript(request, response)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(canonical, reordered) {
		t.Fatal("reordered unknown occurrences did not change transcript")
	}

	request, response = goldenInitializeTranscript()
	request.Protocol.Minimum.ProtoReflect().SetUnknown(unknownBytes(90, []byte("changed")))
	nested, err := pluginv1.CanonicalInitializeTranscript(request, response)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(canonical, nested) {
		t.Fatal("nested unknown field did not change transcript")
	}
}

func TestCanonicalInitializeTranscriptRejectsGroupsAtEveryBoundary(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*pluginv1.InitializeRequest, *pluginv1.InitializeResponse){
		"request": func(request *pluginv1.InitializeRequest, _ *pluginv1.InitializeResponse) {
			request.ProtoReflect().SetUnknown(protowire.AppendTag(nil, 100, protowire.StartGroupType))
		},
		"nested response": func(_ *pluginv1.InitializeRequest, response *pluginv1.InitializeResponse) {
			response.Manifest.Tools[0].Capabilities.ProtoReflect().SetUnknown(
				protowire.AppendTag(nil, 100, protowire.StartGroupType),
			)
		},
	} {
		request, response := goldenInitializeTranscript()
		mutate(request, response)
		if _, err := pluginv1.CanonicalInitializeTranscript(request, response); err == nil {
			t.Errorf("unknown group in %s succeeded", name)
		}
	}
}

func TestCanonicalInitializeTranscriptRejectsInvalidUTF8(t *testing.T) {
	t.Parallel()
	request, response := goldenInitializeTranscript()
	request.Host.Component = string([]byte{0xff})
	if _, err := pluginv1.CanonicalInitializeTranscript(request, response); err == nil {
		t.Fatal("invalid UTF-8 succeeded")
	}
}

func TestCanonicalInitializeTranscriptCoversEveryKnownStatusDetail(t *testing.T) {
	t.Parallel()
	details := []any{
		&commonv1.Status_VersionMismatch{VersionMismatch: &commonv1.VersionMismatch{
			Client: &commonv1.ProtocolRange{
				Minimum: &commonv1.ProtocolVersion{Major: 1},
				Maximum: &commonv1.ProtocolVersion{Major: 1},
			},
			Server: &commonv1.ProtocolRange{
				Minimum: &commonv1.ProtocolVersion{Major: 2},
				Maximum: &commonv1.ProtocolVersion{Major: 2},
			},
		}},
		&commonv1.Status_CapabilityMismatch{CapabilityMismatch: &commonv1.CapabilityMismatch{
			Required: []string{"required"}, Available: []string{"available"}, Missing: []string{"missing"},
		}},
		&commonv1.Status_ReplayBounds{ReplayBounds: &commonv1.ReplayBounds{
			RequestedAfterSequence: 1, EarliestSequence: 2, LatestSequence: 3, RecoverySequence: 4,
		}},
		&commonv1.Status_Overload{Overload: &commonv1.Overload{Resource: "calls", Limit: 2, Observed: 3}},
		&commonv1.Status_StaleClient{StaleClient: &commonv1.StaleClient{ExpectedEpoch: 2, ObservedEpoch: 3}},
		&commonv1.Status_SnapshotVersionMismatch{SnapshotVersionMismatch: &commonv1.SnapshotVersionMismatch{
			Expected: "v1", Observed: "v2",
		}},
		&commonv1.Status_UncertainOperation{UncertainOperation: &commonv1.UncertainOperation{
			OperationId: "operation", OperationKind: "write",
		}},
	}
	seen := make(map[[32]byte]struct{}, len(details))
	for _, detail := range details {
		request, response := goldenInitializeTranscript()
		switch value := detail.(type) {
		case *commonv1.Status_VersionMismatch:
			response.Status.Detail = value
		case *commonv1.Status_CapabilityMismatch:
			response.Status.Detail = value
		case *commonv1.Status_ReplayBounds:
			response.Status.Detail = value
		case *commonv1.Status_Overload:
			response.Status.Detail = value
		case *commonv1.Status_StaleClient:
			response.Status.Detail = value
		case *commonv1.Status_SnapshotVersionMismatch:
			response.Status.Detail = value
		case *commonv1.Status_UncertainOperation:
			response.Status.Detail = value
		}
		transcript, err := pluginv1.CanonicalInitializeTranscript(request, response)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(transcript)
		if _, duplicate := seen[digest]; duplicate {
			t.Fatal("distinct status details produced the same canonical transcript")
		}
		seen[digest] = struct{}{}
	}
}

func goldenInitializeTranscript() (*pluginv1.InitializeRequest, *pluginv1.InitializeResponse) {
	minimum := &commonv1.ProtocolVersion{Major: 1, Minor: 2, Patch: 3}
	minimum.ProtoReflect().SetUnknown(unknownBytes(90, []byte("nested")))
	maximum := &commonv1.ProtocolVersion{Major: 4, Minor: 5, Patch: 6}
	request := &pluginv1.InitializeRequest{
		Protocol: &commonv1.ProtocolRange{Minimum: minimum, Maximum: maximum},
		Host: &pluginv1.BuildIdentity{
			Component: "host-\u2028-雪", Version: "v1", Commit: "commit", Runtime: "go1.26.5",
		},
		SupportedCapabilities: &commonv1.CapabilitySet{Names: []string{"alpha", "runtime-tools-v1"}},
		RequiredCapabilities:  &commonv1.CapabilitySet{Names: []string{"runtime-tools-v1"}},
		RequestedLimits: &pluginv1.Limits{
			MaxMessageBytes: 1 << 20, MaxTools: 16, MaxSchemaBytes: 64 << 10,
			MaxCallArgumentBytes: 64 << 10, MaxResultBytes: 1 << 20,
			MaxProgressBytes: 4096, MaxConcurrentCalls: 8,
		},
		LaunchId:           bytes.Repeat([]byte{0x11}, pluginv1.LaunchIDBytes),
		HandshakeChallenge: bytes.Repeat([]byte{0x22}, pluginv1.HandshakeChallengeBytes),
	}
	request.Protocol.ProtoReflect().SetUnknown(unknownVarint(91, 17))
	request.Host.ProtoReflect().SetUnknown(unknownFixed32(92, 0x11223344))
	request.SupportedCapabilities.ProtoReflect().SetUnknown(unknownBytes(93, []byte("supported")))
	request.ProtoReflect().SetUnknown(bytes.Join([][]byte{
		unknownVarint(6, 42), // launch_id is field 6, but wire type 0 remains unknown
		unknownBytes(127, []byte("future-compatible")),
		unknownBytes(127, []byte("future-compatible-2")),
		unknownVarint(126, ^uint64(0)),
		unknownFixed64(125, 0x0102030405060708),
		unknownFixed32(124, 0x01020304),
	}, nil))

	status := &commonv1.Status{
		Code:        commonv1.ErrorCode_ERROR_CODE_RESOURCE_EXHAUSTED,
		Message:     "busy-\u2029-λ",
		Retryable:   true,
		OperationId: "operation-1",
		Detail: &commonv1.Status_Overload{Overload: &commonv1.Overload{
			Resource: "calls", Limit: ^uint64(0), Observed: 9007199254740993,
		}},
	}
	status.GetOverload().ProtoReflect().SetUnknown(unknownVarint(94, 19))
	definition := &pluginv1.ToolDefinition{
		Name:            "echo",
		Description:     "Echo 雪.",
		InputSchemaJson: []byte(`{"type":"object"}`),
		Effect:          pluginv1.ToolEffect_TOOL_EFFECT_READ_ONLY,
		ReplaySafety:    pluginv1.ReplaySafety_REPLAY_SAFETY_SAFE,
		Capabilities:    &commonv1.CapabilitySet{Names: []string{"filesystem-read"}},
	}
	definition.Capabilities.ProtoReflect().SetUnknown(unknownFixed64(95, 23))
	response := &pluginv1.InitializeResponse{
		Status:       status,
		Protocol:     &commonv1.ProtocolVersion{Major: 1},
		Plugin:       &pluginv1.BuildIdentity{Component: "fixture", Version: "v1", Commit: "python", Runtime: "python3.12"},
		Capabilities: &commonv1.CapabilitySet{Names: []string{"runtime-tools-v1"}},
		Limits: &pluginv1.Limits{
			MaxMessageBytes: 1 << 20, MaxTools: 16, MaxSchemaBytes: 64 << 10,
			MaxCallArgumentBytes: 64 << 10, MaxResultBytes: 1 << 20,
			MaxProgressBytes: 4096, MaxConcurrentCalls: 8,
		},
		Manifest:           &pluginv1.Manifest{Name: "fixture", Version: "v1", Tools: []*pluginv1.ToolDefinition{definition}},
		LaunchId:           bytes.Repeat([]byte{0x11}, pluginv1.LaunchIDBytes),
		SessionId:          bytes.Repeat([]byte{0x33}, pluginv1.SessionIDBytes),
		HandshakeChallenge: bytes.Repeat([]byte{0x22}, pluginv1.HandshakeChallengeBytes),
		HandshakeProof:     []byte("ignored-self-proof"),
	}
	response.ProtoReflect().SetUnknown(unknownBytes(127, []byte("response-future")))
	return request, response
}

func unknownVarint(number protowire.Number, value uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(nil, number, protowire.VarintType), value)
}

func unknownFixed64(number protowire.Number, value uint64) []byte {
	return protowire.AppendFixed64(protowire.AppendTag(nil, number, protowire.Fixed64Type), value)
}

func unknownBytes(number protowire.Number, value []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, number, protowire.BytesType), value)
}

func unknownFixed32(number protowire.Number, value uint32) []byte {
	return protowire.AppendFixed32(protowire.AppendTag(nil, number, protowire.Fixed32Type), value)
}
