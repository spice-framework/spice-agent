# Architecture

```text
generated planning.Planner stage
             |
             v
      Service.Prepare
             |
  immutable Prepared value ---- application review/decline
             |
             v explicit acceptance
   Service.StartPrepared
             |
       agent.Engine
```

The plan is a dedicated final text part in the initial user message. Its JSON
uses a fixed struct-field order and accepts only its byte-identical canonical
encoding. `input_sha256` binds the worker definition and every original message
field. `plan_sha256` binds the contract version, planner semantic identity,
input identity, goal, ordered steps, and backward-only dependencies.

The engine remains authoritative for run identity, events, tools, guards,
interactions, snapshots, and cancellation. The planning package never imports
those execution authorities except the public `agent` values required to start
and inspect a run and the generic typed `stage.Stage` interface.

The current daemon starts `agent.Input` directly and intentionally has no
generic pre-start transformer. Integrating this workflow there would require an
application-owned host outside the kernel, not a planner concept in `RunHost`.
