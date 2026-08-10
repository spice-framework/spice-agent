package telemetry

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/tool"
)

const (
	// MaximumBatchRecords bounds one synchronous exporter call.
	MaximumBatchRecords = 256
	// MaximumBatchBytes bounds deterministic in-memory batch accounting.
	MaximumBatchBytes = 256 << 10
	// MaximumCorrelations bounds process-local open run and tool spans.
	MaximumCorrelations = 4096
	correlationKeyBytes = 32
)

// Config owns all bounded telemetry behavior. A zero CorrelationKey requests a
// fresh process-local random key; the material is never returned or exported.
type Config struct {
	MailboxCapacity int
	BatchRecords    int
	BatchBytes      int
	MaxCorrelations int
	FlushInterval   time.Duration
	ExportTimeout   time.Duration
	ShutdownTimeout time.Duration
	CorrelationKey  CorrelationKey
	ReadinessImpact bool
}

// DefaultConfig returns conservative best-effort defaults.
func DefaultConfig() Config {
	return Config{
		MailboxCapacity: 1024,
		BatchRecords:    128,
		BatchBytes:      128 << 10,
		MaxCorrelations: 4096,
		FlushInterval:   100 * time.Millisecond,
		ExportTimeout:   250 * time.Millisecond,
		ShutdownTimeout: 250 * time.Millisecond,
	}
}

// Validate rejects unbounded or ambiguous configuration.
func (config Config) Validate() error {
	if config.MailboxCapacity < 1 || config.MailboxCapacity > 65536 {
		return errors.New("telemetry mailbox capacity must be between 1 and 65536")
	}
	if config.BatchRecords < 4 || config.BatchRecords > MaximumBatchRecords {
		return fmt.Errorf("telemetry batch records must be between 4 and %d", MaximumBatchRecords)
	}
	if config.BatchBytes < 4*conservativeRecordBytes || config.BatchBytes > MaximumBatchBytes {
		return fmt.Errorf("telemetry batch bytes must be between %d and %d", 4*conservativeRecordBytes, MaximumBatchBytes)
	}
	if config.MaxCorrelations < 1 || config.MaxCorrelations > MaximumCorrelations {
		return fmt.Errorf("telemetry correlations must be between 1 and %d", MaximumCorrelations)
	}
	if config.FlushInterval <= 0 || config.FlushInterval > time.Minute {
		return errors.New("telemetry flush interval must be between zero and one minute")
	}
	if config.ExportTimeout <= 0 || config.ExportTimeout > 30*time.Second {
		return errors.New("telemetry export timeout must be between zero and 30 seconds")
	}
	if config.ShutdownTimeout <= 0 || config.ShutdownTimeout > 30*time.Second {
		return errors.New("telemetry shutdown timeout must be between zero and 30 seconds")
	}
	return config.CorrelationKey.validateOptional()
}

// CorrelationKey is immutable HMAC material. It deliberately has no material
// accessor so telemetry records, logs, health, and errors cannot reveal it.
type CorrelationKey struct {
	material [correlationKeyBytes]byte
	set      bool
}

// Format redacts key material under every fmt verb, including when Config is
// printed recursively with diagnostic formatting.
func (key CorrelationKey) Format(state fmt.State, _ rune) {
	value := "[PROCESS-LOCAL]"
	if key.set {
		value = "[REDACTED]"
	}
	_, _ = state.Write([]byte(value))
}

// NewCorrelationKey defensively copies exact 256-bit application-owned key
// material. Callers should treat the input as a redacted secret.
func NewCorrelationKey(material []byte) (CorrelationKey, error) {
	if len(material) != correlationKeyBytes {
		return CorrelationKey{}, fmt.Errorf("telemetry correlation key must contain %d bytes", correlationKeyBytes)
	}
	var result CorrelationKey
	copy(result.material[:], material)
	result.set = true
	if err := result.validateOptional(); err != nil {
		return CorrelationKey{}, err
	}
	return result, nil
}

