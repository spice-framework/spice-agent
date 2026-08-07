package commonv1_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestProtocolNegotiationSelectsGreatestCompatibleVersion(t *testing.T) {
	t.Parallel()
	client := protocolRange(1, 0, 1, 4)
	server := protocolRange(1, 2, 1, 3)
	version, status := commonv1.NegotiateProtocol(client, server)
	if version == nil {
		t.Fatalf("negotiation omitted version: %#v", status)
		return
	}
	if status.GetCode() != commonv1.ErrorCode_ERROR_CODE_OK ||
		version.GetMajor() != 1 || version.GetMinor() != 3 {
		t.Fatalf("negotiation = %#v, %#v", version, status)
	}
	version.Minor = 99
	if server.GetMaximum().GetMinor() != 3 {
		t.Fatal("negotiation exposed mutable server version state")
	}
}

func TestSupportedProtocolRangeRetainsMinorZeroAndAdvertisesMinorTwo(t *testing.T) {
	t.Parallel()
	supported := commonv1.SupportedProtocolRange()
	if supported.GetMinimum().GetMajor() != 1 || supported.GetMinimum().GetMinor() != 0 ||
		supported.GetMaximum().GetMajor() != 1 || supported.GetMaximum().GetMinor() != 2 {
		t.Fatalf("supported protocol range = %#v", supported)
	}
	selected, status := commonv1.NegotiateProtocol(protocolRange(1, 0, 1, 0), supported)
	if status.GetCode() != commonv1.ErrorCode_ERROR_CODE_OK || selected.GetMinor() != 0 {
		t.Fatalf("minor-zero negotiation = %#v, %#v", selected, status)
	}
	selected, status = commonv1.NegotiateProtocol(protocolRange(1, 0, 1, 1), supported)
	if status.GetCode() != commonv1.ErrorCode_ERROR_CODE_OK || selected.GetMinor() != 1 {
		t.Fatalf("minor-one negotiation = %#v, %#v", selected, status)
	}
	selected, status = commonv1.NegotiateProtocol(protocolRange(1, 2, 1, 2), supported)
	if status.GetCode() != commonv1.ErrorCode_ERROR_CODE_OK || selected.GetMinor() != 2 {
		t.Fatalf("minor-two negotiation = %#v, %#v", selected, status)
	}
}

func TestProtocolNegotiationRejectsOldNewAndInvalidRanges(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		client *commonv1.ProtocolRange
		server *commonv1.ProtocolRange
		code   commonv1.ErrorCode
	}{
		{"old client", protocolRange(1, 0, 1, 1), protocolRange(2, 0, 2, 1), commonv1.ErrorCode_ERROR_CODE_INCOMPATIBLE_VERSION},
		{"new client", protocolRange(3, 0, 3, 1), protocolRange(2, 0, 2, 1), commonv1.ErrorCode_ERROR_CODE_INCOMPATIBLE_VERSION},
		{"inverted", protocolRange(1, 4, 1, 2), protocolRange(1, 0, 1, 3), commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT},
		{"cross major", protocolRange(1, 0, 2, 0), protocolRange(1, 0, 1, 3), commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			version, status := commonv1.NegotiateProtocol(test.client, test.server)
			if version != nil || status.GetCode() != test.code {
				t.Fatalf("negotiation = %#v, %#v", version, status)
			}
			if test.code == commonv1.ErrorCode_ERROR_CODE_INCOMPATIBLE_VERSION &&
				status.GetVersionMismatch() == nil {
				t.Fatal("version mismatch omitted typed ranges")
			}
		})
	}
}

func TestCapabilitiesAndLimitsNegotiateDeterministically(t *testing.T) {
	t.Parallel()
	enabled, status := commonv1.NegotiateCapabilities(
		&commonv1.CapabilitySet{Names: []string{"events", "snapshots", "tools"}},
		&commonv1.CapabilitySet{Names: []string{"events", "snapshots"}},
		&commonv1.CapabilitySet{Names: []string{"events", "plugins", "snapshots"}},
	)
	if status.GetCode() != commonv1.ErrorCode_ERROR_CODE_OK ||
		!slices.Equal(enabled.GetNames(), []string{"events", "snapshots"}) {
		t.Fatalf("capability negotiation = %#v, %#v", enabled, status)
	}
	_, status = commonv1.NegotiateCapabilities(
		&commonv1.CapabilitySet{Names: []string{"events", "tools"}},
		&commonv1.CapabilitySet{Names: []string{"tools"}},
		&commonv1.CapabilitySet{Names: []string{"events"}},
	)
	if status.GetCode() != commonv1.ErrorCode_ERROR_CODE_MISSING_CAPABILITY ||
		!slices.Equal(status.GetCapabilityMismatch().GetMissing(), []string{"tools"}) {
		t.Fatalf("missing capability status = %#v", status)
	}
	requested := limits(1<<20, 100, 50, 1<<20, 4, 8)
	available := limits(2<<20, 50, 25, 512<<10, 2, 16)
	negotiated, status := commonv1.NegotiateLimits(requested, available)
	if status.GetCode() != commonv1.ErrorCode_ERROR_CODE_OK ||
		negotiated.GetMaxMessageBytes() != 1<<20 ||
		negotiated.GetMaxCollectionItems() != 50 ||
		negotiated.GetMaxReplayEvents() != 25 ||
		negotiated.GetMaxConcurrentStreams() != 2 ||
		negotiated.GetMaxActiveRuns() != 8 {
		t.Fatalf("limit negotiation = %#v, %#v", negotiated, status)
	}
}

