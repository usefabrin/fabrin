<!--
One issue per PR. Link it below — "Closes #N" for the issue this finishes,
"Refs #N" for the epic it belongs to.

The PR title becomes the squash commit, so it must be a Conventional Commit:
  type(scope): short imperative summary
Scopes: core, router, config, orm, migrate, auth, admin, cli, modules, transport,
render, tasks, docs, harness, examples
-->

Closes #
Refs #

## What and why

<!-- What changed, and the problem it solves. If a reviewer would ask "why not the
obvious simpler thing?", answer it here rather than in a review round trip. -->

## Public API impact

<!-- Delete the lines that do not apply.
     Fabrin is a library: every exported symbol is a promise to strangers. -->

- [ ] No change to the exported surface.
- [ ] Adds to the public API — `api/fabrin.txt` regenerated in the same commit.
- [ ] **Breaking** — `CHANGELOG.md` records it. v0 may break users, but never
      silently.
- [ ] Introduces no new third-party type in an exported signature. *(Gin is the
      only allowlisted entry; a second one needs an ADR, not a line edit.)*

## Checklist

- [ ] Issue opened first, and linked above.
- [ ] `just check` passes locally. *(It is exactly what CI runs.)*
- [ ] New or changed behaviour is covered by a test, written **before** the code.
- [ ] No module imports another module — dependencies are declared as interfaces
      in the module's own package.
- [ ] Governed-surface changes carry their `docs/` / `specs/` update.
- [ ] `docs/TODO.md` and `docs/DJANGO_PARITY.md` reflect any progress this makes.

## If this PR touches a gate or a boundary rule

<!-- Delete this section if it does not. -->

- [ ] Injected a throwaway violation and confirmed the gate **fails**.
- [ ] Paired it with a **negative control** — confirmed the legitimate case still
      **passes**. A rule that rejects everything looks identical to a correct one,
      and that is how #14 shipped.
- [ ] Evidence is in the description above, not just asserted.
