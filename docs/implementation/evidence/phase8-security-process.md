# Phase 8 security process evidence

`compatibility/security-process.json` freezes the supported pre-v1 response and
dependency update process. The current and previous preview are supported when
a technically safe fix preserves the security contract; withdrawn versions
fail closed. Reports use GitHub private security advisories, with three-day
acknowledgement and seven-day triage targets, coordinated disclosure, and a
fixed release or withdrawal decision.

Critical dependency issues receive review within one day and the routine graph
is reviewed at least every 30 days. Every update passes tidy, gosec,
govulncheck, license, and vendor-reproducibility gates. Private vulnerability
reporting and Dependabot security updates are the repository controls recorded
by the governance evidence. The manifest links the append-only fail-closed
security exception contract; neither process may authorize hidden network
access or a downgrade to a withdrawn version.
