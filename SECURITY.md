# Security Policy

## Supported versions

The current preview and the immediately previous preview receive security fixes
when the fix is technically safe and preserves the security contract. Older or
withdrawn previews are unsupported and must not be selected as a downgrade.
The exact current/previous identities and process are machine-readable in
`compatibility/security-process.json`.

## Reporting

Do not open public issues for suspected vulnerabilities. Use GitHub's private
security advisory flow for `spice-framework/spice-agent` and include affected
versions, reproduction, impact, and suggested remediation when known.

Maintainers target acknowledgement within three calendar days and initial
triage within seven. Confirmed issues use coordinated disclosure. Resolution
requires a fixed release or explicit withdrawal with user migration guidance;
a backport is allowed only when it preserves the same security contract. Do not
publish exploit details before coordinated disclosure completes.

## Trust boundary

Compiled Go extensions and authorized Spice annotation tools are trusted supply
chain components. Runtime plugins are isolated processes but are not sandboxes.
The architecture-proof coding distribution intentionally runs tools with the
user process's privileges and must disclose that fact prominently.

Secrets must never appear in generated source, events, logs, manifests, errors,
snapshots, or test fixtures. Network access must be explicit, cancellable, and
owned by an injected provider or extension.

## Dependency updates

Critical dependency reports are reviewed within one day. The complete graph is
reviewed at least every 30 days. Updates must pass tidy, gosec, govulncheck,
license review, vendor reproducibility, offline tests, and the full repository
gate. See `docs/dependencies.md`. Security exceptions are append-only,
time-bounded, and may never authorize a downgrade to a withdrawn version.
