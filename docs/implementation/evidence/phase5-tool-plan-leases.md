# Phase 5.0B Tool-Plan Lease Evidence

This bounded prerequisite establishes the generic immutable run-plan seam. It
does not implement a plugin protocol, process host, activation manager, daemon,
or language fixture.

| Contract | Executable evidence |
| --- | --- |
| Plan IDs, source-owned immutable generation, idempotent bounded release | `go test ./stage -run 'Test(PlanIDAndLeaseContracts|LeaseReleaseFailureIsBoundedObservableAndIdempotent|LeaseReleaseContextBoundsBlockingCallbackWithOneOutcome)'` |
| Static fallback and exact generation | `go test ./stage -run TestStaticToolPlanSourceLeasesExactGeneration` |
| Ordered, fail-closed decorators | `go test ./stage -run TestApplyToolDispatchDecorators` |
| Per-run old/new source separation | `go test ./agent -run TestRunsLeaseCurrentPlanAndKeepOldAndNewGenerationsSeparate` |
| Acquire/rollback and exactly-once release | `go test ./agent -run 'TestPlan(AcquireFailureMutatesNothingAndEmitsNoRun|LeaseReleasesOnceAcrossTerminalPathsAndRollback)'` |
| Release failure controls authoritative terminal | `go test ./agent -run TestReleaseFailureSelectsOneAuthoritativeFailedTerminal` |
| Blocking release cannot suppress terminal | `go test ./agent -run TestBlockingReleaseCannotSuppressAuthoritativeTerminal` |
| Exact snapshot generation and identity | `go test ./agent -run TestResumeSnapshotLeasesExactRecordedPlan` |
| Post-acquire cancellation rollback | `go test ./agent -run TestCancelledAcquisitionsReleaseBeforeStartOrResumeMutation` |
| Static compatibility and executable bean identities | `go test ./agent -run TestPlanIdentityCoversCompatibilityAndAllExecutableBeanCategories` |
| Cancellation/result commit boundary | `go test ./agent -run 'Test(CommittedToolResultWinsConcurrentCancellation|EngineDistinguishesModelVisibleToolProblemsFromExecutionFailures)'` |
| Combined execution/reporter durability | `go test ./agent -run TestToolExecutionAndReporterDurabilityFailuresRemainStructured` |
| Focused concurrency | `go test -race ./stage ./agent` |
| Repository commit gate | `make verify` on the exact committed tree |

Negative coverage includes nil, panicking, error-plus-lease, missing, wrong-ID,
and changed-generation sources; nil, panicking, nil-returning, and
definition-changing decorators; undeclared post-snapshot calls; release errors
and panics; start and resume rollback; cancellation sentinel spoofing; and
late cancellation after a valid committed tool result.
Default engines cannot import snapshots; configured static compatibility
mismatches make no generation call. The trusted source guarantees executable
behavior immutability because Go cannot structurally freeze function behavior.
