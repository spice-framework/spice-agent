package planning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
)

const secretCanary = "SECRET_PLANNER_ERROR_PATH_TOKEN"

type testPlanner struct {
	identity      string
	process       func(context.Context, Request) (Draft, error)
	calls         atomic.Int64
	identityPanic bool
}

func (planner *testPlanner) Identity() string {
	if planner.identityPanic {
		panic(secretCanary)
	}
	return planner.identity
}

func (planner *testPlanner) Process(ctx context.Context, request Request) (Draft, error) {
	planner.calls.Add(1)
	if planner.process != nil {
		return planner.process(ctx, request)
	}
	return testDraft(nil), nil
}

type terminalProvider struct{ calls atomic.Int64 }

func (provider *terminalProvider) Stream(ctx context.Context, _ model.Request) (model.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	provider.calls.Add(1)
	text, _ := model.TextDelta("worker complete")
	completed, _ := model.Completed(model.NewUsage(1, 1))
	return &testStream{values: []model.StreamEvent{text, completed}}, nil
}

type testStream struct {
	values []model.StreamEvent
	index  int
}

func (stream *testStream) Recv(ctx context.Context) (model.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return model.StreamEvent{}, err
	}
	if stream.index >= len(stream.values) {
		return model.StreamEvent{}, io.EOF
	}
	value := stream.values[stream.index]
	stream.index++
	return value, nil
}

func (*testStream) Close() error { return nil }

func TestPrepareReviewStartAndSnapshotPreserveCanonicalPlan(t *testing.T) {
	planner := &testPlanner{identity: "deterministic:v1"}
	service, engine, provider, recorder := testService(t, planner)
	definition, initial := testInput(t)
	originalParts := initial.Parts()
	prepared, err := service.Prepare(t.Context(), definition, initial)
	if err != nil {
		t.Fatal(err)
	}
	if planner.calls.Load() != 1 || provider.calls.Load() != 0 || len(recorder.Events()) != 0 {
		t.Fatalf("prepare crossed execution boundary: planner=%d provider=%d events=%d", planner.calls.Load(), provider.calls.Load(), len(recorder.Events()))
	}
	if !equalParts(initial.Parts(), originalParts) || prepared.Original().SizeBytes() != initial.SizeBytes() {
		t.Fatal("prepare mutated the original message")
	}
	if prepared.Definition().Name() != definition.Name() {
		t.Fatal("prepared value did not retain the worker definition")
	}
	plan := prepared.Plan()
	if !validDigest(plan.ID()) || !validDigest(plan.InputDigest()) || plan.Producer() != planner.identity || len(plan.Steps()) != 2 {
		t.Fatalf("prepared plan is invalid: id=%q input=%q producer=%q steps=%d", plan.ID(), plan.InputDigest(), plan.Producer(), len(plan.Steps()))
	}
	if plan.Goal() != "Answer the user's request." || plan.Steps()[0].Summary() != "Inspect the bounded request." {
		t.Fatal("prepared plan accessors changed canonical content")
	}
	parsed, err := ParsePlan(plan.CanonicalJSON())
	if err != nil || !bytes.Equal(parsed.CanonicalJSON(), plan.CanonicalJSON()) {
		t.Fatalf("canonical round trip failed: %v", err)
	}
	extracted, found, err := ExtractMessage(definition, prepared.Attached())
	if err != nil || !found || !bytes.Equal(extracted.CanonicalJSON(), plan.CanonicalJSON()) {
		t.Fatalf("attached extraction failed: found=%t err=%v", found, err)
	}
	run, err := service.StartPrepared(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	if err = run.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if planner.calls.Load() != 1 || provider.calls.Load() != 1 {
		t.Fatalf("start reran planning or skipped provider: planner=%d provider=%d", planner.calls.Load(), provider.calls.Load())
	}
	snapshot, err := run.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	fromSnapshot, found, err := Extract(snapshot)
	if err != nil || !found || !bytes.Equal(fromSnapshot.CanonicalJSON(), plan.CanonicalJSON()) {
		t.Fatalf("snapshot extraction failed: found=%t err=%v", found, err)
	}
	if events := recorder.Events(); len(events) == 0 || events[0].Kind() != event.RunStarted || events[len(events)-1].Kind() != event.RunCompleted {
		t.Fatalf("worker lifecycle events=%v", eventKinds(events))
	}
	shutdownEngine(t, engine)
}

func TestPrepareFailuresAreSecretSafeAndCreateNoRun(t *testing.T) {
	tests := []struct {
		name    string
		planner *testPlanner
		context func() context.Context
		want    error
	}{
		{name: "cancelled", planner: &testPlanner{identity: "cancel:v1"}, context: cancelledContext, want: context.Canceled},
		{name: "error", planner: &testPlanner{identity: "error:v1", process: func(context.Context, Request) (Draft, error) {
			return Draft{}, errors.New(secretCanary)
		}}, context: context.Background, want: ErrPlannerFailed},
		{name: "panic", planner: &testPlanner{identity: "panic:v1", process: func(context.Context, Request) (Draft, error) {
			panic(secretCanary)
		}}, context: context.Background, want: ErrPlannerPanicked},
		{name: "invalid", planner: &testPlanner{identity: "invalid:v1", process: func(context.Context, Request) (Draft, error) {
			return Draft{}, nil
		}}, context: context.Background},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, engine, provider, recorder := testService(t, test.planner)
			definition, initial := testInput(t)
			_, err := service.Prepare(test.context(), definition, initial)
			if err == nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("Prepare() error=%v want=%v", err, test.want)
			}
			if strings.Contains(err.Error(), secretCanary) {
				t.Fatalf("Prepare() leaked dependency text: %v", err)
			}
			if provider.calls.Load() != 0 || len(recorder.Events()) != 0 {
				t.Fatalf("failed prepare started work: provider=%d events=%d", provider.calls.Load(), len(recorder.Events()))
			}
			shutdownEngine(t, engine)
		})
	}
}

