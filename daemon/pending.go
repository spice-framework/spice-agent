package daemon

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/spice-framework/spice-agent/interaction"
)

// ErrBrokerClosed rejects requests after daemon broker shutdown.
var ErrBrokerClosed = errors.New("pending interaction broker is closed")

var closedDeltaStream = func() <-chan Delta {
	stream := make(chan Delta)
	close(stream)
	return stream
}()

const (
	maximumPendingInteractions = 4096
	maximumPendingObservers    = 1024
	maximumObserverQueue       = 1024
	maximumObserverQueuedBytes = 4 << 20
	maximumPendingBytes        = 16 << 20
	maximumObserverBytes       = 64 << 20
	maximumPendingDeltaBytes   = 2*interaction.MaximumPayloadBytes + 512
)

// ObserverExhaustedError reports the exact last revision delivered to a slow
// subscriber. Recovery always creates a new subscription and consumes its
// mandatory complete snapshot; deltas alone are never a discovery mechanism.
type ObserverExhaustedError struct{ LastDelivered uint64 }

func (failure *ObserverExhaustedError) Error() string {
	return fmt.Sprintf("pending interaction observer exhausted after revision %d", failure.LastDelivered)
}

// Pending is one immutable pending interaction discovery value.
type Pending struct {
	Scope   interaction.Scope
	Request interaction.Request
}

// DeltaKind identifies a pending-interaction lifecycle mutation.
type DeltaKind string

const (
	// DeltaOpened adds a newly pending request after the complete snapshot.
	DeltaOpened DeltaKind = "opened"
	// DeltaClosed removes a completed or canceled request.
	DeltaClosed DeltaKind = "closed"
)

// Delta is one revision-contiguous mutation following a subscription snapshot.
type Delta struct {
	Revision uint64
	Kind     DeltaKind
	Pending  Pending
}

// PendingSnapshot is the mandatory complete first view for every subscription.
type PendingSnapshot struct {
	Revision uint64
	Pending  []Pending
}

type pendingKey struct {
	runID         string
	interactionID interaction.ID
}

type pendingResult struct {
	response interaction.Response
	err      error
}

type pendingCall struct {
	value  Pending
	done   chan struct{}
	result pendingResult
}

type pendingWatcher struct {
	queue        chan Delta
	out          chan Delta
	stop         chan struct{}
	done         chan struct{}
	terminalOnce sync.Once
	stopOnce     sync.Once
	queueOnce    sync.Once

	mu            sync.Mutex
	err           error
	exhausted     bool
	lastDelivered uint64
	queuedBytes   int
}

// PendingSubscription atomically couples a complete snapshot with a live tail
// registered at exactly that snapshot revision.
type PendingSubscription struct {
	broker   *PendingBroker
	id       uint64
	snapshot PendingSnapshot
	watcher  *pendingWatcher
}

// Snapshot returns the complete immutable first frame.
func (subscription *PendingSubscription) Snapshot() PendingSnapshot {
	if subscription == nil {
		return PendingSnapshot{}
	}
	return clonePendingSnapshot(subscription.snapshot)
}

// Deltas returns revision-contiguous changes following Snapshot.
func (subscription *PendingSubscription) Deltas() <-chan Delta {
	if subscription == nil || subscription.watcher == nil {
		return closedDeltaStream
	}
	return subscription.watcher.out
}

// LastDelivered returns the exact revision whose delta was most recently sent
// to the consumer, or the snapshot revision before the first delta.
func (subscription *PendingSubscription) LastDelivered() uint64 {
	if subscription == nil || subscription.watcher == nil {
		return 0
	}
	subscription.watcher.mu.Lock()
	defer subscription.watcher.mu.Unlock()
	return subscription.watcher.lastDelivered
}

// Wait reports broker shutdown, subscriber cancellation, or typed exhaustion.
func (subscription *PendingSubscription) Wait(ctx context.Context) error {
	if subscription == nil || subscription.watcher == nil {
		return errors.New("pending subscription is nil")
	}
	if ctx == nil {
		return errors.New("pending subscription wait context is nil")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-subscription.watcher.done:
		return subscription.watcher.terminalError()
	}
}

