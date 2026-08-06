package compositionfixture

// @import { Application } from "github.com/spice-framework/spice/annotation/core"

// CompositionProof is compile-time metadata and is never executed.
//
// @Application
func CompositionProof(*Proof) {
	panic("Spice must never execute an application marker")
}
