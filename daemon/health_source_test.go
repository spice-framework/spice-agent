package daemon

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spice-framework/spice-agent/client"
)

type healthSourceFunc func() HealthContribution

func (source healthSourceFunc) HealthContribution() HealthContribution { return source() }

func mustHealthContribution(t *testing.T, reasons ...HealthReasonCode) HealthContribution {
	t.Helper()
	contribution, err := NewHealthContribution(reasons)
	if err != nil {
		t.Fatal(err)
	}
	return contribution
}

func TestHealthContributionIsBoundedCanonicalAndImmutable(t *testing.T) {
	t.Parallel()
	input := []HealthReasonCode{
		HealthReasonDependencyUnavailable,
		HealthReasonDependencyDegraded,
		HealthReasonDependencyUnavailable,
	}
	contribution, err := NewHealthContribution(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = HealthReasonDependencyRecovering
	want := []HealthReasonCode{
		HealthReasonDependencyDegraded,
		HealthReasonDependencyUnavailable,
	}
	if got := contribution.Reasons(); !slices.Equal(got, want) {
		t.Fatalf("reasons = %v, want %v", got, want)
	}
	returned := contribution.Reasons()
	returned[0] = HealthReasonDependencyRecovering
	if got := contribution.Reasons(); !slices.Equal(got, want) {
		t.Fatalf("reasons changed through returned slice: %v", got)
	}
	if err = contribution.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err = (HealthContribution{}).Validate(); err != nil {
		t.Fatalf("zero contribution: %v", err)
	}

	tooMany := make([]HealthReasonCode, MaximumHealthContributionReasons+1)
	for index := range tooMany {
		tooMany[index] = HealthReasonDependencyDegraded
	}
	if _, err = NewHealthContribution(tooMany); err == nil {
		t.Fatal("oversized contribution succeeded")
	}
	if _, err = NewHealthContribution([]HealthReasonCode{"secret-shaped-but-unsupported"}); err == nil {
		t.Fatal("unsupported reason succeeded")
	}
	forged := HealthContribution{reasons: []HealthReasonCode{
		HealthReasonDependencyUnavailable,
		HealthReasonDependencyDegraded,
	}}
	if err = forged.Validate(); err == nil {
		t.Fatal("noncanonical contribution validated")
	}
}

func TestRunHostHealthSourcesAreBoundedAndRejectNil(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	tests := map[string][]HealthSource{
		"nil": {nil},
		"too many": func() []HealthSource {
			values := make([]HealthSource, MaximumHealthSources+1)
			for index := range values {
				values[index] = healthSourceFunc(func() HealthContribution { return HealthContribution{} })
			}
			return values
		}(),
	}
	for name, sources := range tests {
		configuration := fixture.config
		configuration.HealthSources = sources
		if host, err := newRunHost(configuration, fixture.authority); err == nil || host != nil {
			t.Fatalf("%s: newRunHost() = (%p, %v), want nil, error", name, host, err)
		}
	}

	original := &staticHealthSource{contribution: mustHealthContribution(
		t, HealthReasonDependencyDegraded,
	)}
	replacement := &staticHealthSource{contribution: mustHealthContribution(
		t, HealthReasonDependencyUnavailable,
	)}
	sources := []HealthSource{original}
	configuration := fixture.config
	configuration.HealthSources = sources
	host, err := newRunHost(configuration, fixture.authority)
	if err != nil {
		t.Fatal(err)
	}
	sources[0] = replacement
	if len(host.healthSources) != 1 || host.healthSources[0] != original {
		t.Fatalf("configured health sources changed through input slice: %#v", host.healthSources)
	}
	host.cancelRoot(ErrRunHostClosed)
}

type staticHealthSource struct{ contribution HealthContribution }

func (source *staticHealthSource) HealthContribution() HealthContribution {
	return source.contribution
}

func TestRunHostHealthAggregatesSourcesOutsideLock(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	var sampled atomic.Uint32
	source := healthSourceFunc(func() HealthContribution {
		if !fixture.host.mu.TryLock() {
			t.Fatal("health source was called while RunHost.mu was held")
		}
		fixture.host.mu.Unlock()
		sampled.Add(1)
		return mustHealthContribution(
			t,
			HealthReasonDependencyUnavailable,
			HealthReasonDependencyRecovering,
			HealthReasonDependencyUnavailable,
		)
	})
	fixture.host.healthSources = []HealthSource{source, source}
	fixture.host.degrade(degradedAuthorityMissing)

	description, err := fixture.host.Describe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		string(HealthReasonDependencyRecovering),
		string(HealthReasonDependencyUnavailable),
		degradedAuthorityMissing,
	}
	if health := description.Health(); health.State() != client.HealthDegraded ||
		!slices.Equal(health.Reasons(), want) {
		t.Fatalf("described health = state %q reasons %v, want %v", health.State(), health.Reasons(), want)
	}
	if sampled.Load() != 2 {
		t.Fatalf("source samples = %d, want 2", sampled.Load())
	}

	health, err := fixture.host.Health(t.Context(), fixture.session)
	if err != nil || !slices.Equal(health.Reasons(), want) {
		t.Fatalf("session health = %#v, %v", health, err)
	}
	if sampled.Load() != 4 {
		t.Fatalf("source samples after health = %d, want 4", sampled.Load())
	}
}

func TestRunHostHealthContainsSourcePanicAndInvalidContribution(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	const secret = "health-source-secret-must-not-escape"
	fixture.host.healthSources = []HealthSource{
		healthSourceFunc(func() HealthContribution { panic(secret) }),
		healthSourceFunc(func() HealthContribution {
			return HealthContribution{reasons: []HealthReasonCode{HealthReasonCode(secret)}}
		}),
	}
	health, err := fixture.host.Health(t.Context(), fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{string(HealthReasonDependencyUnavailable)}
	if health.State() != client.HealthDegraded || !slices.Equal(health.Reasons(), want) {
		t.Fatalf("health = state %q reasons %v, want %v", health.State(), health.Reasons(), want)
	}
	if strings.Contains(strings.Join(health.Reasons(), " "), secret) {
		t.Fatal("source-controlled secret escaped through health")
	}
}

func TestRunHostStoppingPrecedesAndSkipsHealthSources(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	var sampled atomic.Uint32
	fixture.host.healthSources = []HealthSource{healthSourceFunc(func() HealthContribution {
		sampled.Add(1)
		return mustHealthContribution(t, HealthReasonDependencyUnavailable)
	})}
	fixture.host.mu.Lock()
	fixture.host.closing = true
	fixture.host.mu.Unlock()
	health, err := fixture.host.Health(t.Context(), fixture.session)
	fixture.host.mu.Lock()
	fixture.host.closing = false
	fixture.host.mu.Unlock()
	if err != nil || health.State() != client.HealthStopping || len(health.Reasons()) != 0 {
		t.Fatalf("stopping health = %#v, %v", health, err)
	}
	if sampled.Load() != 0 {
		t.Fatalf("stopping health sampled %d sources", sampled.Load())
	}
}

func TestRunHostPrivateDegradationRejectsArbitraryText(t *testing.T) {
	t.Parallel()
	fixture := newRunHostFixture(t, immediateHostProvider{}, 1, 2)
	fixture.host.degrade("secret arbitrary dependency error")
	health, err := fixture.host.Health(t.Context(), fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	if got := health.Reasons(); !slices.Equal(got, []string{degradedLifecycleCleanup}) {
		t.Fatalf("health reasons = %v, want fixed fallback", got)
	}
}
