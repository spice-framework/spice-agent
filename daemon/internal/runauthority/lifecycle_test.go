package runauthority

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
)

func TestStoreLifecycleAndKeyGeneration(t *testing.T) {
	store := openTestStore(t, filepath.Join(authorityTestRoot(t), "authority"))
	active, err := store.Start(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if active.RunGeneration() != 1 {
		t.Fatalf("run generation = %d", active.RunGeneration())
	}
	if _, err = store.Start(t.Context(), "run"); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent start = %v", err)
	}
	snapshot := internalSnapshot(t, active, "run", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	if snapshot.GetAuthority().GetGeneration() != store.authorityGeneration {
		t.Fatal("wire generation is not the authority-key generation")
	}
	transaction, err := store.PrepareImport(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	request := &enginev1.ImportSnapshotRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "operation", Snapshot: snapshot,
	}
	if err = enginev1.ValidateImportSnapshotRequest(t.Context(), request, transaction); err != nil {
		t.Fatal(err)
	}
	if err = transaction.Consume(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = enginev1.ValidateImportSnapshotRequest(t.Context(), request, transaction); err == nil {
		t.Fatal("consumed transaction remained a verifier")
	}
	resumed, err := transaction.Activate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if resumed.RunGeneration() != 2 {
		t.Fatalf("resumed generation = %d", resumed.RunGeneration())
	}
	if err = resumed.Terminal(t.Context(), PhaseCompleted); err != nil {
		t.Fatal(err)
	}
	if err = resumed.Terminal(t.Context(), PhaseCompleted); !errors.Is(err, ErrClosed) {
		t.Fatalf("second terminal = %v", err)
	}
	if _, err = store.Start(t.Context(), "run"); !errors.Is(err, ErrState) {
		t.Fatalf("terminal identity reuse = %v", err)
	}
}

func TestStoreAbortReplayAndTerminalSigning(t *testing.T) {
	store := openTestStore(t, filepath.Join(authorityTestRoot(t), "authority"))
	active, err := store.Start(t.Context(), "abort")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := internalSnapshot(t, active, "abort", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	transaction, err := store.PrepareImport(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = transaction.Abort(); err != nil {
		t.Fatal(err)
	}
	if err = transaction.Abort(); err != nil {
		t.Fatal(err)
	}
	transaction, err = store.PrepareImport(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = transaction.Consume(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = transaction.Abort(); !errors.Is(err, ErrUncertain) {
		t.Fatalf("consumed abort = %v", err)
	}
	if _, err = store.PrepareImport(t.Context(), snapshot); !errors.Is(err, ErrVerification) {
		t.Fatalf("consumed replay = %v", err)
	}

	terminal, err := store.Start(t.Context(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	completed := internalSnapshot(t, terminal, "terminal", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_COMPLETED)
	if _, err = store.PrepareImport(t.Context(), completed); !errors.Is(err, ErrVerification) {
		t.Fatalf("terminal snapshot import = %v", err)
	}
}

func TestConsumeWriteFailureIsUncertainAndFencesRetry(t *testing.T) {
	store := openTestStore(t, filepath.Join(authorityTestRoot(t), "authority"))
	active, err := store.Start(t.Context(), "write-uncertain")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := internalSnapshot(t, active, "write-uncertain", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	transaction, err := store.PrepareImport(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	store.writeFile = func(string, []byte) error { return errors.New("late durability failure") }
	if err = transaction.Consume(t.Context()); !errors.Is(err, ErrUncertain) {
		t.Fatalf("failed consume = %v", err)
	}
	store.writeFile = store.directory.writeFileAtomic
	if err = transaction.VerifySnapshot(t.Context(), enginev1.SnapshotAuthorityInput{}, nil); !errors.Is(err, ErrUncertain) {
		t.Fatalf("verify after failed consume = %v", err)
	}
	if err = transaction.Consume(t.Context()); !errors.Is(err, ErrUncertain) {
		t.Fatalf("second consume after failed consume = %v", err)
	}
	if _, err = transaction.Activate(t.Context()); !errors.Is(err, ErrUncertain) {
		t.Fatalf("activate after failed consume = %v", err)
	}
	if err = transaction.Abort(); !errors.Is(err, ErrUncertain) {
		t.Fatalf("abort after failed consume = %v", err)
	}
	if _, err = store.PrepareImport(t.Context(), snapshot); !errors.Is(err, ErrUncertain) {
		t.Fatalf("automatic retry after ambiguous write = %v", err)
	}
	if _, err = store.Start(t.Context(), "write-uncertain"); !errors.Is(err, ErrUncertain) {
		t.Fatalf("start after ambiguous write = %v", err)
	}
}

func TestConsumeCancellationBeforeWriteRemainsAbortable(t *testing.T) {
	store := openTestStore(t, filepath.Join(authorityTestRoot(t), "authority"))
	active, err := store.Start(t.Context(), "cancel-before-consume")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := internalSnapshot(t, active, "cancel-before-consume", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	transaction, err := store.PrepareImport(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err = transaction.Consume(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled consume = %v", err)
	}
	if err = transaction.Abort(); err != nil {
		t.Fatalf("abort after pre-write cancellation = %v", err)
	}
	retry, err := store.PrepareImport(t.Context(), snapshot)
	if err != nil {
		t.Fatalf("retry after pre-write cancellation = %v", err)
	}
	if err = retry.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestActivateWriteFailureStaysUncertain(t *testing.T) {
	store := openTestStore(t, filepath.Join(authorityTestRoot(t), "authority"))
	active, err := store.Start(t.Context(), "activate-uncertain")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := internalSnapshot(t, active, "activate-uncertain", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	transaction, err := store.PrepareImport(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = transaction.Consume(t.Context()); err != nil {
		t.Fatal(err)
	}
	store.writeFile = func(string, []byte) error { return errors.New("late activation durability failure") }
	if _, err = transaction.Activate(t.Context()); !errors.Is(err, ErrUncertain) {
		t.Fatalf("failed activation = %v", err)
	}
	store.writeFile = store.directory.writeFileAtomic
	if err = transaction.VerifySnapshot(t.Context(), enginev1.SnapshotAuthorityInput{}, nil); !errors.Is(err, ErrUncertain) {
		t.Fatalf("verify after failed activation = %v", err)
	}
	if err = transaction.Consume(t.Context()); !errors.Is(err, ErrUncertain) {
		t.Fatalf("consume after failed activation = %v", err)
	}
	if _, err = transaction.Activate(t.Context()); !errors.Is(err, ErrUncertain) {
		t.Fatalf("second activation after failed activation = %v", err)
	}
	if err = transaction.Abort(); !errors.Is(err, ErrUncertain) {
		t.Fatalf("abort after failed activation = %v", err)
	}
}

func TestLocalResumeGenerationBoundaryAndTerminalFromSuspended(t *testing.T) {
	store := openTestStore(t, filepath.Join(authorityTestRoot(t), "authority"))
	active, err := store.Start(t.Context(), "generation-boundary")
	if err != nil {
		t.Fatal(err)
	}
	active.runGeneration = math.MaxUint64
	writes := 0
	store.writeFile = func(string, []byte) error {
		writes++
		return nil
	}
	payload := []byte(`{"version":"spice.agent.snapshot/v1alpha2","run_id":"generation-boundary"}`)
	if _, err = enginev1.NewSnapshotEnvelope(
		t.Context(), active, "generation-boundary", 1,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED, payload,
	); !errors.Is(err, enginev1.ErrSnapshotAuthoritySigning) {
		t.Fatalf("suspend at max generation = %v", err)
	}
	if writes != 0 {
		t.Fatalf("max-generation suspension attempted %d writes", writes)
	}
	store.writeFile = store.directory.writeFileAtomic
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}

	active, err = store.Start(t.Context(), "resume-generation-boundary")
	if err != nil {
		t.Fatal(err)
	}
	_ = internalSnapshot(t, active, "resume-generation-boundary", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	active.runGeneration = math.MaxUint64
	store.writeFile = func(string, []byte) error {
		writes++
		return nil
	}
	if err = active.Resume(t.Context()); !errors.Is(err, ErrState) {
		t.Fatalf("resume at max generation = %v", err)
	}
	if writes != 0 {
		t.Fatalf("max-generation resume attempted %d writes", writes)
	}
	store.writeFile = store.directory.writeFileAtomic
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}

	terminal, err := store.Start(t.Context(), "terminal-from-suspended")
	if err != nil {
		t.Fatal(err)
	}
	_ = internalSnapshot(t, terminal, "terminal-from-suspended", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	_ = internalSnapshot(t, terminal, "terminal-from-suspended", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_CANCELLED)
	if _, err = store.Start(t.Context(), "terminal-from-suspended"); !errors.Is(err, ErrState) {
		t.Fatalf("terminal run ID reuse = %v", err)
	}
}

func TestLocalResumeWriteFailureIsTerminallyUncertain(t *testing.T) {
	store := openTestStore(t, filepath.Join(authorityTestRoot(t), "authority"))
	active, err := store.Start(t.Context(), "local-resume-uncertain")
	if err != nil {
		t.Fatal(err)
	}
	_ = internalSnapshot(t, active, "local-resume-uncertain", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	store.writeFile = func(string, []byte) error { return errors.New("late local-resume durability failure") }
	if err = active.Resume(t.Context()); !errors.Is(err, ErrUncertain) {
		t.Fatalf("failed local resume = %v", err)
	}
	store.writeFile = store.directory.writeFileAtomic
	if err = active.Resume(t.Context()); !errors.Is(err, ErrUncertain) {
		t.Fatalf("retry local resume = %v", err)
	}
	if err = active.Terminal(t.Context(), PhaseCancelled); !errors.Is(err, ErrUncertain) {
		t.Fatalf("terminal after uncertain local resume = %v", err)
	}
	if err = active.Close(); !errors.Is(err, ErrUncertain) {
		t.Fatalf("first close after uncertain local resume = %v", err)
	}
	if err = active.Close(); err != nil {
		t.Fatalf("repeated close after uncertain local resume = %v", err)
	}
}

func TestActivePrewriteCancellationRemainsUsable(t *testing.T) {
	store := openTestStore(t, filepath.Join(authorityTestRoot(t), "authority"))
	active, err := store.Start(t.Context(), "resume-cancellation")
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = enginev1.NewSnapshotEnvelope(
		cancelled, active, "resume-cancellation", 1,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED,
		[]byte(`{"version":"spice.agent.snapshot/v1alpha2","run_id":"resume-cancellation"}`),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled suspended signing = %v", err)
	}
	if _, err = enginev1.NewSnapshotEnvelope(
		t.Context(), active, "resume-cancellation", 1,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED,
		[]byte(`{"version":"spice.agent.snapshot/v1alpha2","run_id":"resume-cancellation"}`),
	); err != nil {
		t.Fatal(err)
	}
	writes := 0
	originalWriter := store.writeFile
	store.writeFile = func(name string, value []byte) error {
		writes++
		return originalWriter(name, value)
	}
	prewriteCancelled := &cancelOnErrContext{cancelAt: 3}
	if err = active.Resume(prewriteCancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("prewrite-cancelled resume = %v", err)
	}
	if writes != 0 {
		t.Fatalf("prewrite-cancelled resume invoked writer %d times", writes)
	}
	if err = active.Resume(t.Context()); err != nil {
		t.Fatalf("resume after proven prewrite cancellation = %v", err)
	}
	if writes != 1 {
		t.Fatalf("successful resume writer calls = %d", writes)
	}
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}

	terminal, err := store.Start(t.Context(), "terminal-cancellation")
	if err != nil {
		t.Fatal(err)
	}
	writes = 0
	prewriteCancelled = &cancelOnErrContext{cancelAt: 2}
	if err = terminal.Terminal(prewriteCancelled, PhaseCancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("prewrite-cancelled terminal = %v", err)
	}
	if writes != 0 {
		t.Fatalf("prewrite-cancelled terminal invoked writer %d times", writes)
	}
	if err = terminal.Terminal(t.Context(), PhaseCancelled); err != nil {
		t.Fatalf("terminal after proven prewrite cancellation = %v", err)
	}
}

func TestSuspendedTerminalAndSigningFailuresAreUncertain(t *testing.T) {
	store := openTestStore(t, filepath.Join(authorityTestRoot(t), "authority"))
	terminal, err := store.Start(t.Context(), "terminal-write-failure")
	if err != nil {
		t.Fatal(err)
	}
	_ = internalSnapshot(t, terminal, "terminal-write-failure", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	store.writeFile = func(string, []byte) error { return errors.New("late terminal durability failure") }
	if err = terminal.Terminal(t.Context(), PhaseCancelled); !errors.Is(err, ErrUncertain) {
		t.Fatalf("failed terminal from suspended = %v", err)
	}
	store.writeFile = store.directory.writeFileAtomic
	if err = terminal.Resume(t.Context()); !errors.Is(err, ErrUncertain) {
		t.Fatalf("resume after uncertain terminal = %v", err)
	}
	if err = terminal.Close(); !errors.Is(err, ErrUncertain) {
		t.Fatalf("close after uncertain terminal = %v", err)
	}
	if err = terminal.Close(); err != nil {
		t.Fatalf("repeated close after uncertain terminal = %v", err)
	}

	signer, err := store.Start(t.Context(), "sign-write-failure")
	if err != nil {
		t.Fatal(err)
	}
	store.writeFile = func(string, []byte) error { return errors.New("late signing durability failure") }
	if _, err = enginev1.NewSnapshotEnvelope(
		t.Context(), signer, "sign-write-failure", 1,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED,
		[]byte(`{"version":"spice.agent.snapshot/v1alpha2","run_id":"sign-write-failure"}`),
	); !errors.Is(err, enginev1.ErrSnapshotAuthoritySigning) {
		t.Fatalf("failed suspended signing = %v", err)
	}
	store.writeFile = store.directory.writeFileAtomic
	if err = signer.Terminal(t.Context(), PhaseFailed); !errors.Is(err, ErrUncertain) {
		t.Fatalf("terminal after uncertain signing = %v", err)
	}
	if err = signer.Close(); !errors.Is(err, ErrUncertain) {
		t.Fatalf("close after uncertain signing = %v", err)
	}
}

type cancelOnErrContext struct {
	calls    int
	cancelAt int
}

func (*cancelOnErrContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*cancelOnErrContext) Done() <-chan struct{}       { return nil }
func (*cancelOnErrContext) Value(any) any               { return nil }

func (ctx *cancelOnErrContext) Err() error {
	ctx.calls++
	if ctx.calls >= ctx.cancelAt {
		return context.Canceled
	}
	return nil
}

func TestStoreVerificationAndStateFailures(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "authority")
	store := openTestStore(t, directory)
	active, err := store.Start(t.Context(), "verify")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := internalSnapshot(t, active, "verify", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	other := openTestStore(t, filepath.Join(authorityTestRoot(t), "other"))
	if _, err = other.PrepareImport(t.Context(), snapshot); !errors.Is(err, ErrVerification) {
		t.Fatalf("wrong scope = %v", err)
	}

	statePath := store.statePath("verify")
	wire, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	wire[len(wire)/2] ^= 1
	if err = os.WriteFile(statePath, wire, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.readRecord("verify"); !errors.Is(err, ErrVerification) {
		t.Fatalf("tampered read = %v", err)
	}
	if _, err = store.Start(t.Context(), "verify"); !errors.Is(err, ErrVerification) {
		t.Fatalf("start over tamper = %v", err)
	}
}

func TestStoreValidationBoundariesAndNilSafety(t *testing.T) {
	store := openTestStore(t, filepath.Join(authorityTestRoot(t), "authority"))
	for _, runID := range []string{"", " run", "run\n", strings.Repeat("x", 129)} {
		if _, err := store.Start(t.Context(), runID); !errors.Is(err, ErrConfiguration) {
			t.Fatalf("invalid run ID %q = %v", runID, err)
		}
	}
	if _, err := store.Start(nil, "run"); !errors.Is(err, ErrConfiguration) { //nolint:staticcheck // Explicit nil-safety boundary.
		t.Fatalf("nil context = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Start(cancelled, "run"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled start = %v", err)
	}
	if _, err := store.PrepareImport(nil, nil); !errors.Is(err, ErrVerification) { //nolint:staticcheck // Explicit nil-safety boundary.
		t.Fatalf("nil import = %v", err)
	}
	if _, err := (*Store)(nil).Start(t.Context(), "run"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil store = %v", err)
	}
	var nilActive *Active
	if nilActive.RunGeneration() != 0 || nilActive.Close() != nil {
		t.Fatal("nil active is not safe")
	}
	if err := nilActive.Terminal(t.Context(), PhaseCompleted); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil terminal = %v", err)
	}
	var nilImport *Import
	if err := nilImport.Abort(); err != nil {
		t.Fatal(err)
	}
	if err := nilImport.Consume(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil consume = %v", err)
	}
	if _, err := nilImport.Activate(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatalf("nil activate = %v", err)
	}
}

func TestRecordValidationAndSecureFileReplacement(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "authority")
	store := openTestStore(t, directory)
	valid := store.baseRecord("run", 1, PhaseActive)
	if err := store.validateRecord(valid, "run"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*record){
		"version":     func(value *record) { value.Version++ },
		"scope":       func(value *record) { value.ScopeID = hex.EncodeToString(make([]byte, 32)) },
		"authority":   func(value *record) { value.AuthorityGeneration++ },
		"run":         func(value *record) { value.RunID = "other" },
		"generation":  func(value *record) { value.RunGeneration = 0 },
		"phase":       func(value *record) { value.Phase = "UNKNOWN" },
		"active data": func(value *record) { value.Snapshot = &snapshotRecord{} },
	} {
		candidate := valid
		mutate(&candidate)
		if err := store.validateRecord(candidate, "run"); err == nil {
			t.Fatalf("%s record accepted", name)
		}
	}
	path := filepath.Join(directory, "replacement")
	if err := writeSecureFileAtomic(path, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := writeSecureFileAtomic(path, []byte("two")); err != nil {
		t.Fatal(err)
	}
	value, err := readSecureFile(path, 3)
	if err != nil || string(value) != "two" {
		t.Fatalf("replacement = %q, %v", value, err)
	}
	if _, err = readSecureFile(path, 2); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("oversized secure read = %v", err)
	}
	lock, err := acquireStableLock(filepath.Join(directory, "explicit.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = acquireStableLock(filepath.Join(directory, "explicit.lock")); !errors.Is(err, errLockBusy) {
		t.Fatalf("second stable lock = %v", err)
	}
	if err = lock.close(); err != nil {
		t.Fatal(err)
	}
	if err = lock.close(); err != nil {
		t.Fatal(err)
	}
}

func openTestStore(t *testing.T, directory string) *Store {
	t.Helper()
	store, err := Open(Config{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})
	return store
}

func internalSnapshot(
	t *testing.T,
	signer enginev1.SnapshotAuthoritySigner,
	runID string,
	lifecycle enginev1.SnapshotLifecycle,
) *enginev1.SnapshotEnvelope {
	t.Helper()
	payload := []byte(fmt.Sprintf(`{"version":"spice.agent.snapshot/v1alpha2","run_id":%q}`, runID))
	snapshot, err := enginev1.NewSnapshotEnvelope(t.Context(), signer, runID, 1, lifecycle, payload)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
