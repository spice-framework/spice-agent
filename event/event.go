// Package event defines immutable sequenced run observations.
package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Kind identifies a stable event contract.
type Kind string

const (
	RunStarted     Kind = "run.started"
	RunCompleted   Kind = "run.completed"
	RunFailed      Kind = "run.failed"
	RunCancelled   Kind = "run.cancelled"
	TurnStarted    Kind = "turn.started"
	TurnCompleted  Kind = "turn.completed"
	TurnFailed     Kind = "turn.failed"
	ModelStarted   Kind = "model.started"
	ModelDelta     Kind = "model.delta"
	ModelCompleted Kind = "model.completed"
	ModelFailed    Kind = "model.failed"
	ToolStarted    Kind = "tool.started"
	ToolProgress   Kind = "tool.progress"
	ToolCompleted  Kind = "tool.completed"
	ToolFailed     Kind = "tool.failed"
)

// Envelope is one immutable event in a run sequence.
type Envelope struct {
	runID    string
	sequence uint64
	at       time.Time
	kind     Kind
	data     json.RawMessage
}

// RunID returns the owning run.
func (envelope Envelope) RunID() string { return envelope.runID }

// Sequence returns the strictly increasing run-local sequence.
func (envelope Envelope) Sequence() uint64 { return envelope.sequence }

// At returns the engine-supplied timestamp.
func (envelope Envelope) At() time.Time { return envelope.at }

// Kind returns the event discriminator.
func (envelope Envelope) Kind() Kind { return envelope.kind }

// Data returns a defensive copy of the optional JSON payload.
func (envelope Envelope) Data() json.RawMessage {
	return append(json.RawMessage(nil), envelope.data...)
}

// Sequencer allocates event envelopes for one run. It is safe for concurrent use.
type Sequencer struct {
	mu    sync.Mutex
	runID string
	next  uint64
	clock func() time.Time
}

// NewSequencer constructs a run-local sequencer.
func NewSequencer(runID string, clock func() time.Time) (*Sequencer, error) {
	if runID == "" || runID != strings.TrimSpace(runID) {
		return nil, errors.New("event run ID must be non-empty without surrounding whitespace")
	}
	if clock == nil {
		return nil, errors.New("event sequencer requires a clock")
	}
	return &Sequencer{runID: runID, next: 1, clock: clock}, nil
}

// Next creates the next envelope and JSON-encodes optional payload data.
func (sequencer *Sequencer) Next(kind Kind, payload any) (Envelope, error) {
	if sequencer == nil {
		return Envelope{}, errors.New("event sequencer is nil")
	}
	if !validKind(kind) {
		return Envelope{}, fmt.Errorf("event kind %q is unsupported", kind)
	}
	var data json.RawMessage
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, fmt.Errorf("encode event %s payload: %w", kind, err)
		}
		data = encoded
	}
	sequencer.mu.Lock()
	defer sequencer.mu.Unlock()
	result := Envelope{runID: sequencer.runID, sequence: sequencer.next, at: sequencer.clock(), kind: kind, data: data}
	sequencer.next++
	return result, nil
}

// Observer consumes run events. Required observers may apply backpressure.
type Observer interface {
	Publish(context.Context, Envelope) error
}

// BestEffortObserver is a bounded non-blocking mailbox. Publishing never waits;
// overflow increments Dropped and leaves execution unaffected.
type BestEffortObserver struct {
	events  chan Envelope
	dropped atomic.Uint64
	once    sync.Once
}

// NewBestEffortObserver constructs a mailbox with a fixed positive capacity.
func NewBestEffortObserver(capacity int) (*BestEffortObserver, error) {
	if capacity < 1 || capacity > 65536 {
		return nil, errors.New("best-effort observer capacity must be between 1 and 65536")
	}
	return &BestEffortObserver{events: make(chan Envelope, capacity)}, nil
}

// TryPublish enqueues an event or records one drop without blocking.
func (observer *BestEffortObserver) TryPublish(envelope Envelope) bool {
	select {
	case observer.events <- envelope:
		return true
	default:
		observer.dropped.Add(1)
		return false
	}
}

// Events returns the bounded mailbox stream.
func (observer *BestEffortObserver) Events() <-chan Envelope { return observer.events }

// Dropped returns the number of observations discarded due to a full mailbox.
func (observer *BestEffortObserver) Dropped() uint64 { return observer.dropped.Load() }

// Close closes the mailbox exactly once after all producers have stopped.
func (observer *BestEffortObserver) Close() { observer.once.Do(func() { close(observer.events) }) }

// Recorder is a concurrency-safe in-memory observer for tests and embedding.
type Recorder struct {
	mu     sync.Mutex
	events []Envelope
}

// Publish records an event unless the context is cancelled.
func (recorder *Recorder) Publish(ctx context.Context, envelope Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.events = append(recorder.events, envelope)
	return nil
}

// Events returns an immutable snapshot.
func (recorder *Recorder) Events() []Envelope {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]Envelope(nil), recorder.events...)
}

func validKind(kind Kind) bool {
	switch kind {
	case RunStarted, RunCompleted, RunFailed, RunCancelled,
		TurnStarted, TurnCompleted, TurnFailed,
		ModelStarted, ModelDelta, ModelCompleted, ModelFailed,
		ToolStarted, ToolProgress, ToolCompleted, ToolFailed:
		return true
	default:
		return false
	}
}
