# Threat model

This document makes the security boundary in
[RFC 0008](rfc/0008-security-responsibility.md) operational. It does not claim
that runtime plugins, compiled extensions, or local tools are sandboxes.

## Assets and actors

Protected assets are user credentials and content, tool and interaction
authority, exact executable and module identity, endpoint credentials, durable
run/snapshot authority, generated ownership, and release provenance. Relevant
actors are the local user, an authorized application, compiled extensions,
runtime-plugin processes, providers, client adapters, and an attacker able to
modify local files, endpoints, dependency inputs, or process timing within the
user account.

Compiled extensions and selected executables are trusted supply-chain code.
Runtime plugins are separate processes but share the user's privilege boundary.
Model output, tool arguments, endpoint metadata, recovered durable bytes, and
dependency-controlled errors are untrusted inputs.

## Trust boundaries and abuse cases

| Boundary | Representative abuse | Required mitigation |
| --- | --- | --- |
| generated composition | hidden registry, runtime scan, stale or hand-edited generated source | exact typed generated graph, schema 6 ownership, deterministic regeneration |
| tool dispatch | unknown tool, undeclared mutation, automatic replay of uncertainty | immutable tool plan, terminal ordered guards, effect/replay/capability facts, fail-closed uncertainty |
| local daemon IPC | remote endpoint, stolen token, stale ownership, response-loss replay | current-user endpoint scope, 256-bit bearer credential, stable identity epochs, exact attempt IDs |
| runtime plugin | substituted executable, manifest collision, crash or post-retirement call | retained verified executable lease, authenticated handshake, immutable generation lease, containment and bounded shutdown |
| durable state | forged snapshot, stale authority, ambiguous persistence | keyed envelope verification, OS-bound authority store, two-phase publication, retained uncertainty |
| logs and errors | secret or dependency text disclosure | closed status vocabulary, bounded typed facts, HMAC pseudonyms, canary scans |
| dependencies and releases | compromised module, unreviewed license, mutable artifact | pinned modules, reproducible vendor, vulnerability/license gates, immutable signed release provenance |

## Residual risk

Trusted compiled code and processes running as the user can exercise that
user's authority. A malicious same-user process may attack resources outside
Agent's retained handle and endpoint invariants. Providers may transmit data
only when an application explicitly configures that network boundary. Policy
interception reduces accidental or model-directed authority; it is not a host
sandbox. Security fixes therefore may require a fixed release, withdrawal, and
user migration rather than a compatibility workaround.

The supported response and dependency update process is defined by
[`compatibility/security-process.json`](../compatibility/security-process.json)
and [SECURITY.md](../SECURITY.md). Exceptions cannot authorize a security
downgrade and are recorded in
[`compatibility/security-exceptions.json`](../compatibility/security-exceptions.json).
