# Phase 0: Repositories and Governance

**Objective:** establish five independently versioned Apache-2.0 repositories.
The core repository owns protocols and conformance; TUI, OpenAI, coding tools,
and distribution own their implementations. All use `main`, exact Go 1.26.5,
committed vendor contents, immutable tags, signed artifacts, SBOMs, and local
verification as the gate.

Repositories must join the development catalog and compatibility graph. No
committed `replace` directive is permitted. **Exit:** clean-clone/offline proof
and catalog compatibility. **Status:** in progress.