// PendingBroker implements interaction.Broker and bounded, complete-first
// interaction discovery for daemon clients.
type PendingBroker struct {
	mu                                  sync.Mutex
	maxPending, maxObservers, queueSize int
	pendingBytes                        int
	revision, nextWatcher               uint64
	pending                             map[pendingKey]*pendingCall
	watchers                            map[uint64]*pendingWatcher
	closed                              bool
}

// NewPendingBroker constructs a broker with explicit pending, subscriber, and
// per-subscriber queue bounds.
func NewPendingBroker(maxPending, maxObservers, observerQueue int) (*PendingBroker, error) {
	if maxPending < 1 || maxPending > maximumPendingInteractions ||
		maxObservers < 1 || maxObservers > maximumPendingObservers ||
		observerQueue < 1 || observerQueue > maximumObserverQueue {
		return nil, fmt.Errorf(
			"pending broker bounds must be within pending=[1,%d] observers=[1,%d] queue=[1,%d]",
			maximumPendingInteractions, maximumPendingObservers, maximumObserverQueue,
		)
	}
	perObserverBytes := maximumObserverQueuedBytes
	if observerQueue <= maximumObserverQueuedBytes/maximumPendingDeltaBytes {
		perObserverBytes = observerQueue * maximumPendingDeltaBytes
	}
	if maxObservers > maximumObserverBytes/perObserverBytes {
		return nil, fmt.Errorf("pending observer aggregate byte budget exceeds %d", maximumObserverBytes)
	}
	return &PendingBroker{
		maxPending: maxPending, maxObservers: maxObservers, queueSize: observerQueue,
		pending: make(map[pendingKey]*pendingCall), watchers: make(map[uint64]*pendingWatcher),
	}, nil
}

func makePendingKey(scope interaction.Scope, id interaction.ID) pendingKey {
	return pendingKey{runID: scope.RunID(), interactionID: id}
}

// Request publishes and awaits one run-scoped interaction.
func (broker *PendingBroker) Request(ctx context.Context, scope interaction.Scope, request interaction.Request) (interaction.Response, error) {
	if broker == nil {
		return interaction.Response{}, ErrBrokerClosed
	}
	if ctx == nil {
		return interaction.Response{}, errors.New("pending interaction context is nil")
	}
	if err := ctx.Err(); err != nil {
		return interaction.Response{}, err
	}
	if err := scope.Validate(); err != nil {
		return interaction.Response{}, err
	}
	if err := request.Validate(); err != nil {
		return interaction.Response{}, err
	}
	key := makePendingKey(scope, request.ID())
	call := &pendingCall{value: clonePending(Pending{Scope: scope, Request: request}), done: make(chan struct{})}
	broker.mu.Lock()
	if broker.closed {
		broker.mu.Unlock()
		return interaction.Response{}, ErrBrokerClosed
	}
	if _, exists := broker.pending[key]; exists {
		broker.mu.Unlock()
		return interaction.Response{}, errors.New("interaction is already pending")
	}
	pendingBytes := pendingValueBytes(call.value)
	if len(broker.pending) >= broker.maxPending || pendingBytes > maximumPendingBytes ||
		broker.pendingBytes > maximumPendingBytes-pendingBytes {
		broker.mu.Unlock()
		return interaction.Response{}, errors.New("pending interaction capacity exhausted")
	}
	requiredRevisions := uint64(len(broker.pending)) + 2 // open plus one reserved close for every pending call.
	if broker.revision > math.MaxUint64-requiredRevisions {
		broker.mu.Unlock()
		return interaction.Response{}, errors.New("pending interaction revision capacity exhausted")
	}
	broker.pending[key] = call
	broker.pendingBytes += pendingBytes
	broker.publishLocked(DeltaOpened, call.value)
	broker.mu.Unlock()

	select {
	case <-call.done:
		return cloneResponse(call.result.response), call.result.err
	case <-ctx.Done():
		result := broker.complete(key, call, pendingResult{err: ctx.Err()})
		return cloneResponse(result.response), result.err
	}
}

