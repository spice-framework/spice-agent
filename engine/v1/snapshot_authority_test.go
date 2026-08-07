package enginev1_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const snapshotPayload = `{"version":"spice.agent.snapshot/v1alpha2","run_id":"run-1"}`

func TestSnapshotAuthorityCanonicalGoldenAndDefensiveCopies(t *testing.T) {
	t.Parallel()
	scopeID := bytes.Repeat([]byte{0x11}, 32)
	key := bytes.Repeat([]byte{0x22}, 32)
	codec, err := enginev1.NewHMACSnapshotAuthority(scopeID, 7, key)
	if err != nil {
		t.Fatal(err)
	}
	scopeID[0] = 0xff
	key[0] = 0xff

	snapshot, err := enginev1.NewSnapshotEnvelope(
		context.Background(), codec, "run-1", 42,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED,
		[]byte(snapshotPayload),
	)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "a7ddca34b14c95bfa8f90bead111efa46aa97892aa7a093905e1a367fd77ebfd"
	if got := hex.EncodeToString(snapshot.GetAuthority().GetHmacSha256()); got != expected {
		t.Fatalf("snapshot authority HMAC = %s", got)
	}
	returnedScope := snapshot.GetAuthority().GetScopeId()
	if len(returnedScope) != 32 || returnedScope[0] != 0x11 {
		t.Fatal("authority retained caller-owned scope ID")
	}
	if rendered := fmt.Sprintf("%+v %#v", codec, codec); strings.Contains(rendered, "222222") {
		t.Fatalf("authority rendered secret key: %s", rendered)
	}
}

func TestSnapshotAuthorityStructuralValidationDoesNotPretendToVerify(t *testing.T) {
	t.Parallel()
	valid := &enginev1.SnapshotAuthority{
		ScopeId: bytes.Repeat([]byte{1}, 32), Generation: 1, HmacSha256: bytes.Repeat([]byte{2}, 32),
	}
	if err := enginev1.ValidateSnapshotAuthority(valid); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]*enginev1.SnapshotAuthority{
		"nil":         nil,
		"short scope": {ScopeId: bytes.Repeat([]byte{1}, 31), Generation: 1, HmacSha256: bytes.Repeat([]byte{2}, 32)},
		"long scope":  {ScopeId: bytes.Repeat([]byte{1}, 33), Generation: 1, HmacSha256: bytes.Repeat([]byte{2}, 32)},
		"generation":  {ScopeId: bytes.Repeat([]byte{1}, 32), HmacSha256: bytes.Repeat([]byte{2}, 32)},
		"short HMAC":  {ScopeId: bytes.Repeat([]byte{1}, 32), Generation: 1, HmacSha256: bytes.Repeat([]byte{2}, 31)},
		"long HMAC":   {ScopeId: bytes.Repeat([]byte{1}, 32), Generation: 1, HmacSha256: bytes.Repeat([]byte{2}, 33)},
	} {
		if err := enginev1.ValidateSnapshotAuthority(value); err == nil {
			t.Errorf("invalid %s authority succeeded", name)
		}
	}
}

func TestSnapshotImportRejectsEveryAuthenticatedTamper(t *testing.T) {
	t.Parallel()
	codec := snapshotAuthority(t)
	original, err := enginev1.NewSnapshotEnvelope(
		context.Background(), codec, "run-1", 42,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED,
		[]byte(snapshotPayload),
	)
	if err != nil {
		t.Fatal(err)
	}
	requestFor := func(snapshot *enginev1.SnapshotEnvelope) *enginev1.ImportSnapshotRequest {
		return &enginev1.ImportSnapshotRequest{
			ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "import-1", Snapshot: snapshot,
		}
	}
	for name, mutate := range map[string]func(*enginev1.SnapshotEnvelope){
		"scope":      func(value *enginev1.SnapshotEnvelope) { value.Authority.ScopeId[0] ^= 0xff },
		"generation": func(value *enginev1.SnapshotEnvelope) { value.Authority.Generation++ },
		"HMAC":       func(value *enginev1.SnapshotEnvelope) { value.Authority.HmacSha256[0] ^= 0xff },
		"sequence":   func(value *enginev1.SnapshotEnvelope) { value.LastSequence++ },
		"payload": func(value *enginev1.SnapshotEnvelope) {
			value.Payload = []byte(`{"version":"spice.agent.snapshot/v1alpha2","run_id":"run-1","changed":true}`)
			digest := sha256.Sum256(value.Payload)
			value.Sha256 = digest[:]
		},
		"run ID": func(value *enginev1.SnapshotEnvelope) {
			value.RunId = "run-2"
			value.Payload = []byte(`{"version":"spice.agent.snapshot/v1alpha2","run_id":"run-2"}`)
			digest := sha256.Sum256(value.Payload)
			value.Sha256 = digest[:]
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			candidate := cloneSnapshot(t, original)
			mutate(candidate)
			if validationErr := enginev1.ValidateSnapshotEnvelope(candidate); validationErr != nil {
				t.Fatalf("tamper was not structurally valid: %v", validationErr)
			}
			importErr := enginev1.ValidateImportSnapshotRequest(context.Background(), requestFor(candidate), codec, protocolLimits())
			if !errors.Is(importErr, enginev1.ErrSnapshotAuthorityVerification) {
				t.Fatalf("tampered import error = %v", importErr)
			}
		})
	}

	completed, err := enginev1.NewSnapshotEnvelope(
		context.Background(), codec, "run-1", 42,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_COMPLETED,
		[]byte(snapshotPayload),
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(completed.GetAuthority().GetHmacSha256(), original.GetAuthority().GetHmacSha256()) {
		t.Fatal("snapshot lifecycle was absent from the authority HMAC")
	}
}

