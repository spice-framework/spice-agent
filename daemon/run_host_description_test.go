package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/spice-framework/spice-agent/client"
)

func TestRunHostDescriptionIsValidatedAndImmutable(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	health, err := client.NewHealth(client.HealthReady, nil, 0, fixture.config.Limits)
	if err != nil {
		t.Fatal(err)
	}
	description, err := NewRunHostDescription(fixture.config.Definitions, health)
	if err != nil {
		t.Fatal(err)
	}
	if err = description.Validate(); err != nil {
		t.Fatalf("validate description: %v", err)
	}
	if got := description.Health(); got.State() != client.HealthReady || got.ActiveRuns() != 0 {
		t.Fatalf("description health = %#v", got)
	}

	sourceDefinitions := fixture.config.Definitions.Definitions()
	sourceDefinitions[0].id = "changed-source"
	first := description.Definitions()
	if first.Revision() != fixture.config.Definitions.Revision() {
		t.Fatalf("description revision = %q, want %q", first.Revision(), fixture.config.Definitions.Revision())
	}
	first.values[0].id = "changed-result"
	second := description.Definitions()
	if got := second.Definitions()[0].ID(); got != fixture.definition.ID() {
		t.Fatalf("description definition changed through returned set: %q", got)
	}
}

func TestRunHostDescriptionRejectsInvalidMembers(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	health, err := client.NewHealth(client.HealthReady, nil, 0, fixture.config.Limits)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		definitions DefinitionSet
		health      client.Health
	}{
		{name: "zero definitions", definitions: DefinitionSet{}, health: health},
		{name: "zero health", definitions: fixture.config.Definitions, health: client.Health{}},
		{
			name: "forged revision",
			definitions: DefinitionSet{
				revision: "forged",
				values:   fixture.config.Definitions.Definitions(),
			},
			health: health,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, innerErr := NewRunHostDescription(test.definitions, test.health); innerErr == nil {
				t.Fatal("invalid description succeeded")
			}
		})
	}
	if err = (RunHostDescription{}).Validate(); err == nil {
		t.Fatal("zero description validated")
	}
}

func TestRunHostDescribeRequiresNoSessionAndHonorsCancellation(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	fixture.sessions.Close()

	description, err := fixture.host.Describe(t.Context())
	if err != nil {
		t.Fatalf("describe after session store close: %v", err)
	}
	if err = description.Validate(); err != nil {
		t.Fatalf("validate described host: %v", err)
	}
	if got := description.Definitions().Revision(); got != fixture.config.Definitions.Revision() {
		t.Fatalf("described revision = %q, want %q", got, fixture.config.Definitions.Revision())
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = fixture.host.Describe(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("describe canceled context = %v", err)
	}
	//nolint:staticcheck // Boundary coverage intentionally passes nil to verify defensive context handling.
	if _, err = fixture.host.Describe(nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("describe nil context = %v", err)
	}
	var nilHost *RunHost
	if _, err = nilHost.Describe(t.Context()); !errors.Is(err, ErrRunHostClosed) {
		t.Fatalf("describe nil host = %v", err)
	}
}

func TestRunHostDescribeAndHealthShareCanonicalSnapshot(t *testing.T) {
	t.Parallel()
	provider := &sequenceHostProvider{firstStarted: make(chan struct{})}
	fixture := newRunHostFixture(t, provider, 2, 4)
	started, err := fixture.host.Start(
		t.Context(), fixture.session,
		hostStartRequest(t, "description-start", fixture.definition, "description-input"),
	)
	if err != nil {
		t.Fatal(err)
	}
	<-provider.firstStarted
	fixture.host.degrade(degradedTerminalSnapshot)
	fixture.host.degrade(degradedAuthorityMissing)

	description, err := fixture.host.Describe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	health, err := fixture.host.Health(t.Context(), fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	describedHealth := description.Health()
	if describedHealth.State() != client.HealthDegraded || describedHealth.ActiveRuns() != 1 {
		t.Fatalf("described health = %#v", describedHealth)
	}
	wantReasons := []string{degradedAuthorityMissing, degradedTerminalSnapshot}
	if got := describedHealth.Reasons(); !equalDescriptionStrings(got, wantReasons) {
		t.Fatalf("described reasons = %v, want %v", got, wantReasons)
	}
	if health.State() != describedHealth.State() || health.ActiveRuns() != describedHealth.ActiveRuns() ||
		!equalDescriptionStrings(health.Reasons(), describedHealth.Reasons()) {
		t.Fatalf("health %#v does not reuse description snapshot %#v", health, describedHealth)
	}

	cancelRequest, _ := client.NewCancelRequest(
		started.Run(), mustBoundaryOperation(t, "description-cleanup"), "cleanup",
	)
	if _, err = fixture.host.Cancel(t.Context(), fixture.session, cancelRequest); err != nil {
		t.Fatal(err)
	}
	<-fixture.authority.issued
}

func TestRunHostDescribeIsDeterministicUnderConcurrentReads(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	fixture.host.degrade(degradedTerminalSnapshot)
	fixture.host.degrade(degradedAuthorityMissing)

	const readers = 32
	const iterations = 64
	errorsFound := make(chan error, readers)
	var wait sync.WaitGroup
	for range readers {
		wait.Go(func() {
			for range iterations {
				description, err := fixture.host.Describe(t.Context())
				if err != nil {
					errorsFound <- err
					return
				}
				if description.Definitions().Revision() != fixture.config.Definitions.Revision() ||
					!equalDescriptionStrings(description.Health().Reasons(), []string{
						degradedAuthorityMissing, degradedTerminalSnapshot,
					}) {
					errorsFound <- errors.New("concurrent description was not canonical")
					return
				}
			}
		})
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
}

func equalDescriptionStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
