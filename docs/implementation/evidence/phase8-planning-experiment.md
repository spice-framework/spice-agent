# Phase 8 deterministic planning experiment

The removable `experiments/planning` module pins Agent
`v0.1.0-preview.5` and Spice `v0.1.0-preview.2` without a replacement. It adds
no root dependency or kernel, daemon, protocol, provider, tool, process, or
interaction API.

## Proven contract

- `planning.Planner` is a named exact-interface typed `stage.Stage` supplied by
  the generated Spice graph.
- `Service.Prepare` invokes only that compiled stage and returns immutable
  inspectable content. `StartPrepared` is a separate explicit acceptance call.
- One dedicated final text part carries canonical JSON bounded to 64 KiB.
  SHA-256 identities bind the contract, producer, worker definition, original
  user message, goal, ordered steps, and backward-only dependencies.
- Parsing rejects unknown fields, trailing content, noncanonical encodings,
  corrupt identities, invalid UTF-8, duplicate/forward dependencies, and every
  structural bound.
- Suspended and terminal snapshots preserve the exact bytes. Resume with the
  same semantic identity does not rerun the planner; a changed planner identity
  fails before provider execution.
- Planner cancellation, errors, panics, invalid drafts, corrupt prepared values,
  and review decline start no worker. Error and panic text is never propagated.
- A real plan recommending a mutating tool remains subject to the terminal
  `ToolDispatchGuard`; denial prevents tool execution and produces the ordinary
  Agent failure lifecycle.
- Generated code calls the planner, engine, and service constructors directly,
  with no registry, reflection, or runtime scan.

## Deliberate exclusions

The plan is prompt content, not execution authority. The experiment contains no
daemon pre-start hook, provider wrapper, hidden model call, filesystem or
network operation, process launch, interaction request, automatic retry, or
global state. A model-assisted planner must later be proven as a visible first
Agent run followed by independent review and a second worker run; this module
does not claim that workflow.

The compatibility manifest, security/dependency reviews, benchmarks, generated
source, fuzz target, race tests, committed vendor tree, and root quality gate
are the executable completion evidence.
