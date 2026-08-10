# Git workflow security boundary

This experiment is an authority-constrained command adapter, not a Git sandbox
or operating-system privilege boundary. Git and its repository run with the
application process's privileges.

- The configured Git path must be absolute and canonical. Its regular-file
  identity is held open, and its exact SHA-256 is checked before and after each
  invocation. Preview5 cannot atomically execute that verified handle, so
  promotion is explicitly blocked on the released generic verified launcher.
- Repository and private configuration identities are held open. The hooks
  directory must remain empty, the global configuration must remain an empty
  regular file, and every identity is checked before and after execution.
- Application environment is explicit. Every `GIT_*` override is rejected;
  system/global configuration, prompts, credential helpers, askpass, pagers,
  editors, hooks, fsmonitor commands, automatic maintenance, and signing are
  disabled by fixed runner values. Other repository-local configuration remains
  inside the application-selected repository boundary.
- The argument set is closed in code. Model input supplies only a bounded
  commit message through stdin; it never becomes an executable path, option,
  ref, revision, pathspec, environment name, or process argument.
- Authority is not inferred from MCP/Git metadata or tool arguments. A
  run-owned interaction must return the exact deterministic token bound to the
  approved staged digest. The single-use grant is revoked at continuation
  return and cannot authorize another call.
- Bounded stdout/stderr and process-tree containment apply on Windows and Unix.
  Cancellation requests graceful termination then force-kills and joins the
  tree. Mutating failures after child ownership transfers are uncertain and
  never replayable.

The pre/post strategy narrows but does not close the verification-to-execution
pathname race. Do not promote or describe the experiment as TOCTOU-resistant
until it consumes an immutable released `VerifiedLauncher`.