// Respond atomically commits the only response. Once it succeeds, cancellation
// or broker shutdown cannot replace the accepted response.
func (broker *PendingBroker) Respond(scope interaction.Scope, response interaction.Response) error {
	if broker == nil {
		return ErrBrokerClosed
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	if err := response.Validate(); err != nil {
		return err
	}
	key := makePendingKey(scope, response.ID())
	broker.mu.Lock()
	if broker.closed {
		broker.mu.Unlock()
		return ErrBrokerClosed
	}
	call, exists := broker.pending[key]
	if !exists {
		broker.mu.Unlock()
		return errors.New("interaction is not pending")
	}
	delete(broker.pending, key)
	broker.pendingBytes -= pendingValueBytes(call.value)
	call.result = pendingResult{response: response.Clone()}
	broker.publishLocked(DeltaClosed, call.value)
	broker.mu.Unlock()
	close(call.done)
	return nil
}

func (broker *PendingBroker) complete(key pendingKey, call *pendingCall, proposed pendingResult) pendingResult {
	broker.mu.Lock()
	current, exists := broker.pending[key]
	if exists && current == call {
		delete(broker.pending, key)
		broker.pendingBytes -= pendingValueBytes(call.value)
		call.result = proposed
		broker.publishLocked(DeltaClosed, call.value)
		broker.mu.Unlock()
		close(call.done)
		return proposed
	}
	result := call.result
	broker.mu.Unlock()
	<-call.done
	return result
}

// Subscribe atomically captures the complete sorted pending set and registers
// its tail before releasing the broker lock. There is no snapshot-to-tail gap.
func (broker *PendingBroker) Subscribe(ctx context.Context) (*PendingSubscription, error) {
	if broker == nil {
		return nil, ErrBrokerClosed
	}
	if ctx == nil {
		return nil, errors.New("pending subscription context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	broker.mu.Lock()
	if broker.closed {
		broker.mu.Unlock()
		return nil, ErrBrokerClosed
	}
	if len(broker.watchers) >= broker.maxObservers {
		broker.mu.Unlock()
		return nil, errors.New("pending observer capacity exhausted")
	}
	if broker.nextWatcher == math.MaxUint64 {
		broker.mu.Unlock()
		return nil, errors.New("pending observer identity capacity exhausted")
	}
	snapshot := broker.snapshotLocked()
	broker.nextWatcher++
	watcher := &pendingWatcher{
		queue: make(chan Delta, broker.queueSize), out: make(chan Delta),
		stop: make(chan struct{}), done: make(chan struct{}), lastDelivered: snapshot.Revision,
	}
	id := broker.nextWatcher
	broker.watchers[id] = watcher
	subscription := &PendingSubscription{broker: broker, id: id, snapshot: snapshot, watcher: watcher}
	broker.mu.Unlock()

	go watcher.deliver()
	go func() {
		select {
		case <-ctx.Done():
			broker.detachWatcher(id, watcher, ctx.Err(), false)
		case <-watcher.done:
		}
	}()
	return subscription, nil
}

func (broker *PendingBroker) snapshotLocked() PendingSnapshot {
	values := make([]Pending, 0, len(broker.pending))
	for _, call := range broker.pending {
		values = append(values, clonePending(call.value))
	}
	sort.Slice(values, func(first, second int) bool {
		firstRun, secondRun := values[first].Scope.RunID(), values[second].Scope.RunID()
		if firstRun != secondRun {
			return firstRun < secondRun
		}
		return values[first].Request.ID() < values[second].Request.ID()
	})
	return PendingSnapshot{Revision: broker.revision, Pending: values}
}

func (broker *PendingBroker) publishLocked(kind DeltaKind, pending Pending) {
	broker.revision++
	delta := Delta{Revision: broker.revision, Kind: kind, Pending: clonePending(pending)}
	for id, watcher := range broker.watchers {
		if !watcher.enqueue(delta) {
			delete(broker.watchers, id)
			watcher.finishImmediate(nil, true)
		}
	}
}

func (broker *PendingBroker) detachWatcher(id uint64, expected *pendingWatcher, err error, exhausted bool) {
	broker.mu.Lock()
	watcher, exists := broker.watchers[id]
	if exists && watcher == expected {
		delete(broker.watchers, id)
	}
	broker.mu.Unlock()
	if !exists {
		expected.finishImmediate(err, exhausted)
	} else if watcher == expected {
		watcher.finishImmediate(err, exhausted)
	}
}

func (watcher *pendingWatcher) deliver() {
	defer close(watcher.out)
	defer close(watcher.done)
	for {
		select {
		case <-watcher.stop:
			return
		case delta, open := <-watcher.queue:
			if !open {
				return
			}
			select {
			case <-watcher.stop:
				return
			case watcher.out <- cloneDelta(delta):
				watcher.mu.Lock()
				watcher.lastDelivered = delta.Revision
				watcher.queuedBytes -= pendingDeltaBytes(delta)
				watcher.mu.Unlock()
			}
		}
	}
}

func (watcher *pendingWatcher) enqueue(delta Delta) bool {
	size := pendingDeltaBytes(delta)
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	if size > maximumObserverQueuedBytes || watcher.queuedBytes > maximumObserverQueuedBytes-size {
		return false
	}
	watcher.queuedBytes += size
	select {
	case watcher.queue <- delta:
		return true
	default:
		watcher.queuedBytes -= size
		return false
	}
}

func (watcher *pendingWatcher) finishImmediate(err error, exhausted bool) {
	watcher.setTerminal(err, exhausted)
	watcher.stopOnce.Do(func() { close(watcher.stop) })
}

func (watcher *pendingWatcher) finishDraining(err error) {
	watcher.setTerminal(err, false)
	watcher.queueOnce.Do(func() { close(watcher.queue) })
}

func (watcher *pendingWatcher) setTerminal(err error, exhausted bool) {
	watcher.terminalOnce.Do(func() {
		watcher.mu.Lock()
		watcher.err = err
		watcher.exhausted = exhausted
		watcher.mu.Unlock()
	})
}

func (watcher *pendingWatcher) terminalError() error {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	if watcher.exhausted {
		return &ObserverExhaustedError{LastDelivered: watcher.lastDelivered}
	}
	return watcher.err
}

// Close commits broker shutdown without blocking on clients. Requests are
// released immediately; each subscription drains its already-bounded queue.
func (broker *PendingBroker) Close() {
	if broker == nil {
		return
	}
	broker.mu.Lock()
	if broker.closed {
		broker.mu.Unlock()
		return
	}
	broker.closed = true
	type keyedCall struct {
		key  pendingKey
		call *pendingCall
	}
	calls := make([]keyedCall, 0, len(broker.pending))
	for key, call := range broker.pending {
		calls = append(calls, keyedCall{key: key, call: call})
	}
	sort.Slice(calls, func(first, second int) bool {
		if calls[first].key.runID != calls[second].key.runID {
			return calls[first].key.runID < calls[second].key.runID
		}
		return calls[first].key.interactionID < calls[second].key.interactionID
	})
	for _, value := range calls {
		delete(broker.pending, value.key)
		call := value.call
		broker.pendingBytes -= pendingValueBytes(call.value)
		call.result = pendingResult{err: ErrBrokerClosed}
		broker.publishLocked(DeltaClosed, call.value)
	}
	watchers := make([]*pendingWatcher, 0, len(broker.watchers))
	for id, watcher := range broker.watchers {
		delete(broker.watchers, id)
		watchers = append(watchers, watcher)
	}
	broker.mu.Unlock()
	for _, value := range calls {
		close(value.call.done)
	}
	for _, watcher := range watchers {
		watcher.finishDraining(ErrBrokerClosed)
	}
}

func clonePending(value Pending) Pending {
	return Pending{Scope: value.Scope, Request: value.Request.Clone()}
}

func clonePendingSnapshot(value PendingSnapshot) PendingSnapshot {
	result := PendingSnapshot{Revision: value.Revision, Pending: make([]Pending, len(value.Pending))}
	for index, pending := range value.Pending {
		result.Pending[index] = clonePending(pending)
	}
	return result
}

func cloneDelta(value Delta) Delta {
	return Delta{Revision: value.Revision, Kind: value.Kind, Pending: clonePending(value.Pending)}
}

func cloneResponse(value interaction.Response) interaction.Response {
	if value.Validate() != nil {
		return interaction.Response{}
	}
	return value.Clone()
}

func pendingDeltaBytes(value Delta) int {
	return pendingValueBytes(value.Pending) + 32
}

func pendingValueBytes(value Pending) int {
	return len(value.Scope.RunID()) + len(value.Request.ID()) +
		len(value.Request.Kind()) + len(value.Request.Prompt()) +
		len(value.Request.Schema())
}
