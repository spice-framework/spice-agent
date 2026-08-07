package client

import (
	"testing"
	"time"
)

func TestEventAndInteractionFramesPreserveExplicitControls(t *testing.T) {
	t.Parallel()
	run := mustRun(t, "run-stream")
	detail, _ := NewRunStartedDetail("coding")
	eventValue, _ := NewEvent(run, 1, time.Unix(1, 0), EventRunStarted, detail)
	eventFrame, err := NewEventFrame(eventValue)
	if err != nil || eventFrame.Kind() != EventFrameEvent {
		t.Fatalf("event frame = %#v, err=%v", eventFrame, err)
	}
	if got, ok := eventFrame.Event(); !ok || got.Sequence() != 1 {
		t.Fatalf("event frame payload = %#v, %t", got, ok)
	}
	control, err := NewEventControl(1, 10, 4, 4, true, false)
	if err != nil {
		t.Fatal(err)
	}
	controlFrame, err := NewEventControlFrame(control)
	if err != nil || controlFrame.Kind() != EventFrameControl {
		t.Fatalf("control frame = %#v, err=%v", controlFrame, err)
	}
	gotControl, ok := controlFrame.Control()
	pageLast, hasPage := gotControl.PageLastSequence()
	if !ok || !hasPage || pageLast != 4 || gotControl.EarliestSequence() != 1 || gotControl.LatestSequence() != 10 ||
		gotControl.LastDeliveredSequence() != 4 || !gotControl.HasMore() || gotControl.Tailing() {
		t.Fatalf("event control = %#v, %t", gotControl, ok)
	}
	legacy, err := NewLegacyEventControl(1, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, hasPage := legacy.PageLastSequence(); hasPage {
		t.Fatal("legacy control unexpectedly has a page cursor")
	}

	interactionControl, err := NewInteractionControl(7, 7, false, true)
	if err != nil {
		t.Fatal(err)
	}
	interactionFrame, err := NewInteractionControlFrame(interactionControl)
	if err != nil || interactionFrame.Kind() != InteractionFrameControl {
		t.Fatalf("interaction control frame = %#v, err=%v", interactionFrame, err)
	}
	gotInteractionControl, ok := interactionFrame.Control()
	if !ok || gotInteractionControl.LatestRevision() != 7 || gotInteractionControl.PageLastRevision() != 7 ||
		gotInteractionControl.HasMore() || !gotInteractionControl.Tailing() {
		t.Fatalf("interaction control = %#v, %t", gotInteractionControl, ok)
	}
	snapshot, _ := NewInteractionSnapshot(7, nil, testLimits(t))
	updateFrame, err := NewInteractionFrame(snapshot)
	if err != nil || updateFrame.Kind() != InteractionFrameUpdate {
		t.Fatalf("interaction update frame = %#v, err=%v", updateFrame, err)
	}
	if update, ok := updateFrame.Update(); !ok || update.Kind() != InteractionSnapshot {
		t.Fatalf("interaction update = %#v, %t", update, ok)
	}
}

func TestFramesRejectZeroAndInconsistentControls(t *testing.T) {
	t.Parallel()
	if _, err := NewEventFrame(Event{}); err == nil {
		t.Fatal("zero event frame accepted")
	}
	if _, err := NewEventControlFrame(EventControl{}); err == nil {
		t.Fatal("zero event control frame accepted")
	}
	for _, values := range []struct {
		earliest, latest, delivered, page uint64
		hasMore, tailing                  bool
	}{
		{earliest: 0, latest: 1},
		{earliest: 1, latest: 10, delivered: 4, page: 4, hasMore: false},
		{earliest: 1, latest: 10, delivered: 4, page: 4, hasMore: true, tailing: true},
		{earliest: 1, latest: 10, delivered: 11, page: 10},
	} {
		if _, err := NewEventControl(
			values.earliest,
			values.latest,
			values.delivered,
			values.page,
			values.hasMore,
			values.tailing,
		); err == nil {
			t.Fatalf("invalid event control accepted: %#v", values)
		}
	}
	if _, err := NewLegacyEventControl(1, 3, 4); err == nil {
		t.Fatal("invalid legacy cursor accepted")
	}
	if _, err := NewInteractionControl(7, 8, false, false); err == nil {
		t.Fatal("interaction cursor above latest accepted")
	}
	if _, err := NewInteractionControl(7, 3, false, false); err == nil {
		t.Fatal("interaction has-more mismatch accepted")
	}
	if _, err := NewInteractionFrame(InteractionUpdate{}); err == nil {
		t.Fatal("zero interaction frame accepted")
	}
	if _, err := NewInteractionControlFrame(InteractionControl{}); err != nil {
		t.Fatalf("zero revision empty interaction control is wire-valid: %v", err)
	}
}