func TestConstructionIdentityAndPreparedCorruptionFailClosed(t *testing.T) {
	provider := &terminalProvider{}
	dispatcher, _ := stage.NewDispatcher(nil)
	engine, _ := agent.NewEngine(provider, dispatcher, &agent.AtomicIDSource{}, time.Now, nil, nil)
	t.Cleanup(func() { shutdownEngine(t, engine) })
	if _, err := NewService(nil, engine); err == nil {
		t.Fatal("nil planner succeeded")
	}
	if _, err := NewService(&testPlanner{identity: "valid:v1"}, nil); err == nil {
		t.Fatal("nil engine succeeded")
	}
	if _, err := NewService(&testPlanner{identityPanic: true}, engine); !errors.Is(err, ErrPlannerPanicked) || strings.Contains(err.Error(), secretCanary) {
		t.Fatalf("identity panic error=%v", err)
	}
	if _, err := NewService(&testPlanner{identity: " invalid "}, engine); err == nil {
		t.Fatal("invalid planner identity succeeded")
	}

	service, _, _, recorder := testService(t, &testPlanner{identity: "valid:v1"})
	definition, initial := testInput(t)
	prepared, err := service.Prepare(t.Context(), definition, initial)
	if err != nil {
		t.Fatal(err)
	}
	cancelled := cancelledContext()
	if _, err = service.StartPrepared(cancelled, prepared); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled StartPrepared() error=%v", err)
	}
	corrupt := prepared
	corrupt.plan.encoded[0] = '!'
	if _, err = service.StartPrepared(t.Context(), corrupt); err == nil {
		t.Fatal("corrupt prepared plan started")
	}
	if _, err = service.StartPrepared(t.Context(), Prepared{}); err == nil {
		t.Fatal("zero prepared plan started")
	}
	if len(recorder.Events()) != 0 {
		t.Fatalf("rejected starts emitted events: %v", eventKinds(recorder.Events()))
	}
}

func TestDraftValidationAndImmutability(t *testing.T) {
	first, _ := NewStep("first", "Inspect the request.")
	second, _ := NewStep("second", "Produce the answer.", "first")
	dependencies := []string{"first"}
	third, err := NewStep("third", "Review the answer.", dependencies...)
	if err != nil {
		t.Fatal(err)
	}
	dependencies[0] = "changed"
	if third.DependsOn()[0] != "first" {
		t.Fatal("step retained caller dependency storage")
	}
	draft, err := NewDraft("Answer safely.", first, second, third)
	if err != nil {
		t.Fatal(err)
	}
	steps := draft.Steps()
	steps[0].dependsOn = []string{"changed"}
	if draft.Steps()[0].ID() != "first" || draft.Goal() != "Answer safely." {
		t.Fatal("draft returned mutable storage")
	}

	invalidUTF8 := string([]byte{0xff})
	for name, create := range map[string]func() error{
		"empty id":             func() error { _, err := NewStep("", "summary"); return err },
		"oversized id":         func() error { _, err := NewStep(strings.Repeat("x", MaximumStepIDBytes+1), "summary"); return err },
		"invalid utf8":         func() error { _, err := NewStep(invalidUTF8, "summary"); return err },
		"empty summary":        func() error { _, err := NewStep("id", ""); return err },
		"oversized summary":    func() error { _, err := NewStep("id", strings.Repeat("x", MaximumTextBytes+1)); return err },
		"duplicate dependency": func() error { _, err := NewStep("id", "summary", "a", "a"); return err },
		"too many dependencies": func() error {
			_, err := NewStep("id", "summary", makeIDs(MaximumDependencies+1)...)
			return err
		},
		"empty goal":     func() error { _, err := NewDraft("", first); return err },
		"no steps":       func() error { _, err := NewDraft("goal"); return err },
		"too many steps": func() error { _, err := NewDraft("goal", makeSteps(MaximumSteps+1)...); return err },
		"duplicate id":   func() error { _, err := NewDraft("goal", first, first); return err },
		"forward dependency": func() error {
			value, _ := NewStep("early", "summary", "later")
			_, err := NewDraft("goal", value)
			return err
		},
		"self dependency": func() error {
			value, _ := NewStep("self", "summary", "self")
			_, err := NewDraft("goal", value)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := create(); err == nil {
				t.Fatal("invalid value succeeded")
			}
		})
	}
}

