package enginev1_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"strings"
	"sync/atomic"
	"testing"

	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestImportSnapshotRequestStructureAcceptsValidUnkeyedContract(t *testing.T) {
	t.Parallel()
	request := structuralImportRequest(t)
	if err := enginev1.ValidateImportSnapshotRequestStructure(request, protocolLimits()); err != nil {
		t.Fatal(err)
	}

	request.Snapshot.Authority.HmacSha256[0] ^= 0xff
	if err := enginev1.ValidateImportSnapshotRequestStructure(request, protocolLimits()); err != nil {
		t.Fatalf("structurally valid untrusted HMAC was keyed implicitly: %v", err)
	}
	if err := enginev1.ValidateImportSnapshotRequest(
		t.Context(), request, snapshotAuthority(t), protocolLimits(),
	); !errors.Is(err, enginev1.ErrSnapshotAuthorityVerification) {
		t.Fatalf("keyed validator accepted untrusted HMAC: %v", err)
	}
}

func TestImportSnapshotRequestStructureRejectsEveryInvalidFieldClass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*enginev1.ImportSnapshotRequest)
		limits bool
	}{
		{name: "client identity", mutate: func(value *enginev1.ImportSnapshotRequest) { value.ClientId = "" }},
		{name: "client control character", mutate: func(value *enginev1.ImportSnapshotRequest) { value.ClientId = "client\nforged" }},
		{name: "ownership epoch", mutate: func(value *enginev1.ImportSnapshotRequest) { value.OwnershipEpoch = 0 }},
		{name: "operation identity", mutate: func(value *enginev1.ImportSnapshotRequest) { value.ClientOperationId = "" }},
		{name: "operation whitespace", mutate: func(value *enginev1.ImportSnapshotRequest) { value.ClientOperationId = " import" }},
		{name: "missing limits", limits: true},
		{name: "missing envelope", mutate: func(value *enginev1.ImportSnapshotRequest) { value.Snapshot = nil }},
		{name: "format", mutate: func(value *enginev1.ImportSnapshotRequest) { value.Snapshot.Format = "snapshot/v0" }},
		{name: "run identity", mutate: func(value *enginev1.ImportSnapshotRequest) { value.Snapshot.RunId = "other-run" }},
		{name: "zero sequence", mutate: func(value *enginev1.ImportSnapshotRequest) { value.Snapshot.LastSequence = 0 }},
		{name: "terminal sequence", mutate: func(value *enginev1.ImportSnapshotRequest) { value.Snapshot.LastSequence = math.MaxUint64 }},
		{name: "unknown lifecycle", mutate: func(value *enginev1.ImportSnapshotRequest) {
			value.Snapshot.Lifecycle = enginev1.SnapshotLifecycle(9000)
		}},
		{name: "completed lifecycle", mutate: func(value *enginev1.ImportSnapshotRequest) {
			value.Snapshot.Lifecycle = enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_COMPLETED
		}},
		{name: "empty payload", mutate: func(value *enginev1.ImportSnapshotRequest) { value.Snapshot.Payload = nil }},
		{name: "payload digest", mutate: func(value *enginev1.ImportSnapshotRequest) { value.Snapshot.Payload[0] ^= 0xff }},
		{name: "payload JSON", mutate: func(value *enginev1.ImportSnapshotRequest) {
			value.Snapshot.Payload = []byte("not-json")
			digest := sha256.Sum256(value.Snapshot.Payload)
			value.Snapshot.Sha256 = digest[:]
		}},
		{name: "missing authority", mutate: func(value *enginev1.ImportSnapshotRequest) { value.Snapshot.Authority = nil }},
		{name: "authority scope", mutate: func(value *enginev1.ImportSnapshotRequest) { value.Snapshot.Authority.ScopeId = nil }},
		{name: "authority generation", mutate: func(value *enginev1.ImportSnapshotRequest) { value.Snapshot.Authority.Generation = 0 }},
		{name: "authority HMAC", mutate: func(value *enginev1.ImportSnapshotRequest) { value.Snapshot.Authority.HmacSha256 = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := structuralImportRequest(t)
			if test.mutate != nil {
				test.mutate(request)
			}
			limits := protocolLimits()
			if test.limits {
				limits = nil
			}
			if err := enginev1.ValidateImportSnapshotRequestStructure(request, limits); err == nil {
				t.Fatal("invalid structural import succeeded")
			}
		})
	}
	if err := enginev1.ValidateImportSnapshotRequestStructure(nil, protocolLimits()); err == nil {
		t.Fatal("nil structural import succeeded")
	}
}

func TestImportSnapshotRequestStructureAccountsForExactEncodedSize(t *testing.T) {
	t.Parallel()
	request := structuralImportRequest(t)
	size := proto.Size(request)
	if size < 2 {
		t.Fatalf("unexpected import size %d", size)
	}
	// #nosec G115 -- the positive lower-bound guard makes both converted sizes safe.
	exactSize := uint64(size)
	if err := enginev1.ValidateImportSnapshotRequestStructure(
		request, limitsWithMessageBytes(exactSize),
	); err != nil {
		t.Fatalf("exact-size import = %v", err)
	}
	if err := enginev1.ValidateImportSnapshotRequestStructure(
		request, limitsWithMessageBytes(exactSize-1),
	); err == nil || !strings.Contains(err.Error(), "encoded message size") {
		t.Fatalf("oversized import = %v", err)
	}
}

