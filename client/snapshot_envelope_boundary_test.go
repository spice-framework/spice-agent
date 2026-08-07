package client_test

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/protobuf/proto"
)

func TestMaximumKernelPayloadFitsOpaqueSignedEnvelopeExactly(t *testing.T) {
	t.Parallel()
	if client.MaximumSnapshotPayloadBytes != enginev1.MaximumSnapshotBytes ||
		client.MaximumSnapshotEnvelopeOverheadBytes != enginev1.MaximumSnapshotEnvelopeOverheadBytes ||
		client.MaximumSnapshotEnvelopeBytes != enginev1.MaximumSnapshotEnvelopeBytes {
		t.Fatalf(
			"client/engine snapshot bounds diverged: payload=%d/%d overhead=%d/%d envelope=%d/%d",
			client.MaximumSnapshotPayloadBytes, enginev1.MaximumSnapshotBytes,
			client.MaximumSnapshotEnvelopeOverheadBytes, enginev1.MaximumSnapshotEnvelopeOverheadBytes,
			client.MaximumSnapshotEnvelopeBytes, enginev1.MaximumSnapshotEnvelopeBytes,
		)
	}
	runID := strings.Repeat("r", 128)
	prefix := []byte(`{"run_id":"` + runID + `","padding":"`)
	suffix := []byte(`"}`)
	paddingBytes := client.MaximumSnapshotPayloadBytes - len(prefix) - len(suffix)
	if paddingBytes <= 0 {
		t.Fatal("maximum snapshot payload fixture has no padding capacity")
	}
	payload := make([]byte, 0, client.MaximumSnapshotPayloadBytes)
	payload = append(payload, prefix...)
	payload = append(payload, bytes.Repeat([]byte{'x'}, paddingBytes)...)
	payload = append(payload, suffix...)
	if len(payload) != client.MaximumSnapshotPayloadBytes {
		t.Fatalf("payload size = %d", len(payload))
	}
	authority, err := enginev1.NewHMACSnapshotAuthority(
		bytes.Repeat([]byte{0x11}, 32), math.MaxUint64, bytes.Repeat([]byte{0x22}, 32),
	)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := enginev1.NewSnapshotEnvelope(
		t.Context(), authority, runID, math.MaxUint64-1,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_CANCELLED, payload,
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != client.MaximumSnapshotEnvelopeBytes || proto.Size(envelope) != client.MaximumSnapshotEnvelopeBytes {
		t.Fatalf("maximum envelope size = %d/%d, want %d", len(encoded), proto.Size(envelope), client.MaximumSnapshotEnvelopeBytes)
	}
	repeated, err := (proto.MarshalOptions{Deterministic: true}).Marshal(envelope)
	if err != nil || !bytes.Equal(encoded, repeated) {
		t.Fatalf("maximum envelope encoding is nondeterministic: %v", err)
	}
	snapshot, err := client.ParseSnapshot(encoded)
	if err != nil || snapshot.SizeBytes() != client.MaximumSnapshotEnvelopeBytes {
		t.Fatalf("parse maximum envelope: size=%d err=%v", snapshot.SizeBytes(), err)
	}
	if _, err = client.ParseSnapshot(append(encoded, 0)); err == nil {
		t.Fatal("one-byte oversized opaque envelope succeeded")
	}

	response := &enginev1.ExportSnapshotResponse{Status: commonv1.OKStatus(), Snapshot: envelope}
	responseBytes := uint64(proto.Size(response))
	if responseBytes <= uint64(client.MaximumSnapshotEnvelopeBytes) {
		t.Fatalf("export response wrapper size = %d", responseBytes)
	}
	responseLimits := &commonv1.Limits{
		MaxMessageBytes: responseBytes, MaxCollectionItems: 1, MaxReplayEvents: 1,
		MaxReplayBytes: 1, MaxConcurrentStreams: 1, MaxActiveRuns: 1,
	}
	if err = enginev1.ValidateExportSnapshotResponse(response, responseLimits); err != nil {
		t.Fatalf("exact-size export response wrapper: %v", err)
	}
	responseLimits.MaxMessageBytes = uint64(client.MaximumSnapshotEnvelopeBytes)
	if err = enginev1.ValidateExportSnapshotResponse(response, responseLimits); err == nil {
		t.Fatal("client envelope-only bound accepted the larger RPC response wrapper")
	}
}
