package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
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

func TestLogReplayCapturesBoundedPagesAndRegistersFinalTailAtomically(t *testing.T) {
	limits := event.DefaultLogLimits()
	limits.MaxEvents = 7
	limits.TerminalReserveEvents = 1
	limits.SubscriberMaxEvents = 4
	log := newLog(t, limits)
	for sequence := uint64(1); sequence <= 4; sequence++ {
		appendEnvelope(t, log, sequence, event.ModelDelta)
	}

	first, err := log.Replay(t.Context(), event.ReplayRequest{
		AfterSequence: 0, MaxEvents: 2, MaxBytes: limits.SubscriberMaxBytes, Tail: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.EarliestSequence != 1 || first.LatestSequence != 4 ||
		first.PageLastSequence != 2 || !first.HasMore || first.Tailing || first.Tail != nil {
		t.Fatalf("first page = %#v", first)
	}
	second, err := log.Replay(t.Context(), event.ReplayRequest{
		AfterSequence: first.PageLastSequence,
		MaxEvents:     2, MaxBytes: limits.SubscriberMaxBytes, Tail: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.PageLastSequence != 4 || second.HasMore || !second.Tailing || second.Tail == nil {
		t.Fatalf("second page = %#v", second)
	}
	for sequence := uint64(5); sequence <= 8; sequence++ {
		appendEnvelope(t, log, sequence, event.ModelDelta)
	}
	log.Close()

	all := append(slices.Clone(first.Events), second.Events...)
	all = append(all, collectSubscription(t, second.Tail)...)
	if len(all) != 8 {
		t.Fatalf("replay/tail length = %d", len(all))
	}
	for index, envelope := range all {
		if envelope.Sequence() != uint64(index+1) {
			t.Fatalf("replay/tail event %d sequence = %d", index, envelope.Sequence())
		}
	}
	earliest, latest := log.Bounds()
	if earliest <= 1 || latest != 8 {
		t.Fatalf("post-eviction bounds = [%d,%d]", earliest, latest)
	}
}

func TestLogReplayEmptyInitialAndImportedTails(t *testing.T) {
	for _, test := range []struct {
		name     string
		after    uint64
		newLog   func(*testing.T) *event.Log
		next     uint64
		earliest uint64
		latest   uint64
	}{
		{"initial", 0, func(t *testing.T) *event.Log { t.Helper(); return newLog(t, event.DefaultLogLimits()) }, 1, 1, 0},
		{"imported", 7, func(t *testing.T) *event.Log {
			t.Helper()
			log, err := event.NewLogAfter("run", 7, event.DefaultLogLimits())
			if err != nil {
				t.Fatal(err)
			}
			return log
		}, 8, 8, 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			log := test.newLog(t)
			page, err := log.Replay(t.Context(), event.ReplayRequest{
				AfterSequence: test.after, MaxEvents: 1, MaxBytes: 1024, Tail: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if page.EarliestSequence != test.earliest || page.LatestSequence != test.latest ||
				page.PageLastSequence != test.after || page.HasMore || !page.Tailing || len(page.Events) != 0 {
				t.Fatalf("empty page = %#v", page)
			}
			appendEnvelope(t, log, test.next, event.ModelDelta)
			log.Close()
			tail := collectSubscription(t, page.Tail)
			if len(tail) != 1 || tail[0].Sequence() != test.next {
				t.Fatalf("tail = %#v", tail)
			}
		})
	}
}

func TestLogReplayRejectsStaleFutureAndUnprogressablePages(t *testing.T) {
	limits := event.DefaultLogLimits()
	limits.MaxEvents = 5
	limits.TerminalReserveEvents = 1
	log := newLog(t, limits)
	for sequence := uint64(1); sequence <= 7; sequence++ {
		appendEnvelope(t, log, sequence, event.ModelDelta)
	}
	earliest, latest := log.Bounds()
	for _, after := range []uint64{0, latest + 1} {
		_, err := log.Replay(t.Context(), event.ReplayRequest{
			AfterSequence: after, MaxEvents: 1, MaxBytes: limits.SubscriberMaxBytes,
		})
		var outOfRange *event.OutOfRangeError
		if !errors.As(err, &outOfRange) {
			t.Fatalf("cursor %d error = %v", after, err)
		}
		if after == 0 && outOfRange.RecoveryAfter != earliest-1 || after > latest && outOfRange.RecoveryAfter != latest {
			t.Fatalf("cursor %d recovery = %#v", after, outOfRange)
		}
	}
	_, err := log.Replay(t.Context(), event.ReplayRequest{
		AfterSequence: earliest - 1, MaxEvents: 1, MaxBytes: 1,
	})
	var exhausted *event.ResourceExhaustedError
	if !errors.As(err, &exhausted) || exhausted.LastDelivered != earliest-1 {
		t.Fatalf("unprogressable page = %#v, %v", exhausted, err)
	}
	if log.Stats().SubscriptionExhaustions != 1 {
		t.Fatalf("replay exhaustion stats = %#v", log.Stats())
	}
	if _, err = log.Replay(t.Context(), event.ReplayRequest{MaxEvents: 0, MaxBytes: 1}); err == nil {
		t.Fatal("zero event page bound succeeded")
	}
}

func TestLogReplaySlowTailDisconnectsAtPageCursorAndRecoversWithoutGap(t *testing.T) {
	limits := event.DefaultLogLimits()
	limits.SubscriberMaxEvents = 2
	log := newLog(t, limits)
	appendEnvelope(t, log, 1, event.ModelDelta)
	page, err := log.Replay(t.Context(), event.ReplayRequest{
		AfterSequence: 0, MaxEvents: 1, MaxBytes: limits.SubscriberMaxBytes, Tail: true,
	})
	if err != nil || !page.Tailing || page.PageLastSequence != 1 {
		t.Fatalf("page = %#v, %v", page, err)
	}
	for sequence := uint64(2); sequence <= 4; sequence++ {
		appendEnvelope(t, log, sequence, event.ModelDelta)
	}
	var exhausted *event.ResourceExhaustedError
	if err = page.Tail.Wait(t.Context()); !errors.As(err, &exhausted) || exhausted.LastDelivered != 1 {
		t.Fatalf("slow tail = %#v, %v", exhausted, err)
	}
	recovery, err := log.Replay(t.Context(), event.ReplayRequest{
		AfterSequence: exhausted.LastDelivered, MaxEvents: 2, MaxBytes: limits.SubscriberMaxBytes,
	})
	if err != nil || len(recovery.Events) != 2 || !recovery.HasMore {
		t.Fatalf("recovery page = %#v, %v", recovery, err)
	}
	for index, envelope := range recovery.Events {
		if envelope.Sequence() != uint64(index+2) {
			t.Fatalf("recovery event %d sequence = %d", index, envelope.Sequence())
		}
	}
	final, err := log.Replay(t.Context(), event.ReplayRequest{
		AfterSequence: recovery.PageLastSequence, MaxEvents: 2, MaxBytes: limits.SubscriberMaxBytes,
	})
	if err != nil || len(final.Events) != 1 || final.HasMore || final.Events[0].Sequence() != 4 {
		t.Fatalf("final recovery page = %#v, %v", final, err)
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

func TestSubscriptionCancellationAfterCommittedDeliveryDoesNotRaceDequeue(t *testing.T) {
	t.Parallel()
	for iteration := range 250 {
		log := newLog(t, event.DefaultLogLimits())
		ctx, cancel := context.WithCancel(t.Context())
		subscription, err := log.Subscribe(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		appendEnvelope(t, log, 1, event.ModelDelta)
		select {
		case envelope := <-subscription.Events():
			if envelope.Sequence() != 1 {
				t.Fatalf("iteration %d sequence = %d", iteration, envelope.Sequence())
			}
		case <-time.After(time.Second):
			t.Fatalf("iteration %d did not deliver", iteration)
		}
		cancel()
		if err = subscription.Wait(t.Context()); !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d cancellation = %v", iteration, err)
		}
		if subscription.LastDelivered() != 1 {
			t.Fatalf("iteration %d last delivered = %d", iteration, subscription.LastDelivered())
		}
		log.Close()
	}
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
