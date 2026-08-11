# RFC 0010: API and protocol compatibility before v1

Status: Accepted for pre-v1 enforcement; released-generation criteria proven

## Context

Spice Agent now has a public Go module, generated Protobuf packages, durable
snapshot and occurrence formats, and independently versioned engine and plugin
protocols. Source-built compatibility tests alone cannot define a support
promise. Conversely, freezing preview APIs before external extensions exercise
them would preserve accidental surface.

The repository therefore needs one machine-readable truth which records what
exists, what changed, and what is still unproven without presenting previews as
stable releases.

## Decision

The canonical policy is [`compatibility/policy.json`](../../compatibility/policy.json).
It references separate strict manifests for:

- the written public Go API, including generated protocol exports;
- engine protocol negotiation and source-built evidence;
- plugin protocol negotiation and source-built evidence;
- durable snapshots, plan identities, occurrences, events, and envelopes; and
- time-bounded security exceptions.

Every manifest is unknown-field rejecting, canonical JSON. Reviewed histories
are append-only. The quality gate validates cross-manifest references and
recomputes the current Go surface offline.

### Go API

All non-`internal`, non-`cmd` packages in the root module are public during the
preview period. The baseline contains exactly 26 packages. Build constraints
are evaluated independently for `darwin/arm64`, `linux/amd64`, and
`windows/amd64`; mutually exclusive files are never combined into a fictional
API. Generated `common/v1`, `engine/v1`, and `plugin/v1` declarations are in
scope.

The declaration digest is deliberately conservative. It includes exported
functions, methods, types, constants, variables, struct shapes, and each
source declaration's import bindings while excluding bodies and documentation.
An import-alias-only change may therefore require review, which is preferable
to silently accepting a source-impacting change before v1.

Pre-v1 breaks require an append-only record and a migration guide. The current
ledger records preview4 to preview5 and preview5 to preview6. After v1,
Go semantic import versioning applies: deprecations remain for two minor
releases and at least 180 days, and removal occurs only in the next module
major, except for the security process below.

### Engine and plugin protocols

Engine and plugin versions are negotiated and versioned independently. Engine
1.2 and 1.3 evidence is source-built semantic evidence, not released-binary
N/N-1 proof. Plugin 1.0 has independent source-built Go and Python process
evidence; native Python host containment is not claimed.

Stability requires two independently released generations for each protocol,
tested through old-client/new-server and new-client/old-server combinations.
The repository does not manufacture a generation or call two source builds a
released matrix.

Preview5 and preview6 now supply those immutable public module generations. A
dedicated runner downloads each through the public proxy and SumDB
into separate fresh caches, builds the same reviewed peer against each release,
and crosses both engine and plugin process directions. This is a
released-generation proof, not a claim that the module release publishes
prebuilt executable assets. Workflow run `31454312077` proves the exact matrix
on Linux and Windows at commit
`609f74f0abc7e3eba9f8a9ceab3c68ac17208ca2`; the evidence is recorded
canonically and both released-generation blockers are closed.

### Durable identities

Version identifiers are semantic authority, not labels which may be reused.
Existing bytes are never reinterpreted under an existing version. Unsupported
state fails closed before authority is leased. Automatic migration is absent
until a bounded migration can preserve every security-significant identity;
the current snapshot and plan hard cuts are recorded explicitly.

### Generated source

Toolchain `v0.1.0-preview.2` is the immutable non-development generator. Its
module sums, source commit, schema 6 ownership rules, migration range, path
policy, manual-edit behavior, and determinism are locked by
`compatibility/generated-source.json`. The original migration covered the root
and seven nested Agent targets at schema 5/`0.1.0-dev`. After the optional
telemetry experiment was promoted into production logging, that nested target
was retired and the current manifest covers the root plus six nested targets.
The historical eight-target migration remains recorded rather than rewritten.
The same released generator is also exercised by all three independent
clean-room public-authoring modules, so both the generated-source and
public-authoring criteria are complete; repository-context experiments did not
substitute for those proofs.

### Security exceptions

Compatibility may be narrowed for a vulnerability, but never by silently
downgrading to a withdrawn version. An exception must name the advisory,
affected range, fixed version, user effect, effective/review dates, migration,
and status. Active exceptions and immutable history use unique identifiers and
are rejected when incomplete. Ordinary compatibility changes cannot use this
ledger as an escape hatch.

## Consequences

The project has an enforceable pre-v1 review boundary without claiming v1.
Intentional API work updates the current platform digests and appends a reviewed
break/migration record in the same commit. Historical digests and durable
history cannot be rewritten.

The clean-room public-authoring, generated-source, and released-generation
criteria are complete. The policy has no enumerated v1 blockers, but it remains
explicitly pre-v1 and does not itself declare v1. Stable kernel benchmark
budgets are now a canonical enforced contract: five fixed samples,
median time/byte/allocation ceilings, 20% time and 10% allocation material
regression thresholds, and a measured-evidence requirement for budget changes.
The remaining facts cannot be satisfied by documentation alone.

## Deletion and migration

The machine-readable manifests are permanent compatibility history once a v1
contract exists. The preview-only baseline generator mode may be removed after
an independently maintained API-diff tool consumes the same canonical policy.
Removing a public package, durable decoder, or protocol generation still
requires the policy's deprecation and migration process.
