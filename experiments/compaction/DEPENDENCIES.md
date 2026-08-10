# Dependency review

The product implementation imports only the standard library and public
`message`/`model` packages from the exact released
`github.com/spice-framework/spice-agent v0.1.0-preview.5` dependency. The
generated proof additionally uses exact
`github.com/spice-framework/spice v0.1.0-preview.2` contracts. Tool dependencies
are the same pinned annotation/compiler tools used by the other nested proofs.

There is no compaction, tokenizer, database, network, telemetry, or model SDK
dependency. Consequently the implementation introduces no new runtime license,
maintenance, cancellation, observability, or vulnerability surface. `go.sum`,
`vendor/modules.txt`, and `compatibility.json` lock the exact graph; `replace`
directives are rejected.
