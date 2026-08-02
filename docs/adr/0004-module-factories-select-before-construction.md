# 0004. Named module factories select before construction

- **Status:** Accepted
- **Date:** 2026-08-02
- **Deciders:** Fabrin contributors
- **Requirement / issue:** FR-MODULES-4, [#77](https://github.com/usefabrin/fabrin/issues/77)

## Context

`Options.Modules` originally selected from `Module` values passed to
`fabrin.New`. That correctly sliced routes, checks, models, commands, and
lifecycle, but selection happened after `main` had constructed every module and
its dependency graph. A process selecting only `greet` could still open the
database owned by an excluded `orders` module.

Moving selection earlier requires names before modules exist. It must not weaken
the existing rule that dependencies are explicit, compiler-checked wiring in
`main`, and it must preserve registration order because lifecycle shutdown uses
the reverse of that order. Fabrin also needs to validate an entire deployment
shape before invoking user code: an unknown or duplicate name must not leave a
partially constructed process behind.

This adds public API before v0.1. The representation and the migration path are
therefore part of the decision, not implementation detail.

## Decision

Fabrin adds three root-package API symbols:

```go
type ModuleFactory struct { /* private */ }

func LazyModule(
    name string,
    build func(context.Context) (Module, error),
) ModuleFactory

func NewFromFactories(
    ctx context.Context,
    opts Options,
    factories ...ModuleFactory,
) (*App, error)
```

`NewFromFactories` validates every factory name, builder, duplicate, and selected
name before calling any builder. Empty selection means all. It then invokes only
selected builders, once each, in factory registration order; the selection
value never changes lifecycle order. A returned module must be non-nil and its
`Module.Name()` must match its declared factory name.

The factory is opaque and constructed by `LazyModule`. Its callback captures
typed dependencies or constructors directly. Fabrin supplies no service locator,
untyped dependency bag, module registry, or reflective import mechanism.

`New` remains available with its existing eager semantics. Applications whose
constructors are pure and cheap do not need to migrate. Applications that need
selection-before-construction replace their `New` call with named factories,
one module at a time if useful.

Long-lived resource acquisition belongs in `Lifecycle.Start`, not in a factory.
This lets `App` unwind started modules in reverse order if startup fails and
prevents a later factory or `New` validation error from orphaning a resource.
Factories may still perform fallible construction that is not resource ownership.

## Consequences

**What this buys.** An excluded module's callback is never invoked, so neither
that module nor its dependency graph is constructed. The selected set alone
contributes routes and every optional capability. Catalogue and selection errors
fail before user callbacks run. Typed closures retain ordinary Go navigation,
refactoring, and compile-time interface checks.

**What it costs.** Applications choosing lazy construction write both a stable
name and a callback, and the framework checks at runtime that the built module
repeats that name. Shared selected dependencies may need memoized constructors in
`main`; Fabrin deliberately does not infer or own a dependency graph. A builder
that opens a resource can still leak it when a later builder fails, which is why
the API documentation directs owned I/O to lifecycle startup.

`ModuleFactory` is public but opaque. Callers cannot inspect or compose its
fields, which preserves room to add internal metadata without breaking source
compatibility. The zero value is invalid and fails during construction.

This is still process slicing, not deploy-only service extraction. A selected
module's own dependencies are constructed, and replacing an in-process port with
a remote adapter remains explicit application wiring.

## Alternatives considered

### Change `New` to accept factories

Rejected because it breaks every existing application for no benefit to callers
whose constructors are already pure. A separate constructor makes the migration
explicit and incremental.

### Accept a map of name to builder

Rejected because map iteration cannot define registration order. Sorting by name
would make lifecycle order an accidental lexical property rather than a choice
owned by `main`.

### Pass a service locator or dependency registry to builders

Rejected because it turns missing dependencies from compiler errors into runtime
lookups and hides cross-module coupling. It directly contradicts FR-MODULES-3.

### Put a factory method on `Module`

Rejected because the module must already exist to call its method, so it cannot
prevent construction. A wrapper that implements `Module` before construction
would also need to proxy every optional interface and could silently change the
capabilities a module contributes.

### Export `ModuleFactory` fields

Rejected because public fields permanently commit Fabrin to one representation
and permit invalid combinations. The constructor function gives one validation
path while keeping future metadata private.
