package telemetry

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/tool"
	"github.com/spice-framework/spice/lifecycle"
)

const conservativeRecordBytes = 512

type correlationStart struct {
	kind         SpanKind
	correlation  string
	run          string
	nameDigest   string
	at           time.Time
	sequence     uint64
	ordinal      uint64
	effect       tool.Effect
	replaySafety tool.ReplaySafety
	capabilities CapabilitySet
}

type processorStats struct {
	processed       uint64
	exported        uint64
	exportFailures  uint64
	decodeFailures  uint64
	orphanTerminals uint64
	incompleteSpans uint64
	evictions       uint64
	closed          bool
}

// Processor transforms one Agent-owned best-effort mailbox into bounded safe
// records. It owns exactly one consumer goroutine and invokes Export serially.
type Processor struct {
	config   Config
	mailbox  *event.BestEffortObserver
	exporter Exporter
	key      [correlationKeyBytes]byte

	mu           sync.Mutex
	stats        processorStats
	correlations map[string]correlationStart
	nextOrdinal  uint64

	consumerDone chan struct{}
	closeDone    chan struct{}
	closeOnce    sync.Once
}

// NewMailbox constructs the only queue used by this extension.
func NewMailbox(config Config) (*event.BestEffortObserver, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return event.NewBestEffortObserver(config.MailboxCapacity)
}

// NewProcessor starts the single mailbox consumer and returns its generated
// Spice cleanup. Exporter errors are recorded but never fail an Agent run.
func NewProcessor(
	config Config,
	mailbox *event.BestEffortObserver,
	exporter Exporter,
) (*Processor, lifecycle.Cleanup, error) {
	if err := config.Validate(); err != nil {
		return nil, nil, err
	}
	if mailbox == nil {
		return nil, nil, errors.New("telemetry processor requires a mailbox")
	}
	if exporter == nil {
		return nil, nil, errors.New("telemetry processor requires an exporter")
	}
	key := config.CorrelationKey.material
	if !config.CorrelationKey.set {
		if _, err := rand.Read(key[:]); err != nil {
			return nil, nil, errors.New("telemetry correlation key generation failed")
		}
	}
	processor := &Processor{
		config: config, mailbox: mailbox, exporter: exporter, key: key,
		correlations: make(map[string]correlationStart),
		consumerDone: make(chan struct{}), closeDone: make(chan struct{}),
	}
	go processor.consume()
	return processor, processor.Close, nil
}

// Snapshot returns exact in-process accounting. Dropped reads the authoritative
// mailbox counter directly, including drops since the most recent flush.
func (processor *Processor) Snapshot() Snapshot {
	if processor == nil {
		return Snapshot{}
	}
	processor.mu.Lock()
	defer processor.mu.Unlock()
	dropped := uint64(0)
	if processor.mailbox != nil {
		dropped = processor.mailbox.Dropped()
	}
	return Snapshot{
		processed: processor.stats.processed, exported: processor.stats.exported,
		dropped: dropped, exportFailures: processor.stats.exportFailures,
		decodeFailures:  processor.stats.decodeFailures,
		orphanTerminals: processor.stats.orphanTerminals,
		incompleteSpans: processor.stats.incompleteSpans,
		evictions:       processor.stats.evictions, open: len(processor.correlations),
		closed: processor.stats.closed,
	}
}

// Close closes the mailbox after application producers have stopped, drains
// every accepted envelope, attempts one final export, and shuts down the
// exporter. A non-cooperative exporter can consume the caller deadline; that
// trusted-code limit is reported without exposing its error text.
func (processor *Processor) Close(ctx context.Context) error {
	if processor == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("telemetry cleanup context must not be nil")
	}
	processor.closeOnce.Do(func() {
		processor.mailbox.Close()
		go processor.finish()
	})
	select {
	case <-processor.closeDone:
		return nil
	case <-ctx.Done():
		return errors.New("telemetry final drain did not complete before cancellation")
	}
}

func (processor *Processor) finish() {
	<-processor.consumerDone
	ctx, cancel := context.WithTimeout(context.Background(), processor.config.ShutdownTimeout)
	defer cancel()
	if safeShutdown(ctx, processor.exporter) != nil {
		processor.recordExportFailure()
	}
	processor.mu.Lock()
	processor.stats.closed = true
	processor.mu.Unlock()
	close(processor.closeDone)
}

