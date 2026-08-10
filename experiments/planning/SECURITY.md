# Security boundary

- Plans may contain user-derived prompt injection. They are never permission,
  approval, dispatch, interaction, or tool-plan authority.
- Planner code is trusted application code with ordinary process privileges.
  This experiment does not sandbox it.
- Planner errors and panic values are replaced with fixed messages. They are
  not copied into plans, events, or worker input.
- Canonical SHA-256 values prove deterministic integrity, not secrecy. They
  should not be logged by default because they can correlate inputs.
- Snapshots intentionally contain the advisory plan and therefore inherit the
  application's snapshot confidentiality and retention policy.
- No production planning package opens files, launches processes, performs
  network I/O, calls a provider, dispatches a tool, or requests interaction.
- Cancellation before or during preparation starts no worker run. After
  `StartPrepared`, ordinary Agent lifecycle and uncertainty rules apply.
