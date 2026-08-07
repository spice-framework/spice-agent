package daemon_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	"github.com/spice-framework/spice-agent/daemon"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/protobuf/proto"
)

func authorityProtocolLimits() *commonv1.Limits {
	return &commonv1.Limits{
		MaxMessageBytes:    uint64(enginev1.MaximumSnapshotEnvelopeBytes + 1024),
		MaxCollectionItems: 1, MaxReplayEvents: 1, MaxReplayBytes: 1,
		MaxConcurrentStreams: 1, MaxActiveRuns: 1,
	}
}

func TestRunAuthoritySuspensionImportAndGenerationSeparation(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "authority")
	authority := newRunAuthority(t, directory)
	active, err := authority.Start(t.Context(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if active.RunGeneration() != 1 {
		t.Fatalf("initial run generation = %d", active.RunGeneration())
	}
	if _, err = authority.Start(t.Context(), "run-1"); !errors.Is(err, daemon.ErrRunAuthorityBusy) {
		t.Fatalf("duplicate active start = %v", err)
	}
	snapshot := signedSnapshot(t, active, "run-1", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	if snapshot.GetAuthority().GetGeneration() != 1 {
		t.Fatalf("authority key generation = %d", snapshot.GetAuthority().GetGeneration())
	}

	prepared, err := authority.PrepareImport(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	request := &enginev1.ImportSnapshotRequest{
		ClientId: "client", OwnershipEpoch: 1, ClientOperationId: "import", Snapshot: snapshot,
	}
	if err = enginev1.ValidateImportSnapshotRequest(t.Context(), request, prepared, authorityProtocolLimits()); err != nil {
		t.Fatalf("prepared verifier = %v", err)
	}
	if _, err = authority.PrepareImport(t.Context(), snapshot); !errors.Is(err, daemon.ErrRunAuthorityBusy) {
		t.Fatalf("concurrent import = %v", err)
	}
	if err = prepared.Abort(); err != nil {
		t.Fatal(err)
	}

	prepared, err = authority.PrepareImport(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = prepared.Consume(t.Context()); err != nil {
		t.Fatal(err)
	}
	resumed, err := prepared.Activate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if resumed.RunGeneration() != 2 {
		t.Fatalf("resumed run generation = %d", resumed.RunGeneration())
	}
	second := signedSnapshot(t, resumed, "run-1", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if err = resumed.Close(); err != nil {
		t.Fatal(err)
	}
	if second.GetAuthority().GetGeneration() != snapshot.GetAuthority().GetGeneration() {
		t.Fatal("local run transition changed the persistent authority-key generation")
	}
	if bytes.Equal(second.GetAuthority().GetHmacSha256(), snapshot.GetAuthority().GetHmacSha256()) {
		t.Fatal("local run generation was not bound into the derived snapshot authority key")
	}
	if _, err = authority.PrepareImport(t.Context(), snapshot); !errors.Is(err, daemon.ErrRunAuthorityVerification) {
		t.Fatalf("replayed first snapshot = %v", err)
	}
	local, err := authority.PrepareImport(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	// The daemon prepares the kernel resume here before consuming authority state.
	if err = local.Consume(t.Context()); err != nil {
		t.Fatal(err)
	}
	third, err := local.Activate(t.Context())
	if err != nil || third.RunGeneration() != 3 {
		t.Fatalf("second transaction activation = generation %d, %v", third.RunGeneration(), err)
	}
	if err = third.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRunAuthoritySuspendedOwnershipLocalResumeAndIdempotentExport(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "authority")
	owner := newRunAuthority(t, directory)
	contender := newRunAuthority(t, directory)
	active, err := owner.Start(t.Context(), "local-resume")
	if err != nil {
		t.Fatal(err)
	}
	if err = active.Resume(t.Context()); !errors.Is(err, daemon.ErrRunAuthorityState) {
		t.Fatalf("resume while active = %v", err)
	}
	first := signedSnapshot(t, active, "local-resume", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	repeated := signedSnapshot(t, active, "local-resume", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if !proto.Equal(first, repeated) {
		t.Fatal("identical suspended export changed its claim")
	}
	differentPayload := []byte(`{"version":"spice.agent.snapshot/v1alpha2","run_id":"local-resume","different":true}`)
	if _, err = enginev1.NewSnapshotEnvelope(
		t.Context(), active, "local-resume", 2,
		enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED, differentPayload,
	); !errors.Is(err, enginev1.ErrSnapshotAuthoritySigning) {
		t.Fatalf("differing suspended export = %v", err)
	}
	if _, err = contender.PrepareImport(t.Context(), first); !errors.Is(err, daemon.ErrRunAuthorityBusy) {
		t.Fatalf("import while suspended owner lives = %v", err)
	}
	if err = active.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	if active.RunGeneration() != 2 {
		t.Fatalf("local resume generation = %d", active.RunGeneration())
	}
	if err = active.Resume(t.Context()); !errors.Is(err, daemon.ErrRunAuthorityState) {
		t.Fatalf("second local resume = %v", err)
	}
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = contender.PrepareImport(t.Context(), first); !errors.Is(err, daemon.ErrRunAuthorityVerification) {
		t.Fatalf("old snapshot after local resume = %v", err)
	}

	active, err = owner.Start(t.Context(), "resuspend")
	if err != nil {
		t.Fatal(err)
	}
	initial := signedSnapshot(t, active, "resuspend", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if err = active.Resume(t.Context()); err != nil {
		t.Fatal(err)
	}
	second := signedSnapshot(t, active, "resuspend", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if bytes.Equal(initial.GetAuthority().GetHmacSha256(), second.GetAuthority().GetHmacSha256()) {
		t.Fatal("new run generation reused the suspended authority claim")
	}
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	prepared, err := contender.PrepareImport(t.Context(), second)
	if err != nil {
		t.Fatalf("shutdown did not release suspended ownership: %v", err)
	}
	if err = prepared.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestRunAuthorityConsumedAbortIsUncertainAndNonRetryable(t *testing.T) {
	authority := newRunAuthority(t, filepath.Join(authorityTestRoot(t), "authority"))
	active, err := authority.Start(t.Context(), "run-uncertain")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := signedSnapshot(t, active, "run-uncertain", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	prepared, err := authority.PrepareImport(t.Context(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = prepared.Consume(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = prepared.Abort(); !errors.Is(err, daemon.ErrRunAuthorityUncertain) {
		t.Fatalf("consumed abort = %v", err)
	}
	if _, err = authority.PrepareImport(t.Context(), snapshot); !errors.Is(err, daemon.ErrRunAuthorityVerification) {
		t.Fatalf("retry after uncertain consume = %v", err)
	}
}

func TestRunAuthorityWrongScopeTamperAndCancellationFailClosed(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "first")
	first := newRunAuthority(t, directory)
	second := newRunAuthority(t, filepath.Join(authorityTestRoot(t), "second"))
	active, err := first.Start(t.Context(), "run-scope")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := signedSnapshot(t, active, "run-scope", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = second.PrepareImport(t.Context(), snapshot); !errors.Is(err, daemon.ErrRunAuthorityVerification) {
		t.Fatalf("cross-scope import = %v", err)
	}

	state := onlyRunFile(t, directory, ".state")
	wire, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	wire[len(wire)/2] ^= 1
	if err = os.WriteFile(state, wire, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = first.Start(t.Context(), "run-scope"); !errors.Is(err, daemon.ErrRunAuthorityVerification) {
		t.Fatalf("start over tampered state = %v", err)
	}
	if _, err = first.PrepareImport(t.Context(), snapshot); !errors.Is(err, daemon.ErrRunAuthorityVerification) {
		t.Fatalf("tampered record import = %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = first.Start(cancelled, "cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled start = %v", err)
	}
	if _, err = first.PrepareImport(cancelled, snapshot); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled import = %v", err)
	}
}

func TestRunAuthorityTerminalSnapshotCreatesNonResumableTombstone(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "authority")
	authority := newRunAuthority(t, directory)
	active, err := authority.Start(t.Context(), "run-terminal")
	if err != nil {
		t.Fatal(err)
	}
	lockPath := onlyRunFile(t, directory, ".lock")
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	terminal := signedSnapshot(t, active, "run-terminal", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_COMPLETED)
	if _, err = authority.PrepareImport(t.Context(), terminal); !errors.Is(err, daemon.ErrRunAuthorityVerification) {
		t.Fatalf("terminal import = %v", err)
	}
	if _, err = authority.Start(t.Context(), "run-terminal"); !errors.Is(err, daemon.ErrRunAuthorityState) {
		t.Fatalf("terminal run ID reuse = %v", err)
	}
	after, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("stable lock inode/file identity changed")
	}
}

func TestRunAuthorityIdentityPersistsWithoutRenderingSecrets(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "authority")
	first := newRunAuthority(t, directory)
	identityBefore, err := os.ReadFile(filepath.Join(directory, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	second := newRunAuthority(t, directory)
	identityAfter, err := os.ReadFile(filepath.Join(directory, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	if len(identityBefore) != 72 || !bytes.Equal(identityBefore, identityAfter) ||
		bytes.Equal(identityBefore[8:40], identityBefore[40:72]) {
		t.Fatal("persistent authority identity is malformed")
	}
	rendered := fmt.Sprintf("%v %#v %v", first, first, second)
	for _, secret := range []string{fmt.Sprintf("%x", identityBefore[8:40]), fmt.Sprintf("%x", identityBefore[40:72])} {
		if strings.Contains(rendered, secret) {
			t.Fatal("authority string form exposed secret material")
		}
	}
}

func TestRunAuthorityCrashReleasesLockButActiveStateStaysUnsafe(t *testing.T) {
	if os.Getenv("SPICE_AUTHORITY_HELPER") == "1" {
		directory := os.Getenv("SPICE_AUTHORITY_DIRECTORY")
		authority, err := daemon.NewRunAuthority(daemon.RunAuthorityConfig{Directory: directory})
		if err != nil {
			os.Exit(31)
		}
		if _, err = authority.Start(context.Background(), "crashed-run"); err != nil {
			os.Exit(32)
		}
		if err = os.WriteFile(os.Getenv("SPICE_AUTHORITY_READY"), []byte("ready"), 0o600); err != nil {
			os.Exit(33)
		}
		os.Exit(0) // Deliberately skip Close to model process death.
	}
	directory := filepath.Join(authorityTestRoot(t), "authority")
	ready := filepath.Join(authorityTestRoot(t), "ready")
	command := exec.Command(os.Args[0], "-test.run=^TestRunAuthorityCrashReleasesLockButActiveStateStaysUnsafe$")
	command.Env = append(
		os.Environ(),
		"SPICE_AUTHORITY_HELPER=1", "SPICE_AUTHORITY_DIRECTORY="+directory, "SPICE_AUTHORITY_READY="+ready,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("helper failed: %v: %s", err, output)
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatal(err)
	}
	authority := newRunAuthority(t, directory)
	if _, err := authority.Start(t.Context(), "crashed-run"); !errors.Is(err, daemon.ErrRunAuthorityState) {
		t.Fatalf("crashed ACTIVE run reuse = %v", err)
	}
}

func TestRunAuthoritySuspendedOwnerCrashMakesSnapshotImportable(t *testing.T) {
	if os.Getenv("SPICE_AUTHORITY_SUSPEND_HELPER") == "1" {
		authority, err := daemon.NewRunAuthority(daemon.RunAuthorityConfig{Directory: os.Getenv("SPICE_AUTHORITY_DIRECTORY")})
		if err != nil {
			os.Exit(51)
		}
		active, err := authority.Start(context.Background(), "crashed-suspended")
		if err != nil {
			os.Exit(52)
		}
		payload := []byte(`{"version":"spice.agent.snapshot/v1alpha2","run_id":"crashed-suspended"}`)
		snapshot, err := enginev1.NewSnapshotEnvelope(
			context.Background(), active, "crashed-suspended", 1,
			enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED, payload,
		)
		if err != nil {
			os.Exit(53)
		}
		wire, err := proto.Marshal(snapshot)
		if err != nil || os.WriteFile(os.Getenv("SPICE_AUTHORITY_SNAPSHOT"), wire, 0o600) != nil {
			os.Exit(54)
		}
		os.Exit(0) // Process death releases the retained suspended lock.
	}
	directory := filepath.Join(authorityTestRoot(t), "authority")
	snapshotPath := filepath.Join(authorityTestRoot(t), "snapshot.pb")
	command := exec.Command(os.Args[0], "-test.run=^TestRunAuthoritySuspendedOwnerCrashMakesSnapshotImportable$")
	command.Env = append(
		os.Environ(), "SPICE_AUTHORITY_SUSPEND_HELPER=1",
		"SPICE_AUTHORITY_DIRECTORY="+directory, "SPICE_AUTHORITY_SNAPSHOT="+snapshotPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("suspended helper failed: %v: %s", err, output)
	}
	wire, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot enginev1.SnapshotEnvelope
	if err = proto.Unmarshal(wire, &snapshot); err != nil {
		t.Fatal(err)
	}
	authority := newRunAuthority(t, directory)
	prepared, err := authority.PrepareImport(t.Context(), &snapshot)
	if err != nil {
		t.Fatalf("import after suspended owner crash = %v", err)
	}
	if err = prepared.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestRunAuthorityImportCrashBoundariesStayNonRetryable(t *testing.T) {
	if mode := os.Getenv("SPICE_AUTHORITY_IMPORT_HELPER"); mode != "" {
		directory := os.Getenv("SPICE_AUTHORITY_DIRECTORY")
		wire, err := os.ReadFile(os.Getenv("SPICE_AUTHORITY_SNAPSHOT"))
		if err != nil {
			os.Exit(41)
		}
		var snapshot enginev1.SnapshotEnvelope
		if err = proto.Unmarshal(wire, &snapshot); err != nil {
			os.Exit(42)
		}
		authority, err := daemon.NewRunAuthority(daemon.RunAuthorityConfig{Directory: directory})
		if err != nil {
			os.Exit(43)
		}
		transaction, err := authority.PrepareImport(context.Background(), &snapshot)
		if err != nil {
			os.Exit(44)
		}
		if err = transaction.Consume(context.Background()); err != nil {
			os.Exit(45)
		}
		if mode == "activate" {
			if _, err = transaction.Activate(context.Background()); err != nil {
				os.Exit(46)
			}
		}
		os.Exit(0) // Deliberately die at the selected durable boundary.
	}
	for _, mode := range []string{"consume", "activate"} {
		t.Run(mode, func(t *testing.T) {
			directory := filepath.Join(authorityTestRoot(t), "authority")
			authority := newRunAuthority(t, directory)
			runID := "crash-" + mode
			active, err := authority.Start(t.Context(), runID)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := signedSnapshot(t, active, runID, enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
			if err = active.Close(); err != nil {
				t.Fatal(err)
			}
			wire, err := proto.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			snapshotPath := filepath.Join(authorityTestRoot(t), "snapshot.pb")
			if err = os.WriteFile(snapshotPath, wire, 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(os.Args[0], "-test.run=^TestRunAuthorityImportCrashBoundariesStayNonRetryable$")
			command.Env = append(
				os.Environ(), "SPICE_AUTHORITY_IMPORT_HELPER="+mode,
				"SPICE_AUTHORITY_DIRECTORY="+directory, "SPICE_AUTHORITY_SNAPSHOT="+snapshotPath,
			)
			if output, commandErr := command.CombinedOutput(); commandErr != nil {
				t.Fatalf("%s helper failed: %v: %s", mode, commandErr, output)
			}
			if _, err = authority.PrepareImport(t.Context(), snapshot); !errors.Is(err, daemon.ErrRunAuthorityVerification) {
				t.Fatalf("retry after %s crash = %v", mode, err)
			}
			if _, err = authority.Start(t.Context(), runID); !errors.Is(err, daemon.ErrRunAuthorityState) {
				t.Fatalf("reuse after %s crash = %v", mode, err)
			}
		})
	}
}

func TestRunAuthorityCloseRejectsNewWorkAndDrainsLease(t *testing.T) {
	authority := newRunAuthority(t, filepath.Join(authorityTestRoot(t), "authority"))
	active, err := authority.Start(t.Context(), "open-run")
	if err != nil {
		t.Fatal(err)
	}
	if err = authority.Close(); !errors.Is(err, daemon.ErrRunAuthorityBusy) {
		t.Fatalf("close with live run = %v", err)
	}
	if _, err = authority.Start(t.Context(), "new-run"); !errors.Is(err, daemon.ErrRunAuthorityState) {
		t.Fatalf("new work after close = %v", err)
	}
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	if err = authority.Close(); err != nil {
		t.Fatalf("idempotent close = %v", err)
	}
}

func TestRunAuthorityPublicBoundsAndNilSafety(t *testing.T) {
	var nilAuthority *daemon.RunAuthority
	if _, err := nilAuthority.Start(t.Context(), "run"); !errors.Is(err, daemon.ErrRunAuthorityUnavailable) {
		t.Fatalf("nil start = %v", err)
	}
	if _, err := daemon.NewRunAuthority(daemon.RunAuthorityConfig{Directory: "relative"}); !errors.Is(err, daemon.ErrRunAuthorityState) && !errors.Is(err, daemon.ErrRunAuthorityUnavailable) {
		t.Fatalf("relative directory = %v", err)
	}
	authority := newRunAuthority(t, filepath.Join(authorityTestRoot(t), "authority"))
	for _, runID := range []string{"", " run", strings.Repeat("x", 129), "run\n"} {
		if _, err := authority.Start(t.Context(), runID); !errors.Is(err, daemon.ErrRunAuthorityState) {
			t.Fatalf("invalid run ID %q = %v", runID, err)
		}
	}
}

func newRunAuthority(t *testing.T, directory string) *daemon.RunAuthority {
	t.Helper()
	authority, err := daemon.NewRunAuthority(daemon.RunAuthorityConfig{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := authority.Close(); closeErr != nil {
			t.Errorf("close run authority: %v", closeErr)
		}
	})
	return authority
}

func signedSnapshot(
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

func onlyRunFile(t *testing.T, directory, suffix string) string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var matches []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "run-") && strings.HasSuffix(entry.Name(), suffix) {
			matches = append(matches, filepath.Join(directory, entry.Name()))
		}
	}
	if len(matches) != 1 {
		t.Fatalf("%s files = %v", suffix, matches)
	}
	return matches[0]
}

func TestRunAuthoritySnapshotClaimDefensiveCopy(t *testing.T) {
	authority := newRunAuthority(t, filepath.Join(authorityTestRoot(t), "authority"))
	active, err := authority.Start(t.Context(), "copy-run")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := signedSnapshot(t, active, "copy-run", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	scope := slices.Clone(snapshot.GetAuthority().GetScopeId())
	mac := slices.Clone(snapshot.GetAuthority().GetHmacSha256())
	snapshot.Authority.ScopeId[0] ^= 1
	snapshot.Authority.HmacSha256[0] ^= 1
	if bytes.Equal(scope, snapshot.GetAuthority().GetScopeId()) || bytes.Equal(mac, snapshot.GetAuthority().GetHmacSha256()) {
		t.Fatal("test failed to mutate caller-owned claim")
	}
	if _, err = authority.PrepareImport(t.Context(), snapshot); !errors.Is(err, daemon.ErrRunAuthorityVerification) {
		t.Fatalf("mutated claim = %v", err)
	}
}
