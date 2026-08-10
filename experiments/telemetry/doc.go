// Package telemetry provides a bounded, best-effort, secret-safe derived view
// of Spice Agent events. It is not durable history and never participates in
// replay, recovery, readiness, or execution authority by default.
package telemetry

// ContractVersion identifies this experimental exporter-neutral projection.
const ContractVersion = "spice.agent.telemetry/projection-v1"