func TestCanonicalParsingAttachmentAndIdentityBoundaries(t *testing.T) {
	definition, initial := testInput(t)
	request, err := NewRequest(definition, initial)
	if err != nil {
		t.Fatal(err)
	}
	if request.Definition().Name() != definition.Name() || request.Initial().ID() != initial.ID() || !validDigest(request.InputDigest()) {
		t.Fatal("request accessors changed canonical input")
	}
	plan, err := Finalize(request, "canonical:v1", testDraft(t))
	if err != nil {
		t.Fatal(err)
	}
	canonical := plan.CanonicalJSON()
	mutations := [][]byte{
		nil,
		append(append([]byte(nil), canonical...), []byte(" trailing")...),
		bytes.Replace(canonical, []byte(`"version"`), []byte(`"unknown"`), 1),
		bytes.Replace(canonical, []byte(plan.ID()), []byte(digestPrefix+strings.Repeat("0", 64)), 1),
		append([]byte(" "), canonical...),
		bytes.Repeat([]byte("x"), MaximumPlanBytes+1),
	}
	for index, encoded := range mutations {
		if _, err := ParsePlan(encoded); err == nil {
			t.Fatalf("mutation %d succeeded", index)
		}
	}
	otherDefinition, _ := agent.NewDefinition("other", "scripted", 1)
	if _, err := Attach(Request{definition: otherDefinition, initial: initial, inputDigest: request.inputDigest}, plan); err == nil {
		t.Fatal("mismatched request attached")
	}
	attached, err := Attach(request, plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewRequest(definition, attached); err == nil {
		t.Fatal("existing plan was accepted")
	}
	incidentalPart, _ := message.Text("ordinary text " + planMarker + "is not a dedicated part")
	incidental, _ := message.New("incidental", message.RoleUser, incidentalPart)
	if _, err = NewRequest(definition, incidental); err != nil {
		t.Fatalf("incidental marker rejected: %v", err)
	}

	first, err := SemanticIdentity("application:v1", "canonical:v1")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := SemanticIdentity("application:v1", "canonical:v2")
	if first == second || len(first) > maximumSemanticIdentityBytes {
		t.Fatalf("semantic identities=%q/%q", first, second)
	}
	for _, values := range [][2]string{{"", "planner"}, {" application ", "planner"}, {"application", ""}, {strings.Repeat("x", 256), strings.Repeat("y", 256)}} {
		if _, err = SemanticIdentity(values[0], values[1]); err == nil {
			t.Fatalf("SemanticIdentity(%q,%q) succeeded", values[0], values[1])
		}
	}
}

func TestValidationErrorIsBoundedForZeroValues(t *testing.T) {
	var nilFailure *ValidationError
	if nilFailure.Error() != "planning validation failed" || (&ValidationError{}).Error() != "planning validation failed" {
		t.Fatal("zero validation error exposed unstable detail")
	}
}

func TestConcurrentPrepareIsByteIdentical(t *testing.T) {
	planner := &testPlanner{identity: "concurrent:v1"}
	service, engine, provider, recorder := testService(t, planner)
	definition, initial := testInput(t)
	const workers = 64
	values := make([][]byte, workers)
	errorsFound := make(chan error, workers)
	var wait sync.WaitGroup
	for index := range workers {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			prepared, err := service.Prepare(context.Background(), definition, initial)
			if err != nil {
				errorsFound <- err
				return
			}
			values[index] = prepared.Plan().CanonicalJSON()
		}(index)
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	for index := 1; index < workers; index++ {
		if !bytes.Equal(values[0], values[index]) {
			t.Fatalf("preparation %d differs", index)
		}
	}
	if planner.calls.Load() != workers || provider.calls.Load() != 0 || len(recorder.Events()) != 0 {
		t.Fatalf("concurrent prepare boundary: planner=%d provider=%d events=%d", planner.calls.Load(), provider.calls.Load(), len(recorder.Events()))
	}
	shutdownEngine(t, engine)
}

func FuzzParsePlan(f *testing.F) {
	definition, initial := fuzzInput()
	request, _ := NewRequest(definition, initial)
	plan, _ := Finalize(request, "fuzz:v1", testDraft(nil))
	f.Add(plan.CanonicalJSON())
	f.Add([]byte(`{"version":"invalid"}`))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, encoded []byte) {
		parsed, err := ParsePlan(encoded)
		if err != nil {
			return
		}
		if !bytes.Equal(parsed.CanonicalJSON(), encoded) || parsed.validate() != nil {
			t.Fatal("accepted plan is not canonical")
		}
	})
}

