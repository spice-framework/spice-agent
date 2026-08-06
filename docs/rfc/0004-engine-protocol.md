# RFC 0004: Engine Protocol

**Status:** draft. `common/v1` and `engine/v1` use Protobuf over local gRPC for
initialization, capability/version negotiation, health, run start, event stream
with `after_sequence`, cancellation, interaction responses, and snapshots.
Unknown additive fields are tolerated; incompatible major versions fail before
run creation. Replay and queues are bounded and observable.