func TestSnapshotAuthoritySignerVerifierFailureAndCancellationAreContained(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	codec := snapshotAuthority(t)
	for name, signer := range map[string]enginev1.SnapshotAuthoritySigner{
		"nil": nil,
		"failure": snapshotSignerFunc(func(context.Context, enginev1.SnapshotAuthorityInput) (*enginev1.SnapshotAuthority, error) {
			return nil, errors.New("signing-secret")
		}),
		"panic": snapshotSignerFunc(func(context.Context, enginev1.SnapshotAuthorityInput) (*enginev1.SnapshotAuthority, error) {
			panic("signing-secret")
		}),
		"invalid": snapshotSignerFunc(func(context.Context, enginev1.SnapshotAuthorityInput) (*enginev1.SnapshotAuthority, error) {
			return &enginev1.SnapshotAuthority{}, nil
		}),
	} {
		_, err := enginev1.NewSnapshotEnvelope(
			ctx, signer, "run-1", 42,
			enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED,
			[]byte(snapshotPayload),
		)
		if !errors.Is(err, enginev1.ErrSnapshotAuthoritySigning) || strings.Contains(fmt.Sprint(err), "secret") {
			t.Errorf("%s signer error = %v", name, err)
		}
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := enginev1.NewSnapshotEnvelope(
		timeoutCtx,
		snapshotSignerFunc(func(ctx context.Context, _ enginev1.SnapshotAuthorityInput) (*enginev1.SnapshotAuthority, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}),
		"run-1", 42, enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED, []byte(snapshotPayload),
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled signer error = %v", err)
	}

	ignoringSignerStarted := make(chan struct{})
	ignoringSignerRelease := make(chan struct{})
	ignoringSignerResult := make(chan error, 1)
	ignoringSignerCtx, ignoringSignerCancel := context.WithCancel(context.Background())
	go func() {
		_, signingErr := enginev1.NewSnapshotEnvelope(
			ignoringSignerCtx,
			snapshotSignerFunc(func(_ context.Context, input enginev1.SnapshotAuthorityInput) (*enginev1.SnapshotAuthority, error) {
				close(ignoringSignerStarted)
				<-ignoringSignerRelease
				return codec.SignSnapshot(context.Background(), input)
			}),
			"run-1", 42, enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED, []byte(snapshotPayload),
		)
		ignoringSignerResult <- signingErr
	}()
	<-ignoringSignerStarted
	ignoringSignerCancel()
	close(ignoringSignerRelease)
	if err = <-ignoringSignerResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("context-ignoring signer error = %v", err)
	}

	snapshot, err := enginev1.NewSnapshotEnvelope(
		ctx, codec, "run-1", 42,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED, []byte(snapshotPayload),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := &enginev1.ImportSnapshotRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "import-1", Snapshot: snapshot,
	}
	for name, verifier := range map[string]enginev1.SnapshotAuthorityVerifier{
		"nil": nil,
		"failure": snapshotVerifierFunc(func(context.Context, enginev1.SnapshotAuthorityInput, *enginev1.SnapshotAuthority) error {
			return errors.New("verification-secret")
		}),
		"panic": snapshotVerifierFunc(func(context.Context, enginev1.SnapshotAuthorityInput, *enginev1.SnapshotAuthority) error {
			panic("verification-secret")
		}),
	} {
		err = enginev1.ValidateImportSnapshotRequest(ctx, request, verifier, protocolLimits())
		if !errors.Is(err, enginev1.ErrSnapshotAuthorityVerification) || strings.Contains(fmt.Sprint(err), "secret") {
			t.Errorf("%s verifier error = %v", name, err)
		}
	}

	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer verifyCancel()
	err = enginev1.ValidateImportSnapshotRequest(
		verifyCtx,
		request,
		snapshotVerifierFunc(func(ctx context.Context, _ enginev1.SnapshotAuthorityInput, _ *enginev1.SnapshotAuthority) error {
			<-ctx.Done()
			return ctx.Err()
		}),
		protocolLimits(),
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled verifier error = %v", err)
	}

	ignoringVerifierStarted := make(chan struct{})
	ignoringVerifierRelease := make(chan struct{})
	ignoringVerifierResult := make(chan error, 1)
	ignoringVerifierCtx, ignoringVerifierCancel := context.WithCancel(context.Background())
	go func() {
		validationErr := enginev1.ValidateImportSnapshotRequest(
			ignoringVerifierCtx,
			request,
			snapshotVerifierFunc(func(context.Context, enginev1.SnapshotAuthorityInput, *enginev1.SnapshotAuthority) error {
				close(ignoringVerifierStarted)
				<-ignoringVerifierRelease
				return nil
			}),
			protocolLimits(),
		)
		ignoringVerifierResult <- validationErr
	}()
	<-ignoringVerifierStarted
	ignoringVerifierCancel()
	close(ignoringVerifierRelease)
	if err = <-ignoringVerifierResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("context-ignoring verifier error = %v", err)
	}
}

