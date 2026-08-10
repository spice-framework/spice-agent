package planning

import (
	"context"
	"errors"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/message"
	"github.com/spice-framework/spice-agent/stage"
)

var (
	// ErrPlannerFailed is returned without propagating application error text.
	ErrPlannerFailed = errors.New("planning planner failed")
	// ErrPlannerPanicked contains no recovered panic value.
	ErrPlannerPanicked = errors.New("planning planner panicked")
)

// Planner is a named exact-interface typed stage with a stable semantic
// identity. Implementations are application-owned compiled Spice beans.
type Planner interface {
	stage.Stage[Request, Draft]
	Identity() string
}

// Prepared is immutable reviewed-before-start content, not a capability. The
// explicit StartPrepared call is the application's review/accept boundary.
type Prepared struct {
	definition agent.Definition
	original   message.Message
	attached   message.Message
	plan       Plan
}

// Definition returns the worker definition.
func (prepared Prepared) Definition() agent.Definition { return prepared.definition }

// Original returns the unchanged user message.
func (prepared Prepared) Original() message.Message { return prepared.original.Clone() }

// Attached returns the durable user message containing the canonical plan.
func (prepared Prepared) Attached() message.Message { return prepared.attached.Clone() }

// Plan returns a defensive immutable plan copy.
func (prepared Prepared) Plan() Plan {
	prepared.plan.steps = prepared.plan.Steps()
	prepared.plan.encoded = prepared.plan.CanonicalJSON()
	return prepared.plan
}

func (prepared Prepared) validate() error {
	request, err := NewRequest(prepared.definition, prepared.original)
	if err != nil {
		return err
	}
	if err = prepared.plan.validate(); err != nil {
		return err
	}
	if request.inputDigest != prepared.plan.inputDigest {
		return invalid("prepared_input")
	}
	extracted, found, err := ExtractMessage(prepared.definition, prepared.attached)
	if err != nil || !found || extracted.id != prepared.plan.id ||
		!bytesEqual(extracted.encoded, prepared.plan.encoded) {
		return invalid("prepared_attachment")
	}
	return nil
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// Service owns explicit pre-run planning and worker start. It creates no run,
// event, interaction, provider request, or tool dispatch during Prepare.
type Service struct {
	planner         Planner
	plannerIdentity string
	engine          *agent.Engine
}

// NewService captures and validates the compiled planner identity once.
func NewService(planner Planner, engine *agent.Engine) (*Service, error) {
	if planner == nil {
		return nil, errors.New("planning service requires a planner")
	}
	if engine == nil {
		return nil, errors.New("planning service requires an engine")
	}
	identity, err := safeIdentity(planner)
	if err != nil {
		return nil, err
	}
	if err = validatePlannerIdentity(identity); err != nil {
		return nil, invalid("planner_identity")
	}
	return &Service{planner: planner, plannerIdentity: identity, engine: engine}, nil
}

// Prepare runs only the deterministic application-owned stage and returns
// inspectable content. Worker execution requires a later explicit call.
func (service *Service) Prepare(
	ctx context.Context,
	definition agent.Definition,
	initial message.Message,
) (Prepared, error) {
	if ctx == nil {
		return Prepared{}, errors.New("planning prepare context must not be nil")
	}
	if service == nil || service.planner == nil || service.engine == nil {
		return Prepared{}, errors.New("planning service is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, err
	}
	request, err := NewRequest(definition, initial)
	if err != nil {
		return Prepared{}, err
	}
	draft, plannerErr := safePlan(ctx, service.planner, request)
	if err = ctx.Err(); err != nil {
		return Prepared{}, err
	}
	if plannerErr != nil {
		return Prepared{}, plannerErr
	}
	plan, err := Finalize(request, service.plannerIdentity, draft)
	if err != nil {
		return Prepared{}, err
	}
	attached, err := Attach(request, plan)
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{
		definition: definition, original: initial.Clone(), attached: attached, plan: plan,
	}, nil
}

// StartPrepared is the explicit review/accept boundary and delegates ordinary
// lifecycle ownership to the injected Agent engine. It never reruns planning.
func (service *Service) StartPrepared(ctx context.Context, prepared Prepared) (*agent.Run, error) {
	if ctx == nil {
		return nil, errors.New("planning start context must not be nil")
	}
	if service == nil || service.engine == nil {
		return nil, errors.New("planning service is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := prepared.validate(); err != nil {
		return nil, err
	}
	input, err := agent.NewInput(prepared.attached)
	if err != nil {
		return nil, invalid("prepared_input")
	}
	return service.engine.Start(ctx, prepared.definition, input)
}

func safeIdentity(planner Planner) (identity string, err error) {
	defer func() {
		if recover() != nil {
			identity = ""
			err = ErrPlannerPanicked
		}
	}()
	return planner.Identity(), nil
}

func safePlan(ctx context.Context, planner Planner, request Request) (draft Draft, err error) {
	defer func() {
		if recover() != nil {
			draft = Draft{}
			err = ErrPlannerPanicked
		}
	}()
	draft, err = planner.Process(ctx, request)
	if err != nil {
		return Draft{}, ErrPlannerFailed
	}
	return draft, nil
}

var _ stage.Stage[Request, Draft] = Planner(nil)
