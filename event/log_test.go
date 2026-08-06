package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/event"
)

func TestImportedTailRejectsMissingHistoryAndContinuesAtCursor(t *testing.T) {
	log, err := event.NewLogAfter("run", 7, event.DefaultLogLimits())
	if err != nil {
		t.Fatal(err)
	}
	if earliest, latest := log.Bounds(); earliest != 8 || latest != 7 {
		t.Fatalf("empty imported bounds = [%d,%d]", earliest, latest)
	}
	_, err = log.Subscribe(t.Context(), 0)
	var behind *event.OutOfRangeError
	if !errors.As(err, &behind) || behind.Earliest != 8 || behind.Latest != 7 || behind.RecoveryAfter != 7 {
		t.Fatalf("imported gap diagnostic = %#v, %v", behind, err)
	}
	subscription, err := log.Subscribe(t.Context(), 7)
	if err != nil {
		t.Fatal(err)
	}
	appendEnvelope(t, log, 8, event.TurnStarted)
	log.Close()
	events := collectSubscription(t, subscription)
	if len(events) != 1 || events[0].Sequence() != 8 {
		t.Fatalf("imported tail events = %#v", events)
	}
	if _, err = event.NewLogAfter("run", math.MaxUint64, event.DefaultLogLimits()); err == nil {
		t.Fatal("maximum cursor succeeded")
	}
}

