package grpcclient

import (
	"math"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
)

func TestLimitsFromWireRejectsUnrepresentablePlatformCapacity(t *testing.T) {
	t.Parallel()
	value := &commonv1.Limits{
		MaxMessageBytes: math.MaxUint64, MaxCollectionItems: 1,
		MaxReplayEvents: 1, MaxReplayBytes: 1,
		MaxConcurrentStreams: 1, MaxActiveRuns: 1,
	}
	if _, err := limitsFromWire(value); err == nil {
		t.Fatal("unrepresentable message capacity was accepted")
	}
}

func TestEventPageRejectsOversizedFirstReplayEvent(t *testing.T) {
	t.Parallel()
	limits, err := client.NewLimits(1<<20, 8, 1, 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	run, err := client.NewRunRef("run-1")
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := client.NewCursor(run, 0)
	if err != nil {
		t.Fatal(err)
	}
	options, err := client.NewEventStreamOptions(1, false, limits)
	if err != nil {
		t.Fatal(err)
	}
	state := eventPageState{cursor: cursor, options: options, limits: limits}
	value := &enginev1.RunEvent{
		RunId: "run-1", Sequence: 1, UnixNano: time.Now().UnixNano(),
		Kind: enginev1.EventKind_EVENT_KIND_RUN_STARTED, PayloadJson: []byte(`{"definition":"acceptance"}`),
	}
	if err = state.acceptEvent(value); err == nil {
		t.Fatal("oversized first replay event was accepted")
	}
}
