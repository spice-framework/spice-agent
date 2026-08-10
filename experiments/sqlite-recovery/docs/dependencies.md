# Dependency and security review

`github.com/ncruces/go-sqlite3 v0.35.3` is the sole storage implementation. It
uses a generated WebAssembly SQLite runtime rather than cgo, is BSD-3-Clause,
supports `database/sql` contexts and transactions, and is maintained on the
same public module path. The connection is deliberately single-owner because
individual driver connections are not concurrently safe. WAL and the bounded
busy timeout provide local contention behavior; cancellation remains
cooperative while SQLite is executing.

The alternative `modernc.org/sqlite v1.56.0` was not selected for this prototype:
its translated C toolchain graph and vendor footprint are materially larger.
That is not a quality judgment and may be revisited with measured evidence.

The dependency is confined to this nested module. Exact sums are locked in
`compatibility.json`, committed vendor enables offline builds, and no database
module is added to the root Agent graph. No migration or download occurs in the
background.