func TestImportSnapshotRequestStructurePreservesCompatibleUnknownFields(t *testing.T) {
	t.Parallel()
	request := structuralImportRequest(t)
	requestUnknown := protowire.AppendTag(nil, 100, protowire.BytesType)
	requestUnknown = protowire.AppendString(requestUnknown, "future-request")
	authorityUnknown := protowire.AppendTag(nil, 102, protowire.BytesType)
	authorityUnknown = protowire.AppendString(authorityUnknown, "future-authority")
	request.ProtoReflect().SetUnknown(requestUnknown)
	request.Snapshot.Authority.ProtoReflect().SetUnknown(authorityUnknown)

	if err := enginev1.ValidateImportSnapshotRequestStructure(request, protocolLimits()); err != nil {
		t.Fatalf("compatible unknown fields changed structural semantics: %v", err)
	}
	if err := enginev1.ValidateImportSnapshotRequest(
		t.Context(), request, snapshotAuthority(t), protocolLimits(),
	); err != nil {
		t.Fatalf("compatible unknown fields changed keyed semantics: %v", err)
	}
	encoded, err := proto.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) < 2 {
		t.Fatalf("unexpected encoded import size %d", len(encoded))
	}
	for _, marker := range [][]byte{[]byte("future-request"), []byte("future-authority")} {
		if !bytes.Contains(encoded, marker) {
			t.Fatalf("unknown marker %q was discarded", marker)
		}
	}
	// #nosec G115 -- the positive lower-bound guard makes this conversion safe.
	encodedSize := uint64(len(encoded))
	if err = enginev1.ValidateImportSnapshotRequestStructure(
		request, limitsWithMessageBytes(encodedSize-1),
	); err == nil {
		t.Fatal("unknown fields were omitted from the encoded-size bound")
	}
}

func TestImportSnapshotRequestStructureRejectsUnsignedEnvelopeExtensionsAndOpaqueOverflow(t *testing.T) {
	t.Parallel()
	request := structuralImportRequest(t)
	envelopeUnknown := protowire.AppendTag(nil, 101, protowire.BytesType)
	envelopeUnknown = protowire.AppendString(envelopeUnknown, "future-envelope")
	request.Snapshot.ProtoReflect().SetUnknown(envelopeUnknown)
	if err := enginev1.ValidateImportSnapshotRequestStructure(request, protocolLimits()); err == nil ||
		!strings.Contains(err.Error(), "unsupported fields") {
		t.Fatalf("unsigned envelope extension = %v", err)
	}

	request = structuralImportRequest(t)
	remaining := enginev1.MaximumSnapshotEnvelopeBytes - proto.Size(request.Snapshot) + 1
	if remaining <= 4 {
		t.Fatalf("unexpected envelope remaining bound %d", remaining)
	}
	unknown := protowire.AppendTag(nil, 102, protowire.BytesType)
	unknown = protowire.AppendBytes(unknown, make([]byte, remaining))
	request.Snapshot.Authority.ProtoReflect().SetUnknown(unknown)
	if err := enginev1.ValidateImportSnapshotRequestStructure(
		request, limitsWithMessageBytes(uint64(proto.Size(request))+1),
	); err == nil || !strings.Contains(err.Error(), "snapshot envelope exceeds") {
		t.Fatalf("oversized opaque envelope = %v", err)
	}
}

func TestKeyedImportValidationRunsStructureBeforeAuthority(t *testing.T) {
	t.Parallel()
	request := structuralImportRequest(t)
	var calls atomic.Int32
	verifier := snapshotVerifierFunc(func(
		context.Context,
		enginev1.SnapshotAuthorityInput,
		*enginev1.SnapshotAuthority,
	) error {
		calls.Add(1)
		return nil
	})
	request.Snapshot.Format = "invalid"
	if err := enginev1.ValidateImportSnapshotRequest(t.Context(), request, verifier, protocolLimits()); err == nil {
		t.Fatal("invalid structure reached keyed verification")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("verifier calls for invalid structure = %d", got)
	}

	request = structuralImportRequest(t)
	if err := enginev1.ValidateImportSnapshotRequest(t.Context(), request, verifier, protocolLimits()); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("verifier calls for valid structure = %d", got)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	request.ClientId = ""
	if err := enginev1.ValidateImportSnapshotRequest(canceled, request, verifier, protocolLimits()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled keyed validation changed precedence: %v", err)
	}
	if err := enginev1.ValidateImportSnapshotRequest(t.Context(), request, nil, protocolLimits()); !errors.Is(err, enginev1.ErrSnapshotAuthorityVerification) {
		t.Fatalf("nil verifier precedence changed: %v", err)
	}
}

func structuralImportRequest(t *testing.T) *enginev1.ImportSnapshotRequest {
	t.Helper()
	snapshot, err := enginev1.NewSnapshotEnvelope(
		t.Context(), snapshotAuthority(t), "run-structural", 11,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED,
		[]byte(`{"version":"spice.agent.snapshot/v1alpha3","run_id":"run-structural"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &enginev1.ImportSnapshotRequest{
		ClientId: "client-structural", OwnershipEpoch: 7,
		ClientOperationId: "import-structural", Snapshot: snapshot,
	}
}
