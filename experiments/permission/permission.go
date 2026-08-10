package permission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spice-framework/spice-agent/interaction"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

// Decision is one policy result. A policy must make every prompt explicit;
// the guard never infers a decision from an effect or capability.
type Decision string

const (
	DecisionAllow  Decision = "allow"
	DecisionDeny   Decision = "deny"
	DecisionPrompt Decision = "prompt"
)

var (
	// ErrDenied is returned without invoking the executable continuation.
	ErrDenied = errors.New("tool dispatch denied by permission policy")
	// ErrPolicyFailed is a secret-safe failure for errors and panics from Policy.
	ErrPolicyFailed = errors.New("permission policy evaluation failed")
)

// Facts is immutable, bounded policy input. Correlation identities are
// SHA-256 digests so durable policy records cannot disclose raw identifiers.
type Facts struct {
	runDigest            string
	callDigest           string
	planDigest           string
	toolName             string
	toolDigest           string
	definitionDigest     string
	planFingerprint      string
	workspaceFingerprint string
	effect               tool.Effect
	replaySafety         tool.ReplaySafety
	capabilities         []tool.Capability
}

func (facts Facts) RunDigest() string               { return facts.runDigest }
func (facts Facts) CallDigest() string              { return facts.callDigest }
func (facts Facts) PlanDigest() string              { return facts.planDigest }
func (facts Facts) ToolName() string                { return facts.toolName }
func (facts Facts) ToolDigest() string              { return facts.toolDigest }
func (facts Facts) DefinitionDigest() string        { return facts.definitionDigest }
func (facts Facts) PlanFingerprint() string         { return facts.planFingerprint }
func (facts Facts) WorkspaceFingerprint() string    { return facts.workspaceFingerprint }
func (facts Facts) Effect() tool.Effect             { return facts.effect }
func (facts Facts) ReplaySafety() tool.ReplaySafety { return facts.replaySafety }
func (facts Facts) Capabilities() []tool.Capability {
	return append([]tool.Capability(nil), facts.capabilities...)
}

// MarshalJSON is the durable-record boundary. It intentionally excludes raw
// scope identities, call arguments, descriptions, schemas, paths, and secrets.
func (facts Facts) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		RunDigest            string            `json:"run_digest"`
		CallDigest           string            `json:"call_digest"`
		PlanDigest           string            `json:"plan_digest"`
		ToolDigest           string            `json:"tool_digest"`
		DefinitionDigest     string            `json:"definition_digest"`
		PlanFingerprint      string            `json:"plan_fingerprint"`
		WorkspaceFingerprint string            `json:"workspace_fingerprint,omitempty"`
		Effect               tool.Effect       `json:"effect"`
		ReplaySafety         tool.ReplaySafety `json:"replay_safety"`
		Capabilities         []tool.Capability `json:"capabilities"`
	}{
		RunDigest: facts.runDigest, CallDigest: facts.callDigest, PlanDigest: facts.planDigest,
		ToolDigest: facts.toolDigest, DefinitionDigest: facts.definitionDigest,
		PlanFingerprint: facts.planFingerprint, WorkspaceFingerprint: facts.workspaceFingerprint,
		Effect: facts.effect, ReplaySafety: facts.replaySafety,
		Capabilities: append([]tool.Capability(nil), facts.capabilities...),
	})
}

// Policy decides whether a canonical tool-dispatch attempt may continue.
// Implementations must be concurrent-safe and honor context cancellation.
type Policy interface {
	Decide(context.Context, Facts) (Decision, error)
}

// PolicyFunc adapts a function to Policy.
type PolicyFunc func(context.Context, Facts) (Decision, error)

func (policy PolicyFunc) Decide(ctx context.Context, facts Facts) (Decision, error) {
	return policy(ctx, facts)
}

// Options selects the result used only when a requested prompt cannot obtain
// a valid boolean response. The zero value denies. Cancellation always wins.
type Options struct {
	PromptFailure Decision
}

// Guard is a terminal stage.ToolDispatchGuard. It never retries and never
// obtains raw interaction.Broker authority.
type Guard struct {
	policy        Policy
	promptFailure Decision
}

