# 0001. Gin is blessed, and `fabrin.Context` is a type alias rather than a wrapper

- **Status:** Accepted
- **Date:** 2026-08-01 — recorded retroactively. The decision itself was made in
  F0, [#6](https://github.com/usefabrin/fabrin/issues/6), and shipped in
  `router.go`; this file records the alternatives, which were argued in that PR
  and then lived nowhere.
- **Deciders:** Fabrin contributors
- **Requirement / issue:** INV-1, [#28](https://github.com/usefabrin/fabrin/issues/28)

## Context

Fabrin is a batteries-included framework built on an existing HTTP router. That
router's request type appears in every handler a user writes, which makes the
relationship between Fabrin's types and the router's types the most consequential
decision in the public API — and the hardest to change later, because it is
visible in every line of user code.

Three facts shape it:

**Fabrin is a library, not an application.** Every exported symbol is a promise
to strangers. `internal/` is invisible to users, so anything at root level is
permanent in a way application code never is.

**"Built on Gin" is a reason people choose Fabrin.** It is not an implementation
detail to be hidden. A user arriving from Gin brings working knowledge of
`c.ShouldBindJSON`, `c.Param`, `c.SSEvent`, and an ecosystem of middleware they
already run in production.

**Go's type aliases are not a hiding place.** `type Context = gin.Context` makes
`*fabrin.Context` and `*gin.Context` *the same type*. There is no adapter, no
conversion, and no separate identity — which is the whole benefit, and also the
whole cost.

## Decision

`fabrin.Context`, `fabrin.HandlerFunc`, and `fabrin.H` are **type aliases** for
the corresponding Gin types, not wrappers around them (`router.go`).

Gin is **blessed**: it is the only third-party package permitted in a Fabrin
exported signature. `apicheck`'s allowlist is the single reviewable record of
that, it holds exactly one entry, and `TestAllowlist_HoldsOnlyGin` fails if a
second is added without this conversation happening
(`tools/apicheck/apicheck_test.go`).

Containment applies to the **exported surface**, not to imports. Any Fabrin
package may import Gin.

## Consequences

**What this buys.** Every Gin middleware in the ecosystem works unmodified. There
is nothing to learn twice, no adapter layer between a handler and the router, and
nothing for Fabrin to keep in sync as Gin evolves. A Gin user is productive in
Fabrin immediately, and a Fabrin user is not trapped: their handlers are Gin
handlers.

**What it costs.** Gin's v1 API is part of Fabrin's semver contract. If Gin ships
a v2 with a changed `Context`, Fabrin inherits a breaking change it did not
choose. The cost is bounded — Gin has been on v1 since 2015 — but it is real and
unhedged, and it is the reason blessing a *second* package needs its own ADR.

A second consequence, less obvious: `api/fabrin.txt` records aliases
**unexpanded**, so the snapshot cannot show Gin's own breaking changes. Gin's
release notes are the only source for those. Expanding the alias was rejected
separately, because it would turn `api-check` red on any Gin patch bump that
changed nothing about Fabrin, and a gate that cries wolf gets ignored.

**What it forecloses.** Fabrin cannot swap its router without a major version and
a rewrite of every user's handler signatures. That door is closed deliberately;
a framework that keeps it open pays for the option in every request, forever.

## Alternatives considered

### A wrapper type — `type Context struct { *gin.Context }`

Rejected. It buys the ability to swap routers later and to add Fabrin-specific
methods, at the cost of the thing users came for: a `*fabrin.Context` is then a
*different* type from a `*gin.Context`, so no stock Gin middleware works without
an adapter. Every piece of the ecosystem needs a shim, and Fabrin owns
maintaining those shims forever.

The swap-later benefit is also mostly illusory. By the time swapping is worth
doing, the wrapper's surface has grown to mirror the router it wraps, and the
mirror is what actually blocks the swap.

### A narrow interface — Fabrin defines `Context`, Gin satisfies it

Rejected. This is the right pattern for a *module's* dependency (hard rule 3 — a
module declares the interface it needs), and the wrong one for a request context.
`gin.Context` has around forty methods that users legitimately reach for; either
the interface reproduces them — at which point it is a wrapper with extra steps —
or it does not, and users hit a wall the first time they need `SSEvent` or
`FileAttachment`.

It also inverts the cost. An interface makes Fabrin cheap to change and user code
expensive to write. For a batteries-included framework, that is the wrong way
round.

### Bless nothing; build Fabrin's own router

Rejected for v0. It is the only option that makes Fabrin's public API entirely
its own, and it costs a router, a middleware ecosystem, and years of production
hardening — to solve a problem Gin does not have. Fabrin's scarce resource is the
batteries, not the transport.

### Restrict which packages may *import* Gin

Rejected as unimplementable, and this is the alternative that actually got
drafted before it was discarded. "Only the root package may import Gin" makes
`fabrin/health` and `fabrin/logging` unwritable: their handlers and middleware
**are** `gin.HandlerFunc` by definition.

The invariant that survives is narrower and needs type information rather than
import paths — Gin may appear in an exported signature because it is
allowlisted; nothing else may. That is why `apicheck` enforces it and depguard
does not, and it is why `apicheck` had to be written in Go rather than in bash.
