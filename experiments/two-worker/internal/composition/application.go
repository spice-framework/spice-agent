package composition

// @import { Application } from "github.com/spice-framework/spice/annotation/core"

// TwoWorkerProof is compile-time metadata and is never executed.
//
// @Application
func TwoWorkerProof(*Proof) {
	panic("Spice must never execute an application marker")
}
