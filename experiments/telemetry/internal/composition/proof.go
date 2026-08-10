package composition

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/spice-framework/spice-agent/agent"
	"github.com/spice-framework/spice-agent/daemon"
	"github.com/spice-framework/spice-agent/event"
	telemetry "github.com/spice-framework/spice-agent/experiments/telemetry"
	"github.com/spice-framework/spice-agent/model"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice/lifecycle"
)

// @import { Bean } from "github.com/spice-framework/spice/annotation/core"

// Proof exposes generated construction and lifecycle evidence without a registry.
type Proof struct {
	Engine    *agent.Engine
	Processor *telemetry.Processor
	Exporter  *ProofExporter
	Lifecycle *ProofLifecycle
	Provider  *ProofProvider
	Health    daemon.HealthSource
}

// ProofLifecycle records only fixed cleanup identities.
type ProofLifecycle struct {
	mu    sync.Mutex
	order []string
}

func (lifecycle *ProofLifecycle) mark(value string) {
	lifecycle.mu.Lock()
	lifecycle.order = append(lifecycle.order, value)
	lifecycle.mu.Unlock()
}

// Order returns a defensive lifecycle snapshot.
func (lifecycle *ProofLifecycle) Order() []string {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	return append([]string(nil), lifecycle.order...)
}

// ProofExporter is an application-owned, non-network exporter fixture.
type ProofExporter struct {
	lifecycle *ProofLifecycle
	mu        sync.Mutex
	records   int
	shutdowns int
}

func (exporter *ProofExporter) Export(ctx context.Context, batch telemetry.Batch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	exporter.mu.Lock()
	exporter.records += batch.Records()
	exporter.mu.Unlock()
	return nil
}

func (exporter *ProofExporter) Shutdown(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	exporter.mu.Lock()
	exporter.shutdowns++
	exporter.mu.Unlock()
	exporter.lifecycle.mark("telemetry")
	return nil
}

// Evidence returns exported record and shutdown counts.
func (exporter *ProofExporter) Evidence() (int, int) {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	return exporter.records, exporter.shutdowns
}

// ProofProvider blocks cooperatively so generated Engine.Shutdown must first
// cancel and finalize the active run before telemetry closes its mailbox.
type ProofProvider struct {
	once    sync.Once
	started chan struct{}
}

func (provider *ProofProvider) Stream(context.Context, model.Request) (model.Stream, error) {
	provider.once.Do(func() { close(provider.started) })
	return proofStream{}, nil
}

// Started closes after the generated engine reaches the provider.
func (provider *ProofProvider) Started() <-chan struct{} { return provider.started }

type proofStream struct{}

func (proofStream) Recv(ctx context.Context) (model.StreamEvent, error) {
	<-ctx.Done()
	return model.StreamEvent{}, ctx.Err()
}

func (proofStream) Close() error { return nil }

// NewProofConfig contributes explicit bounded, deterministic proof settings.
//
// @Bean(name="telemetryConfig")
func NewProofConfig() (telemetry.Config, error) {
	config := telemetry.DefaultConfig()
	material := make([]byte, 32)
	for index := range material {
		material[index] = byte(index + 1)
	}
	key, err := telemetry.NewCorrelationKey(material)
	if err != nil {
		return telemetry.Config{}, err
	}
	config.CorrelationKey = key
	config.FlushInterval = time.Second
	return config, nil
}

// NewProofLifecycle contributes fixed cleanup evidence.
//
// @Bean(name="telemetryProofLifecycle")
func NewProofLifecycle() *ProofLifecycle { return &ProofLifecycle{} }

// NewProofExporter contributes the application-owned exporter.
//
// @Bean(name="telemetryExporter")
func NewProofExporter(proofLifecycle *ProofLifecycle) telemetry.Exporter {
	return &ProofExporter{lifecycle: proofLifecycle}
}

// NewProofMailbox contributes the sole best-effort queue.
//
// @Bean(name="telemetryMailbox")
func NewProofMailbox(config telemetry.Config) (*event.BestEffortObserver, error) {
	return telemetry.NewMailbox(config)
}

// NewProofProcessor contributes the one consumer and its generated cleanup.
//
// @Bean(name="telemetryProcessor")
func NewProofProcessor(
	config telemetry.Config,
	mailbox *event.BestEffortObserver,
	exporter telemetry.Exporter,
) (*telemetry.Processor, lifecycle.Cleanup, error) {
	return telemetry.NewProcessor(config, mailbox, exporter)
}

// NewProofHealth contributes optional fixed-code health; default config keeps
// it outside readiness decisions.
//
// @Bean(name="telemetryHealth")
func NewProofHealth(config telemetry.Config, processor *telemetry.Processor) daemon.HealthSource {
	return telemetry.NewHealthSource(config, processor)
}

// NewProofProvider contributes a cooperative model provider.
//
// @Bean(name="telemetryProofProvider")
func NewProofProvider() *ProofProvider { return &ProofProvider{started: make(chan struct{})} }

// NewProofDispatcher contributes an empty immutable dispatcher.
//
// @Bean(name="telemetryProofDispatcher")
func NewProofDispatcher() (*stage.Dispatcher, error) { return stage.NewDispatcher(nil) }

// NewProofEngine forces generated construction after telemetry. Reverse cleanup
// therefore shuts the real engine down before closing the mailbox.
//
// @Bean(name="telemetryProofEngine")
func NewProofEngine(
	processor *telemetry.Processor,
	mailbox *event.BestEffortObserver,
	provider *ProofProvider,
	dispatcher *stage.Dispatcher,
	proofLifecycle *ProofLifecycle,
) (*agent.Engine, lifecycle.Cleanup, error) {
	if processor == nil {
		return nil, nil, errors.New("telemetry proof processor is nil")
	}
	engine, err := agent.NewEngine(
		provider, dispatcher, &agent.AtomicIDSource{}, time.Now, nil,
		[]*event.BestEffortObserver{mailbox},
	)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func(ctx context.Context) error {
		if shutdownErr := engine.Shutdown(ctx); shutdownErr != nil {
			return shutdownErr
		}
		proofLifecycle.mark("engine")
		return nil
	}
	return engine, cleanup, nil
}

// NewProof exposes exact generated dependencies.
//
// @Bean(name="proof")
func NewProof(
	engine *agent.Engine,
	processor *telemetry.Processor,
	exporter telemetry.Exporter,
	proofLifecycle *ProofLifecycle,
	provider *ProofProvider,
	health daemon.HealthSource,
) *Proof {
	proofExporter, _ := exporter.(*ProofExporter)
	return &Proof{
		Engine: engine, Processor: processor, Exporter: proofExporter,
		Lifecycle: proofLifecycle, Provider: provider, Health: health,
	}
}

var (
	_ model.Provider     = (*ProofProvider)(nil)
	_ telemetry.Exporter = (*ProofExporter)(nil)
)