func TestValidationRejectsNondeterministicMetadataAndMalformedStatus(t *testing.T) {
	t.Parallel()
	if err := commonv1.ValidateCapabilities(&commonv1.CapabilitySet{Names: []string{"tools", "events"}}); err == nil {
		t.Fatal("unsorted capabilities succeeded")
	}
	health := &commonv1.Health{
		State:           commonv1.HealthState_HEALTH_STATE_DEGRADED,
		DegradedReasons: []string{"zeta", "alpha"},
		Limits:          limits(1024, 4, 2, 1024, 1, 1),
	}
	if err := commonv1.ValidateHealth(health); err == nil {
		t.Fatal("unsorted degraded reasons succeeded")
	}
	health.DegradedReasons = []string{"capacity"}
	health.ActiveRuns = 2
	health.Limits.MaxActiveRuns = 1
	if err := commonv1.ValidateHealth(health); err == nil {
		t.Fatal("health above active-run limit succeeded")
	}
	malformed := &commonv1.Status{
		Code:    commonv1.ErrorCode_ERROR_CODE_OUT_OF_RANGE,
		Message: "replay gap",
	}
	if err := commonv1.ValidateStatus(malformed); err == nil {
		t.Fatal("out-of-range status without replay bounds succeeded")
	}
	if err := commonv1.ValidateStatus(&commonv1.Status{
		Code:    commonv1.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
		Message: "bad request",
		Detail: &commonv1.Status_Overload{Overload: &commonv1.Overload{
			Resource: "runs", Limit: 1, Observed: 2,
		}},
	}); err == nil {
		t.Fatal("status with unrelated detail succeeded")
	}
	if err := commonv1.ValidateStatus(&commonv1.Status{
		Code: commonv1.ErrorCode(9000), Message: "future error",
	}); err == nil {
		t.Fatal("unknown status code succeeded")
	}
	if err := commonv1.ValidateStatus(&commonv1.Status{
		Code: commonv1.ErrorCode_ERROR_CODE_RESOURCE_EXHAUSTED, Message: "overload",
		Detail: &commonv1.Status_Overload{Overload: &commonv1.Overload{
			Resource: "runs", Limit: 2, Observed: 1,
		}},
	}); err == nil {
		t.Fatal("malformed overload detail succeeded")
	}
}

func TestStatusErrorDefensivelyCopiesTypedStatus(t *testing.T) {
	t.Parallel()
	status := &commonv1.Status{
		Code:    commonv1.ErrorCode_ERROR_CODE_UNCERTAIN_OPERATION,
		Message: "operation acknowledgement was lost",
		Detail: &commonv1.Status_UncertainOperation{UncertainOperation: &commonv1.UncertainOperation{
			OperationId: "operation-1", OperationKind: "tool.execute",
		}},
	}
	err := commonv1.AsError(status)
	var typed *commonv1.StatusError
	if err == nil || !errors.As(err, &typed) || typed == nil || !strings.Contains(err.Error(), "acknowledgement") {
		t.Fatalf("typed error = %T, %v", err, err)
		return
	}
	status.Message = "mutated"
	first := typed.Status()
	if first == nil {
		t.Fatal("typed status error omitted status")
		return
	}
	first.Message = "also mutated"
	if typed.Status().GetMessage() != "operation acknowledgement was lost" {
		t.Fatal("typed status error exposed mutable state")
	}
	if err = commonv1.AsError(commonv1.OKStatus()); err != nil {
		t.Fatalf("success converted to error: %v", err)
	}
}

func TestUnknownFieldsSurviveAdditiveRoundTrip(t *testing.T) {
	t.Parallel()
	encoded, err := proto.Marshal(commonv1.SupportedProtocolRange())
	if err != nil {
		t.Fatal(err)
	}
	encoded = protowire.AppendTag(encoded, 100, protowire.VarintType)
	encoded = protowire.AppendVarint(encoded, 42)
	var decoded commonv1.ProtocolRange
	if err = proto.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.ProtoReflect().GetUnknown()) == 0 {
		t.Fatal("unknown additive field was discarded")
	}
	if err = commonv1.ValidateProtocolRange(&decoded); err != nil {
		t.Fatalf("known fields rejected because an additive field exists: %v", err)
	}
	roundTrip, err := proto.Marshal(&decoded)
	if err != nil || !slices.Contains(roundTrip, byte(42)) {
		t.Fatalf("unknown field round trip = %x, %v", roundTrip, err)
	}
}

func FuzzCommonEnvelope(f *testing.F) {
	seed, err := proto.Marshal(commonv1.SupportedProtocolRange())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{0xff, 0x01})
	f.Fuzz(func(_ *testing.T, data []byte) {
		var protocolRange commonv1.ProtocolRange
		if proto.Unmarshal(data, &protocolRange) == nil {
			_ = commonv1.ValidateProtocolRange(&protocolRange)
		}
		var status commonv1.Status
		if proto.Unmarshal(data, &status) == nil {
			_ = commonv1.ValidateStatus(&status)
		}
	})
}

func protocolRange(minimumMajor, minimumMinor, maximumMajor, maximumMinor uint32) *commonv1.ProtocolRange {
	return &commonv1.ProtocolRange{
		Minimum: &commonv1.ProtocolVersion{Major: minimumMajor, Minor: minimumMinor},
		Maximum: &commonv1.ProtocolVersion{Major: maximumMajor, Minor: maximumMinor},
	}
}

func limits(messageBytes uint64, items, replayEvents uint32, replayBytes uint64, streams, runs uint32) *commonv1.Limits {
	return &commonv1.Limits{
		MaxMessageBytes: messageBytes, MaxCollectionItems: items,
		MaxReplayEvents: replayEvents, MaxReplayBytes: replayBytes,
		MaxConcurrentStreams: streams, MaxActiveRuns: runs,
	}
}
