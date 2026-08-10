package twoworker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spice-framework/spice-agent/client"
	"github.com/spice-framework/spice-agent/tool"
)

const (
	// ToolName is the canonical model-visible and Spice bean identity.
	ToolName = "worker.delegate"

	defaultMaximumEvents    = uint32(512)
	defaultMaximumTextBytes = 1 << 20
	maximumTaskBytes        = 64 << 10
	remoteCancelTimeout     = 2 * time.Second
)

var inputSchema = json.RawMessage(`{"type":"object","properties":{"task":{"type":"string","minLength":1,"maxLength":65536}},"required":["task"],"additionalProperties":false}`)

// Options binds the exact generated worker definition and local collection
// bounds. A zero event or text bound selects the conservative defaults.
type Options struct {
	Definition       client.DefinitionRef
	MaximumEvents    uint32
	MaximumTextBytes int
}

// Delegate is an ordinary constructor-injected tool. It owns no process,
// endpoint, registry, scheduler, or session lifecycle.
type Delegate struct {
	session          client.Session
	definition       client.DefinitionRef
	maximumEvents    uint32
	maximumTextBytes int
	toolDefinition   tool.Definition
}

// NewDelegate validates the injected public client boundary and exact worker
// definition. The session remains owned by its application.
func NewDelegate(session client.Session, options Options) (*Delegate, error) {
	if session == nil {
		return nil, errors.New("worker delegate session is nil")
	}
	if err := options.Definition.Validate(); err != nil {
		return nil, fmt.Errorf("worker delegate definition: %w", err)
	}
	maximumEvents := options.MaximumEvents
	if maximumEvents == 0 {
		maximumEvents = defaultMaximumEvents
	}
	limits := session.Connection().Limits()
	if err := limits.Validate(); err != nil {
		return nil, errors.New("worker delegate session limits are invalid")
	}
	if maximumEvents > limits.ReplayEvents() && options.MaximumEvents == 0 {
		maximumEvents = limits.ReplayEvents()
	}
	if maximumEvents > limits.ReplayEvents() {
		return nil, fmt.Errorf("worker delegate event bound exceeds negotiated replay limit %d", limits.ReplayEvents())
	}
	maximumTextBytes := options.MaximumTextBytes
	if maximumTextBytes == 0 {
		maximumTextBytes = defaultMaximumTextBytes
	}
	if maximumTextBytes < 1 || maximumTextBytes > client.MaximumTextBytes {
		return nil, fmt.Errorf("worker delegate text bound must be between 1 and %d", client.MaximumTextBytes)
	}
	definition, err := tool.NewDefinition(
		ToolName,
		"Delegate one bounded task to a separately hosted ordinary Spice Agent run.",
		inputSchema,
		tool.EffectMutating,
		tool.ReplayIdempotent,
		tool.CapabilityNetworkAccess,
	)
	if err != nil {
		return nil, fmt.Errorf("construct worker delegate definition: %w", err)
	}
	return &Delegate{
		session: session, definition: options.Definition,
		maximumEvents: maximumEvents, maximumTextBytes: maximumTextBytes,
		toolDefinition: definition,
	}, nil
}

// Definition returns the immutable tool contract.
func (delegate *Delegate) Definition() tool.Definition {
	if delegate == nil {
		return tool.Definition{}
	}
	return delegate.toolDefinition.Clone()
}

type arguments struct {
	Task string `json:"task"`
}

type resultPayload struct {
	OK     bool   `json:"ok"`
	RunID  string `json:"run_id"`
	PlanID string `json:"plan_id"`
	Text   string `json:"text"`
}

type errorPayload struct {
	OK   bool   `json:"ok"`
	Code string `json:"code"`
}

// Execute starts or resumes the exact idempotent remote operation and consumes
// its ordinary event stream to one run terminal. Cancellation is propagated to
// the remote run through a distinct deterministic mutation identity.
func (delegate *Delegate) Execute(ctx context.Context, call tool.Call, reporter tool.Reporter) (tool.Result, error) {
	if delegate == nil || delegate.session == nil {
		return executionFailure(call.ID(), tool.ExecutionDefinitive, tool.RetryAllowed, "worker delegate is unavailable", nil)
	}
	if ctx == nil {
		return executionFailure(call.ID(), tool.ExecutionDefinitive, tool.RetryAllowed, "worker delegate context is nil", nil)
	}
	if err := call.Validate(); err != nil || call.Name() != ToolName {
		return modelFailure(call.ID(), "invalid_arguments", "worker delegate call is invalid")
	}
	if err := ctx.Err(); err != nil {
		return executionFailure(call.ID(), tool.ExecutionDefinitive, tool.RetryAllowed, "worker delegation was canceled", err)
	}
	var input arguments
	if err := decodeArguments(call.Arguments(), &input); err != nil || input.Task != strings.TrimSpace(input.Task) || input.Task == "" || len(input.Task) > maximumTaskBytes {
		return modelFailure(call.ID(), "invalid_arguments", "worker delegation requires one bounded trimmed task")
	}
	identity := callIdentity(call.ID())
	operation, _ := client.NewOperationID("worker.delegate.start." + identity)
	messageID := "worker.delegate.message." + identity
	message, err := client.NewInput(messageID, input.Task)
	if err != nil {
		return modelFailure(call.ID(), "invalid_arguments", "worker delegation task is invalid")
	}
	request, err := client.NewStartRequest(operation, delegate.definition, message)
	if err != nil {
		return executionFailure(call.ID(), tool.ExecutionDefinitive, tool.RetryAllowed, "worker delegation request could not be constructed", err)
	}
	started, err := delegate.session.Start(ctx, request)
	if err != nil {
		if _, uncertain := errors.AsType[*client.UncertainOperationError](err); uncertain {
			return executionFailure(call.ID(), tool.ExecutionUncertain, tool.RetryNever, "worker delegation start outcome is uncertain", err)
		}
		return executionFailure(call.ID(), tool.ExecutionDefinitive, tool.RetryAllowed, "worker delegation could not start", err)
	}
	if err = report(ctx, reporter, call.ID(), "delegated worker run started"); err != nil {
		delegate.cancelRemote(started.Run(), identity)
		return executionFailure(call.ID(), tool.ExecutionUncertain, tool.RetryNever, "worker delegation progress was rejected", err)
	}
	return delegate.consume(ctx, call.ID(), identity, started)
}

