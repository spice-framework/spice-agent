package compaction_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"

	compaction "github.com/spice-framework/spice-agent/experiments/compaction"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
)

func TestCompactReplacesOnlyOldContiguousCompleteRounds(t *testing.T) {
	messages := []message.Message{mustTextMessage(t, "user", message.RoleUser, strings.Repeat("context ", 80))}
	messages = append(messages, mustRound(t, 1)...)
	messages = append(messages, mustRound(t, 2)...)
	messages = append(messages, mustRound(t, 3)...)
	request := mustRequest(t, messages)
	options := compaction.Options{TriggerBytes: 256, RetainRecentRounds: 1, MaximumSummaryBytes: 512}

	first, report, err := compaction.Compact(request, options)
	if err != nil {
		t.Fatal(err)
	}
	second, secondReport, err := compaction.Compact(request, options)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Compacted || report.Rounds != 2 || report.Messages != 4 ||
		report.OutputBytes >= report.InputBytes || len(report.SourceDigest) != 64 {
		t.Fatalf("report = %#v", report)
	}
	if report != secondReport || messageSignature(first.Messages()) != messageSignature(second.Messages()) {
		t.Fatal("compaction is not deterministic")
	}
	got := first.Messages()
	if len(got) != 4 || got[0].ID() != "user" || got[1].Role() != message.RoleSystem ||
		got[2].ID() != "assistant-3" || got[3].ID() != "tool-3" {
		t.Fatalf("compacted history = %s", messageSignature(got))
	}
	summary, _ := got[1].Parts()[0].TextValue()
	if !strings.Contains(summary, compaction.ContractVersion) || !strings.Contains(summary, report.SourceDigest) ||
		!strings.Contains(summary, "assistant-1") {
		t.Fatalf("summary = %q", summary)
	}
	if first.OperationID() != request.OperationID() || first.Model() != request.Model() || len(first.Tools()) != len(request.Tools()) {
		t.Fatal("compaction changed model operation identity or tools")
	}
	if len(request.Messages()) != 7 {
		t.Fatal("compaction mutated the caller request")
	}
}

