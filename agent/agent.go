// Package agent implements the deterministic provider-neutral execution kernel.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spice-framework/spice-agent/event"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

const maxQueuedRunEvents = 65536

// IDSource supplies stable operation identities. Applications may replace the
// default through ordinary typed Spice injection.
type IDSource interface {
	Next(prefix string) (string, error)
}

// AtomicIDSource is a deterministic process-local ID source for embedding and tests.
type AtomicIDSource struct {
	next atomic.Uint64
}

// Next returns prefix-N, starting at one.
func (source *AtomicIDSource) Next(prefix string) (string, error) {
	if source == nil {
		return "", errors.New("agent ID source is nil")
	}
	if prefix == "" || prefix != strings.TrimSpace(prefix) {
		return "", errors.New("agent ID prefix must be non-empty without surrounding whitespace")
	}
	return prefix + "-" + strconv.FormatUint(source.next.Add(1), 10), nil
}

// Definition is immutable per-run behavior selected before execution begins.
type Definition struct {
	name     string
	model    string
	maxTurns uint32
}

// NewDefinition constructs one run definition.
func NewDefinition(name, modelName string, maxTurns uint32) (Definition, error) {
	if name == "" || name != strings.TrimSpace(name) {
		return Definition{}, errors.New("agent definition name must be non-empty without surrounding whitespace")
	}
	if modelName == "" || modelName != strings.TrimSpace(modelName) {
		return Definition{}, errors.New("agent model name must be non-empty without surrounding whitespace")
	}
	if maxTurns == 0 || maxTurns > 1000 {
		return Definition{}, errors.New("agent maximum turns must be between 1 and 1000")
	}
	return Definition{name: name, model: modelName, maxTurns: maxTurns}, nil
}

// Name returns the logical agent definition name.
func (definition Definition) Name() string { return definition.name }

// Model returns the selected provider model name.
func (definition Definition) Model() string { return definition.model }

// MaxTurns returns the deterministic loop bound.
func (definition Definition) MaxTurns() uint32 { return definition.maxTurns }

// Input is immutable initial run input.
type Input struct {
	message message.Message
}

// NewInput validates an initial user message.
func NewInput(initial message.Message) (Input, error) {
	if initial.Role() != message.RoleUser {
		return Input{}, errors.New("agent input initial message must have user role")
	}
	return Input{message: initial}, nil
}

// Engine executes an already-constructed Spice graph. It is not a container.
type Engine struct {
	provider   model.Provider
	dispatcher *stage.Dispatcher
	ids        IDSource
	clock      func() time.Time
	observers  []event.Observer
	bestEffort []*event.BestEffortObserver
}

// NewEngine constructs the kernel from exact typed dependencies.
func NewEngine(provider model.Provider, dispatcher *stage.Dispatcher, ids IDSource, clock func() time.Time, observers []event.Observer, bestEffort []*event.BestEffortObserver) (*Engine, error) {
	if provider == nil {
		return nil, errors.New("agent engine requires a model provider")
	}
	if dispatcher == nil {
		return nil, errors.New("agent engine requires a tool dispatcher")
	}
	if ids == nil {
		return nil, errors.New("agent engine requires an ID source")
	}
	if clock == nil {
		return nil, errors.New("agent engine requires a clock")
	}
	for index, observer := range observers {
		if observer == nil {
			return nil, fmt.Errorf("agent observer %d is nil", index)
		}
	}
	for index, observer := range bestEffort {
		if observer == nil {
			return nil, fmt.Errorf("agent best-effort observer %d is nil", index)
		}
	}
	return &Engine{provider: provider, dispatcher: dispatcher, ids: ids, clock: clock, observers: append([]event.Observer(nil), observers...), bestEffort: append([]*event.BestEffortObserver(nil), bestEffort...)}, nil
}

// Run is one asynchronous execution and its required event subscription.
type Run struct {
	id          string
	events      chan event.Envelope
	done        chan struct{}
	cancel      context.CancelFunc
	mu          sync.Mutex
	err         error
	queueMu     sync.Mutex
	queueCond   *sync.Cond
	queue       []event.Envelope
	eventsFinal bool
}

// ID returns the stable run identity.
func (run *Run) ID() string { return run.id }

// Events returns every event in sequence and closes after the terminal event.
func (run *Run) Events() <-chan event.Envelope { return run.events }

