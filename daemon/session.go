package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
)

const maximumSessions = 4096

var (
	// ErrStaleSession rejects a client ownership epoch that lost its CAS.
	ErrStaleSession = errors.New("daemon session ownership is stale")
	// ErrSessionStoreClosed rejects work after daemon-root shutdown.
	ErrSessionStoreClosed = errors.New("daemon session store is closed")
)

// Session is one immutable client ownership epoch.
type Session struct {
	clientID string
	epoch    uint64
	ctx      context.Context //nolint:containedctx // an immutable session owns its epoch lifetime.
}

// ClientID returns the stable cryptographic identity preserved by reconnect.
func (session Session) ClientID() string { return session.clientID }

// Epoch returns the current ownership generation.
func (session Session) Epoch() uint64 { return session.epoch }

// Context returns the daemon-root-owned epoch context.
func (session Session) Context() context.Context { return session.ctx }

type sessionState struct {
	epoch  uint64
	ctx    context.Context //nolint:containedctx // state owns the current fencing lifetime.
	cancel context.CancelFunc
}

// SessionStore assigns stable cryptographic client identities and fences stale
// owners. All epoch contexts derive from the caller-owned daemon root.
type SessionStore struct {
	mu       sync.Mutex
	root     context.Context //nolint:containedctx // the store derives and owns every session lifetime from this root.
	maximum  int
	random   io.Reader
	sessions map[string]*sessionState
	closed   bool
}

// NewSessionStore constructs a bounded store owned by root.
func NewSessionStore(root context.Context, maximum int) (*SessionStore, error) {
	return newSessionStore(root, maximum, rand.Reader)
}

func newSessionStore(root context.Context, maximum int, random io.Reader) (*SessionStore, error) {
	if root == nil || maximum < 1 || maximum > maximumSessions || random == nil {
		return nil, fmt.Errorf("session store requires a root context, capacity between 1 and %d, and randomness", maximumSessions)
	}
	store := &SessionStore{root: root, maximum: maximum, random: random, sessions: map[string]*sessionState{}}
	context.AfterFunc(root, store.Close)
	return store, nil
}

// Fresh creates a cryptographically random stable client ID at epoch one.
func (store *SessionStore) Fresh() (Session, error) {
	if store == nil {
		return Session{}, ErrSessionStoreClosed
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || store.root.Err() != nil {
		return Session{}, ErrSessionStoreClosed
	}
	if len(store.sessions) >= store.maximum {
		return Session{}, errors.New("daemon session capacity exhausted")
	}
	for range 4 {
		raw := make([]byte, 16)
		if _, err := io.ReadFull(store.random, raw); err != nil {
			return Session{}, fmt.Errorf("generate client ID: %w", err)
		}
		clientID := hex.EncodeToString(raw)
		if _, exists := store.sessions[clientID]; exists {
			continue
		}
		ctx, cancel := context.WithCancel(store.root)
		state := &sessionState{epoch: 1, ctx: ctx, cancel: cancel}
		store.sessions[clientID] = state
		return Session{clientID: clientID, epoch: 1, ctx: ctx}, nil
	}
	return Session{}, errors.New("generate unique client ID")
}

// Reconnect performs an exact compare-and-swap to the next ownership epoch.
// Exactly one concurrent claimant can own expected.
func (store *SessionStore) Reconnect(clientID string, expected uint64) (Session, error) {
	if store == nil {
		return Session{}, ErrSessionStoreClosed
	}
	if err := boundedToken("client ID", clientID); err != nil {
		return Session{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || store.root.Err() != nil {
		return Session{}, ErrSessionStoreClosed
	}
	state, exists := store.sessions[clientID]
	if !exists || expected == 0 || state.epoch != expected {
		return Session{}, ErrStaleSession
	}
	if expected == math.MaxUint64 {
		return Session{}, errors.New("session ownership epoch overflow")
	}
	state.cancel()
	ctx, cancel := context.WithCancel(store.root)
	state.epoch++
	state.ctx = ctx
	state.cancel = cancel
	return Session{clientID: clientID, epoch: state.epoch, ctx: ctx}, nil
}

// Check verifies that an epoch still owns the stable client identity.
func (store *SessionStore) Check(clientID string, epoch uint64) error {
	if store == nil {
		return ErrSessionStoreClosed
	}
	if err := boundedToken("client ID", clientID); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || store.root.Err() != nil {
		return ErrSessionStoreClosed
	}
	state, exists := store.sessions[clientID]
	if !exists || state.epoch != epoch {
		return ErrStaleSession
	}
	return nil
}

// Fence returns the current ownership context or rejects a stale epoch.
func (store *SessionStore) Fence(clientID string, epoch uint64) (context.Context, error) {
	if store == nil {
		return nil, ErrSessionStoreClosed
	}
	if err := boundedToken("client ID", clientID); err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || store.root.Err() != nil {
		return nil, ErrSessionStoreClosed
	}
	state, exists := store.sessions[clientID]
	if !exists || state.epoch != epoch {
		return nil, ErrStaleSession
	}
	return state.ctx, nil
}

// Close fences every owner and rejects future session work.
func (store *SessionStore) Close() {
	if store == nil {
		return
	}
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return
	}
	store.closed = true
	states := make([]*sessionState, 0, len(store.sessions))
	for _, state := range store.sessions {
		states = append(states, state)
	}
	store.sessions = nil
	store.mu.Unlock()
	for _, state := range states {
		state.cancel()
	}
}
