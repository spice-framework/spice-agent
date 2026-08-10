# Phase 8 clean-room public-authoring contract

The project has two maintainers and cannot truthfully require an outside human
author. External authorship was therefore an unachievable social dependency,
not a product, security, or compatibility property. It is removed as a release
blocker and is never simulated or claimed.

The replacement preserves the useful test: can a consumer build an extension
without maintainer-only knowledge? The canonical contract is
`compatibility/public-authoring.json`, referenced by
`compatibility/policy.json` and enforced by the root quality gate.

Completion requires three separately versioned modules. Each proof must:

- begin in a repository-external clean directory with fresh module and build
  caches and `GOWORK=off`;
- consume only immutable released modules, tools, and public documentation;
- contain no `replace`, `exclude`, or `retract` directive, private/internal
  import, absolute workspace path, or dependency on a maintainer checkout;
- include a generated Spice composition proof and committed reproducible
  vendor tree;
- complete install, configure, debug, test, package, and deletion exercises;
- pass on `linux/amd64` and `windows/amd64`, including vendor-offline tests; and
- record exact module, version, commit, profile, platforms, and hosted
  verification evidence before the manifest may set `proven`.

Models may review the public instructions and resulting source, but model
review is not represented as human authorship. Existing first-party experiments
do not satisfy this contract because they were developed with repository
context and before the public clean-room boundary was frozen.

The proof remains pending until all three evidence records exist. Removing the
impossible external-human requirement does not mark any proof complete and does
not weaken the remaining generated-source or released-protocol blockers.
