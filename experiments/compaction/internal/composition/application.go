package composition

// @import { Application } from "github.com/spice-framework/spice/annotation/core"

// CompactionProof is compile-time metadata and is never executed.
//
// @Application
func CompactionProof(*Proof) {
	panic("Spice must never execute an application marker")
}
