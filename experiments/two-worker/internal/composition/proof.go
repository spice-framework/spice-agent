package composition

import (
	"github.com/spice-framework/spice-agent/client"
	twoworker "github.com/spice-framework/spice-agent/experiments/two-worker"
	"github.com/spice-framework/spice-agent/tool"
)

// @import { Bean } from "github.com/spice-framework/spice/annotation/core"

// Proof captures the generated interface injection.
type Proof struct{ delegate tool.Tool }

type proofSession struct {
	client.Session
	connection client.Connection
}

func (session proofSession) Connection() client.Connection { return session.connection }

// NewProofSession contributes the application-owned public client boundary.
// The proof never executes it; real applications own the negotiated session.
//
// @Bean(name="workerSession")
func NewProofSession() (client.Session, error) {
	limits, err := client.NewLimits(2<<20, 256, 128, 2<<20, 8, 8)
	if err != nil {
		return nil, err
	}
	protocol, err := client.NewProtocolVersion(1, 3, 0)
	if err != nil {
		return nil, err
	}
	build, err := client.NewBuild("two-worker-proof", "experimental", "generated", "go1.26.5")
	if err != nil {
		return nil, err
	}
	definitionRef, err := client.NewDefinitionRef("worker", "revision-1")
	if err != nil {
		return nil, err
	}
	definition, err := client.NewDefinition(definitionRef, "scripted", 1)
	if err != nil {
		return nil, err
	}
	catalog, err := client.NewCatalog("proof", []client.Definition{definition}, limits)
	if err != nil {
		return nil, err
	}
	health, err := client.NewHealth(client.HealthReady, nil, 0, limits)
	if err != nil {
		return nil, err
	}
	connection, err := client.NewConnection(client.ConnectionSpec{
		Protocol: protocol, Server: build, Limits: limits, Health: health,
		ClientID: "two-worker-proof", OwnershipEpoch: 1, Catalog: catalog,
	})
	if err != nil {
		return nil, err
	}
	return proofSession{connection: connection}, nil
}

// NewWorkerDelegate contributes the ordinary tool interface bean.
//
// @Bean(name="worker.delegate")
func NewWorkerDelegate(session client.Session) (tool.Tool, error) {
	definition, err := client.NewDefinitionRef("worker", "revision-1")
	if err != nil {
		return nil, err
	}
	return twoworker.NewDelegate(session, twoworker.Options{Definition: definition, MaximumEvents: 64})
}

// NewProof proves generated interface injection without a registry.
//
// @Bean(name="proof")
func NewProof(tools map[string]tool.Tool) *Proof { return &Proof{delegate: tools[twoworker.ToolName]} }

// DelegateName returns the generated canonical tool identity.
func (proof *Proof) DelegateName() string { return proof.delegate.Definition().Name() }