// NewGuard validates and snapshots one experimental permission guard.
func NewGuard(policy Policy, options Options) (*Guard, error) {
	if policy == nil {
		return nil, errors.New("permission policy is required")
	}
	failure := options.PromptFailure
	if failure == "" {
		failure = DecisionDeny
	}
	if failure != DecisionAllow && failure != DecisionDeny {
		return nil, errors.New("prompt failure decision must allow or deny")
	}
	return &Guard{policy: policy, promptFailure: failure}, nil
}

// Guard applies the policy to one continuation. The continuation is invoked at
// most once and only after an allow decision or affirmative prompt response.
func (guard *Guard) Guard(
	ctx context.Context,
	scope stage.ToolDispatchScope,
	definition tool.Definition,
	call tool.Call,
	next stage.ToolDispatchNext,
) (tool.Result, error) {
	if ctx == nil || guard == nil || guard.policy == nil || next == nil {
		return tool.Result{}, ErrPolicyFailed
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	facts, err := newFacts(scope, definition, call)
	if err != nil {
		return tool.Result{}, ErrPolicyFailed
	}
	decision, err := safeDecision(ctx, guard.policy, facts)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return tool.Result{}, ctxErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return tool.Result{}, err
		}
		return tool.Result{}, ErrPolicyFailed
	}
	switch decision {
	case DecisionAllow:
		return next()
	case DecisionDeny:
		return tool.Result{}, ErrDenied
	case DecisionPrompt:
		return guard.prompt(ctx, scope, facts, next)
	default:
		return tool.Result{}, ErrPolicyFailed
	}
}

func newFacts(scope stage.ToolDispatchScope, definition tool.Definition, call tool.Call) (Facts, error) {
	if err := scope.Validate(); err != nil {
		return Facts{}, err
	}
	if err := definition.Validate(); err != nil {
		return Facts{}, err
	}
	if err := call.Validate(); err != nil || definition.Name() != call.Name() {
		return Facts{}, errors.New("permission facts do not identify one tool")
	}
	capabilities := definition.Capabilities()
	slices.Sort(capabilities)
	return Facts{
		runDigest: digest(scope.RunID()), callDigest: digest(string(call.ID())),
		planDigest: digest(scope.ToolPlanID().String()), toolName: definition.Name(),
		toolDigest:       digest(definition.Name()),
		definitionDigest: definition.Fingerprint(), planFingerprint: scope.PlanFingerprint(),
		workspaceFingerprint: scope.WorkspaceFingerprint(), effect: definition.Effect(),
		replaySafety: definition.ReplaySafety(), capabilities: capabilities,
	}, nil
}

func safeDecision(ctx context.Context, policy Policy, facts Facts) (decision Decision, err error) {
	defer func() {
		if recover() != nil {
			decision = ""
			err = ErrPolicyFailed
		}
	}()
	return policy.Decide(ctx, facts)
}

func (guard *Guard) prompt(
	ctx context.Context,
	scope stage.ToolDispatchScope,
	facts Facts,
	next stage.ToolDispatchNext,
) (tool.Result, error) {
	request, err := interaction.NewRequest(
		interaction.ID("permission-"+digest(facts.runDigest + ":" + facts.callDigest)[:32]),
		"tool_permission",
		promptText(facts),
		json.RawMessage(`{"type":"boolean"}`),
	)
	if err != nil {
		return tool.Result{}, ErrPolicyFailed
	}
	response, err := scope.RequestInteraction(ctx, request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return tool.Result{}, ctxErr
		}
		return guard.promptDefault(next)
	}
	var approved bool
	if err = json.Unmarshal(response.Value(), &approved); err != nil {
		return guard.promptDefault(next)
	}
	if !approved {
		return tool.Result{}, ErrDenied
	}
	return next()
}

func (guard *Guard) promptDefault(next stage.ToolDispatchNext) (tool.Result, error) {
	if guard.promptFailure == DecisionAllow {
		return next()
	}
	return tool.Result{}, ErrDenied
}

func promptText(facts Facts) string {
	capabilities := make([]string, len(facts.capabilities))
	for index, capability := range facts.capabilities {
		capabilities[index] = string(capability)
	}
	return fmt.Sprintf(
		"Allow tool %q (%s; capabilities: %s)?",
		facts.toolName, facts.effect, strings.Join(capabilities, ", "),
	)
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

var _ stage.ToolDispatchGuard = (*Guard)(nil)
