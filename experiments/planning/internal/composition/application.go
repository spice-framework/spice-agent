package composition

// @import { Application } from "github.com/spice-framework/spice/annotation/core"

// PlanningProof is compile-time metadata and is never executed.
//
// @Application
func PlanningProof(*Proof) {
	panic("Spice must never execute an application marker")
}
