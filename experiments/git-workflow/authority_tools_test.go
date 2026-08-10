package gitworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/spice-framework/spice-agent/interaction"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

const testFingerprint = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestCommitGuardBindsInteractionGrantToStagedIndex(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{staged: testFingerprint}
	store := NewAuthorityStore()
	inspect, commit, guard := mustGitTools(t, backend, store)
	dispatcher := mustDispatcher(t, inspect, commit, guard)
	call := mustCall(t, "call-commit", CommitStagedToolName, `{"message":"commit staged proof"}`)
	result, err := dispatcher.Dispatch(t.Context(), mustScope(t, approvingRequester{}), call, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.CallID() != call.ID() || backend.commitMessage != "commit staged proof" || backend.expectedDigest != testFingerprint {
		t.Fatalf("commit result/backend = %q/%q/%q", result.CallID(), backend.commitMessage, backend.expectedDigest)
	}
	if _, err = store.consume(call); !errors.Is(err, ErrAuthorizationRequired) {
		t.Fatalf("grant survived tool return: %v", err)
	}
}

func TestCommitToolFailsClosedWithoutGuard(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{staged: testFingerprint}
	implementation, err := NewCommitStagedTool(backend, NewAuthorityStore())
	if err != nil {
		t.Fatal(err)
	}
	call := mustCall(t, "call-direct", CommitStagedToolName, `{"message":"forbidden"}`)
	result, err := implementation.Execute(t.Context(), call, nil)
	if !result.IsZero() || !errors.Is(err, ErrAuthorizationRequired) || backend.commitMessage != "" {
		t.Fatalf("direct commit = %#v, %v, backend message %q", result, err, backend.commitMessage)
	}
	assertExecutionState(t, err, tool.ExecutionDefinitive, tool.RetryAllowed)
}

func TestCommitGuardDenialAndCancellationNeverGrant(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		requester interaction.Requester
		want      error
	}{
		{name: "wrong token", requester: fixedRequester{value: json.RawMessage(`"wrong"`)}, want: ErrAuthorizationDenied},
		{name: "request failure", requester: fixedRequester{err: errors.New("broker detail")}, want: ErrAuthorizationFailed},
		{name: "panic", requester: panicRequester{}, want: ErrAuthorizationFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			backend := &fakeBackend{staged: testFingerprint}
			store := NewAuthorityStore()
			inspect, commit, guard := mustGitTools(t, backend, store)
			dispatcher := mustDispatcher(t, inspect, commit, guard)
			call := mustCall(t, "call-denied", CommitStagedToolName, `{"message":"denied"}`)
			result, err := dispatcher.Dispatch(t.Context(), mustScope(t, test.requester), call, nil)
			if !result.IsZero() || !errors.Is(err, test.want) || backend.commitMessage != "" {
				t.Fatalf("dispatch = %#v, %v, backend message %q", result, err, backend.commitMessage)
			}
			if _, err = store.consume(call); !errors.Is(err, ErrAuthorizationRequired) {
				t.Fatalf("denial left grant: %v", err)
			}
		})
	}

	backend := &fakeBackend{staged: testFingerprint}
	store := NewAuthorityStore()
	inspect, commit, guard := mustGitTools(t, backend, store)
	dispatcher := mustDispatcher(t, inspect, commit, guard)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := dispatcher.Dispatch(ctx, mustScope(t, approvingRequester{}), mustCall(t, "call-cancel", CommitStagedToolName, `{"message":"canceled"}`), nil)
	if !errors.Is(err, context.Canceled) || backend.commitMessage != "" {
		t.Fatalf("canceled dispatch = %v, backend message %q", err, backend.commitMessage)
	}
}

func TestInspectUsesSamePipelineWithoutInteraction(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{inspection: Inspection{Status: "# branch.head main\n"}}
	inspect, commit, guard := mustGitTools(t, backend, NewAuthorityStore())
	dispatcher := mustDispatcher(t, inspect, commit, guard)
	call := mustCall(t, "call-inspect", InspectToolName, `{}`)
	result, err := dispatcher.Dispatch(t.Context(), mustScope(t, interaction.UnavailableRequester{}), call, nil)
	if err != nil || result.CallID() != call.ID() || backend.inspectCalls != 1 {
		t.Fatalf("inspect = %#v, %v, calls %d", result, err, backend.inspectCalls)
	}
}

func TestAuthorityTokenIsDeterministicAndBound(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{}
	_, commit, _ := mustGitTools(t, backend, NewAuthorityStore())
	scope := mustScope(t, interaction.UnavailableRequester{})
	call := mustCall(t, "call-token", CommitStagedToolName, `{"message":"one"}`)
	first, err := AuthorityToken(scope, commit.Definition(), call, testFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AuthorityToken(scope, commit.Definition(), call, testFingerprint)
	if err != nil || first != second || !validDigest(first) {
		t.Fatalf("token = %q/%q, %v", first, second, err)
	}
	changedCall := mustCall(t, "call-token", CommitStagedToolName, `{"message":"two"}`)
	changed, err := AuthorityToken(scope, commit.Definition(), changedCall, testFingerprint)
	if err != nil || changed == first {
		t.Fatalf("changed token = %q, %v", changed, err)
	}
}

func TestConcurrentCommitGrantsRemainIsolated(t *testing.T) {
	backend := &fakeBackend{staged: testFingerprint}
	store := NewAuthorityStore()
	inspect, commit, guard := mustGitTools(t, backend, store)
	dispatcher := mustDispatcher(t, inspect, commit, guard)
	var group sync.WaitGroup
	for index := range 32 {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			call := mustCall(t, tool.CallID(fmt.Sprintf("call-concurrent-%d", index)), CommitStagedToolName, fmt.Sprintf(`{"message":"commit %d"}`, index))
			if _, err := dispatcher.Dispatch(t.Context(), mustScope(t, approvingRequester{}), call, nil); err != nil {
				t.Error(err)
			}
		}(index)
	}
	group.Wait()
	if backend.commitCalls != 32 {
		t.Fatalf("commit calls = %d", backend.commitCalls)
	}
}

