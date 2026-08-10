package sqliterecovery

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/tool"
)

func BenchmarkRequiredObserverCommit(b *testing.B) {
	store, err := Open(b.Context(), filepath.Join(b.TempDir(), "observer.db"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	recorder, err := store.Start(b.Context(), testSeed("benchmark-observer"))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		envelope, reconstructErr := event.Reconstruct("benchmark-observer", uint64(index+1), store.now(), event.ModelDelta, nil)
		if reconstructErr != nil {
			b.Fatal(reconstructErr)
		}
		if err = recorder.Publish(b.Context(), envelope); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCheckpointCommit(b *testing.B) {
	store, err := Open(b.Context(), filepath.Join(b.TempDir(), "checkpoint.db"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		runID := fmt.Sprintf("benchmark-checkpoint-%d", index)
		seed := testSeed(runID)
		recorder, startErr := store.Start(b.Context(), seed)
		if startErr != nil {
			b.Fatal(startErr)
		}
		envelope, _ := event.Reconstruct(runID, 1, store.now(), event.RunStarted, nil)
		if err = recorder.Publish(b.Context(), envelope); err != nil {
			b.Fatal(err)
		}
		snapshot := benchmarkSnapshot(b, runID, 1)
		if _, err = store.persistCheckpoint(b.Context(), snapshot); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRestoreValidation(b *testing.B) {
	store, err := Open(b.Context(), filepath.Join(b.TempDir(), "restore.db"), Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	runID := "benchmark-restore"
	recorder, err := store.Start(b.Context(), testSeed(runID))
	if err != nil {
		b.Fatal(err)
	}
	envelope, _ := event.Reconstruct(runID, 1, store.now(), event.RunStarted, nil)
	if err = recorder.Publish(b.Context(), envelope); err != nil {
		b.Fatal(err)
	}
	snapshot := benchmarkSnapshot(b, runID, 1)
	checkpoint, err := store.persistCheckpoint(b.Context(), snapshot)
	if err != nil {
		b.Fatal(err)
	}
	expected := expectedFrom(snapshot)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err = store.Restore(b.Context(), checkpoint.ID, expected); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkSnapshot(b *testing.B, runID string, sequence uint64) agent.Snapshot {
	b.Helper()
	definition, plan := testToolPlanNoTest(tool.EffectReadOnly, tool.ReplaySafe)
	_ = definition
	part, _ := message.Text("benchmark")
	history, _ := message.New("benchmark-message", message.RoleUser, part)
	agentDefinition, _ := agent.NewDefinition("benchmark-agent", "benchmark-model", 2)
	snapshot, err := agent.NewSnapshot(runID, agentDefinition, 1, []message.Message{history}, plan, sequence, agent.LifecycleSuspended)
	if err != nil {
		b.Fatal(err)
	}
	return snapshot
}