func (delegate *Delegate) consume(ctx context.Context, callID tool.CallID, identity string, started client.StartResult) (tool.Result, error) {
	cursor, err := client.NewCursor(started.Run(), 0)
	if err != nil {
		return executionFailure(callID, tool.ExecutionUncertain, tool.RetryNever, "worker delegation cursor is invalid", err)
	}
	options, err := client.NewEventStreamOptions(delegate.maximumEvents, true, delegate.session.Connection().Limits())
	if err != nil {
		return executionFailure(callID, tool.ExecutionUncertain, tool.RetryNever, "worker delegation stream bounds are invalid", err)
	}
	stream, err := delegate.session.Events(ctx, cursor, options)
	if err != nil {
		if ctx.Err() != nil {
			delegate.cancelRemote(started.Run(), identity)
		}
		return executionFailure(callID, tool.ExecutionUncertain, tool.RetryNever, "worker delegation event stream could not open", err)
	}
	defer stream.Close() //nolint:errcheck // local close cannot change a terminal outcome.
	var text strings.Builder
	var observed uint32
	for {
		frame, nextErr := stream.Next(ctx)
		if nextErr != nil {
			if ctx.Err() != nil {
				delegate.cancelRemote(started.Run(), identity)
			}
			return executionFailure(callID, tool.ExecutionUncertain, tool.RetryNever, "worker delegation event stream ended before a run terminal", nextErr)
		}
		event, ok := frame.Event()
		if !ok {
			continue
		}
		observed++
		if observed > delegate.maximumEvents {
			delegate.cancelRemote(started.Run(), identity)
			return executionFailure(callID, tool.ExecutionUncertain, tool.RetryNever, "worker delegation exceeded its event bound", nil)
		}
		if event.Kind() == client.EventModelDelta {
			value, _ := event.Detail().Text()
			if text.Len()+len(value) > delegate.maximumTextBytes {
				delegate.cancelRemote(started.Run(), identity)
				return executionFailure(callID, tool.ExecutionUncertain, tool.RetryNever, "worker delegation exceeded its text bound", nil)
			}
			text.WriteString(value)
		}
		switch event.Kind() {
		case client.EventRunCompleted:
			return success(callID, resultPayload{OK: true, RunID: started.Run().ID(), PlanID: started.PlanID(), Text: text.String()})
		case client.EventRunFailed:
			return modelFailure(callID, "worker_failed", "delegated worker run failed")
		case client.EventRunCancelled:
			return modelFailure(callID, "worker_cancelled", "delegated worker run was canceled")
		}
	}
}

func (delegate *Delegate) cancelRemote(run client.RunRef, identity string) {
	ctx, cancel := context.WithTimeout(context.Background(), remoteCancelTimeout)
	defer cancel()
	operation, err := client.NewOperationID("worker.delegate.cancel." + identity)
	if err != nil {
		return
	}
	request, err := client.NewCancelRequest(run, operation, "delegating caller canceled")
	if err != nil {
		return
	}
	_, _ = delegate.session.Cancel(ctx, request)
}

func callIdentity(callID tool.CallID) string {
	digest := sha256.Sum256([]byte(callID))
	return hex.EncodeToString(digest[:16])
}

func decodeArguments(value json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func report(ctx context.Context, reporter tool.Reporter, callID tool.CallID, message string) error {
	if reporter == nil {
		return nil
	}
	progress, err := tool.NewProgress(callID, message)
	if err != nil {
		return err
	}
	return reporter.Report(ctx, progress)
}

func success(callID tool.CallID, payload any) (tool.Result, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return executionFailure(callID, tool.ExecutionDefinitive, tool.RetryAllowed, "worker delegation result could not be encoded", err)
	}
	result, err := tool.NewResult(callID, encoded)
	if err != nil {
		return executionFailure(callID, tool.ExecutionDefinitive, tool.RetryAllowed, "worker delegation result is invalid", err)
	}
	return result, nil
}

func modelFailure(callID tool.CallID, code, problem string) (tool.Result, error) {
	encoded, err := json.Marshal(errorPayload{OK: false, Code: code})
	if err != nil {
		return executionFailure(callID, tool.ExecutionDefinitive, tool.RetryAllowed, "worker delegation failure could not be encoded", err)
	}
	result, err := tool.NewErrorResult(callID, encoded, problem)
	if err != nil {
		return executionFailure(callID, tool.ExecutionDefinitive, tool.RetryAllowed, "worker delegation failure is invalid", err)
	}
	return result, nil
}

func executionFailure(callID tool.CallID, state tool.ExecutionState, retry tool.RetryDisposition, message string, cause error) (tool.Result, error) {
	if callID == "" {
		callID = "worker.delegate.invalid"
	}
	if cause == nil {
		cause = errors.New(message)
	} else {
		cause = fmt.Errorf("%s: %w", message, cause)
	}
	failure, err := tool.NewExecutionError(callID, state, retry, cause)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{}, failure
}