func TestArgumentDecodersFailClosed(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{}`, `{"message":""}`, `{"message":" x "}`, `{"message":"x","extra":true}`,
		`{"message":"x"} {}`, `[]`, `null`,
	} {
		if _, err := decodeCommitArguments(json.RawMessage(raw)); err == nil {
			t.Fatalf("decodeCommitArguments(%q) succeeded", raw)
		}
	}
	for _, raw := range []string{`{"extra":true}`, `[]`, `null`, `{} {}`} {
		if err := decodeEmptyArguments(json.RawMessage(raw)); err == nil {
			t.Fatalf("decodeEmptyArguments(%q) succeeded", raw)
		}
	}
}

func FuzzDecodeCommitArguments(f *testing.F) {
	for _, seed := range []string{`{"message":"valid"}`, `{}`, `null`, `{"message":1}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		result, err := decodeCommitArguments(json.RawMessage(raw))
		if err == nil && (result.Message == "" || result.Message != strings.TrimSpace(result.Message) || len(result.Message) > maximumCommitMessageBytes) {
			t.Fatalf("accepted invalid result %#v", result)
		}
	})
}

func BenchmarkAuthorityToken(b *testing.B) {
	_, commit, _ := mustGitTools(b, &fakeBackend{}, NewAuthorityStore())
	scope := mustScope(b, interaction.UnavailableRequester{})
	call := mustCall(b, "call-benchmark", CommitStagedToolName, `{"message":"benchmark"}`)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := AuthorityToken(scope, commit.Definition(), call, testFingerprint); err != nil {
			b.Fatal(err)
		}
	}
}

type fakeBackend struct {
	mu             sync.Mutex
	inspection     Inspection
	staged         string
	inspectCalls   int
	commitCalls    int
	commitMessage  string
	expectedDigest string
	err            error
}

func (backend *fakeBackend) Inspect(context.Context) (Inspection, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.inspectCalls++
	return backend.inspection, backend.err
}

func (backend *fakeBackend) StagedDigest(context.Context) (string, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.staged, backend.err
}

func (backend *fakeBackend) CommitStaged(_ context.Context, message, digest string) error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	backend.commitCalls++
	backend.commitMessage, backend.expectedDigest = message, digest
	return backend.err
}

type approvingRequester struct{}

func (approvingRequester) Request(_ context.Context, request interaction.Request) (interaction.Response, error) {
	var schema struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(request.Schema(), &schema); err != nil || !validDigest(schema.Const) || !strings.Contains(request.Prompt(), schema.Const) {
		return interaction.Response{}, errors.New("invalid authority prompt")
	}
	value, _ := json.Marshal(schema.Const)
	return interaction.NewResponse(request.ID(), value)
}

type fixedRequester struct {
	value json.RawMessage
	err   error
}

func (requester fixedRequester) Request(_ context.Context, request interaction.Request) (interaction.Response, error) {
	if requester.err != nil {
		return interaction.Response{}, requester.err
	}
	return interaction.NewResponse(request.ID(), requester.value)
}

type panicRequester struct{}

func (panicRequester) Request(context.Context, interaction.Request) (interaction.Response, error) {
	panic("secret")
}

func mustGitTools(t testing.TB, backend Backend, store *AuthorityStore) (*InspectTool, *CommitStagedTool, *CommitGuard) {
	t.Helper()
	inspect, err := NewInspectTool(backend)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := NewCommitStagedTool(backend, store)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := NewCommitGuard(backend, store)
	if err != nil {
		t.Fatal(err)
	}
	return inspect, commit, guard
}

func mustDispatcher(t testing.TB, inspect, commit tool.Tool, guard stage.ToolDispatchGuard) stage.ToolDispatcher {
	t.Helper()
	base, err := stage.NewDispatcher(map[string]tool.Tool{InspectToolName: inspect, CommitStagedToolName: commit})
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := stage.ApplyToolDispatchPipeline(base, []stage.ToolDispatchGuard{guard}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pipeline
}

func mustScope(t testing.TB, requester interaction.Requester) stage.ToolDispatchScope {
	t.Helper()
	authority, err := interaction.NewScope("run-git-proof")
	if err != nil {
		t.Fatal(err)
	}
	planID, err := stage.NewPlanID("git-plan-proof")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := stage.NewToolDispatchScope(authority.RunID(), 1, planID, testFingerprint, testFingerprint, authority, requester)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func mustCall(t testing.TB, id tool.CallID, name, arguments string) tool.Call {
	t.Helper()
	call, err := tool.NewCall(id, name, json.RawMessage(arguments))
	if err != nil {
		t.Fatal(err)
	}
	return call
}

func assertExecutionState(t testing.TB, err error, state tool.ExecutionState, retry tool.RetryDisposition) {
	t.Helper()
	failure, ok := errors.AsType[*tool.ExecutionError](err)
	if !ok || failure.State() != state || failure.RetryDisposition() != retry {
		t.Fatalf("execution error = %#v, %v", failure, err)
	}
}
