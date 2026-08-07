package grpcserver

import (
	"context"
	"errors"
	"math"
	"sync"

	"github.com/spice-framework/spice-agent/daemon"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"google.golang.org/protobuf/proto"
)

const maximumNegotiatedSessions = 4096

var (
	errNegotiatedSessionClosed      = errors.New("negotiated session registry is closed")
	errNegotiatedSessionCapacity    = errors.New("negotiated session capacity is exhausted")
	errNegotiatedSessionInvalid     = errors.New("negotiated session is invalid")
	errNegotiatedSessionUnavailable = errors.New("negotiated session is unavailable")
)

// negotiatedSession is the immutable adapter view used by RPC handlers.
type negotiatedSession struct {
	session  daemon.Session
	response *enginev1.InitializeResponse
}

// negotiatedSessionRegistry binds completed Initialize responses to the exact
// daemon ownership epochs that produced them. It does not own SessionStore.
type negotiatedSessionRegistry struct {
	root          context.Context //nolint:containedctx // caller-owned service lifetime only.
	stopRootWatch func() bool
	maximum       int

	mu       sync.Mutex
	entries  map[string]negotiatedSession
	closed   bool
	closeOne sync.Once
}

func newNegotiatedSessionRegistry(root context.Context, maximum int) (*negotiatedSessionRegistry, error) {
	if root == nil || maximum < 1 || maximum > maximumNegotiatedSessions {
		return nil, errNegotiatedSessionInvalid
	}
	if err := root.Err(); err != nil {
		return nil, errNegotiatedSessionClosed
	}
	registry := &negotiatedSessionRegistry{
		root: root, maximum: maximum,
		entries: make(map[string]negotiatedSession),
	}
	registry.mu.Lock()
	registry.stopRootWatch = context.AfterFunc(root, registry.close)
	registry.mu.Unlock()
	return registry, nil
}

// installFresh installs exactly one epoch-one session. Duplicate identities are
// deliberately indistinguishable from other unavailable ownership claims.
func (registry *negotiatedSessionRegistry) installFresh(
	session daemon.Session,
	response *enginev1.InitializeResponse,
) error {
	if registry == nil {
		return errNegotiatedSessionClosed
	}
	validated, err := validateNegotiatedSession(session, response)
	if err != nil || session.Epoch() != 1 {
		return errNegotiatedSessionInvalid
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closedLocked() {
		return errNegotiatedSessionClosed
	}
	if _, exists := registry.entries[session.ClientID()]; exists {
		return errNegotiatedSessionUnavailable
	}
	if len(registry.entries) >= registry.maximum {
		return errNegotiatedSessionCapacity
	}
	entry, err := registry.newEntryLocked(session, validated)
	if err != nil {
		return err
	}
	registry.entries[session.ClientID()] = entry
	return nil
}

// replaceReconnect performs an exact registry compare-and-swap after
// SessionStore has advanced the same stable client to the next epoch.
func (registry *negotiatedSessionRegistry) replaceReconnect(
	clientID string,
	expectedEpoch uint64,
	next daemon.Session,
	response *enginev1.InitializeResponse,
) error {
	if registry == nil {
		return errNegotiatedSessionClosed
	}
	validated, err := validateNegotiatedSession(next, response)
	if err != nil || expectedEpoch == 0 || expectedEpoch == math.MaxUint64 ||
		next.ClientID() != clientID || next.Epoch() != expectedEpoch+1 {
		return errNegotiatedSessionInvalid
	}
	registry.mu.Lock()
	if registry.closedLocked() {
		registry.mu.Unlock()
		return errNegotiatedSessionClosed
	}
	current, exists := registry.entries[clientID]
	if !exists || current.session.Epoch() != expectedEpoch {
		registry.mu.Unlock()
		return errNegotiatedSessionUnavailable
	}
	replacement, err := registry.newEntryLocked(next, validated)
	if err != nil {
		registry.mu.Unlock()
		return err
	}
	registry.entries[clientID] = replacement
	registry.mu.Unlock()
	return nil
}

// lookup requires an exact current epoch and never reveals whether a stable
// identity is unknown, stale, duplicated, or already fenced.
func (registry *negotiatedSessionRegistry) lookup(clientID string, epoch uint64) (negotiatedSession, error) {
	if registry == nil {
		return negotiatedSession{}, errNegotiatedSessionClosed
	}
	registry.mu.Lock()
	if registry.closedLocked() {
		registry.mu.Unlock()
		return negotiatedSession{}, errNegotiatedSessionClosed
	}
	entry, exists := registry.entries[clientID]
	if !exists || epoch == 0 || entry.session.Epoch() != epoch || entry.session.Context().Err() != nil {
		registry.mu.Unlock()
		return negotiatedSession{}, errNegotiatedSessionUnavailable
	}
	registry.mu.Unlock()
	return negotiatedSession{
		session:  entry.session,
		response: proto.CloneOf(entry.response),
	}, nil
}

func (registry *negotiatedSessionRegistry) newEntryLocked(
	session daemon.Session,
	response *enginev1.InitializeResponse,
) (negotiatedSession, error) {
	if registry.closedLocked() || session.Context().Err() != nil {
		return negotiatedSession{}, errNegotiatedSessionUnavailable
	}
	entry := negotiatedSession{session: session, response: response}
	if session.Context().Err() != nil {
		return negotiatedSession{}, errNegotiatedSessionUnavailable
	}
	return entry, nil
}

func validateNegotiatedSession(
	session daemon.Session,
	response *enginev1.InitializeResponse,
) (*enginev1.InitializeResponse, error) {
	if session.ClientID() == "" || session.Epoch() == 0 || session.Context() == nil || session.Context().Err() != nil ||
		response == nil {
		return nil, errNegotiatedSessionInvalid
	}
	cloned := proto.CloneOf(response)
	if enginev1.ValidateInitializeResponse(cloned) != nil || cloned.GetClientId() != session.ClientID() ||
		cloned.GetOwnershipEpoch() != session.Epoch() {
		return nil, errNegotiatedSessionInvalid
	}
	return cloned, nil
}

func (registry *negotiatedSessionRegistry) close() {
	if registry == nil {
		return
	}
	registry.closeOne.Do(func() {
		registry.mu.Lock()
		registry.closed = true
		clear(registry.entries)
		registry.mu.Unlock()

		if registry.stopRootWatch != nil {
			registry.stopRootWatch()
		}
	})
}

func (registry *negotiatedSessionRegistry) closedLocked() bool {
	return registry.closed || registry.root == nil || registry.root.Err() != nil
}
