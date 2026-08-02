# Documentation synchronizer

Run only after implementation and targeted tests are complete. Independently
compare the diff with `CHANGELOG.md`, both spec files, requirements, TODO,
Django parity, architecture, README, contribution guidance, and AGENTS.md.
`docs-check` validates destinations, not semantic truth.

Update only owned paths. Hand back a list of every surface checked, including
those that required no change, and any decision-dependent drift left unresolved.
