package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/daemon"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

const testCanary = "SECRET_PROMPT_PATH_ERROR_ARGUMENT_RESULT_TOKEN"

type captureExporter struct {
	mu          sync.Mutex
	batches     []Batch
	exportCalls int
	shutdowns   int
	export      func(context.Context, Batch) error
	shutdown    func(context.Context) error
}

func (exporter *captureExporter) Export(ctx context.Context, batch Batch) error {
	exporter.mu.Lock()
	exporter.exportCalls++
	exporter.batches = append(exporter.batches, batch.clone())
	exporter.mu.Unlock()
	if exporter.export != nil {
		return exporter.export(ctx, batch)
	}
	return nil
}

func (exporter *captureExporter) Shutdown(ctx context.Context) error {
	exporter.mu.Lock()
	exporter.shutdowns++
	exporter.mu.Unlock()
	if exporter.shutdown != nil {
		return exporter.shutdown(ctx)
	}
	return nil
}

func (exporter *captureExporter) snapshot() ([]Batch, int, int) {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	return append([]Batch(nil), exporter.batches...), exporter.exportCalls, exporter.shutdowns
}

func TestProcessorFinalDrainAndSecretSafeTypedSpans(t *testing.T) {
	config := testConfig(t)
	config.BatchRecords = 64
	exporter := &captureExporter{}
	processor, mailbox := newTestProcessor(t, config, exporter)

	started, terminal := toolPair(t, "run-"+testCanary, 3, "tool-"+testCanary, event.ToolCompleted)
	events := []event.Envelope{
		mustEnvelope(t, "run-"+testCanary, 1, event.RunStarted, json.RawMessage(`{"prompt":"`+testCanary+`"}`)),
		mustEnvelope(t, "run-"+testCanary, 2, event.ModelDelta, json.RawMessage(`{"text":"`+testCanary+`"}`)),
		started,
		terminal,
		mustEnvelope(t, "run-"+testCanary, 5, event.RunCompleted, nil),
	}
	for _, envelope := range events {
		if !mailbox.TryPublish(envelope) {
			t.Fatal("unexpected mailbox drop")
		}
	}
	if err := processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot := processor.Snapshot()
	if snapshot.Processed() != uint64(len(events)) || snapshot.Dropped() != 0 ||
		snapshot.ExportFailures() != 0 || !snapshot.Closed() || snapshot.OpenCorrelations() != 0 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	batches, calls, shutdowns := exporter.snapshot()
	if calls != 1 || shutdowns != 1 {
		t.Fatalf("export calls=%d shutdowns=%d", calls, shutdowns)
	}
	var spans []SpanRecord
	for _, batch := range batches {
		spans = append(spans, batch.Spans()...)
	}
	if len(spans) != 2 || spans[0].Kind() != SpanTool || spans[1].Kind() != SpanRun {
		t.Fatalf("spans = %#v", spans)
	}
	for _, span := range spans {
		if len(span.Correlation()) != 32 || span.Correlation() == testCanary || span.RunCorrelation() == testCanary {
			t.Fatalf("unsafe correlation %#v", span)
		}
	}
	if spans[0].Effect() != tool.EffectReadOnly || spans[0].ReplaySafety() != tool.ReplaySafe ||
		spans[0].Capabilities() != CapabilityFilesystemRead || spans[0].Status() != StatusCompleted {
		t.Fatalf("typed tool span = %#v", spans[0])
	}
}

