# Phase 5.0A Tool Execution Evidence

This bounded prerequisite establishes one execution-outcome contract before a
runtime-plugin protocol or generation manager exists.

| Contract | Executable evidence |
| --- | --- |
| Mandatory effect/replay metadata | `go test ./tool -run TestDefinitionRejectsMissingAndInvalidExecutionMetadata` |
| Canonical capability set and fingerprint | `go test ./tool -run TestDefinitionFingerprintCoversContractAndNormalizesCapabilities` |
| Bounded correlated typed errors | `go test ./tool -run TestExecutionErrorIsBoundedCorrelatedAndCancellationCompatible` |
| Dispatcher fail-closed validation | `go test ./stage -run 'TestDispatcher(PreservesAndValidatesTypedExecutionFailures|RejectsUntypedMismatchedAndAmbiguousExecutionFailures)'` |
| Model-visible result versus infrastructure failure | `go test ./agent -run TestEngineDistinguishesModelVisibleToolProblemsFromExecutionFailures` |
| Generated composition compatibility | `go test ./internal/compositionacceptance -run 'TestComposition(GenerationIsDeterministicAndOwnershipGuarded|GeneratedCompositionUsesOnlyDirectCalls)'` |
| Repository commit gate | `make verify` on the exact committed tree |

Negative coverage includes absent/unknown metadata, read-only mutation
capabilities, invalid effect/replay pairs, duplicate capabilities, unsafe retry
advice, uncertain read-only outcomes, wrong call IDs, untyped errors,
simultaneous result/error returns, nested failures, wrapped or joined typed
failures, oversized causes/siblings, and canceled causes. Accepted direct typed
failures preserve their original cause chain. Agent tests also assert that the
terminal tool-failure event retains bounded call ID, name, outcome, and retry
correlation.

This evidence deliberately excludes plugin protocol messages, dynamic plan
sources, generation leases, retry execution, and process-host behavior. Those
remain later Phase 5 slices.
