// Package compaction provides a removable, deterministic model-request
// compaction experiment. It wraps an application-owned model.Provider and
// rewrites only the transient request sent to that provider. Authoritative run
// history, events, snapshots, tools, and interactions remain engine-owned and
// unchanged.
package compaction