func (processor *Processor) consume() {
	defer close(processor.consumerDone)
	ticker := time.NewTicker(processor.config.FlushInterval)
	defer ticker.Stop()
	var metrics []MetricRecord
	var spans []SpanRecord
	var logs []LogRecord
	bufferBytes := 0
	lastDropped := uint64(0)

	exportCurrent := func() {
		if len(metrics)+len(spans)+len(logs) == 0 {
			return
		}
		batch := newBatch(metrics, spans, logs)
		ctx, cancel := context.WithTimeout(context.Background(), processor.config.ExportTimeout)
		err := safeExport(ctx, processor.exporter, batch)
		cancel()
		processor.mu.Lock()
		if err != nil {
			processor.stats.exportFailures++
		} else {
			processor.stats.exported += uint64(batch.Records())
		}
		processor.mu.Unlock()
		metrics = nil
		spans = nil
		logs = nil
		bufferBytes = 0
	}
	appendMetric := func(metric MetricRecord) {
		if len(metrics)+len(spans)+len(logs)+1 > processor.config.BatchRecords ||
			bufferBytes+conservativeRecordBytes > processor.config.BatchBytes {
			exportCurrent()
		}
		metrics = append(metrics, metric)
		bufferBytes += conservativeRecordBytes
	}
	flush := func() {
		currentDropped := processor.mailbox.Dropped()
		if currentDropped > lastDropped {
			appendMetric(newMetricRecord(MetricEventsDropped, "", currentDropped-lastDropped))
			lastDropped = currentDropped
		}
		exportCurrent()
	}

	for {
		select {
		case envelope, open := <-processor.mailbox.Events():
			if !open {
				incomplete := processor.abandonCorrelations()
				if incomplete != 0 {
					appendMetric(newMetricRecord(MetricSpansIncomplete, "", incomplete))
				}
				flush()
				return
			}
			metric, span, hasSpan, log := processor.translate(envelope)
			additional := 2
			if hasSpan {
				additional++
			}
			if len(metrics)+len(spans)+len(logs)+additional > processor.config.BatchRecords ||
				bufferBytes+additional*conservativeRecordBytes > processor.config.BatchBytes {
				flush()
			}
			metrics = append(metrics, metric)
			logs = append(logs, log)
			if hasSpan {
				spans = append(spans, span)
			}
			bufferBytes += additional * conservativeRecordBytes
			if len(metrics)+len(spans)+len(logs) >= processor.config.BatchRecords ||
				bufferBytes >= processor.config.BatchBytes {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (processor *Processor) abandonCorrelations() uint64 {
	processor.mu.Lock()
	defer processor.mu.Unlock()
	count := uint64(len(processor.correlations))
	processor.stats.incompleteSpans += count
	clear(processor.correlations)
	return count
}

func (processor *Processor) translate(envelope event.Envelope) (MetricRecord, SpanRecord, bool, LogRecord) {
	run := processor.digest("run", envelope.RunID())
	metric := newMetricRecord(MetricEventsObserved, envelope.Kind(), 1)
	log := LogRecord{kind: envelope.Kind(), correlation: run, sequence: envelope.Sequence(), at: envelope.At()}
	processor.mu.Lock()
	defer processor.mu.Unlock()
	processor.stats.processed++

	switch envelope.Kind() {
	case event.RunStarted:
		processor.putCorrelation("run:"+run, correlationStart{
			kind: SpanRun, correlation: run, run: run, at: envelope.At(), sequence: envelope.Sequence(),
		})
	case event.RunCompleted, event.RunFailed, event.RunCancelled:
		start, found := processor.takeCorrelation("run:" + run)
		if !found {
			processor.stats.orphanTerminals++
			return metric, SpanRecord{}, false, log
		}
		if !terminalAfter(start, envelope) {
			processor.stats.decodeFailures++
			return metric, SpanRecord{}, false, log
		}
		return metric, completeSpan(start, envelope, runStatus(envelope.Kind()), "", ""), true, log
	case event.ToolStarted:
		occurrence, err := agent.DecodeToolStartedOccurrence(envelope.Data())
		if err != nil {
			processor.stats.decodeFailures++
			return metric, SpanRecord{}, false, log
		}
		call := processor.digest("call", envelope.RunID()+"\x00"+string(occurrence.CallID()))
		nameDigest := processor.digest("tool-name", occurrence.Name())
		metric.effect = occurrence.Effect()
		metric.replaySafety = occurrence.ReplaySafety()
		metric.capabilities = capabilitySet(occurrence.Capabilities())
		processor.putCorrelation("tool:"+run+":"+call, correlationStart{
			kind: SpanTool, correlation: call, run: run, nameDigest: nameDigest,
			at: envelope.At(), sequence: envelope.Sequence(), effect: occurrence.Effect(),
			replaySafety: occurrence.ReplaySafety(), capabilities: capabilitySet(occurrence.Capabilities()),
		})
	case event.ToolCompleted, event.ToolFailed:
		occurrence, err := agent.DecodeToolTerminalOccurrence(envelope.Kind(), envelope.Data())
		if err != nil {
			processor.stats.decodeFailures++
			return metric, SpanRecord{}, false, log
		}
		metric.outcome = occurrence.ExecutionState()
		metric.retry = occurrence.RetryDisposition()
		call := processor.digest("call", envelope.RunID()+"\x00"+string(occurrence.CallID()))
		start, found := processor.takeCorrelation("tool:" + run + ":" + call)
		if !found || start.nameDigest != processor.digest("tool-name", occurrence.Name()) {
			processor.stats.orphanTerminals++
			return metric, SpanRecord{}, false, log
		}
		if !terminalAfter(start, envelope) {
			processor.stats.decodeFailures++
			return metric, SpanRecord{}, false, log
		}
		metric.effect = start.effect
		metric.replaySafety = start.replaySafety
		metric.capabilities = start.capabilities
		status := StatusCompleted
		if envelope.Kind() == event.ToolFailed {
			status = StatusFailed
		}
		return metric, completeSpan(start, envelope, status, occurrence.ExecutionState(), occurrence.RetryDisposition()), true, log
	}
	return metric, SpanRecord{}, false, log
}

func terminalAfter(start correlationStart, terminal event.Envelope) bool {
	return terminal.Sequence() > start.sequence && !terminal.At().Before(start.at)
}

func completeSpan(
	start correlationStart,
	terminal event.Envelope,
	status TerminalStatus,
	outcome tool.ExecutionState,
	retry tool.RetryDisposition,
) SpanRecord {
	return SpanRecord{
		kind: start.kind, correlation: start.correlation, run: start.run,
		startedAt: start.at, finishedAt: terminal.At(), startSequence: start.sequence,
		endSequence: terminal.Sequence(), status: status, effect: start.effect,
		replaySafety: start.replaySafety, capabilities: start.capabilities,
		outcome: outcome, retry: retry,
	}
}

func runStatus(kind event.Kind) TerminalStatus {
	switch kind {
	case event.RunCompleted:
		return StatusCompleted
	case event.RunCancelled:
		return StatusCancelled
	default:
		return StatusFailed
	}
}

func (processor *Processor) putCorrelation(key string, value correlationStart) {
	if _, duplicate := processor.correlations[key]; duplicate {
		processor.stats.decodeFailures++
		return
	}
	if len(processor.correlations) >= processor.config.MaxCorrelations {
		var oldestKey string
		oldest := ^uint64(0)
		for candidate, current := range processor.correlations {
			if current.ordinal < oldest || current.ordinal == oldest && candidate < oldestKey {
				oldest = current.ordinal
				oldestKey = candidate
			}
		}
		delete(processor.correlations, oldestKey)
		processor.stats.evictions++
	}
	processor.nextOrdinal++
	value.ordinal = processor.nextOrdinal
	processor.correlations[key] = value
}

func (processor *Processor) takeCorrelation(key string) (correlationStart, bool) {
	value, found := processor.correlations[key]
	if found {
		delete(processor.correlations, key)
	}
	return value, found
}

func (processor *Processor) digest(domain, value string) string {
	mac := hmac.New(sha256.New, processor.key[:])
	_, _ = mac.Write([]byte(domain))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

func (processor *Processor) recordExportFailure() {
	processor.mu.Lock()
	processor.stats.exportFailures++
	processor.mu.Unlock()
}

func safeExport(ctx context.Context, exporter Exporter, batch Batch) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("telemetry exporter panicked")
		}
	}()
	if ctx == nil || exporter == nil || batch.Records() == 0 {
		return errors.New("telemetry export invocation is invalid")
	}
	if err = exporter.Export(ctx, batch.clone()); err != nil {
		return errors.New("telemetry exporter rejected a batch")
	}
	return nil
}

func safeShutdown(ctx context.Context, exporter Exporter) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("telemetry exporter shutdown panicked")
		}
	}()
	if ctx == nil || exporter == nil {
		return errors.New("telemetry exporter shutdown is invalid")
	}
	if err = exporter.Shutdown(ctx); err != nil {
		return errors.New("telemetry exporter shutdown failed")
	}
	return nil
}