func TestCompactPreservesIncompleteAndNoncontiguousHistory(t *testing.T) {
	complete := mustRound(t, 1)
	incomplete := mustAssistantCall(t, 2)
	gap := mustTextMessage(t, "user-gap", message.RoleUser, strings.Repeat("gap ", 80))
	messages := []message.Message{mustTextMessage(t, "user", message.RoleUser, strings.Repeat("input ", 80))}
	messages = append(messages, complete...)
	messages = append(messages, gap)
	messages = append(messages, mustRound(t, 3)...)
	messages = append(messages, incomplete)
	request := mustRequest(t, messages)

	result, report, err := compaction.Compact(request, compaction.Options{
		TriggerBytes: 256, RetainRecentRounds: 0, MaximumSummaryBytes: 512,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Compacted || report.Rounds != 1 || report.Messages != 2 {
		t.Fatalf("report = %#v", report)
	}
	got := result.Messages()
	if got[len(got)-1].ID() != incomplete.ID() || got[2].ID() != gap.ID() || got[3].ID() != "assistant-3" {
		t.Fatalf("history = %s", messageSignature(got))
	}
}

func TestCompactDoesNothingBelowThresholdOrWithoutOldCompleteRound(t *testing.T) {
	request := mustRequest(t, []message.Message{mustTextMessage(t, "user", message.RoleUser, "small")})
	options := compaction.Options{TriggerBytes: 256, RetainRecentRounds: 0, MaximumSummaryBytes: 512}
	result, report, err := compaction.Compact(request, options)
	if err != nil || report.Compacted || messageSignature(result.Messages()) != messageSignature(request.Messages()) {
		t.Fatalf("Compact() = %#v, %#v, %v", result, report, err)
	}

	large := mustRequest(t, []message.Message{mustTextMessage(t, "large", message.RoleUser, strings.Repeat("x", 512))})
	result, report, err = compaction.Compact(large, options)
	if err != nil || report.Compacted || messageSignature(result.Messages()) != messageSignature(large.Messages()) {
		t.Fatalf("Compact(large) = %#v, %#v, %v", result, report, err)
	}
}

func TestCompactAllocatesCollisionFreeSummaryIDAndBoundsUTF8(t *testing.T) {
	messages := []message.Message{mustTextMessage(t, "user", message.RoleUser, strings.Repeat("🙂", 100))}
	messages = append(messages, mustRoundWithText(t, 1, strings.Repeat("🙂", 300))...)
	request := mustRequest(t, messages)
	result, report, err := compaction.Compact(request, compaction.Options{
		TriggerBytes: 256, RetainRecentRounds: 0, MaximumSummaryBytes: 512,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Compacted {
		t.Fatal("large complete round was not compacted")
	}
	summary := result.Messages()[1]
	text, _ := summary.Parts()[0].TextValue()
	if len(text) > 512 || !strings.Contains(text, "extract truncated") {
		t.Fatalf("bounded summary length=%d text=%q", len(text), text)
	}
	if strings.Contains(text, "\ufffd") {
		t.Fatal("summary truncated through a UTF-8 encoding")
	}
}

func TestOptionsAndSemanticIdentityFailClosed(t *testing.T) {
	valid := compaction.DefaultOptions()
	identity, err := compaction.SemanticIdentity("application:v1", valid)
	if err != nil || !strings.HasPrefix(identity, "application:v1|compaction:") {
		t.Fatalf("SemanticIdentity() = %q, %v", identity, err)
	}
	changed := valid
	changed.RetainRecentRounds++
	other, err := compaction.SemanticIdentity("application:v1", changed)
	if err != nil || other == identity {
		t.Fatal("semantic configuration did not change identity")
	}

	invalid := []compaction.Options{
		{},
		{TriggerBytes: 255, MaximumSummaryBytes: 512},
		{TriggerBytes: 256, RetainRecentRounds: -1, MaximumSummaryBytes: 512},
		{TriggerBytes: 256, RetainRecentRounds: 4097, MaximumSummaryBytes: 512},
		{TriggerBytes: 256, MaximumSummaryBytes: 511},
		{TriggerBytes: 32<<20 + 1, MaximumSummaryBytes: 512},
	}
	for _, options := range invalid {
		if err := options.Validate(); err == nil {
			t.Fatalf("Options.Validate(%#v) succeeded", options)
		}
	}
	if _, err = compaction.SemanticIdentity(" invalid ", valid); err == nil {
		t.Fatal("SemanticIdentity accepted a malformed application identity")
	}
	if _, err = compaction.SemanticIdentity(strings.Repeat("x", 240), valid); err == nil {
		t.Fatal("SemanticIdentity accepted an oversized combined identity")
	}
}

func TestProviderDelegatesExactlyOnceAndHonorsCancellation(t *testing.T) {
	delegate := &recordingProvider{stream: inertStream{}}
	provider, err := compaction.NewProvider(delegate, compaction.Options{
		TriggerBytes: 256, RetainRecentRounds: 0, MaximumSummaryBytes: 512,
	})
	if err != nil {
		t.Fatal(err)
	}
	messages := []message.Message{mustTextMessage(t, "user", message.RoleUser, strings.Repeat("input ", 80))}
	messages = append(messages, mustRound(t, 1)...)
	request := mustRequest(t, messages)
	stream, err := provider.Stream(t.Context(), request)
	if err != nil || stream == nil || delegate.calls != 1 || len(delegate.request.Messages()) >= len(request.Messages()) {
		t.Fatalf("Provider.Stream() calls=%d messages=%d err=%v", delegate.calls, len(delegate.request.Messages()), err)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = provider.Stream(cancelled, request); !errors.Is(err, context.Canceled) || delegate.calls != 1 {
		t.Fatalf("cancelled Stream() calls=%d err=%v", delegate.calls, err)
	}
	if _, err = provider.Stream(nil, request); err == nil {
		t.Fatal("Stream accepted a nil context")
	}
	var unavailable *compaction.Provider
	if _, err = unavailable.Stream(t.Context(), request); err == nil {
		t.Fatal("nil provider accepted a request")
	}
	if _, err = compaction.NewProvider(nil, compaction.DefaultOptions()); err == nil {
		t.Fatal("NewProvider accepted a nil delegate")
	}
}

func TestProviderIsRaceSafe(t *testing.T) {
	delegate := &recordingProvider{stream: inertStream{}}
	provider, err := compaction.NewProvider(delegate, compaction.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	request := mustRequest(t, []message.Message{mustTextMessage(t, "user", message.RoleUser, "hello")})
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, streamErr := provider.Stream(t.Context(), request); streamErr != nil {
				t.Errorf("Stream() error = %v", streamErr)
			}
		}()
	}
	wait.Wait()
	delegate.mu.Lock()
	defer delegate.mu.Unlock()
	if delegate.calls != 32 {
		t.Fatalf("delegate calls = %d", delegate.calls)
	}
}

func FuzzCompact(f *testing.F) {
	f.Add("ordinary text", uint8(0))
	f.Add("🙂 multiline\ntext", uint8(2))
	f.Fuzz(func(t *testing.T, value string, retained uint8) {
		if value == "" || len(value) > 4096 {
			t.Skip()
		}
		messages := []message.Message{mustTextMessage(t, "user", message.RoleUser, value)}
		messages = append(messages, mustRoundWithText(t, 1, value)...)
		messages = append(messages, mustRoundWithText(t, 2, value)...)
		request := mustRequest(t, messages)
		result, _, err := compaction.Compact(request, compaction.Options{
			TriggerBytes: 256, RetainRecentRounds: int(retained % 3), MaximumSummaryBytes: 512,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = model.NewRequest(result.OperationID(), result.Model(), result.Messages(), result.Tools()); err != nil {
			t.Fatalf("compacted request invalid: %v", err)
		}
	})
}

func BenchmarkCompact(b *testing.B) {
	messages := []message.Message{mustTextMessage(b, "user", message.RoleUser, strings.Repeat("context ", 256))}
	for round := 1; round <= 32; round++ {
		messages = append(messages, mustRoundWithText(b, round, strings.Repeat("result ", 64))...)
	}
	request := mustRequest(b, messages)
	options := compaction.Options{TriggerBytes: 256, RetainRecentRounds: 2, MaximumSummaryBytes: 64 << 10}
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := compaction.Compact(request, options); err != nil {
			b.Fatal(err)
		}
	}
}

type testHelper interface {
	Helper()
	Fatal(...any)
}

func mustRound(helper testHelper, number int) []message.Message {
	return mustRoundWithText(helper, number, strings.Repeat("round ", 80))
}

func mustRoundWithText(helper testHelper, number int, text string) []message.Message {
	helper.Helper()
	return []message.Message{mustAssistantCallWithText(helper, number, text), mustToolResult(helper, number)}
}

func mustAssistantCall(helper testHelper, number int) message.Message {
	return mustAssistantCallWithText(helper, number, "working")
}

func mustAssistantCallWithText(helper testHelper, number int, text string) message.Message {
	helper.Helper()
	textPart, err := message.Text(text)
	if err != nil {
		helper.Fatal(err)
	}
	call, err := message.ToolCall("call-"+itoa(number), "fixture.echo", json.RawMessage(`{"value":"input"}`))
	if err != nil {
		helper.Fatal(err)
	}
	value, err := message.New(message.ID("assistant-"+itoa(number)), message.RoleAssistant, textPart, call)
	if err != nil {
		helper.Fatal(err)
	}
	return value
}

func mustToolResult(helper testHelper, number int) message.Message {
	helper.Helper()
	part, err := message.ToolResult("call-"+itoa(number), "fixture.echo", json.RawMessage(`{"value":"output"}`))
	if err != nil {
		helper.Fatal(err)
	}
	value, err := message.New(message.ID("tool-"+itoa(number)), message.RoleTool, part)
	if err != nil {
		helper.Fatal(err)
	}
	return value
}

func mustTextMessage(helper testHelper, id string, role message.Role, text string) message.Message {
	helper.Helper()
	part, err := message.Text(text)
	if err != nil {
		helper.Fatal(err)
	}
	value, err := message.New(message.ID(id), role, part)
	if err != nil {
		helper.Fatal(err)
	}
	return value
}

func mustRequest(helper testHelper, messages []message.Message) model.Request {
	helper.Helper()
	request, err := model.NewRequest("operation-1", "scripted", messages, nil)
	if err != nil {
		helper.Fatal(err)
	}
	return request
}

func messageSignature(messages []message.Message) string {
	var result strings.Builder
	for _, value := range messages {
		result.WriteString(string(value.ID()))
		result.WriteByte(':')
		result.WriteString(string(value.Role()))
		result.WriteByte(';')
	}
	return result.String()
}

func itoa(value int) string { return strconv.Itoa(value) }

type inertStream struct{}

func (inertStream) Recv(context.Context) (model.StreamEvent, error) {
	return model.StreamEvent{}, io.EOF
}
func (inertStream) Close() error { return nil }

type recordingProvider struct {
	mu      sync.Mutex
	calls   int
	request model.Request
	stream  model.Stream
}

func (provider *recordingProvider) Stream(_ context.Context, request model.Request) (model.Stream, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.calls++
	provider.request = request
	return provider.stream, nil
}
