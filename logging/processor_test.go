package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/daemon"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
	spicelogging "github.com/spice-framework/spice/logging"
)

const secretCanary = "SECRET_PROMPT_DELTA_ARGUMENT_RESULT_PATH_ENDPOINT_CREDENTIAL_RAW_ERROR"

func TestMailboxFiltersHighVolumeKindsBeforeEnqueue(t *testing.T) {
	t.Parallel()
	config := testConfig(t, 1)
	mailbox, err := NewMailbox(config)
	if err != nil {
		t.Fatal(err)
	}
	if mailbox.TryPublish(testEnvelope(t, 1, event.ModelDelta, json.RawMessage(`"`+secretCanary+`"`))) ||
		mailbox.TryPublish(testEnvelope(t, 2, event.ToolProgress, json.RawMessage(`"`+secretCanary+`"`))) {
		t.Fatal("default mailbox admitted a high-volume event")
	}
	if mailbox.Filtered() != 2 || mailbox.Dropped() != 0 {
		t.Fatalf("filtered=%d dropped=%d", mailbox.Filtered(), mailbox.Dropped())
	}
	if !mailbox.TryPublish(testEnvelope(t, 3, event.RunStarted, nil)) ||
		mailbox.TryPublish(testEnvelope(t, 4, event.RunCompleted, nil)) {
		t.Fatal("mailbox capacity accounting is invalid")
	}
	if mailbox.Filtered() != 2 || mailbox.Dropped() != 1 {
		t.Fatalf("filtered=%d dropped=%d", mailbox.Filtered(), mailbox.Dropped())
	}
	mailbox.Close()
}

func TestProcessorEmitsSafeStructuredLifecycleAndToolRecords(t *testing.T) {
	config := testConfig(t, 32)
	buffer := &bytes.Buffer{}
	logger := testLogger(t, buffer, nil)
	mailbox, err := NewMailbox(config)
	if err != nil {
		t.Fatal(err)
	}
	processor, _, err := NewProcessor(config, mailbox, logger)
	if err != nil {
		t.Fatal(err)
	}
	started, completed := testToolPair(t, 2)
	events := []event.Envelope{
		testEnvelope(t, 1, event.RunStarted, nil),
		started,
		completed,
		testEnvelope(t, 4, event.RunCompleted, nil),
	}
	for _, envelope := range events {
		if !mailbox.TryPublish(envelope) {
			t.Fatalf("publish %s failed", envelope.Kind())
		}
	}
	if err := processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot := processor.Snapshot()
	if snapshot.Processed() != 4 || snapshot.Handled() != 4 || snapshot.LogFailures() != 0 || !snapshot.Closed() {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	content := buffer.String()
	for _, forbidden := range []string{secretCanary, "private-run-id", "private-call-id", "private_tool_name"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("log contains forbidden value %q: %s", forbidden, content)
		}
	}
	records := parseRecords(t, content)
	if got := recordEvents(records); !slicesEqual(got, []string{
		"agent.run.started", "agent.tool.started", "agent.tool.completed", "agent.run.completed",
	}) {
		t.Fatalf("events = %v", got)
	}
	startAttributes := attributes(t, records[1])
	terminalAttributes := attributes(t, records[2])
	if startAttributes["effect"] != "read_only" || startAttributes["replay_safety"] != "safe" ||
		startAttributes["capabilities"] != "filesystem.read" || startAttributes["declared"] != true ||
		startAttributes["executable"] != true {
		t.Fatalf("start attributes = %#v", startAttributes)
	}
	runCorrelation, _ := attributes(t, records[0])["run_correlation"].(string)
	callCorrelation, _ := startAttributes["tool_call_correlation"].(string)
	if len(runCorrelation) != 32 || len(callCorrelation) != 32 ||
		terminalAttributes["tool_call_correlation"] != callCorrelation ||
		attributes(t, records[3])["run_correlation"] != runCorrelation {
		t.Fatalf("correlations run=%q call=%q", runCorrelation, callCorrelation)
	}
}

