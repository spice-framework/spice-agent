# Phase 4 Installed Distribution Evidence

## Scope

This record reconciles the generated-distribution ledger with the later
`spice-agent-coding` process and release evidence. It distinguishes three
different claims that must not be collapsed:

1. executing the committed generated applications as independent operating
   system processes;
2. executing exact authenticated release bytes; and
3. interacting through a native PTY or ConPTY terminal.

The first two are proven within the limits below. The third remains open.

## Independent daemon and Bubble Tea processes

Distribution commit `7f36894a16768210242967431477eda3cc02c566`
introduced `TestInstalledDaemonAndTerminalReconnect`. The test builds the
committed generated `spice-agentd` and `spice-agent` package-main targets in
vendor-only, trimpath mode and launches both resulting executables as
independent processes. A pipe-backed accessible terminal drives the real Bubble
Tea event loop while the generated daemon serves authenticated current-user
local IPC.

The test proves all of the following without restarting the terminal process:

- explicit `spice-agentd serve` and `spice-agent attach --endpoint ...`;
- an in-flight streamed run surviving a deliberate transport fault by resuming
  from the exact acknowledged event sequence;
- exactly one terminal event for the recovered run;
- daemon process replacement between runs with a fresh process identity and
  session;
- a second successful run after replacement;
- preservation of both prompt-history entries in the same Bubble Tea process;
- graceful terminal shutdown and bounded daemon cleanup.

Cross-platform hardening landed before the published preview 2 source at
`c6b74c3740c20b54eb7c48205698921f6b2706c9`. Exact-head CI run
`31335588161` passed the complete repository contract on Windows and Ubuntu,
including shuffled and race-enabled installed-process acceptance, and also
passed the reusable macOS verification, vendor-offline proof, and required
aggregators.

This is an installed-process proof, not a native-terminal claim. The test owns
ordinary stdin/stdout pipes and selects the terminal's accessible renderer; it
does not allocate a PTY or ConPTY, send a native resize event, or inspect native
terminal presentation.

## Authenticated release bytes

The immutable preview 2 tag object
`bc453199cccab40bf8ec33578909044e2893ad03` points to distribution commit
`c6b74c3740c20b54eb7c48205698921f6b2706c9`. Release workflow run
`31336113907` completed validation, deterministic rendering, independent
verification, keyless attestation, provenance authentication, and publication.

Distribution commit `b4b9714fafe9c25d8f1808b98c700f1d7b335b44`
then added the separate `make verify-release-artifacts` boundary. It accepts
only the complete nine-subject set already authenticated by the independent
Toolchain verifier, rejects metadata/checksum/SBOM/archive drift, extracts the
native archive beneath a Unicode-and-spaces path, and executes the exact daemon
and terminal bytes. The retained preview 2 Linux/amd64 subject replay passed
both `--version` and `--check`, explicit serve/attach, zero-argument managed
sibling startup, and owned cleanup. The implementation's exact-head CI
`31341635872` and documentation run `31341635867` are terminal green.

The release-byte gate intentionally performs no model request and no terminal
presentation assertion. It proves executable identity, composition, local
publication, attach-or-start ownership, and cleanup after independent artifact
authentication.

## Remaining Phase 4 boundary

The remaining Phase 4 distribution exit is native-terminal acceptance. Windows
and Linux still need a real ConPTY/PTY workflow that proves initial
presentation, resize delivery, reconnect presentation after the established
transport and daemon-replacement cases, and clean interactive shutdown. That
proof must run the installed distribution bytes and must not be replaced by the
accessible pipe renderer, source-built adapters, or generated-component tests.

The Phase 5 two-supervisor developer-loop boundary is separate: it requires the
production daemon and terminal `spice dev` supervisors to run simultaneously
through a daemon-only invalid edit, last-known-good continuity, valid daemon
replacement, and visible Bubble Tea reconnection without restarting the
terminal supervisor.
