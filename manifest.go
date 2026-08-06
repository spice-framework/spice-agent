package spiceagent

import "github.com/spice-framework/spice/starter"

// Manifest identifies the compiled SDK. It does not discover or activate beans;
// applications construct the kernel explicitly or through future annotations.
var Manifest = starter.Must(starter.Spec{
	Schema:    starter.Schema,
	ID:        "github.com/spice-framework/spice-agent",
	Version:   "0.1.0-dev",
	Module:    "github.com/spice-framework/spice-agent",
	SpiceAPI:  starter.APIVersion,
	MinimumGo: "1.26.5",
	License:   "Apache-2.0",
	Review:    "docs/dependencies.md",
	Activation: starter.Activation{
		Mode: starter.ActivationExplicitConstructor,
		EntryPoints: []starter.EntryPoint{{
			Package: "github.com/spice-framework/spice-agent/agent",
			Symbol:  "NewEngine",
		}},
	},
	Capabilities: []string{
		"agent.kernel",
		"agent.messages",
		"agent.model-spi",
		"agent.tool-spi",
	},
})
