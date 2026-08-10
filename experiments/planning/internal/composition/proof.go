package composition

import (
	"context"
	"io"
	"sync/atomic"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	planning "github.com/spice-framework/spice-agent/experiments/planning"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice/lifecycle"
)

// @import { Bean } from "github.com/spice-framework/spice/annotation/core"
// @import { Stage } from "github.com/spice-framework/spice-agent/annotation/agent"

const proofWorkspace = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// Proof exposes exact generated typed stage and service construction.
type Proof struct {
	Planner  planning.Planner
	Service  *planning.Service
	Engine   *agent.Engine
	Provider *ProofProvider
}

// ProofPlanner is one deterministic application-owned typed stage.
type ProofPlanner struct{}

func (*ProofPlanner) Identity() string { return "planning-proof:deterministic-v1" }

func (*ProofPlanner) Process(ctx context.Context, request planning.Request) (planning.Draft, error) {
	if err := ctx.Err(); err != nil {
		return planning.Draft{}, err
	}
	first, err := planning.NewStep("inspect", "Inspect the bounded worker request.")
	if err != nil {
		return planning.Draft{}, err
	}
	second, err := planning.NewStep("respond", "Produce a bounded worker response.", "inspect")
	if err != nil {
		return planning.Draft{}, err
	}
	return planning.NewDraft("Answer the worker request.", first, second)
}

// ProofProvider completes one worker turn and records calls.
type ProofProvider struct{ calls atomic.Int64 }

func (provider *ProofProvider) Stream(ctx context.Context, _ model.Request) (model.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	provider.calls.Add(1)
	text, _ := model.TextDelta("worker complete")
	completed, _ := model.Completed(model.NewUsage(1, 1))
	return &proofStream{values: []model.StreamEvent{text, completed}}, nil
}

func (provider *ProofProvider) Calls() int64 { return provider.calls.Load() }

type proofStream struct {
	values []model.StreamEvent
	index  int
}

func (stream *proofStream) Recv(ctx context.Context) (model.StreamEvent, error) {
	if err := ctx.Err(); err != nil {
		return model.StreamEvent{}, err
	}
	if stream.index >= len(stream.values) {
		return model.StreamEvent{}, io.EOF
	}
	value := stream.values[stream.index]
	stream.index++
	return value, nil
}

func (*proofStream) Close() error { return nil }

// NewProofPlanner contributes the replaceable typed planning stage.
//
// @Stage(name="planning.default", fallback=true)
func NewProofPlanner() planning.Planner { return &ProofPlanner{} }

// NewProofProvider contributes the worker provider.
//
// @Bean(name="planningProofProvider")
func NewProofProvider() *ProofProvider { return &ProofProvider{} }

// NewProofDispatcher contributes an empty immutable tool dispatcher.
//
// @Bean(name="planningProofDispatcher")
func NewProofDispatcher() (*stage.Dispatcher, error) { return stage.NewDispatcher(nil) }

// NewProofEngine binds portable snapshot compatibility to planner semantics.
//
// @Bean(name="planningProofEngine")
func NewProofEngine(
	planner planning.Planner,
	provider *ProofProvider,
	dispatcher *stage.Dispatcher,
) (*agent.Engine, lifecycle.Cleanup, error) {
	identity, err := planning.SemanticIdentity("planning-proof:v1", planner.Identity())
	if err != nil {
		return nil, nil, err
	}
	options := agent.DefaultEngineOptions()
	options.SnapshotCompatibilityIdentity = identity
	options.WorkspaceFingerprint = proofWorkspace
	engine, err := agent.NewEngineWithOptions(
		provider, dispatcher, &agent.AtomicIDSource{}, time.Now, nil, nil, options,
	)
	if err != nil {
		return nil, nil, err
	}
	return engine, engine.Shutdown, nil
}

// NewProofService contributes the explicit review-before-start boundary.
//
// @Bean(name="planningService")
func NewProofService(planner planning.Planner, engine *agent.Engine) (*planning.Service, error) {
	return planning.NewService(planner, engine)
}

// NewProof exposes generated exact-interface injection.
//
// @Bean(name="proof")
func NewProof(
	planner planning.Planner,
	service *planning.Service,
	engine *agent.Engine,
	provider *ProofProvider,
) *Proof {
	return &Proof{Planner: planner, Service: service, Engine: engine, Provider: provider}
}

var (
	_ planning.Planner = (*ProofPlanner)(nil)
	_ model.Provider   = (*ProofProvider)(nil)
)
