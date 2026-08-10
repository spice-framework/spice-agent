package gitworkflow

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/spice-framework/spice-agent/interaction"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

const authorityVersion = "spice.agent.git-authority/v1alpha1"

var (
	// ErrAuthorizationRequired reports that the commit tool did not receive a
	// matching, single-use grant from the terminal guard.
	ErrAuthorizationRequired = errors.New("git commit requires a current interaction authorization")
	// ErrAuthorizationDenied reports a missing or mismatched user response.
	ErrAuthorizationDenied = errors.New("git commit authorization was denied")
	// ErrAuthorizationFailed is the fixed secret-safe guard failure.
	ErrAuthorizationFailed = errors.New("git commit authorization failed")
)

type grant struct {
	callDigest   string
	stagedDigest string
	token        string
}

// AuthorityStore is an application-owned, concurrency-safe, single-use grant
// ledger shared only by the commit guard and commit tool.
type AuthorityStore struct {
	mu     sync.Mutex
	grants map[tool.CallID]grant
	closed bool
}

// NewAuthorityStore constructs an empty fail-closed ledger.
func NewAuthorityStore() *AuthorityStore {
	return &AuthorityStore{grants: make(map[tool.CallID]grant)}
}

func (store *AuthorityStore) authorize(call tool.Call, stagedDigest, token string) error {
	if store == nil || call.Validate() != nil || !validDigest(stagedDigest) || !validDigest(token) {
		return ErrAuthorizationFailed
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return ErrAuthorizationFailed
	}
	if _, exists := store.grants[call.ID()]; exists {
		return ErrAuthorizationFailed
	}
	store.grants[call.ID()] = grant{callDigest: digestCall(call), stagedDigest: stagedDigest, token: token}
	return nil
}

func (store *AuthorityStore) consume(call tool.Call) (string, error) {
	if store == nil || call.Validate() != nil {
		return "", ErrAuthorizationRequired
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	value, exists := store.grants[call.ID()]
	if exists {
		delete(store.grants, call.ID())
	}
	if !exists || value.callDigest != digestCall(call) || !validDigest(value.token) || !validDigest(value.stagedDigest) {
		return "", ErrAuthorizationRequired
	}
	return value.stagedDigest, nil
}

func (store *AuthorityStore) revoke(callID tool.CallID) {
	if store == nil {
		return
	}
	store.mu.Lock()
	delete(store.grants, callID)
	store.mu.Unlock()
}

// Close rejects future grants and erases every unused authorization.
func (store *AuthorityStore) Close() {
	if store == nil {
		return
	}
	store.mu.Lock()
	store.closed = true
	clear(store.grants)
	store.mu.Unlock()
}

// CommitGuard leaves unrelated tools unchanged and requires one exact run-owned
// interaction before the staged-index commit continuation can execute.
type CommitGuard struct {
	backend Backend
	store   *AuthorityStore
}

// NewCommitGuard constructs the terminal Git authority guard.
func NewCommitGuard(backend Backend, store *AuthorityStore) (*CommitGuard, error) {
	if backend == nil || store == nil {
		return nil, ErrAuthorizationFailed
	}
	return &CommitGuard{backend: backend, store: store}, nil
}

// Guard binds approval to the exact engine scope, definition, call arguments,
// and staged-index digest. The single-use grant exists only around next.
func (guard *CommitGuard) Guard(
	ctx context.Context,
	scope stage.ToolDispatchScope,
	definition tool.Definition,
	call tool.Call,
	next stage.ToolDispatchNext,
) (tool.Result, error) {
	if ctx == nil || guard == nil || guard.backend == nil || guard.store == nil || next == nil {
		return tool.Result{}, ErrAuthorizationFailed
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if err := scope.Validate(); err != nil || definition.Validate() != nil || call.Validate() != nil || definition.Name() != call.Name() {
		return tool.Result{}, ErrAuthorizationFailed
	}
	if call.Name() != CommitStagedToolName {
		return next()
	}
	if _, err := decodeCommitArguments(call.Arguments()); err != nil {
		return tool.Result{}, ErrAuthorizationFailed
	}
	stagedDigest, err := guard.backend.StagedDigest(ctx)
	if err != nil {
		return tool.Result{}, guardFailure(ctx, err)
	}
	token, err := AuthorityToken(scope, definition, call, stagedDigest)
	if err != nil {
		return tool.Result{}, ErrAuthorizationFailed
	}
	request, err := interaction.NewRequest(
		interaction.ID("git-authority-"+token[len("sha256:"):len("sha256:")+32]),
		"git_commit_staged",
		fmt.Sprintf("Approve the staged Git index %s with authority token %s?", stagedDigest, token),
		json.RawMessage(fmt.Sprintf(`{"type":"string","const":%q}`, token)),
	)
	if err != nil {
		return tool.Result{}, ErrAuthorizationFailed
	}
	response, err := scope.RequestInteraction(ctx, request)
	if err != nil {
		return tool.Result{}, guardFailure(ctx, err)
	}
	var approved string
	if err = json.Unmarshal(response.Value(), &approved); err != nil || approved != token {
		return tool.Result{}, ErrAuthorizationDenied
	}
	if err = guard.store.authorize(call, stagedDigest, token); err != nil {
		return tool.Result{}, err
	}
	defer guard.store.revoke(call.ID())
	return next()
}

func guardFailure(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return ErrAuthorizationFailed
}

// AuthorityToken returns the deterministic secret-free grant identity for one
// exact staged commit occurrence.
func AuthorityToken(
	scope stage.ToolDispatchScope,
	definition tool.Definition,
	call tool.Call,
	stagedDigest string,
) (string, error) {
	if err := scope.Validate(); err != nil {
		return "", err
	}
	if err := definition.Validate(); err != nil {
		return "", err
	}
	if err := call.Validate(); err != nil || call.Name() != definition.Name() || !validDigest(stagedDigest) {
		return "", ErrAuthorizationFailed
	}
	hash := sha256.New()
	for _, value := range []string{
		authorityVersion, scope.RunID(), fmt.Sprintf("%d", scope.Turn()),
		scope.ToolPlanID().String(), scope.PlanFingerprint(), scope.WorkspaceFingerprint(),
		string(call.ID()), call.Name(), string(call.Arguments()), definition.Fingerprint(), stagedDigest,
	} {
		writeDigestField(hash, []byte(value))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func digestCall(call tool.Call) string {
	hash := sha256.New()
	writeDigestField(hash, []byte(call.ID()))
	writeDigestField(hash, []byte(call.Name()))
	writeDigestField(hash, call.Arguments())
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

type digestWriter interface{ Write([]byte) (int, error) }

func writeDigestField(destination digestWriter, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = destination.Write(size[:])
	_, _ = destination.Write(value)
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	decoded, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value[len("sha256:"):]
}

var _ stage.ToolDispatchGuard = (*CommitGuard)(nil)