func TestSnapshotAuthorityDescriptorAndUnknownFields(t *testing.T) {
	t.Parallel()
	authorityDescriptor := (&enginev1.SnapshotAuthority{}).ProtoReflect().Descriptor()
	for name, number := range map[protoreflect.Name]protoreflect.FieldNumber{
		"scope_id": 1, "generation": 2, "hmac_sha256": 3,
	} {
		field := authorityDescriptor.Fields().ByName(name)
		if field == nil || field.Number() != number {
			t.Fatalf("snapshot authority field %s = %#v", name, field)
		}
	}
	envelopeAuthority := (&enginev1.SnapshotEnvelope{}).ProtoReflect().Descriptor().Fields().ByName("authority")
	if envelopeAuthority == nil || envelopeAuthority.Number() != 7 ||
		envelopeAuthority.Message().FullName() != authorityDescriptor.FullName() {
		t.Fatalf("snapshot envelope authority descriptor = %#v", envelopeAuthority)
	}

	codec := snapshotAuthority(t)
	snapshot, err := enginev1.NewSnapshotEnvelope(
		context.Background(), codec, "run-1", 42,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED, []byte(snapshotPayload),
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := proto.Marshal(snapshot.GetAuthority())
	if err != nil {
		t.Fatal(err)
	}
	encoded = protowire.AppendTag(encoded, 100, protowire.BytesType)
	encoded = protowire.AppendString(encoded, "future-authority")
	var decoded enginev1.SnapshotAuthority
	if err = proto.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	snapshot.Authority = &decoded
	request := &enginev1.ImportSnapshotRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "import-1", Snapshot: snapshot,
	}
	if err = enginev1.ValidateImportSnapshotRequest(context.Background(), request, codec, protocolLimits()); err != nil {
		t.Fatalf("unknown authority field changed known semantics: %v", err)
	}
	roundTrip, err := proto.Marshal(snapshot.GetAuthority())
	if err != nil || !bytes.Contains(roundTrip, []byte("future-authority")) {
		t.Fatalf("unknown authority round trip = %x, %v", roundTrip, err)
	}
}

type snapshotSignerFunc func(context.Context, enginev1.SnapshotAuthorityInput) (*enginev1.SnapshotAuthority, error)

func (function snapshotSignerFunc) SignSnapshot(
	ctx context.Context,
	input enginev1.SnapshotAuthorityInput,
) (*enginev1.SnapshotAuthority, error) {
	return function(ctx, input)
}

type snapshotVerifierFunc func(context.Context, enginev1.SnapshotAuthorityInput, *enginev1.SnapshotAuthority) error

func (function snapshotVerifierFunc) VerifySnapshot(
	ctx context.Context,
	input enginev1.SnapshotAuthorityInput,
	authority *enginev1.SnapshotAuthority,
) error {
	return function(ctx, input, authority)
}

func cloneSnapshot(t *testing.T, value *enginev1.SnapshotEnvelope) *enginev1.SnapshotEnvelope {
	t.Helper()
	result, ok := proto.Clone(value).(*enginev1.SnapshotEnvelope)
	if !ok {
		t.Fatal("Protobuf clone changed snapshot envelope type")
	}
	return result
}
