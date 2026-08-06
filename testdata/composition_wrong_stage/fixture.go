// Package composition_wrong_stage is a negative standalone annotation fixture.
package composition_wrong_stage

// @import { Stage } from "github.com/spice-framework/spice-agent/annotation/agent"

// @Stage(name="wrong-stage")
func WrongStage() string { return "not a stage" }
