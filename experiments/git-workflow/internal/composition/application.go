package composition

// @import { Application } from "github.com/spice-framework/spice/annotation/core"

// GitWorkflowProof is compile-time metadata and is never executed.
//
// @Application
func GitWorkflowProof(*Proof) {
	panic("Spice must never execute an application marker")
}
