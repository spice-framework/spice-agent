package gitworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spice-framework/spice-agent/tool"
)

const (
	// InspectToolName is the canonical inspection bean and model identity.
	InspectToolName = "git.inspect"
	// CommitStagedToolName is the only mutating Git operation exposed.
	CommitStagedToolName      = "git.commit_staged"
	maximumCommitMessageBytes = 4096
)

// Inspection is bounded model-visible repository state.
type Inspection struct {
	Status       string `json:"status"`
	StagedDigest string `json:"staged_digest,omitempty"`
}

// Backend is the narrow application-owned Git execution boundary.
type Backend interface {
	Inspect(context.Context) (Inspection, error)
	StagedDigest(context.Context) (string, error)
	CommitStaged(context.Context, string, string) error
}

// InspectTool exposes only the fixed repository inspection.
type InspectTool struct {
	backend    Backend
	definition tool.Definition
}

// NewInspectTool constructs git.inspect. It is conservatively mutation-
// classified because the shared contract treats process execution as a
// mutation-capable capability even though optional Git locks are disabled.
func NewInspectTool(backend Backend) (*InspectTool, error) {
	if backend == nil {
		return nil, errors.New("git inspect backend is required")
	}
	definition, err := tool.NewDefinition(
		InspectToolName,
		"Inspect the configured Git worktree with a fixed, bounded porcelain status command.",
		json.RawMessage(`{"type":"object","additionalProperties":false}`),
		tool.EffectMutating,
		tool.ReplayIdempotent,
		tool.CapabilityFilesystemRead,
		tool.CapabilityProcessExecute,
	)
	if err != nil {
		return nil, err
	}
	return &InspectTool{backend: backend, definition: definition}, nil
}

func (implementation *InspectTool) Definition() tool.Definition {
	return implementation.definition.Clone()
}

// Execute rejects every argument and returns bounded status JSON.
func (implementation *InspectTool) Execute(
	ctx context.Context,
	call tool.Call,
	_ tool.Reporter,
) (tool.Result, error) {
	if implementation == nil || implementation.backend == nil {
		return tool.Result{}, executionFailure(call.ID(), false, errors.New("git inspect is unavailable"))
	}
	if err := validateCall(call, InspectToolName); err != nil {
		return tool.Result{}, executionFailure(call.ID(), false, err)
	}
	if err := decodeEmptyArguments(call.Arguments()); err != nil {
		return tool.Result{}, executionFailure(call.ID(), false, err)
	}
	inspection, err := implementation.backend.Inspect(ctx)
	if err != nil {
		return tool.Result{}, executionFailure(call.ID(), operationUncertain(err), err)
	}
	content, err := json.Marshal(inspection)
	if err != nil {
		return tool.Result{}, executionFailure(call.ID(), false, errors.New("encode git inspection"))
	}
	return tool.NewResult(call.ID(), content)
}

// CommitStagedTool commits only the already-staged index after consuming the
// current guard grant.
type CommitStagedTool struct {
	backend    Backend
	store      *AuthorityStore
	definition tool.Definition
}

// NewCommitStagedTool constructs git.commit_staged.
func NewCommitStagedTool(backend Backend, store *AuthorityStore) (*CommitStagedTool, error) {
	if backend == nil || store == nil {
		return nil, errors.New("git commit backend and authority store are required")
	}
	definition, err := tool.NewDefinition(
		CommitStagedToolName,
		"Commit exactly the configured repository's already-staged index after explicit interaction approval.",
		json.RawMessage(`{"type":"object","additionalProperties":false,"required":["message"],"properties":{"message":{"type":"string","minLength":1,"maxLength":4096}}}`),
		tool.EffectMutating,
		tool.ReplayUnsafe,
		tool.CapabilityFilesystemRead,
		tool.CapabilityFilesystemWrite,
		tool.CapabilityProcessExecute,
	)
	if err != nil {
		return nil, err
	}
	return &CommitStagedTool{backend: backend, store: store, definition: definition}, nil
}