func (key CorrelationKey) validateOptional() error {
	if !key.set {
		if subtle.ConstantTimeCompare(key.material[:], make([]byte, correlationKeyBytes)) != 1 {
			return errors.New("telemetry correlation key is invalid")
		}
		return nil
	}
	if subtle.ConstantTimeCompare(key.material[:], make([]byte, correlationKeyBytes)) == 1 {
		return errors.New("telemetry correlation key must not be all zero")
	}
	return nil
}

// Exporter synchronously accepts one bounded immutable batch. Implementations
// must honor context cancellation. Export and Shutdown are called serially.
type Exporter interface {
	Export(context.Context, Batch) error
	Shutdown(context.Context) error
}

// MetricName is a closed metric identity.
type MetricName string

const (
	MetricEventsObserved  MetricName = "agent.events.observed"
	MetricEventsDropped   MetricName = "agent.events.dropped"
	MetricSpansIncomplete MetricName = "agent.spans.incomplete"
)

// SpanKind is a closed completed-span identity.
type SpanKind string

const (
	SpanRun  SpanKind = "agent.run"
	SpanTool SpanKind = "agent.tool"
)

// TerminalStatus is a fixed secret-safe lifecycle status.
type TerminalStatus string

const (
	StatusCompleted TerminalStatus = "completed"
	StatusFailed    TerminalStatus = "failed"
	StatusCancelled TerminalStatus = "cancelled"
)

// CapabilitySet is a closed bitset of the public Spice tool capability enums.
type CapabilitySet uint16

const (
	CapabilityFilesystemRead CapabilitySet = 1 << iota
	CapabilityFilesystemWrite
	CapabilityProcessExecute
	CapabilityNetworkAccess
	CapabilitySecretsRead
	CapabilityEnvironmentRead
	CapabilityEnvironmentWrite
)

// MetricRecord is one immutable counter observation. It cannot contain event
// payload data, run IDs, call IDs, paths, model text, or error strings.
type MetricRecord struct {
	name         MetricName
	eventKind    event.Kind
	count        uint64
	effect       tool.Effect
	replaySafety tool.ReplaySafety
	capabilities CapabilitySet
	outcome      tool.ExecutionState
	retry        tool.RetryDisposition
}

func newMetricRecord(name MetricName, kind event.Kind, count uint64) MetricRecord {
	return MetricRecord{name: name, eventKind: kind, count: count}
}

// Name returns the closed metric identity.
func (record MetricRecord) Name() MetricName { return record.name }

// EventKind returns the closed Agent event kind, or empty for a drop sample.
func (record MetricRecord) EventKind() event.Kind { return record.eventKind }

// Count returns the positive counter delta.
func (record MetricRecord) Count() uint64 { return record.count }

// Effect returns typed tool effect metadata when available.
func (record MetricRecord) Effect() tool.Effect { return record.effect }

// ReplaySafety returns typed tool replay metadata when available.
func (record MetricRecord) ReplaySafety() tool.ReplaySafety { return record.replaySafety }

// Capabilities returns the closed capability bitset.
func (record MetricRecord) Capabilities() CapabilitySet { return record.capabilities }

// ExecutionState returns typed terminal tool state when available.
func (record MetricRecord) ExecutionState() tool.ExecutionState { return record.outcome }

// RetryDisposition returns typed terminal tool retry advice when available.
func (record MetricRecord) RetryDisposition() tool.RetryDisposition { return record.retry }

// SpanRecord is one immutable complete run or tool span. Correlation values are
// process-local HMAC pseudonyms rather than source identities.
type SpanRecord struct {
	kind          SpanKind
	correlation   string
	run           string
	startedAt     time.Time
	finishedAt    time.Time
	startSequence uint64
	endSequence   uint64
	status        TerminalStatus
	effect        tool.Effect
	replaySafety  tool.ReplaySafety
	capabilities  CapabilitySet
	outcome       tool.ExecutionState
	retry         tool.RetryDisposition
}

func (record SpanRecord) clone() SpanRecord { return record }