// Cancel requests cooperative run cancellation.
func (run *Run) Cancel() { run.cancel() }

// Wait blocks for terminal finalization and returns the normalized run error.
func (run *Run) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-run.done:
		run.mu.Lock()
		defer run.mu.Unlock()
		return run.err
	}
}

// Start begins one run. Caller context ownership propagates to providers and tools.
func (engine *Engine) Start(ctx context.Context, definition Definition, input Input) (*Run, error) {
	if engine == nil {
		return nil, errors.New("agent engine is nil")
	}
	if _, err := NewDefinition(definition.name, definition.model, definition.maxTurns); err != nil {
		return nil, err
	}
	if _, err := NewInput(input.message); err != nil {
		return nil, err
	}
	runID, err := engine.ids.Next("run")
	if err != nil {
		return nil, fmt.Errorf("allocate run ID: %w", err)
	}
	runContext, cancel := context.WithCancel(ctx)
	run := &Run{id: runID, events: make(chan event.Envelope), done: make(chan struct{}), cancel: cancel}
	run.queueCond = sync.NewCond(&run.queueMu)
	go run.deliverEvents()
	go engine.execute(runContext, run, definition, input)
	return run, nil
}

func (engine *Engine) execute(ctx context.Context, run *Run, definition Definition, input Input) {
	sequencer, sequenceErr := event.NewSequencer(run.id, engine.clock)
	if sequenceErr != nil {
		engine.completeRun(run, sequenceErr)
		return
	}
	emitter := runEmitter{engine: engine, run: run, sequencer: sequencer}
	var terminal bool
	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr := fmt.Errorf("agent execution panic: %v", recovered)
			if !terminal {
				emitter.terminal(ctx, event.RunFailed, panicErr)
			}
			engine.completeRun(run, panicErr)
		}
	}()
	if err := emitter.emit(ctx, event.RunStarted, map[string]string{"definition": definition.name}); err != nil {
		terminal = true
		emitter.terminal(ctx, event.RunFailed, err)
		engine.completeRun(run, err)
		return
	}
	history := []message.Message{input.message}
	for turn := uint32(1); turn <= definition.maxTurns; turn++ {
		completed, err := engine.executeTurn(ctx, &emitter, definition, turn, &history)
		if err != nil {
			terminal = true
			kind := event.RunFailed
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				kind = event.RunCancelled
			}
			emitter.terminal(ctx, kind, err)
			engine.completeRun(run, err)
			return
		}
		if completed {
			terminal = true
			emitter.terminal(ctx, event.RunCompleted, nil)
			engine.completeRun(run, nil)
			return
		}
	}
	err := fmt.Errorf("agent run exceeded maximum turns %d", definition.maxTurns)
	terminal = true
	emitter.terminal(ctx, event.RunFailed, err)
	engine.completeRun(run, err)
}

func (engine *Engine) executeTurn(ctx context.Context, emitter *runEmitter, definition Definition, turn uint32, history *[]message.Message) (bool, error) {
	if err := emitter.emit(ctx, event.TurnStarted, map[string]uint32{"turn": turn}); err != nil {
		emitter.emitFailure(ctx, event.TurnFailed, err)
		return false, err
	}
	request, err := model.NewRequest(definition.model, *history, engine.dispatcher.Definitions())
	if err != nil {
		emitter.emitFailure(ctx, event.TurnFailed, err)
		return false, err
	}
	if err = emitter.emit(ctx, event.ModelStarted, map[string]uint32{"turn": turn}); err != nil {
		emitter.emitFailure(ctx, event.ModelFailed, err)
		emitter.emitFailure(ctx, event.TurnFailed, err)
		return false, err
	}
	stream, err := safeStream(ctx, engine.provider, request)
	if err != nil {
		emitter.emitFailure(ctx, event.ModelFailed, err)
		emitter.emitFailure(ctx, event.TurnFailed, err)
		return false, err
	}
	text, calls, err := consumeStream(ctx, emitter, stream)
	if err != nil {
		emitter.emitFailure(ctx, event.ModelFailed, err)
		emitter.emitFailure(ctx, event.TurnFailed, err)
		return false, err
	}
	if err = emitter.emit(ctx, event.ModelCompleted, map[string]uint32{"turn": turn}); err != nil {
		emitter.emitFailure(ctx, event.TurnFailed, err)
		return false, err
	}
	if len(calls) == 0 {
		if err = emitter.emit(ctx, event.TurnCompleted, map[string]uint32{"turn": turn}); err != nil {
			return false, err
		}
		return true, nil
	}
	if err = engine.appendToolRound(ctx, emitter, text, calls, history); err != nil {
		emitter.emitFailure(ctx, event.TurnFailed, err)
		return false, err
	}
	if err = emitter.emit(ctx, event.TurnCompleted, map[string]uint32{"turn": turn}); err != nil {
		return false, err
	}
	return false, nil
}