func TestSlowExporterCausesExactMailboxDropsWithoutRetry(t *testing.T) {
	config := testConfig(t)
	config.MailboxCapacity = 2
	config.BatchRecords = 4
	config.BatchBytes = 4 * conservativeRecordBytes
	config.ExportTimeout = 2 * time.Second
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	exporter := &captureExporter{export: func(context.Context, Batch) error {
		once.Do(func() { close(started) })
		<-release
		return errors.New(testCanary)
	}}
	processor, mailbox := newTestProcessor(t, config, exporter)
	if !mailbox.TryPublish(mustEnvelope(t, "run", 1, event.RunStarted, nil)) {
		t.Fatal("first event dropped")
	}
	if !mailbox.TryPublish(mustEnvelope(t, "run", 2, event.ModelDelta, nil)) {
		t.Fatal("second event dropped")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("export did not start")
	}
	rejected := uint64(0)
	for sequence := uint64(2); sequence <= 100; sequence++ {
		if !mailbox.TryPublish(mustEnvelope(t, "run", sequence, event.ModelDelta, json.RawMessage(`"`+testCanary+`"`))) {
			rejected++
		}
	}
	if rejected == 0 {
		t.Fatal("slow exporter did not exercise mailbox overflow")
	}
	close(release)
	if err := processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := processor.Snapshot().Dropped(); got != rejected {
		t.Fatalf("dropped=%d want %d", got, rejected)
	}
	batches, calls, _ := exporter.snapshot()
	if calls < 1 || processor.Snapshot().ExportFailures() < 1 {
		t.Fatalf("calls=%d snapshot=%+v", calls, processor.Snapshot())
	}
	exportedDropped := uint64(0)
	for _, batch := range batches {
		if batch.Records() > config.BatchRecords || batch.SizeBytes() > config.BatchBytes {
			t.Fatalf("oversized batch: records=%d bytes=%d", batch.Records(), batch.SizeBytes())
		}
		for _, metric := range batch.Metrics() {
			if metric.Name() == MetricEventsDropped {
				exportedDropped += metric.Count()
			}
		}
	}
	if exportedDropped != rejected {
		t.Fatalf("exported drop delta=%d want %d", exportedDropped, rejected)
	}
}

func TestExporterPanicAndCancellationAreContainedWithFixedAccounting(t *testing.T) {
	tests := map[string]func(context.Context, Batch) error{
		"panic": func(context.Context, Batch) error { panic(testCanary) },
		"cancel": func(ctx context.Context, _ Batch) error {
			<-ctx.Done()
			return fmt.Errorf("%s: %w", testCanary, ctx.Err())
		},
	}
	for name, export := range tests {
		t.Run(name, func(t *testing.T) {
			config := testConfig(t)
			config.BatchRecords = 4
			config.ExportTimeout = 10 * time.Millisecond
			exporter := &captureExporter{export: export, shutdown: func(context.Context) error { panic(testCanary) }}
			processor, mailbox := newTestProcessor(t, config, exporter)
			mailbox.TryPublish(mustEnvelope(t, "run", 1, event.RunStarted, nil))
			mailbox.TryPublish(mustEnvelope(t, "run", 2, event.ModelDelta, nil))
			if err := processor.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if processor.Snapshot().ExportFailures() != 3 {
				t.Fatalf("snapshot = %+v", processor.Snapshot())
			}
			if _, calls, shutdowns := exporter.snapshot(); calls != 2 || shutdowns != 1 {
				t.Fatalf("calls=%d shutdowns=%d", calls, shutdowns)
			}
		})
	}
}

func TestCloseIsIdempotentAndCallerCancellationIsBounded(t *testing.T) {
	config := testConfig(t)
	blocked := make(chan struct{})
	exporter := &captureExporter{export: func(context.Context, Batch) error { <-blocked; return nil }}
	processor, mailbox := newTestProcessor(t, config, exporter)
	mailbox.TryPublish(mustEnvelope(t, "run", 1, event.RunStarted, nil))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := processor.Close(ctx)
	if err == nil || err.Error() != "telemetry final drain did not complete before cancellation" {
		t.Fatalf("close error = %v", err)
	}
	close(blocked)
	if err = processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, _, shutdowns := exporter.snapshot(); shutdowns != 1 {
		t.Fatalf("shutdowns=%d", shutdowns)
	}
	if err = processor.Close(nil); err == nil {
		t.Fatal("nil close context succeeded")
	}
	var nilProcessor *Processor
	if err = nilProcessor.Close(t.Context()); err != nil || nilProcessor.Snapshot() != (Snapshot{}) {
		t.Fatalf("nil processor: snapshot=%+v err=%v", nilProcessor.Snapshot(), err)
	}
}

