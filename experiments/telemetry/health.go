package telemetry

import "github.com/spice-framework/spice-agent/daemon"

// HealthSource is an optional passive daemon contribution. Readiness impact is
// disabled by default and cannot contain exporter errors or other free text.
type HealthSource struct {
	processor *Processor
	enabled   bool
}

// NewHealthSource constructs an in-memory-only health adapter.
func NewHealthSource(config Config, processor *Processor) *HealthSource {
	return &HealthSource{processor: processor, enabled: config.ReadinessImpact}
}

// HealthContribution returns only fixed daemon reason codes.
func (source *HealthSource) HealthContribution() daemon.HealthContribution {
	if source == nil || !source.enabled {
		return daemon.HealthContribution{}
	}
	snapshot := source.processor.Snapshot()
	if snapshot.Closed() && snapshot.ExportFailures() != 0 {
		contribution, _ := daemon.NewHealthContribution([]daemon.HealthReasonCode{
			daemon.HealthReasonDependencyUnavailable,
		})
		return contribution
	}
	if snapshot.ExportFailures() != 0 || snapshot.DecodeFailures() != 0 ||
		snapshot.Dropped() != 0 || snapshot.Evictions() != 0 ||
		snapshot.OrphanTerminals() != 0 || snapshot.IncompleteSpans() != 0 {
		contribution, _ := daemon.NewHealthContribution([]daemon.HealthReasonCode{
			daemon.HealthReasonDependencyDegraded,
		})
		return contribution
	}
	return daemon.HealthContribution{}
}

var _ daemon.HealthSource = (*HealthSource)(nil)