func TestLogSubscriptionHasGapFreeReplayAndTail(t *testing.T) {
	log := newLog(t, event.DefaultLogLimits())
	for sequence := uint64(1); sequence <= 3; sequence++ {
		appendEnvelope(t, log, sequence, event.ModelDelta)
	}
	subscription, err := log.Subscribe(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for sequence := uint64(4); sequence <= 8; sequence++ {
		appendEnvelope(t, log, sequence, event.ModelDelta)
	}
	log.Close()
	got := collectSubscription(t, subscription)
	if len(got) != 7 {
		t.Fatalf("received %d events", len(got))
	}
	for index, envelope := range got {
		if envelope.Sequence() != uint64(index+2) {
			t.Fatalf("event %d sequence = %d", index, envelope.Sequence())
		}
	}
}

func TestLogOutOfRangeGivesDirectionalRecoveryAndStats(t *testing.T) {
	limits := event.DefaultLogLimits()
	limits.MaxEvents = 6
	limits.TerminalReserveEvents = 2
	log := newLog(t, limits)
	for sequence := uint64(1); sequence <= 8; sequence++ {
		appendEnvelope(t, log, sequence, event.ModelDelta)
	}
	earliest, latest := log.Bounds()
	if earliest <= 1 || latest != 8 {
		t.Fatalf("bounds = [%d,%d]", earliest, latest)
	}
	_, err := log.Subscribe(t.Context(), 0)
	var behind *event.OutOfRangeError
	if !errors.As(err, &behind) || behind.RecoveryAfter != earliest-1 {
		t.Fatalf("behind recovery = %#v, %v", behind, err)
	}
	if behind.Error() == "" {
		t.Fatal("out-of-range error text empty")
	}
	_, err = log.Subscribe(t.Context(), 99)
	var ahead *event.OutOfRangeError
	if !errors.As(err, &ahead) || ahead.RecoveryAfter != latest {
		t.Fatalf("ahead recovery = %#v, %v", ahead, err)
	}
	stats := log.Stats()
	if stats.EvictedEvents == 0 || stats.EvictedBytes == 0 || stats.RetainedEvents != latest-earliest+1 || stats.RetainedBytes == 0 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestLogTerminatesSlowAndCancelledSubscribers(t *testing.T) {
	t.Run("slow", func(t *testing.T) {
		limits := event.DefaultLogLimits()
		limits.SubscriberMaxEvents = 2
		log := newLog(t, limits)
		subscription, err := log.Subscribe(t.Context(), 0)
		if err != nil {
			t.Fatal(err)
		}
		for sequence := uint64(1); sequence <= 4; sequence++ {
			appendEnvelope(t, log, sequence, event.ModelDelta)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		err = subscription.Wait(ctx)
		var exhausted *event.ResourceExhaustedError
		if !errors.As(err, &exhausted) || exhausted.LastDelivered != 0 {
			t.Fatalf("slow subscription = %#v, %v", exhausted, err)
		}
		if exhausted.Error() == "" || subscription.LastDelivered() != 0 {
			t.Fatal("exhaustion diagnostics mismatch")
		}
		if log.Stats().SlowSubscriberDisconnects != 1 {
			t.Fatalf("stats = %#v", log.Stats())
		}
	})
	t.Run("cancel without append", func(t *testing.T) {
		log := newLog(t, event.DefaultLogLimits())
		ctx, cancel := context.WithCancel(t.Context())
		subscription, err := log.Subscribe(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		if err = subscription.Wait(t.Context()); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled subscription = %v", err)
		}
		if _, open := <-subscription.Events(); open {
			t.Fatal("cancelled subscription events remained open")
		}
	})
}

func TestLogReservesCapacityForEveryLifecycleTerminal(t *testing.T) {
	terminalKinds := []event.Kind{
		event.RunCompleted, event.RunFailed, event.RunCancelled,
		event.TurnCompleted, event.TurnFailed,
		event.ModelCompleted, event.ModelFailed,
		event.ToolCompleted, event.ToolFailed,
		event.InteractionCompleted, event.InteractionFailed, event.InteractionCancelled,
	}
	for _, kind := range terminalKinds {
		t.Run(string(kind), func(t *testing.T) {
			limits := event.DefaultLogLimits()
			limits.MaxEvents = 4
			limits.TerminalReserveEvents = 1
			log := newLog(t, limits)
			for sequence := uint64(1); sequence <= 3; sequence++ {
				appendEnvelope(t, log, sequence, event.ModelDelta)
			}
			appendEnvelope(t, log, 4, kind)
			if _, latest := log.Bounds(); latest != 4 {
				t.Fatalf("terminal %s was not retained", kind)
			}
		})
	}
}

func TestLogRejectsReplayAndAuthoritativeExhaustion(t *testing.T) {
	limits := event.DefaultLogLimits()
	limits.SubscriberMaxEvents = 1
	limits.MaxBytes = 2048
	limits.TerminalReserveBytes = 512
	log := newLog(t, limits)
	appendEnvelope(t, log, 1, event.ModelDelta)
	appendEnvelope(t, log, 2, event.ModelDelta)
	_, err := log.Subscribe(t.Context(), 0)
	var exhausted *event.ResourceExhaustedError
	if !errors.As(err, &exhausted) || log.Stats().SubscriptionExhaustions != 1 {
		t.Fatalf("subscription exhaustion = %v, %#v", err, log.Stats())
	}
	large, err := event.Reconstruct("run", 3, time.Unix(1, 0).UTC(), event.ModelDelta, json.RawMessage(`"`+strings.Repeat("x", 1800)+`"`))
	if err != nil {
		t.Fatal(err)
	}
	if err = log.Append(large); !errors.As(err, &exhausted) || log.Stats().AuthoritativeExhaustions != 1 {
		t.Fatalf("authoritative exhaustion = %v, %#v", err, log.Stats())
	}
	if _, err = event.NewLog(" run", limits); err == nil {
		t.Fatal("invalid run ID succeeded")
	}
}

func TestLogNilAndClosedBoundaries(t *testing.T) {
	var nilLog *event.Log
	if nilLog.Stats() != (event.LogStats{}) {
		t.Fatal("nil log stats nonzero")
	}
	if err := nilLog.Append(event.Envelope{}); err == nil {
		t.Fatal("nil log append succeeded")
	}
	log := newLog(t, event.DefaultLogLimits())
	log.Close()
	log.Close()
	envelope, _ := event.Reconstruct("run", 1, time.Unix(1, 0).UTC(), event.RunCompleted, nil)
	if err := log.Append(envelope); err == nil {
		t.Fatal("closed log append succeeded")
	}
	var nilContext context.Context
	if _, err := log.Subscribe(nilContext, 0); err == nil {
		t.Fatal("nil subscription context succeeded")
	}
}

func newLog(t *testing.T, limits event.LogLimits) *event.Log {
	t.Helper()
	log, err := event.NewLog("run", limits)
	if err != nil {
		t.Fatal(err)
	}
	return log
}

func appendEnvelope(t *testing.T, log *event.Log, sequence uint64, kind event.Kind) {
	t.Helper()
	envelope, err := event.Reconstruct("run", sequence, time.Unix(int64(sequence), 0).UTC(), kind, json.RawMessage(`{"value":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = log.Append(envelope); err != nil {
		t.Fatal(err)
	}
}

func collectSubscription(t *testing.T, subscription *event.Subscription) []event.Envelope {
	t.Helper()
	var values []event.Envelope
	for envelope := range subscription.Events() {
		values = append(values, envelope)
	}
	if err := subscription.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	return values
}
