// Package process defines the provider-neutral process-launch contract used by
// compiled tools and daemon applications.
//
// The package describes process intent and ownership only. Platform launch,
// process-tree containment, and resource joining belong to injected Launcher
// implementations. In particular, this package does not claim that an
// operating system can universally contain descendants.
//
// @import { NamedInterface } from "github.com/spice-framework/spice/annotation/modulith"
// @NamedInterface("process")
package process
