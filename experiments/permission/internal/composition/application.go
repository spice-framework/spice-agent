package composition

// @import { Application } from "github.com/spice-framework/spice/annotation/core"

// PermissionProof is compile-time metadata and is never executed.
//
// @Application
func PermissionProof(*Proof) {
	panic("Spice must never execute an application marker")
}