func TestCorrelationEvictionAndOrphanTerminalNeverFabricateSpan(t *testing.T) {
	config := testConfig(t)
	config.MaxCorrelations = 1
	processor := &Processor{config: config, key: config.CorrelationKey.material, correlations: make(map[string]correlationStart)}
	processor.translate(mustEnvelope(t, "run-a", 1, event.RunStarted, nil))
	processor.translate(mustEnvelope(t, "run-b", 1, event.RunStarted, nil))
	_, _, hasSpan, _ := processor.translate(mustEnvelope(t, "run-a", 2, event.RunCompleted, nil))
	if hasSpan {
		t.Fatal("evicted start fabricated a span")
	}
	snapshot := processor.Snapshot()
	if snapshot.Evictions() != 1 || snapshot.OrphanTerminals() != 1 || snapshot.OpenCorrelations() != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestFinalDrainAccountsAndClearsIncompleteCorrelations(t *testing.T) {
	config := testConfig(t)
	exporter := &captureExporter{}
	processor, mailbox := newTestProcessor(t, config, exporter)
	mailbox.TryPublish(mustEnvelope(t, "run", 1, event.RunStarted, nil))
	if err := processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot := processor.Snapshot()
	if snapshot.IncompleteSpans() != 1 || snapshot.OpenCorrelations() != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	batches, _, _ := exporter.snapshot()
	count := uint64(0)
	for _, batch := range batches {
		for _, metric := range batch.Metrics() {
			if metric.Name() == MetricSpansIncomplete {
				count += metric.Count()
			}
		}
	}
	if count != 1 {
		t.Fatalf("incomplete metric=%d", count)
	}
}

func TestTypedDecoderFailureAndNameMismatchFailClosed(t *testing.T) {
	config := testConfig(t)
	processor := &Processor{config: config, key: config.CorrelationKey.material, correlations: make(map[string]correlationStart)}
	invalid := mustEnvelope(t, "run", 1, event.ToolStarted, json.RawMessage(`{"argument":"`+testCanary+`"}`))
	processor.translate(invalid)
	start, _ := toolPair(t, "run", 2, "read", event.ToolCompleted)
	processor.translate(start)
	terminalOccurrence, err := agent.NewToolTerminalOccurrence(event.ToolCompleted, "call-1", "other", "", "")
	if err != nil {
		t.Fatal(err)
	}
	terminalData, _ := terminalOccurrence.Encode()
	_, _, hasSpan, _ := processor.translate(mustEnvelope(t, "run", 3, event.ToolCompleted, terminalData))
	if hasSpan || processor.Snapshot().DecodeFailures() != 1 || processor.Snapshot().OrphanTerminals() != 1 {
		t.Fatalf("snapshot=%+v hasSpan=%v", processor.Snapshot(), hasSpan)
	}
}

func TestImpossibleTerminalOrderingFailsClosed(t *testing.T) {
	config := testConfig(t)
	processor := &Processor{config: config, key: config.CorrelationKey.material, correlations: make(map[string]correlationStart)}
	start := mustEnvelope(t, "run", 2, event.RunStarted, nil)
	processor.translate(start)
	terminal, err := event.Reconstruct("run", 1, start.At().Add(-time.Second), event.RunCompleted, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, hasSpan, _ := processor.translate(terminal)
	if hasSpan || processor.Snapshot().DecodeFailures() != 1 || processor.Snapshot().OpenCorrelations() != 0 {
		t.Fatalf("snapshot=%+v hasSpan=%v", processor.Snapshot(), hasSpan)
	}
}

func TestBatchAndRecordSnapshotsAreDefensive(t *testing.T) {
	metric := newMetricRecord(MetricEventsObserved, event.RunStarted, 1)
	span := SpanRecord{kind: SpanRun, correlation: "01234567890123456789012345678901"}
	log := LogRecord{kind: event.RunStarted, sequence: 1}
	batch := newBatch([]MetricRecord{metric}, []SpanRecord{span}, []LogRecord{log})
	metrics := batch.Metrics()
	spans := batch.Spans()
	logs := batch.Logs()
	metrics[0].count = 99
	spans[0].correlation = testCanary
	logs[0].sequence = 99
	if batch.Metrics()[0].Count() != 1 || batch.Spans()[0].Correlation() == testCanary || batch.Logs()[0].Sequence() != 1 {
		t.Fatal("batch accessors exposed mutable storage")
	}
}

func TestConcurrentPublishingAndSnapshotsAreRaceSafe(t *testing.T) {
	config := testConfig(t)
	config.MailboxCapacity = 4096
	config.BatchRecords = 256
	exporter := &captureExporter{}
	processor, mailbox := newTestProcessor(t, config, exporter)
	var accepted atomic.Uint64
	var group sync.WaitGroup
	for worker := range 16 {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for index := range 100 {
				sequence := uint64(worker*100 + index + 1)
				if mailbox.TryPublish(mustEnvelope(t, fmt.Sprintf("run-%d", worker), sequence, event.ModelDelta, nil)) {
					accepted.Add(1)
				}
				_ = processor.Snapshot()
			}
		}(worker)
	}
	group.Wait()
	if err := processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := processor.Snapshot().Processed(); got != accepted.Load() {
		t.Fatalf("processed=%d accepted=%d", got, accepted.Load())
	}
}

func TestHealthIsOptInAndUsesOnlyFixedReasons(t *testing.T) {
	config := testConfig(t)
	processor := &Processor{config: config, mailbox: mustMailbox(t, config), correlations: make(map[string]correlationStart)}
	disabled := NewHealthSource(config, processor)
	if reasons := disabled.HealthContribution().Reasons(); len(reasons) != 0 {
		t.Fatalf("disabled reasons=%v", reasons)
	}
	config.ReadinessImpact = true
	enabled := NewHealthSource(config, processor)
	processor.stats.exportFailures = 1
	if reasons := enabled.HealthContribution().Reasons(); len(reasons) != 1 || reasons[0] != daemon.HealthReasonDependencyDegraded {
		t.Fatalf("degraded reasons=%v", reasons)
	}
	processor.stats.closed = true
	if reasons := enabled.HealthContribution().Reasons(); len(reasons) != 1 || reasons[0] != daemon.HealthReasonDependencyUnavailable {
		t.Fatalf("closed reasons=%v", reasons)
	}
	var nilSource *HealthSource
	if len(nilSource.HealthContribution().Reasons()) != 0 {
		t.Fatal("nil health source is not ready")
	}
}

func TestConfigKeyAndConstructionBoundaries(t *testing.T) {
	valid := testConfig(t)
	bad := []Config{
		{},
		withConfig(valid, func(config *Config) { config.MailboxCapacity = 65537 }),
		withConfig(valid, func(config *Config) { config.BatchRecords = 3 }),
		withConfig(valid, func(config *Config) { config.BatchBytes = 100 }),
		withConfig(valid, func(config *Config) { config.MaxCorrelations = 0 }),
		withConfig(valid, func(config *Config) { config.FlushInterval = 0 }),
		withConfig(valid, func(config *Config) { config.ExportTimeout = 31 * time.Second }),
		withConfig(valid, func(config *Config) { config.ShutdownTimeout = 0 }),
	}
	for index, config := range bad {
		if err := config.Validate(); err == nil {
			t.Fatalf("bad config %d succeeded", index)
		}
	}
	if _, err := NewCorrelationKey([]byte("short")); err == nil {
		t.Fatal("short key succeeded")
	}
	if _, err := NewCorrelationKey(make([]byte, 32)); err == nil {
		t.Fatal("all-zero key succeeded")
	}
	printed := fmt.Sprintf("%v %+v %#v %s", valid.CorrelationKey, valid.CorrelationKey, valid.CorrelationKey, valid.CorrelationKey)
	if printed != "[REDACTED] [REDACTED] [REDACTED] [REDACTED]" {
		t.Fatalf("correlation key formatting is not redacted: %q", printed)
	}
	if configPrinted := fmt.Sprintf("%+v", valid); !strings.Contains(configPrinted, "[REDACTED]") {
		t.Fatalf("config formatting did not redact key: %q", configPrinted)
	}
	if _, err := NewMailbox(Config{}); err == nil {
		t.Fatal("invalid mailbox succeeded")
	}
	mailbox := mustMailbox(t, valid)
	if _, _, err := NewProcessor(valid, nil, DiscardExporter{}); err == nil {
		t.Fatal("nil mailbox succeeded")
	}
	if _, _, err := NewProcessor(valid, mailbox, nil); err == nil {
		t.Fatal("nil exporter succeeded")
	}
	if _, err := TranslateEnvelope(CorrelationKey{}, event.Envelope{}, event.Envelope{}); err == nil {
		t.Fatal("unset translation key succeeded")
	}
	if err := (DiscardExporter{}).Export(nil, Batch{}); err == nil {
		t.Fatal("nil discard export context succeeded")
	}
	if err := (DiscardExporter{}).Shutdown(nil); err == nil {
		t.Fatal("nil discard shutdown context succeeded")
	}
}

func TestDeterministicHMACCorrelation(t *testing.T) {
	key := testKey(t, 1)
	start := mustEnvelope(t, "run", 1, event.RunStarted, nil)
	terminal := mustEnvelope(t, "run", 2, event.RunCompleted, nil)
	first, err := TranslateEnvelope(key, start, terminal)
	if err != nil {
		t.Fatal(err)
	}
	second, err := TranslateEnvelope(key, start, terminal)
	if err != nil {
		t.Fatal(err)
	}
	other, err := TranslateEnvelope(testKey(t, 2), start, terminal)
	if err != nil {
		t.Fatal(err)
	}
	if first.Correlation() != second.Correlation() || first.Correlation() == other.Correlation() {
		t.Fatalf("correlations = %q %q %q", first.Correlation(), second.Correlation(), other.Correlation())
	}
}

func FuzzTranslateEnvelope(f *testing.F) {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	f.Add(uint8(0), []byte(`{}`))
	f.Add(uint8(1), []byte(testCanary))
	f.Fuzz(func(t *testing.T, selector uint8, payload []byte) {
		correlationKey, err := NewCorrelationKey(key)
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(payload) {
			payload, _ = json.Marshal(string(payload))
		}
		var startKind, terminalKind event.Kind
		if selector%2 == 0 {
			startKind, terminalKind = event.RunStarted, event.RunCompleted
		} else {
			startKind, terminalKind = event.ToolStarted, event.ToolFailed
		}
		start := mustEnvelope(t, "run", 1, startKind, payload)
		terminal := mustEnvelope(t, "run", 2, terminalKind, payload)
		span, translateErr := TranslateEnvelope(correlationKey, start, terminal)
		if translateErr == nil && (len(span.Correlation()) != 32 || span.EndSequence() != 2) {
			t.Fatalf("invalid successful span: %#v", span)
		}
	})
}

func BenchmarkTranslateEnvelope(b *testing.B) {
	key := testKey(b, 7)
	start := mustEnvelope(b, "run", 1, event.RunStarted, nil)
	terminal := mustEnvelope(b, "run", 2, event.RunCompleted, nil)
	b.ReportAllocs()
	for range b.N {
		if _, err := TranslateEnvelope(key, start, terminal); err != nil {
			b.Fatal(err)
		}
	}
}

func newTestProcessor(t testing.TB, config Config, exporter Exporter) (*Processor, *event.BestEffortObserver) {
	t.Helper()
	mailbox := mustMailbox(t, config)
	processor, _, err := NewProcessor(config, mailbox, exporter)
	if err != nil {
		t.Fatal(err)
	}
	return processor, mailbox
}

func mustMailbox(t testing.TB, config Config) *event.BestEffortObserver {
	t.Helper()
	mailbox, err := NewMailbox(config)
	if err != nil {
		t.Fatal(err)
	}
	return mailbox
}

func testConfig(t testing.TB) Config {
	t.Helper()
	config := DefaultConfig()
	config.CorrelationKey = testKey(t, 1)
	config.FlushInterval = time.Second
	return config
}

func testKey(t testing.TB, seed byte) CorrelationKey {
	t.Helper()
	material := make([]byte, 32)
	for index := range material {
		material[index] = seed + byte(index)
	}
	key, err := NewCorrelationKey(material)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func withConfig(config Config, mutate func(*Config)) Config {
	mutate(&config)
	return config
}

type testOrBenchmark interface {
	Helper()
	Fatal(...any)
}

func mustEnvelope(t testOrBenchmark, run string, sequence uint64, kind event.Kind, data json.RawMessage) event.Envelope {
	t.Helper()
	envelope, err := event.Reconstruct(run, sequence, time.Unix(1700000000+int64(sequence), 0).UTC(), kind, data)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func toolPair(
	t testing.TB,
	run string,
	startSequence uint64,
	name string,
	terminalKind event.Kind,
) (event.Envelope, event.Envelope) {
	t.Helper()
	definition, err := tool.NewDefinition(
		name, "Secret-safe test tool.", json.RawMessage(`{"type":"object"}`),
		tool.EffectReadOnly, tool.ReplaySafe, tool.CapabilityFilesystemRead,
	)
	if err != nil {
		t.Fatal(err)
	}
	planID, err := stage.NewPlanID("generation:telemetry-test")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := agent.NewPlanIdentity(
		[]string{"provider:test"}, "telemetry:test-v1",
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		planID, []tool.Definition{definition},
	)
	if err != nil {
		t.Fatal(err)
	}
	started, err := agent.NewToolStartedOccurrence("call-1", name, true, true, &definition, plan, 1)
	if err != nil {
		t.Fatal(err)
	}
	startedData, err := started.Encode()
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := agent.NewToolTerminalOccurrence(terminalKind, "call-1", name, "", "")
	if err != nil {
		t.Fatal(err)
	}
	terminalData, err := terminal.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return mustEnvelope(t, run, startSequence, event.ToolStarted, startedData),
		mustEnvelope(t, run, startSequence+1, terminalKind, terminalData)
}
