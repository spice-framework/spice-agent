package composition

// @import { Application } from "github.com/spice-framework/spice/annotation/core"

// TelemetryProof is compile-time metadata and is never executed.
//
// @Application
func TelemetryProof(*Proof) {
	panic("Spice must never execute an application marker")
}
