package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	pluginv1 "github.com/spice-framework/spice-agent/plugin/v1"
)

func TestReadinessSinkAcceptsExactRecordAtEveryChunkBoundary(t *testing.T) {
	t.Parallel()
	record := []byte(pluginv1.ReadinessRecord)
	for split := range len(record) + 1 {
		t.Run(fmt.Sprintf("split-%d", split), func(t *testing.T) {
			t.Parallel()
			sink := newReadinessSink()
			assertDrained(t, sink, record[:split])
			assertDrained(t, sink, record[split:])
			if err := sink.wait(t.Context()); err != nil {
				t.Fatalf("wait exact readiness: %v", err)
			}
			if !sink.isReady() {
				t.Fatal("sink did not become ready")
			}
			if err := sink.err(); err != nil {
				t.Fatalf("exact readiness failed: %v", err)
			}
			if err := sink.Close(); err != nil {
				t.Fatalf("close exact readiness: %v", err)
			}
		})
	}
}

func TestReadinessSinkAcceptsOneByteChunks(t *testing.T) {
	t.Parallel()
	sink := newReadinessSink()
	for _, value := range []byte(pluginv1.ReadinessRecord) {
		assertDrained(t, sink, []byte{value})
	}
	if err := sink.wait(t.Context()); err != nil {
		t.Fatalf("wait one-byte chunks: %v", err)
	}
}

func TestReadinessSinkRejectsEveryMismatchWithoutRetainingOutput(t *testing.T) {
	t.Parallel()
	record := []byte(pluginv1.ReadinessRecord)
	for offset := range len(record) {
		t.Run(fmt.Sprintf("offset-%d", offset), func(t *testing.T) {
			t.Parallel()
			invalid := append([]byte(nil), record...)
			invalid[offset] ^= 0xff
			sink := newReadinessSink()
			assertDrained(t, sink, invalid)
			if err := sink.wait(t.Context()); !errors.Is(err, errReadinessInvalid) {
				t.Fatalf("wait mismatch = %v, want invalid", err)
			}
			assertReadinessFailureRedacted(t, sink, invalid)
		})
	}
}

func TestReadinessSinkRejectsTrailingOutputInSameAndLaterWrites(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		writes [][]byte
	}{
		{
			name:   "same write",
			writes: [][]byte{append([]byte(pluginv1.ReadinessRecord), []byte("private-output")...)},
		},
		{
			name: "later write",
			writes: [][]byte{
				[]byte(pluginv1.ReadinessRecord),
				[]byte("private-output"),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sink := newReadinessSink()
			for _, write := range test.writes {
				assertDrained(t, sink, write)
			}
			if err := sink.wait(t.Context()); !errors.Is(err, errReadinessContaminated) {
				t.Fatalf("wait contaminated output = %v", err)
			}
			select {
			case <-sink.failureSignal():
			default:
				t.Fatal("failure signal remained open")
			}
			assertReadinessFailureRedacted(t, sink, bytes.Join(test.writes, nil))
		})
	}
}

