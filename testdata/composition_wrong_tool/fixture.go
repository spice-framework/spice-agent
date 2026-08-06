// Package composition_wrong_tool is a negative standalone annotation fixture.
package composition_wrong_tool

// @import { Tool } from "github.com/spice-framework/spice-agent/annotation/agent"

// @Tool(name="wrong-tool")
func WrongTool() string { return "not a tool" }
