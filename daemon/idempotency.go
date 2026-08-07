package daemon

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// ErrOperationExecutor is the secret-safe sentinel committed after an executor
// error, panic, or invalid result.
var ErrOperationExecutor = errors.New("idempotency operation executor failed")

// OutcomeKind is the durable, client-safe terminal classification.
type OutcomeKind string

const (
	// OutcomeSuccess records a successfully committed operation.
	OutcomeSuccess OutcomeKind = "success"
	// OutcomeFailure records an expected, canonical business failure.
	OutcomeFailure OutcomeKind = "failure"
	// OutcomeUncertain records an operation whose safe terminal state is unknown.
	OutcomeUncertain OutcomeKind = "uncertain"
)

const maximumOutcomeBytes = 1 << 20

const maximumLedgerDimension = 1 << 20

// Outcome is the bounded canonical result persisted by the ledger. Expected
// business failures are OutcomeFailure values, not executor errors.
type Outcome struct {
	kind    OutcomeKind
	payload []byte
}

// NewOutcome validates and defensively copies a canonical result.
func NewOutcome(kind OutcomeKind, payload []byte) (Outcome, error) {
	if kind != OutcomeSuccess && kind != OutcomeFailure && kind != OutcomeUncertain {
		return Outcome{}, errors.New("idempotency outcome kind is invalid")
	}
	if len(payload) > maximumOutcomeBytes {
		return Outcome{}, errors.New("idempotency outcome exceeds 1048576 bytes")
	}
	return Outcome{kind: kind, payload: slices.Clone(payload)}, nil
}

// Kind returns the terminal classification.
func (outcome Outcome) Kind() OutcomeKind { return outcome.kind }

// Payload returns a defensive copy of the canonical result bytes.
func (outcome Outcome) Payload() []byte { return slices.Clone(outcome.payload) }

type operationKey struct {
	clientID    string
	operationID string
}

type operationEntry struct {
	kind    string
	digest  [32]byte
	done    chan struct{}
	outcome Outcome
	err     error
}

// Ledger is a bounded concurrent idempotency ledger. Entries intentionally
// survive client reconnect because stable client identity, not epoch, owns the
// operation namespace.
type Ledger struct {
	mu               sync.Mutex
	maximumClients   int
	maximumPerClient int
	entries          map[operationKey]*operationEntry
	clientCounts     map[string]int
}

// NewLedger constructs independently bounded client and per-client operation
// namespaces, preventing one client from exhausting every other client.
func NewLedger(maximumClients, maximumPerClient int) (*Ledger, error) {
	if maximumClients < 1 || maximumClients > maximumLedgerDimension ||
		maximumPerClient < 1 || maximumPerClient > maximumLedgerDimension {
		return nil, fmt.Errorf("idempotency ledger bounds must be between 1 and %d", maximumLedgerDimension)
	}
	if maximumClients > maximumLedgerDimension/maximumPerClient {
		return nil, fmt.Errorf("idempotency ledger total capacity exceeds %d", maximumLedgerDimension)
	}
	return &Ledger{
		maximumClients: maximumClients, maximumPerClient: maximumPerClient,
		entries: make(map[operationKey]*operationEntry), clientCounts: make(map[string]int),
	}, nil
}

// CanonicalDigest hashes a caller-canonicalized operation request.
func CanonicalDigest(value []byte) [32]byte { return sha256.Sum256(value) }

// Do assigns one owner to an operation and makes exact duplicates wait with
// their own context. Executor errors and panics commit the same bounded
// uncertain result and sentinel error for the owner and later duplicates.
func (ledger *Ledger) Do(
	ctx context.Context,
	clientID, operationID, kind string,
	digest [32]byte,
	execute func(context.Context) (Outcome, error),
) (Outcome, bool, error) {
	if ledger == nil || ctx == nil || execute == nil {
		return Outcome{}, false, errors.New("idempotency operation requires context and executor")
	}
	if err := boundedToken("client ID", clientID); err != nil {
		return Outcome{}, false, err
	}
	if err := boundedToken("operation ID", operationID); err != nil {
		return Outcome{}, false, err
	}
	if err := boundedToken("operation kind", kind); err != nil {
		return Outcome{}, false, err
	}
	key := operationKey{clientID: clientID, operationID: operationID}
	ledger.mu.Lock()
	existing, exists := ledger.entries[key]
	if exists {
		if existing.kind != kind || existing.digest != digest {
			ledger.mu.Unlock()
			return Outcome{}, false, errors.New("idempotency operation conflicts with existing input")
		}
		ledger.mu.Unlock()
		return awaitExisting(ctx, ledger, existing)
	}
	if err := ctx.Err(); err != nil {
		ledger.mu.Unlock()
		return Outcome{}, false, err
	}
	count, clientExists := ledger.clientCounts[clientID]
	if (!clientExists && len(ledger.clientCounts) >= ledger.maximumClients) || count >= ledger.maximumPerClient {
		ledger.mu.Unlock()
		return Outcome{}, false, errors.New("idempotency ledger capacity exhausted")
	}
	entry := &operationEntry{kind: kind, digest: digest, done: make(chan struct{})}
	ledger.entries[key] = entry
	ledger.clientCounts[clientID] = count + 1
	ledger.mu.Unlock()

	outcome, executionErr := executeSafely(ctx, execute)
	if executionErr != nil {
		outcome = uncertainExecutorOutcome()
		executionErr = ErrOperationExecutor
	} else if _, validationErr := NewOutcome(outcome.kind, outcome.payload); validationErr != nil {
		outcome = uncertainExecutorOutcome()
		executionErr = ErrOperationExecutor
	}

	ledger.mu.Lock()
	entry.outcome = cloneOutcome(outcome)
	entry.err = executionErr
	close(entry.done)
	ledger.mu.Unlock()
	return cloneOutcome(outcome), false, executionErr
}

func awaitExisting(ctx context.Context, ledger *Ledger, existing *operationEntry) (Outcome, bool, error) {
	select {
	case <-existing.done:
		return committedOutcome(ledger, existing)
	default:
	}
	select {
	case <-existing.done:
		return committedOutcome(ledger, existing)
	case <-ctx.Done():
		select {
		case <-existing.done:
			return committedOutcome(ledger, existing)
		default:
			return Outcome{}, true, ctx.Err()
		}
	}
}

func committedOutcome(ledger *Ledger, existing *operationEntry) (Outcome, bool, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	return cloneOutcome(existing.outcome), true, existing.err
}

func executeSafely(ctx context.Context, execute func(context.Context) (outcome Outcome, err error)) (outcome Outcome, err error) {
	defer func() {
		if recover() != nil {
			outcome = uncertainExecutorOutcome()
			err = ErrOperationExecutor
		}
	}()
	return execute(ctx)
}

func uncertainExecutorOutcome() Outcome {
	value, _ := NewOutcome(OutcomeUncertain, []byte("operation outcome uncertain"))
	return value
}

func cloneOutcome(value Outcome) Outcome {
	return Outcome{kind: value.kind, payload: slices.Clone(value.payload)}
}