func (implementation *CommitStagedTool) Definition() tool.Definition {
	return implementation.definition.Clone()
}

// Execute consumes one exact grant, rechecks the staged digest, and invokes no
// Git operation other than the fixed commit command.
func (implementation *CommitStagedTool) Execute(
	ctx context.Context,
	call tool.Call,
	_ tool.Reporter,
) (tool.Result, error) {
	if implementation == nil || implementation.backend == nil || implementation.store == nil {
		return tool.Result{}, executionFailure(call.ID(), false, errors.New("git commit is unavailable"))
	}
	if err := validateCall(call, CommitStagedToolName); err != nil {
		return tool.Result{}, executionFailure(call.ID(), false, err)
	}
	arguments, err := decodeCommitArguments(call.Arguments())
	if err != nil {
		return tool.Result{}, executionFailure(call.ID(), false, err)
	}
	stagedDigest, err := implementation.store.consume(call)
	if err != nil {
		return tool.Result{}, executionFailure(call.ID(), false, err)
	}
	if err = implementation.backend.CommitStaged(ctx, arguments.Message, stagedDigest); err != nil {
		return tool.Result{}, executionFailure(call.ID(), operationUncertain(err), err)
	}
	content, err := json.Marshal(struct {
		Committed    bool   `json:"committed"`
		StagedDigest string `json:"staged_digest"`
	}{Committed: true, StagedDigest: stagedDigest})
	if err != nil {
		return tool.Result{}, executionFailure(call.ID(), true, errors.New("encode git commit result"))
	}
	return tool.NewResult(call.ID(), content)
}

type commitArguments struct {
	Message string `json:"message"`
}

func decodeCommitArguments(raw json.RawMessage) (commitArguments, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var result commitArguments
	if err := decoder.Decode(&result); err != nil {
		return commitArguments{}, errors.New("git commit arguments are invalid")
	}
	if err := requireJSONEnd(decoder); err != nil {
		return commitArguments{}, err
	}
	if result.Message == "" || result.Message != strings.TrimSpace(result.Message) ||
		len(result.Message) > maximumCommitMessageBytes || strings.ContainsRune(result.Message, 0) {
		return commitArguments{}, errors.New("git commit message is invalid")
	}
	return result, nil
}

func decodeEmptyArguments(raw json.RawMessage) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	var result map[string]json.RawMessage
	if err := decoder.Decode(&result); err != nil {
		return errors.New("git inspect arguments are invalid")
	}
	if result == nil || len(result) != 0 {
		return errors.New("git inspect arguments are invalid")
	}
	return requireJSONEnd(decoder)
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("git arguments contain trailing data")
	}
	return nil
}

func validateCall(call tool.Call, name string) error {
	if err := call.Validate(); err != nil || call.Name() != name {
		return fmt.Errorf("git tool call does not match %s", name)
	}
	return nil
}

func executionFailure(callID tool.CallID, uncertain bool, cause error) error {
	state, retry := tool.ExecutionDefinitive, tool.RetryAllowed
	if uncertain {
		state, retry = tool.ExecutionUncertain, tool.RetryNever
	}
	failure, err := tool.NewExecutionError(callID, state, retry, fixedOperationFailure{cause: cause})
	if err != nil {
		return errors.New("git tool execution failed")
	}
	return failure
}

type fixedOperationFailure struct{ cause error }

func (fixedOperationFailure) Error() string         { return "git workflow operation failed" }
func (failure fixedOperationFailure) Unwrap() error { return failure.cause }

type operationFailure struct {
	uncertain bool
	cause     error
}

func (*operationFailure) Error() string { return "git workflow operation failed" }
func (failure *operationFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

func operationUncertain(err error) bool {
	var failure *operationFailure
	return errors.As(err, &failure) && failure.uncertain
}

var (
	_ tool.Tool = (*InspectTool)(nil)
	_ tool.Tool = (*CommitStagedTool)(nil)
)