func BenchmarkFinalize64Steps(b *testing.B) {
	definition, initial := fuzzInput()
	request, _ := NewRequest(definition, initial)
	draft, _ := NewDraft("Benchmark planning.", makeSteps(MaximumSteps)...)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Finalize(request, "benchmark:v1", draft); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAttachExtract(b *testing.B) {
	definition, initial := fuzzInput()
	request, _ := NewRequest(definition, initial)
	plan, _ := Finalize(request, "benchmark:v1", testDraft(nil))
	b.ReportAllocs()
	for b.Loop() {
		attached, err := Attach(request, plan)
		if err != nil {
			b.Fatal(err)
		}
		if _, found, err := ExtractMessage(definition, attached); err != nil || !found {
			b.Fatalf("extract found=%t err=%v", found, err)
		}
	}
}

func testService(t *testing.T, planner Planner) (*Service, *agent.Engine, *terminalProvider, *event.Recorder) {
	t.Helper()
	provider := &terminalProvider{}
	dispatcher, err := stage.NewDispatcher(nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &event.Recorder{}
	engine, err := agent.NewEngine(provider, dispatcher, &agent.AtomicIDSource{}, time.Now, []event.Observer{recorder}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(planner, engine)
	if err != nil {
		shutdownEngine(t, engine)
		t.Fatal(err)
	}
	return service, engine, provider, recorder
}

func testInput(t *testing.T) (agent.Definition, message.Message) {
	t.Helper()
	definition, initial := fuzzInput()
	return definition, initial
}

func fuzzInput() (agent.Definition, message.Message) {
	definition, _ := agent.NewDefinition("worker", "scripted", 1)
	part, _ := message.Text("Answer the request safely.")
	initial, _ := message.New("input", message.RoleUser, part)
	return definition, initial
}

func testDraft(t *testing.T) Draft {
	if t != nil {
		t.Helper()
	}
	first, _ := NewStep("inspect", "Inspect the bounded request.")
	second, _ := NewStep("respond", "Produce the bounded response.", "inspect")
	draft, err := NewDraft("Answer the user's request.", first, second)
	if err != nil && t != nil {
		t.Fatal(err)
	}
	return draft
}

func makeIDs(count int) []string {
	result := make([]string, count)
	for index := range count {
		result[index] = "step-" + string(rune('a'+index))
	}
	return result
}

func makeSteps(count int) []Step {
	result := make([]Step, count)
	for index := range count {
		id := "step-" + strings.Repeat("x", index/26) + string(rune('a'+index%26))
		result[index], _ = NewStep(id, "Perform one bounded operation.")
	}
	return result
}

func equalParts(left, right []message.Part) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		leftJSON, _ := json.Marshal([]any{left[index].Kind(), left[index].Name(), left[index].CallID(), left[index].Namespace(), left[index].Data()})
		rightJSON, _ := json.Marshal([]any{right[index].Kind(), right[index].Name(), right[index].CallID(), right[index].Namespace(), right[index].Data()})
		leftText, _ := left[index].TextValue()
		rightText, _ := right[index].TextValue()
		if leftText != rightText || !bytes.Equal(leftJSON, rightJSON) {
			return false
		}
	}
	return true
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func shutdownEngine(t *testing.T, engine *agent.Engine) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := engine.Shutdown(ctx); err != nil {
		t.Fatalf("engine shutdown: %v", err)
	}
}

func eventKinds(events []event.Envelope) []event.Kind {
	result := make([]event.Kind, len(events))
	for index, envelope := range events {
		result[index] = envelope.Kind()
	}
	return result
}

var _ model.Provider = (*terminalProvider)(nil)
