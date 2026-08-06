package event_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/event"
)

func TestSequencerIsStrictUnderConcurrency(t *testing.T) {
	now := time.Unix(10, 0).UTC()
	sequencer, err := event.NewSequencer("run-1", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	const count = 32
	sequences := make(chan uint64, count)
	var group sync.WaitGroup
	for range count {
		group.Go(func() {
			envelope, nextErr := sequencer.Next(event.ModelDelta, map[string]string{"text": "x"})
			if nextErr != nil {
				t.Errorf("Next: %v", nextErr)
				return
			}
			if envelope.RunID() != "run-1" || envelope.Kind() != event.ModelDelta || envelope.At() != now {
				t.Error("envelope metadata mismatch")
			}
			sequences <- envelope.Sequence()
		})
	}
	group.Wait()
	close(sequences)
	seen := make(map[uint64]bool, count)
	for sequence := range sequences {
		seen[sequence] = true
	}
	for sequence := uint64(1); sequence <= count; sequence++ {
		if !seen[sequence] {
			t.Fatalf("missing sequence %d", sequence)
		}
	}
}

func TestObserversHaveDistinctBackpressureContracts(t *testing.T) {
	sequencer, _ := event.NewSequencer("run", time.Now)
	envelope, _ := sequencer.Next(event.RunStarted, nil)
	withData, _ := sequencer.Next(event.ModelDelta, map[string]string{"text": "x"})
	data := withData.Data()
	data[0] = 'X'
	if got := string(withData.Data()); got == string(data) {
		t.Fatal("event payload was mutable")
	}
	recorder := &event.Recorder{}
	if err := recorder.Publish(context.Background(), envelope); err != nil || len(recorder.Events()) != 1 {
		t.Fatalf("required recorder: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := recorder.Publish(cancelled, envelope); err == nil {
		t.Fatal("required observer ignored cancellation")
	}
	mailbox, err := event.NewBestEffortObserver(1)
	if err != nil {
		t.Fatal(err)
	}
	if !mailbox.TryPublish(envelope) || mailbox.TryPublish(envelope) || mailbox.Dropped() != 1 {
		t.Fatal("best-effort mailbox did not report overflow")
	}
	<-mailbox.Events()
	mailbox.Close()
	mailbox.Close()
	if _, err = event.NewBestEffortObserver(0); err == nil {
		t.Fatal("zero capacity succeeded")
	}
}

func TestSequencerRejectsInvalidInput(t *testing.T) {
	if _, err := event.NewSequencer("", time.Now); err == nil {
		t.Fatal("empty run ID succeeded")
	}
	if _, err := event.NewSequencer("run", nil); err == nil {
		t.Fatal("nil clock succeeded")
	}
	var sequencer *event.Sequencer
	if _, err := sequencer.Next(event.RunStarted, nil); err == nil {
		t.Fatal("nil sequencer succeeded")
	}
	valid, _ := event.NewSequencer("run", time.Now)
	if _, err := valid.Next("unknown", nil); err == nil {
		t.Fatal("unknown event succeeded")
	}
	if _, err := valid.Next(event.RunStarted, func() {}); err == nil {
		t.Fatal("unencodable payload succeeded")
	}
}
