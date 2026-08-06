# Security Policy

## Reporting

Do not open public issues for suspected vulnerabilities. Use GitHub's private
security advisory flow for `spice-framework/spice-agent` and include affected
versions, reproduction, impact, and suggested remediation when known.

## Trust boundary

Compiled Go extensions and authorized Spice annotation tools are trusted supply
chain components. Runtime plugins are isolated processes but are not sandboxes.
The architecture-proof coding distribution intentionally runs tools with the
user process's privileges and must disclose that fact prominently.

Secrets must never appear in generated source, events, logs, manifests, errors,
snapshots, or test fixtures. Network access must be explicit, cancellable, and
owned by an injected provider or extension.

