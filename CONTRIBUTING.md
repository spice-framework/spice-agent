# Contributing

Use Go 1.26.5 and read `AGENTS.md` plus the applicable RFC before changing a
contract. Keep commits narrow, add deterministic failure tests, and run:

```text
make fast
make check
make verify
```

Public APIs remain pre-1.0. Architecture changes require an RFC or ADR update.
Contributions are accepted under Apache-2.0.