func consumeStream(ctx context.Context, emitter *runEmitter, stream model.Stream) (string, []tool.Call, error) {
	var text strings.Builder
	var calls []tool.Call
	completed := false
	for {
		streamEvent, err := safeRecv(ctx, stream)
		if err != nil {
			err = model.RequireCompletion(streamEvent, err, completed)
			if errors.Is(err, io.EOF) && completed {
				break
			}
			_ = safeClose(stream)
			return "", nil, err
		}
		if err = streamEvent.Validate(); err != nil {
			_ = safeClose(stream)
			return "", nil, fmt.Errorf("validate model stream: %w", err)
		}
		switch streamEvent.Kind {
		case model.EventTextDelta:
			text.WriteString(streamEvent.Text)
			if err = emitter.emit(ctx, event.ModelDelta, map[string]string{"text": streamEvent.Text}); err != nil {
				_ = safeClose(stream)
				return "", nil, err
			}
		case model.EventToolCall:
			calls = append(calls, streamEvent.Call)
		case model.EventCompleted:
			completed = true
		case model.EventFailed:
			_ = safeClose(stream)
			return "", nil, fmt.Errorf("model %s: %s", streamEvent.Problem.Code, streamEvent.Problem.Message)
		}
		if completed {
			break
		}
	}
	if err := safeClose(stream); err != nil {
		return "", nil, fmt.Errorf("close model stream: %w", err)
	}
	return text.String(), calls, nil
}

func (engine *Engine) appendToolRound(ctx context.Context, emitter *runEmitter, textValue string, calls []tool.Call, history *[]message.Message) error {
	parts := make([]message.Part, 0, len(calls)+1)
	if textValue != "" {
		part, err := message.Text(textValue)
		if err != nil {
			return err
		}
		parts = append(parts, part)
	}
	for _, call := range calls {
		part, err := message.ToolCall(string(call.ID), call.Name, call.Arguments)
		if err != nil {
			return err
		}
		parts = append(parts, part)
	}
	assistantMessage, err := engine.newMessage(message.RoleAssistant, parts...)
	if err != nil {
		return err
	}
	*history = append(*history, assistantMessage)
	for _, call := range calls {
		if err = emitter.emit(ctx, event.ToolStarted, map[string]string{"call_id": string(call.ID), "name": call.Name}); err != nil {
			emitter.emitFailure(ctx, event.ToolFailed, err)
			return err
		}
		result, panicErr := safeDispatch(ctx, engine.dispatcher, call, emitter)
		if panicErr != nil {
			emitter.emitFailure(ctx, event.ToolFailed, panicErr)
			return panicErr
		}
		if err = result.Validate(); err != nil {
			emitter.emitFailure(ctx, event.ToolFailed, err)
			return fmt.Errorf("validate tool %q result: %w", call.Name, err)
		}
		terminalKind := event.ToolCompleted
		if result.Error != "" {
			terminalKind = event.ToolFailed
		}
		if err = emitter.emit(ctx, terminalKind, map[string]string{"call_id": string(call.ID), "name": call.Name, "error": result.Error}); err != nil {
			return err
		}
		part, partErr := message.ToolResult(string(call.ID), call.Name, result.Content)
		if partErr != nil {
			return partErr
		}
		resultMessage, messageErr := engine.newMessage(message.RoleTool, part)
		if messageErr != nil {
			return messageErr
		}
		*history = append(*history, resultMessage)
	}
	return nil
}

func (engine *Engine) newMessage(role message.Role, parts ...message.Part) (message.Message, error) {
	id, err := engine.ids.Next("message")
	if err != nil {
		return message.Message{}, fmt.Errorf("allocate message ID: %w", err)
	}
	messageID, err := message.NewID(id)
	if err != nil {
		return message.Message{}, err
	}
	return message.New(messageID, role, parts...)
}