// TranslateEnvelope is a testable single-envelope boundary. It deliberately
// exposes no record when the envelope cannot form a complete span; production
// processing still emits its fixed metric and log.
func TranslateEnvelope(
	key CorrelationKey,
	start event.Envelope,
	terminal event.Envelope,
) (SpanRecord, error) {
	if !key.set {
		return SpanRecord{}, errors.New("telemetry translation requires an explicit correlation key")
	}
	config := DefaultConfig()
	config.CorrelationKey = key
	if err := config.Validate(); err != nil {
		return SpanRecord{}, err
	}
	processor := &Processor{config: config, key: key.material, correlations: make(map[string]correlationStart)}
	processor.translate(start)
	_, span, ok, _ := processor.translate(terminal)
	if !ok {
		return SpanRecord{}, fmt.Errorf("telemetry envelopes do not form a complete safe span")
	}
	return span, nil
}

// DiscardExporter is the explicit no-op fallback. It performs no I/O.
type DiscardExporter struct{}

func (DiscardExporter) Export(ctx context.Context, _ Batch) error {
	if ctx == nil {
		return errors.New("telemetry discard export context is nil")
	}
	return ctx.Err()
}

func (DiscardExporter) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("telemetry discard shutdown context is nil")
	}
	return ctx.Err()
}
