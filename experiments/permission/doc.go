// Package permission is an experimental, fail-closed policy guard for Spice
// Agent tool dispatch. It is deliberately outside the stable Agent module API.
//
// The package receives only bounded metadata: it never receives call arguments,
// tool schemas, paths, environment values, secrets, interaction responses, or
// raw run/call/plan identities. Prompting uses the run-owned interaction
// capability carried by stage.ToolDispatchScope, not interaction.Broker.
package permission
