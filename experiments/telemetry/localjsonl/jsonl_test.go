package localjsonl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/event"
	telemetry "github.com/spice-framework/spice-agent/experiments/telemetry"
)

const secretCanary = "SECRET_JSONL_PROMPT_PATH_ERROR_TOKEN"

func TestExporterIsByteDeterministicAndSecretSafe(t *testing.T) {
	first := produce(t)
	second := produce(t)
	if !bytes.Equal(first, second) {
		t.Fatalf("deterministic outputs differ:\n%s\n%s", first, second)
	}
	if bytes.Contains(first, []byte(secretCanary)) {
		t.Fatal("JSONL contains secret canary")
	}
	lines := bytes.Split(bytes.TrimSpace(first), []byte{'\n'})
	if len(lines) != 5 {
		t.Fatalf("line count=%d\n%s", len(lines), first)
	}
	for _, line := range lines {
		var value map[string]any
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		if value["version"] != schemaVersion {
			t.Fatalf("version=%v", value["version"])
		}
	}
}

func TestExporterHandlesPartialWritesAndDoesNotCloseWriter(t *testing.T) {
	destination := &shortWriter{maximum: 3}
	exporter, err := New(destination)
	if err != nil {
		t.Fatal(err)
	}
	config := deterministicConfig(t)
	mailbox, _ := telemetry.NewMailbox(config)
	processor, _, err := telemetry.NewProcessor(config, mailbox, exporter)
	if err != nil {
		t.Fatal(err)
	}
	mailbox.TryPublish(envelope(t, 1, event.RunStarted))
	mailbox.TryPublish(envelope(t, 2, event.RunCompleted))
	if err = processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if destination.buffer.Len() == 0 {
		t.Fatal("partial writer received no bytes")
	}
	if err = exporter.Export(t.Context(), telemetry.Batch{}); err == nil {
		t.Fatal("closed exporter accepted a batch")
	}
}

func TestExporterRejectsInvalidBoundariesAndSecretSafeWriteFailure(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("nil writer succeeded")
	}
	var nilExporter *Exporter
	if err := nilExporter.Export(t.Context(), telemetry.Batch{}); err == nil {
		t.Fatal("nil exporter succeeded")
	}
	if err := nilExporter.Shutdown(t.Context()); err == nil {
		t.Fatal("nil exporter shutdown succeeded")
	}
	exporter, _ := New(errorWriter{})
	config := deterministicConfig(t)
	mailbox, _ := telemetry.NewMailbox(config)
	processor, _, err := telemetry.NewProcessor(config, mailbox, exporter)
	if err != nil {
		t.Fatal(err)
	}
	mailbox.TryPublish(envelope(t, 1, event.RunStarted))
	mailbox.TryPublish(envelope(t, 2, event.RunCompleted))
	if err = processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if processor.Snapshot().ExportFailures() != 1 {
		t.Fatalf("snapshot=%+v", processor.Snapshot())
	}
	standalone, _ := New(&bytes.Buffer{})
	if err = standalone.Export(nil, telemetry.Batch{}); err == nil {
		t.Fatal("nil context succeeded")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err = standalone.Shutdown(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown error=%v", err)
	}
}

func produce(t *testing.T) []byte {
	t.Helper()
	var destination bytes.Buffer
	exporter, err := New(&destination)
	if err != nil {
		t.Fatal(err)
	}
	config := deterministicConfig(t)
	mailbox, err := telemetry.NewMailbox(config)
	if err != nil {
		t.Fatal(err)
	}
	processor, _, err := telemetry.NewProcessor(config, mailbox, exporter)
	if err != nil {
		t.Fatal(err)
	}
	mailbox.TryPublish(envelope(t, 1, event.RunStarted))
	mailbox.TryPublish(envelope(t, 2, event.RunCompleted))
	if err = processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	return bytes.Clone(destination.Bytes())
}

func deterministicConfig(t testing.TB) telemetry.Config {
	t.Helper()
	material := bytes.Repeat([]byte{7}, 32)
	key, err := telemetry.NewCorrelationKey(material)
	if err != nil {
		t.Fatal(err)
	}
	config := telemetry.DefaultConfig()
	config.CorrelationKey = key
	config.FlushInterval = time.Second
	return config
}

func envelope(t testing.TB, sequence uint64, kind event.Kind) event.Envelope {
	t.Helper()
	value, err := event.Reconstruct(
		"run-"+secretCanary,
		sequence,
		time.Unix(1700000000+int64(sequence), 0).UTC(),
		kind,
		json.RawMessage(`{"private":"`+secretCanary+`"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type shortWriter struct {
	maximum int
	buffer  bytes.Buffer
}

func (writer *shortWriter) Write(content []byte) (int, error) {
	if len(content) > writer.maximum {
		content = content[:writer.maximum]
	}
	return writer.buffer.Write(content)
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New(secretCanary)
}

func TestOutputDoesNotContainFreeFormFields(t *testing.T) {
	output := string(produce(t))
	for _, forbidden := range []string{"prompt", "path", "error", "argument", "result", "token", "model_text"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("output contains forbidden field %q", forbidden)
		}
	}
}

func BenchmarkJSONLRecords(b *testing.B) {
	batch := captureBatch(b)
	exporter, err := New(io.Discard)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for range b.N {
		if err = exporter.Export(context.Background(), batch); err != nil {
			b.Fatal(err)
		}
	}
}

type oneBatchExporter struct {
	batch telemetry.Batch
}

func (exporter *oneBatchExporter) Export(_ context.Context, batch telemetry.Batch) error {
	if exporter.batch.Records() == 0 {
		exporter.batch = batch
	}
	return nil
}

func (*oneBatchExporter) Shutdown(context.Context) error { return nil }

func captureBatch(t testing.TB) telemetry.Batch {
	t.Helper()
	exporter := &oneBatchExporter{}
	config := deterministicConfig(t)
	mailbox, err := telemetry.NewMailbox(config)
	if err != nil {
		t.Fatal(err)
	}
	processor, _, err := telemetry.NewProcessor(config, mailbox, exporter)
	if err != nil {
		t.Fatal(err)
	}
	mailbox.TryPublish(envelope(t, 1, event.RunStarted))
	mailbox.TryPublish(envelope(t, 2, event.RunCompleted))
	if err = processor.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if exporter.batch.Records() == 0 {
		t.Fatal("capture exporter received no batch")
	}
	return exporter.batch
}
