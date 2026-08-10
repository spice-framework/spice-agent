# Deterministic advisory planning experiment

This removable nested module proves an application-owned planning workflow on
the public Agent `v0.1.0-preview.5` and Spice `v0.1.0-preview.2` contracts.

An application injects one named typed `Planner` stage. `Service.Prepare`
validates the worker definition and original user message, invokes only that
stage, finalizes bounded canonical JSON with SHA-256 identities, and appends it
as one dedicated text part. The call creates no Agent run. The caller inspects
the returned `Prepared` value and invokes `StartPrepared` only after accepting
the advisory content.

The attached bytes enter ordinary Agent history, terminal and suspended
snapshots, and resume. `Extract` revalidates the canonical bytes, plan identity,
and original input digest. `SemanticIdentity` binds portable snapshot
compatibility to the planner and local renderer semantics, so a different
planner identity cannot resume the snapshot.

Plans are prompt content. They never select tools, bypass a
`ToolDispatchGuard`, grant an interaction scope, approve mutations, or modify a
tool-plan lease. Tests prove a plan recommending a mutating tool is still denied
by the terminal guard.

There is deliberately no daemon hook, provider wrapper, hidden model request,
filesystem/process/network call, runtime registry, or automatic retry. A future
model-assisted planner must be a visible separate Agent run whose final bounded
JSON is reviewed before a distinct worker run starts; it is not claimed here.

Run `make fast`, `make check`, `make verify`, and `make benchmark` from this
directory. All ordinary gates use committed vendor contents with network access
disabled.