func TestToolFailureEmitsOnlyTypedExecutionMetadata(t *testing.T) {
	config := testConfig(t, 4)
	buffer := &bytes.Buffer{}
	mailbox := mustMailbox(t, config)
	started, _ := testToolPair(t, 1)
	terminal, err := agent.NewToolTerminalOccurrence(
		event.ToolFailed, tool.CallID("private-call-id"), "private_tool_name",
		tool.ExecutionUncertain, tool.RetryNever,
	)
	if err != nil {
		t.Fatal(err)
	}
	terminalData, err := terminal.Encode()
	if err != nil {
		t.Fatal(err)
	}
	mailbox.TryPublish(started)
	mailbox.TryPublish(testEnvelope(t, 2, event.ToolFailed, terminalData))
	processor, _, err := NewProcessor(config, mailbox, testLogger(t, buffer, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err = processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buffer.String(), "private_tool_name") || strings.Contains(buffer.String(), "private-call-id") {
		t.Fatalf("tool identity leaked: %s", buffer.String())
	}
	records := parseRecords(t, buffer.String())
	terminalAttributes := attributes(t, records[1])
	if records[1]["severity"] != "ERROR" || terminalAttributes["execution_state"] != "uncertain" ||
		terminalAttributes["retry_disposition"] != "never" {
		t.Fatalf("terminal record = %#v", records[1])
	}
}

func TestStructuredOutputIsDeterministic(t *testing.T) {
	t.Parallel()
	produce := func() string {
		config := testConfig(t, 4)
		buffer := &bytes.Buffer{}
		mailbox := mustMailbox(t, config)
		mailbox.TryPublish(testEnvelope(t, 1, event.RunStarted, nil))
		mailbox.TryPublish(testEnvelope(t, 2, event.RunCompleted, nil))
		processor, _, err := NewProcessor(config, mailbox, testLogger(t, buffer, nil))
		if err != nil {
			t.Fatal(err)
		}
		if err = processor.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		return buffer.String()
	}
	first := produce()
	if second := produce(); second != first {
		t.Fatalf("output differs\nfirst: %s\nsecond: %s", first, second)
	}
}

func TestProcessorNeverReadsGenericPayloadsAndWarnsOnMalformedTypedOccurrence(t *testing.T) {
	config := testConfig(t, 8)
	buffer := &bytes.Buffer{}
	mailbox := mustMailbox(t, config)
	if !mailbox.TryPublish(testEnvelope(t, 1, event.ToolStarted, json.RawMessage(`{"argument":"`+secretCanary+`"}`))) {
		t.Fatal("publish failed")
	}
	processor, _, err := NewProcessor(config, mailbox, testLogger(t, buffer, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err = processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if snapshot := processor.Snapshot(); snapshot.DecodeFailures() != 1 || snapshot.Processed() != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if strings.Contains(buffer.String(), secretCanary) || strings.Contains(buffer.String(), "argument") {
		t.Fatalf("malformed payload leaked: %s", buffer.String())
	}
	records := parseRecords(t, buffer.String())
	if len(records) != 1 || records[0]["event"] != "agent.event_decode_failed" || records[0]["severity"] != "WARN" {
		t.Fatalf("records = %#v", records)
	}
}

func TestIncludeProgressEmitsMetadataOnlyTraceRecord(t *testing.T) {
	config := testConfig(t, 4)
	config.IncludeProgress = true
	buffer := &bytes.Buffer{}
	mailbox := mustMailbox(t, config)
	if !mailbox.TryPublish(testEnvelope(t, 1, event.ToolProgress, json.RawMessage(`{"output":"`+secretCanary+`"}`))) {
		t.Fatal("progress was filtered")
	}
	processor, _, err := NewProcessor(config, mailbox, testLogger(t, buffer, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err = processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buffer.String(), secretCanary) || strings.Contains(buffer.String(), "output") {
		t.Fatalf("progress payload leaked: %s", buffer.String())
	}
	records := parseRecords(t, buffer.String())
	if len(records) != 1 || records[0]["event"] != "agent.tool.progress" || records[0]["severity"] != "DEBUG-4" {
		t.Fatalf("records = %#v", records)
	}
}

func TestOverflowIsAccountedSeparatelyAndReported(t *testing.T) {
	config := testConfig(t, 1)
	buffer := &bytes.Buffer{}
	mailbox := mustMailbox(t, config)
	mailbox.TryPublish(testEnvelope(t, 1, event.RunStarted, nil))
	mailbox.TryPublish(testEnvelope(t, 2, event.RunCompleted, nil))
	processor, _, err := NewProcessor(config, mailbox, testLogger(t, buffer, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err = processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot := processor.Snapshot()
	if snapshot.OverflowDropped() != 1 || snapshot.Filtered() != 0 || snapshot.Processed() != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	records := parseRecords(t, buffer.String())
	if got := recordEvents(records); !slicesEqual(got, []string{"agent.run.started", "agent.events_dropped"}) {
		t.Fatalf("events = %v", got)
	}
	if attributes(t, records[1])["dropped_total"] != float64(1) {
		t.Fatalf("drop attributes = %#v", attributes(t, records[1]))
	}
}

func TestLoggerFailuresAndPanicsRemainDiagnosticOnly(t *testing.T) {
	t.Parallel()
	for name, handler := range map[string]slog.Handler{
		"error": failingHandler{},
		"panic": panicHandler{},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config := testConfig(t, 2)
			mailbox := mustMailbox(t, config)
			mailbox.TryPublish(testEnvelope(t, 1, event.RunFailed, json.RawMessage(`"`+secretCanary+`"`)))
			processor, _, err := NewProcessor(config, mailbox, testLogger(t, nil, handler))
			if err != nil {
				t.Fatal(err)
			}
			if err = processor.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if snapshot := processor.Snapshot(); snapshot.Processed() != 1 || snapshot.LogFailures() != 1 || !snapshot.Closed() {
				t.Fatalf("snapshot = %+v", snapshot)
			}
		})
	}
}

func TestBlockedSinkMakesCancellationHonestAndCanFinishLater(t *testing.T) {
	config := testConfig(t, 2)
	handler := &blockingHandler{started: make(chan struct{}), release: make(chan struct{})}
	mailbox := mustMailbox(t, config)
	mailbox.TryPublish(testEnvelope(t, 1, event.RunStarted, nil))
	processor, _, err := NewProcessor(config, mailbox, testLogger(t, nil, handler))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-handler.started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err = processor.Close(cancelled); err == nil || !strings.Contains(err.Error(), "did not complete") {
		t.Fatalf("Close(cancelled) = %v", err)
	}
	close(handler.release)
	if err = processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestPseudonymsAreStableWithinKeyAndIsolatedAcrossKeys(t *testing.T) {
	t.Parallel()
	first := testConfig(t, 2)
	second := testConfig(t, 2)
	second.CorrelationKey = testKey(t, 19)
	firstValue := runCorrelation(t, first)
	if repeat := runCorrelation(t, first); repeat != firstValue {
		t.Fatalf("same-key correlation changed: %q != %q", repeat, firstValue)
	}
	if isolated := runCorrelation(t, second); isolated == firstValue {
		t.Fatalf("different keys produced %q", isolated)
	}
}

func TestHealthIsOptInAndUsesFixedReasons(t *testing.T) {
	t.Parallel()
	config := testConfig(t, 2)
	processor := &Processor{mailbox: mustMailbox(t, config)}
	if reasons := NewHealthSource(config, processor).HealthContribution().Reasons(); len(reasons) != 0 {
		t.Fatalf("disabled reasons = %v", reasons)
	}
	config.ReadinessImpact = true
	source := NewHealthSource(config, processor)
	processor.stats.decodeFailures = 1
	if reasons := source.HealthContribution().Reasons(); !slicesEqual(reasons, []daemon.HealthReasonCode{daemon.HealthReasonDependencyDegraded}) {
		t.Fatalf("degraded reasons = %v", reasons)
	}
	processor.stats.logFailures = 1
	processor.stats.closed = true
	if reasons := source.HealthContribution().Reasons(); !slicesEqual(reasons, []daemon.HealthReasonCode{daemon.HealthReasonDependencyUnavailable}) {
		t.Fatalf("unavailable reasons = %v", reasons)
	}
	if reasons := NewHealthSource(config, nil).HealthContribution().Reasons(); !slicesEqual(reasons, []daemon.HealthReasonCode{daemon.HealthReasonDependencyUnavailable}) {
		t.Fatalf("nil reasons = %v", reasons)
	}
}

func TestConfigAndConstructionBoundaries(t *testing.T) {
	t.Parallel()
	valid := testConfig(t, 2)
	for index, config := range []Config{{}, {MailboxCapacity: 65537}} {
		if err := config.Validate(); err == nil {
			t.Fatalf("invalid config %d succeeded", index)
		}
	}
	if _, err := NewCorrelationKey([]byte("short")); err == nil {
		t.Fatal("short key succeeded")
	}
	if _, err := NewCorrelationKey(make([]byte, correlationKeyBytes)); err == nil {
		t.Fatal("zero key succeeded")
	}
	if printed := strings.Join([]string{
		formatValue(valid.CorrelationKey, "%v"), formatValue(valid.CorrelationKey, "%+v"),
		formatValue(valid.CorrelationKey, "%#v"), formatValue(valid.CorrelationKey, "%s"),
	}, " "); printed != "[REDACTED] [REDACTED] [REDACTED] [REDACTED]" {
		t.Fatalf("key formatting = %q", printed)
	}
	mailbox := mustMailbox(t, valid)
	if processor, cleanup, err := NewProcessor(valid, nil, testLogger(t, &bytes.Buffer{}, nil)); err == nil || processor != nil || cleanup != nil {
		t.Fatal("nil mailbox succeeded")
	}
	if processor, cleanup, err := NewProcessor(valid, mailbox, nil); err == nil || processor != nil || cleanup != nil {
		t.Fatal("nil logger succeeded")
	}
	var nilProcessor *Processor
	if nilProcessor.Snapshot() != (Snapshot{}) || nilProcessor.Close(t.Context()) != nil {
		t.Fatal("nil processor boundary failed")
	}
	if err := (&Processor{}).Close(nil); err == nil { //nolint:staticcheck // verifies the public nil-context boundary
		t.Fatal("nil cleanup context succeeded")
	}
}

func TestEventPresentationCoversEveryKind(t *testing.T) {
	t.Parallel()
	tests := map[event.Kind]spicelogging.Level{
		event.RunStarted: spicelogging.LevelInfo, event.RunCompleted: spicelogging.LevelInfo,
		event.RunFailed: spicelogging.LevelError, event.RunCancelled: spicelogging.LevelInfo,
		event.TurnStarted: spicelogging.LevelDebug, event.TurnCompleted: spicelogging.LevelDebug,
		event.TurnFailed: spicelogging.LevelError, event.ModelStarted: spicelogging.LevelDebug,
		event.ModelDelta: spicelogging.LevelDebug, event.ModelCompleted: spicelogging.LevelDebug,
		event.ModelFailed: spicelogging.LevelError, event.ToolStarted: spicelogging.LevelDebug,
		event.ToolProgress: spicelogging.LevelTrace, event.ToolCompleted: spicelogging.LevelDebug,
		event.ToolFailed: spicelogging.LevelError, event.InteractionStarted: spicelogging.LevelDebug,
		event.InteractionCompleted: spicelogging.LevelDebug, event.InteractionFailed: spicelogging.LevelError,
		event.InteractionCancelled: spicelogging.LevelInfo,
	}
	for kind, want := range tests {
		level, message := eventPresentation(kind)
		if level != want || message == "" {
			t.Fatalf("presentation %q = (%v, %q)", kind, level, message)
		}
	}
}

func TestConcurrentPublishingAndSnapshotsAreRaceSafe(t *testing.T) {
	config := testConfig(t, 4096)
	processor, _, err := NewProcessor(config, mustMailbox(t, config), testLogger(t, nil, slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	mailbox := processor.mailbox
	var group sync.WaitGroup
	for worker := range 8 {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for index := range 100 {
				mailbox.TryPublish(testRunEnvelope(t, worker*100+index+1))
				_ = processor.Snapshot()
			}
		}(worker)
	}
	group.Wait()
	if err = processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if snapshot := processor.Snapshot(); snapshot.Processed()+snapshot.OverflowDropped() != 800 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func FuzzToolOccurrenceProjection(f *testing.F) {
	f.Add(uint8(0), []byte(`{}`))
	f.Add(uint8(1), []byte(secretCanary))
	f.Fuzz(func(t *testing.T, selector uint8, payload []byte) {
		if !json.Valid(payload) {
			payload, _ = json.Marshal(string(payload))
		}
		kind := event.ToolStarted
		if selector%2 != 0 {
			kind = event.ToolFailed
		}
		envelope := testEnvelope(t, 1, kind, payload)
		key := testKey(t, 1)
		processor := &Processor{key: key.material}
		_, _ = processor.toolFields(
			envelope,
			[]spicelogging.Field{spicelogging.String("run_correlation", "01234567890123456789012345678901")},
		)
	})
}

func testRunEnvelope(tb testing.TB, sequence int) event.Envelope {
	tb.Helper()
	envelope, err := event.Reconstruct(
		"run-concurrent", uint64(sequence), time.Unix(1700000000+int64(sequence), 0).UTC(), event.RunStarted, nil,
	)
	if err != nil {
		tb.Fatal(err)
	}
	return envelope
}

func runCorrelation(t *testing.T, config Config) string {
	t.Helper()
	buffer := &bytes.Buffer{}
	mailbox := mustMailbox(t, config)
	mailbox.TryPublish(testEnvelope(t, 1, event.RunStarted, nil))
	processor, _, err := NewProcessor(config, mailbox, testLogger(t, buffer, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err = processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	value, ok := attributes(t, parseRecords(t, buffer.String())[0])["run_correlation"].(string)
	if !ok {
		t.Fatal("run correlation is not a string")
	}
	return value
}

func testConfig(tb testing.TB, capacity int) Config {
	tb.Helper()
	return Config{MailboxCapacity: capacity, CorrelationKey: testKey(tb, 1)}
}

func testKey(tb testing.TB, seed byte) CorrelationKey {
	tb.Helper()
	material := make([]byte, correlationKeyBytes)
	for index := range material {
		material[index] = seed + byte(index)
	}
	key, err := NewCorrelationKey(material)
	if err != nil {
		tb.Fatal(err)
	}
	return key
}

func mustMailbox(tb testing.TB, config Config) *event.BestEffortObserver {
	tb.Helper()
	mailbox, err := NewMailbox(config)
	if err != nil {
		tb.Fatal(err)
	}
	return mailbox
}

func testLogger(tb testing.TB, writer *bytes.Buffer, handler slog.Handler) *spicelogging.Logger {
	tb.Helper()
	options := spicelogging.Options{
		Application: "agent-logging-test",
		Configuration: spicelogging.Configuration{
			Format: spicelogging.FormatJSON, Level: spicelogging.LevelTrace,
		},
		Handler: handler,
	}
	if writer != nil {
		options.Writer = writer
	}
	logger, err := spicelogging.New(options)
	if err != nil {
		tb.Fatal(err)
	}
	return logger
}

func testEnvelope(
	tb testing.TB,
	sequence uint64,
	kind event.Kind,
	data json.RawMessage,
) event.Envelope {
	tb.Helper()
	envelope, err := event.Reconstruct(
		"private-run-id", sequence, time.Unix(1700000000+int64(sequence), 0).UTC(), kind, data,
	)
	if err != nil {
		tb.Fatal(err)
	}
	return envelope
}

func testToolPair(tb testing.TB, startSequence uint64) (event.Envelope, event.Envelope) {
	tb.Helper()
	definition, err := tool.NewDefinition(
		"private_tool_name", "Private test tool.", json.RawMessage(`{"type":"object"}`),
		tool.EffectReadOnly, tool.ReplaySafe, tool.CapabilityFilesystemRead,
	)
	if err != nil {
		tb.Fatal(err)
	}
	planID, err := stage.NewPlanID("generation:agent-logging-test")
	if err != nil {
		tb.Fatal(err)
	}
	plan, err := agent.NewPlanIdentity(
		[]string{"provider:test"}, "agent-logging-test-v1",
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		planID, []tool.Definition{definition},
	)
	if err != nil {
		tb.Fatal(err)
	}
	started, err := agent.NewToolStartedOccurrence(
		tool.CallID("private-call-id"), definition.Name(), true, true, &definition, plan, 1,
	)
	if err != nil {
		tb.Fatal(err)
	}
	startedData, err := started.Encode()
	if err != nil {
		tb.Fatal(err)
	}
	completed, err := agent.NewToolTerminalOccurrence(
		event.ToolCompleted, tool.CallID("private-call-id"), definition.Name(), "", "",
	)
	if err != nil {
		tb.Fatal(err)
	}
	completedData, err := completed.Encode()
	if err != nil {
		tb.Fatal(err)
	}
	return testEnvelope(tb, startSequence, event.ToolStarted, startedData),
		testEnvelope(tb, startSequence+1, event.ToolCompleted, completedData)
}

func parseRecords(tb testing.TB, content string) []map[string]any {
	tb.Helper()
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return []map[string]any{}
	}
	records := make([]map[string]any, len(lines))
	for index, line := range lines {
		if err := json.Unmarshal([]byte(line), &records[index]); err != nil {
			tb.Fatalf("decode log line %d: %v\n%s", index, err, line)
		}
	}
	return records
}

func recordEvents(records []map[string]any) []string {
	result := make([]string, len(records))
	for index, record := range records {
		result[index], _ = record["event"].(string)
	}
	return result
}

func attributes(tb testing.TB, record map[string]any) map[string]any {
	tb.Helper()
	attributes, ok := record["attributes"].(map[string]any)
	if !ok {
		tb.Fatalf("attributes = %#v", record["attributes"])
	}
	return attributes
}

func formatValue(value any, format string) string {
	return fmt.Sprintf(format, value)
}

func slicesEqual[T comparable](left, right []T) bool {
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

type failingHandler struct{}

func (failingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (failingHandler) Handle(context.Context, slog.Record) error {
	return errors.New(secretCanary)
}
func (handler failingHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler failingHandler) WithGroup(string) slog.Handler      { return handler }

type panicHandler struct{}

func (panicHandler) Enabled(context.Context, slog.Level) bool   { return true }
func (panicHandler) Handle(context.Context, slog.Record) error  { panic(secretCanary) }
func (handler panicHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler panicHandler) WithGroup(string) slog.Handler      { return handler }

type blockingHandler struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (*blockingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (handler *blockingHandler) Handle(context.Context, slog.Record) error {
	handler.once.Do(func() { close(handler.started) })
	<-handler.release
	return nil
}
func (handler *blockingHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler *blockingHandler) WithGroup(string) slog.Handler      { return handler }