func TestReadinessSinkCloseAndCancellation(t *testing.T) {
	t.Parallel()
	t.Run("empty close", func(t *testing.T) {
		t.Parallel()
		sink := newReadinessSink()
		if err := sink.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		if err := sink.wait(t.Context()); !errors.Is(err, errReadinessStreamClosed) {
			t.Fatalf("wait closed stream = %v", err)
		}
		if err := sink.Close(); err != nil {
			t.Fatalf("repeat close: %v", err)
		}
	})
	t.Run("partial close", func(t *testing.T) {
		t.Parallel()
		sink := newReadinessSink()
		assertDrained(t, sink, []byte("{\"ready\""))
		if err := sink.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		if err := sink.wait(t.Context()); !errors.Is(err, errReadinessStreamClosed) {
			t.Fatalf("wait partial stream = %v", err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		t.Parallel()
		sink := newReadinessSink()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := sink.wait(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("wait cancellation = %v", err)
		}
	})
}

func TestReadinessSinkContinuesDrainingHugeAndConcurrentOutput(t *testing.T) {
	t.Parallel()
	sink := newReadinessSink()
	assertDrained(t, sink, []byte("invalid"))

	content := bytes.Repeat([]byte("private-child-output"), 1<<18)
	start := time.Now()
	const writers = 16
	failures := make(chan error, writers)
	var group sync.WaitGroup
	group.Add(writers)
	for range writers {
		go func() {
			defer group.Done()
			if written, err := sink.Write(content); err != nil || written != len(content) {
				failures <- fmt.Errorf("write %d of %d bytes: %w", written, len(content), err)
			}
		}()
	}
	group.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("discarding invalid stdout took %s", elapsed)
	}
	if err := sink.err(); !errors.Is(err, errReadinessInvalid) {
		t.Fatalf("first failure = %v, want invalid", err)
	}
	assertReadinessFailureRedacted(t, sink, content)
}

func TestStderrSinkBoundsAndSnapshotsOutput(t *testing.T) {
	t.Parallel()
	sink := newStderrSink()
	prefix := bytes.Repeat([]byte("a"), maximumCapturedStderrBytes-1)
	assertDrained(t, sink, prefix)
	assertDrained(t, sink, []byte("bc-private"))

	snapshot := sink.snapshot()
	if got := len(snapshot.bytes()); got != maximumCapturedStderrBytes {
		t.Fatalf("captured bytes = %d, want %d", got, maximumCapturedStderrBytes)
	}
	if snapshot.bytes()[maximumCapturedStderrBytes-1] != 'b' {
		t.Fatal("snapshot did not retain the bounded prefix")
	}
	if got, want := snapshot.totalBytes(), uint64(len(prefix)+len("bc-private")); got != want {
		t.Fatalf("total bytes = %d, want %d", got, want)
	}
	if !snapshot.wasTruncated() {
		t.Fatal("snapshot did not report truncation")
	}

	copyBytes := snapshot.bytes()
	copyBytes[0] = 'z'
	if sink.snapshot().bytes()[0] != 'a' {
		t.Fatal("snapshot exposed mutable sink storage")
	}
	assertStderrFormattingRedacted(t, sink, snapshot, []byte("bc-private"))
}

func TestStderrSinkConcurrentWritesSaturateAndRemainBounded(t *testing.T) {
	t.Parallel()
	sink := newStderrSink()
	sink.total = math.MaxUint64 - 2

	content := bytes.Repeat([]byte("sensitive-stderr"), 1<<16)
	start := time.Now()
	const writers = 24
	failures := make(chan error, writers)
	var group sync.WaitGroup
	group.Add(writers)
	for range writers {
		go func() {
			defer group.Done()
			if written, err := sink.Write(content); err != nil || written != len(content) {
				failures <- fmt.Errorf("write %d of %d bytes: %w", written, len(content), err)
			}
		}()
	}
	group.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("bounded stderr writes took %s", elapsed)
	}

	snapshot := sink.snapshot()
	if snapshot.totalBytes() != math.MaxUint64 {
		t.Fatalf("saturated total = %d", snapshot.totalBytes())
	}
	if len(snapshot.bytes()) != maximumCapturedStderrBytes {
		t.Fatalf("captured bytes = %d", len(snapshot.bytes()))
	}
	if !snapshot.wasTruncated() {
		t.Fatal("concurrent stderr was not marked truncated")
	}
	assertStderrFormattingRedacted(t, sink, snapshot, []byte("sensitive-stderr"))
}

func assertDrained(t *testing.T, writer interface{ Write([]byte) (int, error) }, content []byte) {
	t.Helper()
	written, err := writer.Write(content)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if written != len(content) {
		t.Fatalf("write count = %d, want %d", written, len(content))
	}
}

func assertReadinessFailureRedacted(t *testing.T, sink *readinessSink, private []byte) {
	t.Helper()
	failure := fmt.Sprint(sink.err())
	for _, token := range []string{string(private), "private-output", "secret-address"} {
		if token != "" && strings.Contains(failure, token) {
			t.Fatalf("failure exposed child output %q", token)
		}
	}
}

func assertStderrFormattingRedacted(
	t *testing.T,
	sink *stderrSink,
	snapshot stderrSnapshot,
	private []byte,
) {
	t.Helper()
	formatted := []string{
		fmt.Sprint(sink),
		fmt.Sprintf("%+v", sink),
		fmt.Sprintf("%#v", sink),
		fmt.Sprint(snapshot),
		fmt.Sprintf("%+v", snapshot),
		fmt.Sprintf("%#v", snapshot),
	}
	for _, value := range formatted {
		if strings.Contains(value, string(private)) {
			t.Fatalf("formatting exposed stderr: %q", value)
		}
	}
	for _, value := range []any{sink, snapshot} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal stderr metadata: %v", err)
		}
		if strings.Contains(string(encoded), string(private)) {
			t.Fatalf("JSON exposed stderr: %s", encoded)
		}
		if !bytes.Contains(encoded, []byte(`"total_bytes"`)) {
			t.Fatalf("JSON omitted metadata: %s", encoded)
		}
	}
}
