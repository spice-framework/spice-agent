package composition

// @import { Application } from "github.com/spice-framework/spice/annotation/core"

// SQLiteRecoveryProof is compile-time metadata and is never executed.
//
// @Application
func SQLiteRecoveryProof(*Proof) {
	panic("Spice must never execute an application marker")
}
