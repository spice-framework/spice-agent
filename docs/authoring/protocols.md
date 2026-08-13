# Protocol and client authoring

The transport-neutral [`client`](../../client) package is the preferred client
API. Authentication, endpoint discovery, gRPC, and operating-system IPC belong
to adapters. The matching daemon wire contract is published as Protobuf source
under [`proto/spice/agent`](../../proto/spice/agent).

The authoritative protocol records are:

- [`engine/v1/compatibility.json`](../../engine/v1/compatibility.json) for the
  engine range and source-built semantic profiles;
- [`plugin/v1/compatibility.json`](../../plugin/v1/compatibility.json) for the
  independently versioned runtime-plugin protocol;
- [`compatibility/released-generation.json`](../../compatibility/released-generation.json)
  for the immutable preview5/preview6 cross-generation matrix; and
- [`schema-baseline`](../../schema-baseline) for committed Buf breaking-change
  inputs.

The reusable [`client/conformance`](../../client/conformance) suite exercises
the initial local client lifecycle through public `client.Connector` and
`client.Session` contracts. It covers negotiation truthfulness, health, one
complete run and event tail, the interaction snapshot/control prefix,
reconnect fencing, cancellation, and cleanup. Authentication rejection,
malformed wire traffic, protocol downgrade/replay, suspend/resume, and signed
snapshot transfer remain in their specialized adapter and protocol suites.

Runtime-plugin implementers use
[`plugin/conformance`](../../plugin/conformance). That black-box suite validates
the canonical tool manifest, handshake, echo, typed failure, malformed calls,
cancellation, drain, and shutdown. Neither conformance package discovers or
launches a process or grants runtime authority.

Public package documentation is included in repository source and published by
pkg.go.dev for released modules. Pre-v1 API changes must follow
[the compatibility policy](../compatibility.md) and add a concrete guide under
[`docs/migrations`](../migrations). Existing guides cover only changes that
actually occurred; a new migration must not be invented without a reviewed
break.
