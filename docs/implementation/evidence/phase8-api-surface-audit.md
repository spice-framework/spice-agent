# Phase 8 API usage and conformance evidence

The public Go inventory is the exact 29-package set in
`compatibility/go-api.json`. The only addition in this slice is
`client/conformance`, an intentional black-box client lifecycle suite. It uses
only the public standard-library `client` contract and is consumed by the
generated gRPC client acceptance. The initial profile proves a complete run,
a waiting run cancelled through `Session.Cancel`, the cancelled terminal,
zero active runs, interaction snapshot/tail framing, reconnect fencing,
context cancellation, and authoritative cleanup errors for every owned stream
and session. Interaction response remains in its existing owning suites. A
second independent consumer in the Coding
architecture end-to-end harness is deliberately pending until this API is
frozen for an exact repin. It owns sessions and streams but never discovers,
authenticates, launches, or closes a caller-owned connector.

`compatibility/api-usage.json` assigns every public package a reviewed retain
disposition and at least two concrete consumer, contract, or explicitly pending
evidence identities.
The quality gate requires that inventory to equal the Go API manifest exactly;
unknown, missing, reordered, unresolved, or evidence-free entries fail. This is
the package-level pre-v1 deletion review. Future exported-package additions must
enter both manifests in the same reviewed change, and removals still require
the existing migration policy.