func (record SpanRecord) Kind() SpanKind                          { return record.kind }
func (record SpanRecord) Correlation() string                     { return record.correlation }
func (record SpanRecord) RunCorrelation() string                  { return record.run }
func (record SpanRecord) StartedAt() time.Time                    { return record.startedAt }
func (record SpanRecord) FinishedAt() time.Time                   { return record.finishedAt }
func (record SpanRecord) StartSequence() uint64                   { return record.startSequence }
func (record SpanRecord) EndSequence() uint64                     { return record.endSequence }
func (record SpanRecord) Status() TerminalStatus                  { return record.status }
func (record SpanRecord) Effect() tool.Effect                     { return record.effect }
func (record SpanRecord) ReplaySafety() tool.ReplaySafety         { return record.replaySafety }
func (record SpanRecord) Capabilities() CapabilitySet             { return record.capabilities }
func (record SpanRecord) ExecutionState() tool.ExecutionState     { return record.outcome }
func (record SpanRecord) RetryDisposition() tool.RetryDisposition { return record.retry }

// LogRecord is a fixed lifecycle summary without a free-form body.
type LogRecord struct {
	kind        event.Kind
	correlation string
	sequence    uint64
	at          time.Time
}

func (record LogRecord) Kind() event.Kind    { return record.kind }
func (record LogRecord) Correlation() string { return record.correlation }
func (record LogRecord) Sequence() uint64    { return record.sequence }
func (record LogRecord) At() time.Time       { return record.at }

// Batch is an immutable bounded exporter snapshot.
type Batch struct {
	metrics []MetricRecord
	spans   []SpanRecord
	logs    []LogRecord
}

func newBatch(metrics []MetricRecord, spans []SpanRecord, logs []LogRecord) Batch {
	return Batch{
		metrics: slices.Clone(metrics),
		spans:   slices.Clone(spans),
		logs:    slices.Clone(logs),
	}
}

func (batch Batch) clone() Batch { return newBatch(batch.metrics, batch.spans, batch.logs) }

func (batch Batch) Metrics() []MetricRecord { return slices.Clone(batch.metrics) }
func (batch Batch) Spans() []SpanRecord     { return slices.Clone(batch.spans) }
func (batch Batch) Logs() []LogRecord       { return slices.Clone(batch.logs) }
func (batch Batch) Records() int            { return len(batch.metrics) + len(batch.spans) + len(batch.logs) }
func (batch Batch) SizeBytes() int          { return batch.Records() * conservativeRecordBytes }

// Snapshot is an immutable processor health and accounting snapshot.
type Snapshot struct {
	processed       uint64
	exported        uint64
	dropped         uint64
	exportFailures  uint64
	decodeFailures  uint64
	orphanTerminals uint64
	incompleteSpans uint64
	evictions       uint64
	open            int
	closed          bool
}

func (snapshot Snapshot) Processed() uint64       { return snapshot.processed }
func (snapshot Snapshot) Exported() uint64        { return snapshot.exported }
func (snapshot Snapshot) Dropped() uint64         { return snapshot.dropped }
func (snapshot Snapshot) ExportFailures() uint64  { return snapshot.exportFailures }
func (snapshot Snapshot) DecodeFailures() uint64  { return snapshot.decodeFailures }
func (snapshot Snapshot) OrphanTerminals() uint64 { return snapshot.orphanTerminals }
func (snapshot Snapshot) IncompleteSpans() uint64 { return snapshot.incompleteSpans }
func (snapshot Snapshot) Evictions() uint64       { return snapshot.evictions }
func (snapshot Snapshot) OpenCorrelations() int   { return snapshot.open }
func (snapshot Snapshot) Closed() bool            { return snapshot.closed }

func capabilitySet(values []tool.Capability) CapabilitySet {
	var result CapabilitySet
	for _, value := range values {
		switch value {
		case tool.CapabilityFilesystemRead:
			result |= CapabilityFilesystemRead
		case tool.CapabilityFilesystemWrite:
			result |= CapabilityFilesystemWrite
		case tool.CapabilityProcessExecute:
			result |= CapabilityProcessExecute
		case tool.CapabilityNetworkAccess:
			result |= CapabilityNetworkAccess
		case tool.CapabilitySecretsRead:
			result |= CapabilitySecretsRead
		case tool.CapabilityEnvironmentRead:
			result |= CapabilityEnvironmentRead
		case tool.CapabilityEnvironmentWrite:
			result |= CapabilityEnvironmentWrite
		}
	}
	return result
}
