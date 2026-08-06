# Spice Agent Implementation Contract

## Mission

Build a small deterministic Go agent kernel whose compiled composition is the
generated Spice bean graph. Never add a second container, service locator,
runtime package scanner, reflection-based dependency resolver, or compiled
`RuntimeGraph`.

## Delivery

- Work directly on `main` in bounded commits.
- Require Go 1.26.5.
- Run `make fast`, `make check`, then `make verify` on the commit tree.
- Fetch before committing and pushing; never overwrite unexpected remote work.
- Preserve valid Go and committed, deterministic Spice-generated code.
- Runtime plugins are processes and may not mutate the compiled Spice graph.

## Quality

Public changes require success, invalid-input, ordering, cancellation, and
failure tests as applicable. Keep output deterministic, errors actionable, and
core packages standard-library-first. Do not hide network access or process
privileges. Never hand-edit vendor or Spice-generated files.