func safeStream(ctx context.Context, provider model.Provider, request model.Request) (stream model.Stream, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("model provider panic: %v", recovered)
		}
	}()
	return provider.Stream(ctx, request)
}

func safeRecv(ctx context.Context, stream model.Stream) (streamEvent model.StreamEvent, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("model stream panic: %v", recovered)
		}
	}()
	return stream.Recv(ctx)
}

func safeClose(stream model.Stream) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("model stream close panic: %v", recovered)
		}
	}()
	return stream.Close()
}

func safeDispatch(ctx context.Context, dispatcher stage.ToolDispatcher, call tool.Call, reporter tool.Reporter) (result tool.Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("tool %q panic: %v", call.Name, recovered)
		}
	}()
	return dispatcher.Dispatch(ctx, call, reporter), nil
}

func (engine *Engine) completeRun(run *Run, err error) {
	run.mu.Lock()
	run.err = err
	run.mu.Unlock()
	close(run.done)
	run.cancel()
	run.queueMu.Lock()
	run.eventsFinal = true
	run.queueCond.Broadcast()
	run.queueMu.Unlock()
}

func (run *Run) enqueue(envelope event.Envelope, terminal bool) error {
	run.queueMu.Lock()
	defer run.queueMu.Unlock()
	if !terminal && len(run.queue) >= maxQueuedRunEvents-1 {
		return fmt.Errorf("run event replay exceeds %d queued events", maxQueuedRunEvents)
	}
	run.queue = append(run.queue, envelope)
	run.queueCond.Signal()
	return nil
}

func (run *Run) deliverEvents() {
	defer close(run.events)
	for {
		run.queueMu.Lock()
		for len(run.queue) == 0 && !run.eventsFinal {
			run.queueCond.Wait()
		}
		if len(run.queue) == 0 && run.eventsFinal {
			run.queueMu.Unlock()
			return
		}
		envelope := run.queue[0]
		run.queue = run.queue[1:]
		run.queueMu.Unlock()
		run.events <- envelope
	}
}

type runEmitter struct {
	engine    *Engine
	run       *Run
	sequencer *event.Sequencer
}

func (emitter *runEmitter) emit(ctx context.Context, kind event.Kind, payload any) error {
	envelope, err := emitter.sequencer.Next(kind, payload)
	if err != nil {
		return err
	}
	if err = emitter.run.enqueue(envelope, false); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	for index, observer := range emitter.engine.observers {
		if err = observer.Publish(ctx, envelope); err != nil {
			return fmt.Errorf("publish event to required observer %d: %w", index, err)
		}
	}
	for _, observer := range emitter.engine.bestEffort {
		observer.TryPublish(envelope)
	}
	return nil
}

func (emitter *runEmitter) emitFailure(ctx context.Context, kind event.Kind, err error) {
	_ = emitter.emitWithoutCancellation(ctx, kind, map[string]string{"error": err.Error()})
}

func (emitter *runEmitter) terminal(ctx context.Context, kind event.Kind, err error) {
	var payload any
	if err != nil {
		payload = map[string]string{"error": err.Error()}
	}
	_ = emitter.emitWithoutCancellation(ctx, kind, payload)
}

func (emitter *runEmitter) emitWithoutCancellation(ctx context.Context, kind event.Kind, payload any) error {
	envelope, err := emitter.sequencer.Next(kind, payload)
	if err != nil {
		return err
	}
	if err = emitter.run.enqueue(envelope, true); err != nil {
		return err
	}
	for _, observer := range emitter.engine.observers {
		_ = observer.Publish(context.WithoutCancel(ctx), envelope)
	}
	for _, observer := range emitter.engine.bestEffort {
		observer.TryPublish(envelope)
	}
	return nil
}

// Report emits bounded tool progress through the canonical event stream.
func (emitter *runEmitter) Report(ctx context.Context, progress tool.Progress) error {
	if progress.CallID == "" || strings.TrimSpace(progress.Message) == "" {
		return errors.New("tool progress requires call ID and message")
	}
	if len(progress.Message) > 4096 {
		return errors.New("tool progress message exceeds 4096 bytes")
	}
	encoded, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	return emitter.emit(ctx, event.ToolProgress, json.RawMessage(encoded))
}
