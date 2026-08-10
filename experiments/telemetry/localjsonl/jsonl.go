// Package localjsonl exports secret-safe telemetry records to a caller-owned
// writer. It never opens files, closes the writer, discovers endpoints, or
// performs network I/O.
package localjsonl

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	telemetry "github.com/spice-framework/spice-agent/experiments/telemetry"
)

const schemaVersion = "spice.agent.telemetry/jsonl-v1"

// Exporter serializes batches deterministically and synchronously.
type Exporter struct {
	mu     sync.Mutex
	writer io.Writer
	closed bool
}

// New constructs an exporter around an application-owned destination.
func New(writer io.Writer) (*Exporter, error) {
	if writer == nil {
		return nil, errors.New("telemetry JSONL exporter requires a writer")
	}
	return &Exporter{writer: writer}, nil
}

// Export writes metrics, spans, then logs in their stable batch order.
func (exporter *Exporter) Export(ctx context.Context, batch telemetry.Batch) error {
	if ctx == nil {
		return errors.New("telemetry JSONL export context is nil")
	}
	if exporter == nil {
		return errors.New("telemetry JSONL exporter is nil")
	}
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	if exporter.closed {
		return errors.New("telemetry JSONL exporter is closed")
	}
	for _, metric := range batch.Metrics() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := exporter.write(metricWire{
			Version: schemaVersion, Type: "metric", Name: string(metric.Name()),
			Event: string(metric.EventKind()), Count: metric.Count(),
			Effect: string(metric.Effect()), Replay: string(metric.ReplaySafety()),
			Capabilities: uint16(metric.Capabilities()), Outcome: string(metric.ExecutionState()),
			Retry: string(metric.RetryDisposition()),
		}); err != nil {
			return err
		}
	}
	for _, span := range batch.Spans() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := exporter.write(spanWire{
			Version: schemaVersion, Type: "span", Kind: string(span.Kind()),
			Correlation: span.Correlation(), Run: span.RunCorrelation(),
			StartedAt: span.StartedAt(), FinishedAt: span.FinishedAt(),
			StartSequence: span.StartSequence(), EndSequence: span.EndSequence(),
			Status: string(span.Status()), Effect: string(span.Effect()),
			Replay: string(span.ReplaySafety()), Capabilities: uint16(span.Capabilities()),
			Outcome: string(span.ExecutionState()), Retry: string(span.RetryDisposition()),
		}); err != nil {
			return err
		}
	}
	for _, log := range batch.Logs() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := exporter.write(logWire{
			Version: schemaVersion, Type: "log", Event: string(log.Kind()),
			Correlation: log.Correlation(), Sequence: log.Sequence(), At: log.At(),
		}); err != nil {
			return err
		}
	}
	return nil
}

// Shutdown marks the exporter closed without closing its caller-owned writer.
func (exporter *Exporter) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("telemetry JSONL shutdown context is nil")
	}
	if exporter == nil {
		return errors.New("telemetry JSONL exporter is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	exporter.mu.Lock()
	exporter.closed = true
	exporter.mu.Unlock()
	return nil
}

func (exporter *Exporter) write(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.New("telemetry JSONL encoding failed")
	}
	encoded = append(encoded, '\n')
	for len(encoded) != 0 {
		written, writeErr := exporter.writer.Write(encoded)
		if writeErr != nil {
			return errors.New("telemetry JSONL write failed")
		}
		if written < 1 || written > len(encoded) {
			return io.ErrShortWrite
		}
		encoded = encoded[written:]
	}
	return nil
}

type metricWire struct {
	Version      string `json:"version"`
	Type         string `json:"type"`
	Name         string `json:"name"`
	Event        string `json:"event,omitempty"`
	Count        uint64 `json:"count"`
	Effect       string `json:"effect,omitempty"`
	Replay       string `json:"replay,omitempty"`
	Capabilities uint16 `json:"capabilities,omitempty"`
	Outcome      string `json:"outcome,omitempty"`
	Retry        string `json:"retry,omitempty"`
}

type spanWire struct {
	Version       string    `json:"version"`
	Type          string    `json:"type"`
	Kind          string    `json:"kind"`
	Correlation   string    `json:"correlation"`
	Run           string    `json:"run"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	StartSequence uint64    `json:"start_sequence"`
	EndSequence   uint64    `json:"end_sequence"`
	Status        string    `json:"status"`
	Effect        string    `json:"effect,omitempty"`
	Replay        string    `json:"replay,omitempty"`
	Capabilities  uint16    `json:"capabilities,omitempty"`
	Outcome       string    `json:"outcome,omitempty"`
	Retry         string    `json:"retry,omitempty"`
}

type logWire struct {
	Version     string    `json:"version"`
	Type        string    `json:"type"`
	Event       string    `json:"event"`
	Correlation string    `json:"correlation"`
	Sequence    uint64    `json:"sequence"`
	At          time.Time `json:"at"`
}

var _ telemetry.Exporter = (*Exporter)(nil)
