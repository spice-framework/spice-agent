# Phase 4 gRPC endpoint-authentication evidence

## Bounded outcome

The local daemon boundary now has a reusable, fail-closed authentication
primitive without moving transport concerns into `daemon.RunHost`. A generated
endpoint token is 256 random bits, has one canonical unpadded base64url form,
and is accepted only through exactly one case-sensitive `Bearer` value in gRPC
transport metadata.

## Proven invariants

| Invariant | Evidence |
| --- | --- |
| Cryptographic generation | `GenerateEndpointToken` reads exactly 32 bytes from the operating-system CSPRNG, rejects zero material, and has bounded attempts. Short, nil, and repeated-zero readers fail. |
| Canonical representation | Parsing rejects padding, whitespace, malformed syntax, wrong length, and the all-zero value; a valid token round-trips byte-for-byte through its authorization representation. |
| Secret safety | The token implements explicit string, Go-string, and format redaction. Tests exercise all standard formatting verbs, flags, width/precision, wrapped errors, structured logging, and JSON and reject encoded or raw credential material. |
| Exact metadata contract | Missing, malformed, wrong, case-changed, and duplicate authorization values receive one fixed `Unauthenticated` status before a handler is called. |
| Unary and stream parity | Direct middleware tests and a real generated `engine/v1` service over `bufconn` prove both RPC shapes. The stream wrapper preserves the service receiver and changes only the authenticated context. |
| No response leakage | Rejection status, unary headers/trailers, and stream trailers are checked for expected and presented credential material. |
| Installation safety | The interceptor constructor is package-private. The later public server constructor must install unary and streaming authentication together; exposing either middleware as an optional public server assembly step is not supported. |
| Architecture | gRPC remains in `daemon/grpcserver`; transport-independent `daemon`, kernel, and client contracts do not import it. No listener, endpoint discovery, metadata persistence, or production connection is introduced. |

## Verification

Focused tests run repeatedly with shuffled order and under the race detector.
Repository `make fast` covers every package, including real generated gRPC
service registration with vendor-only dependencies. The exact committed tree is
also required to pass `make verify` before this evidence is marked complete.

## Deliberate exclusions

This slice does not claim a public gRPC server, negotiated sessions, RPC
translation, message limits, deadlines, event/interaction streaming, a Unix
socket, a Windows named pipe, user-only endpoint metadata permissions, managed
startup, or a client adapter. Authentication occurs after gRPC framing and
Protobuf decode, which is the enforceable interceptor boundary, but before any
application handler can inspect daemon state.
