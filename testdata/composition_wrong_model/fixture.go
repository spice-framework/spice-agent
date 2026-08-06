// Package composition_wrong_model is a negative standalone annotation fixture.
package composition_wrong_model

// @import { ModelProvider } from "github.com/spice-framework/spice-agent/annotation/agent"

// @ModelProvider(name="wrong-model")
func WrongModel() string { return "not a provider" }
