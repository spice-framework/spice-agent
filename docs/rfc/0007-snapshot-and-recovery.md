# RFC 0007: Snapshot and Recovery

**Status:** draft pending SQLite stress proof. A snapshot contains validated
provider-neutral run state and the last event sequence; it excludes clients,
secrets, functions, processes, and mutable registries. Import validates schema,
identity, monotonic sequence, and terminal state. Uncertain mutating operations
are recorded explicitly and never replayed automatically.

