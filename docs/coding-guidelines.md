# Coding guidelines

Style and API-design standards. The working agreement is [AGENTS.md](../AGENTS.md);
the contribution flow is [CONTRIBUTING.md](../CONTRIBUTING.md).

Start from [Effective Go](https://go.dev/doc/effective_go) and the
[Go Code Review Comments](https://go.dev/wiki/CodeReviewComments). What follows is
what is *specific to Fabrin* — mostly consequences of being a library rather than
an application.

## API design

These are the rules that are expensive to fix later, so they come first.

### Every exported symbol is a promise

Adding one is cheap; removing one breaks strangers' builds. When in doubt,
**do not export it.** You can always export later; you cannot un-export without a
major version.

Concretely, before exporting: is there a user story that needs this *now*? If it
exists "for flexibility" or "someone might want it", it is unexported.

### No third-party type in an exported signature

Gin is the only exception, recorded in `apicheck`'s allowlist. A second entry needs
an ADR.

The reason is not purity. An exported signature naming `redis.Client` makes
go-redis's version part of Fabrin's semver contract — a major bump in *their*
library becomes a breaking change in *ours*, for every user, whether they use
Redis or not.

```go
// Wrong — a dependency's version is now Fabrin's problem forever.
func NewCache(c *redis.Client) Cache

// Right — Fabrin defines what it needs.
type Store interface {
    Get(ctx context.Context, key string) ([]byte, error)
    Set(ctx context.Context, key string, v []byte, ttl time.Duration) error
}
func NewCache(s Store) Cache
```

### Accept interfaces, return structs

The caller decides what satisfies your parameter; they should not have to guess
what your return value can do.

### Functional options for anything with more than two knobs — except when a loader produces the value

```go
// Default: a struct of exported fields cannot gain a required field, cannot
// validate, and cannot distinguish "unset" from "zero".
cache := cache.New(store, cache.WithTTL(time.Minute), cache.WithMaxEntries(1000))
```

Exported struct fields are a promise you cannot revise. An `Option` is a function,
so its internals stay yours.

**`fabrin.Options` is the deliberate exception**, and it is worth knowing why
before applying the rule elsewhere. `config.Load` *produces* settings from
defaults, a file, the environment, and flags. Functional options would force every
caller to translate between two shapes — a loaded struct and a list of
`Option` functions — so the struct wins. The cost is accepted and written down in
`CHANGELOG.md`: **fields may be added, never removed or retyped.**

The rule to take from this is not "structs are fine" but: *choose the shape the
value's producer already has.* Nothing produces a `cache.Option`; something does
produce an `Options`.

### Constructors validate; methods assume

`New` returns an error when the arguments cannot produce a working value. After
that, methods do not re-check what the constructor guaranteed. A type that can
exist in an invalid state pushes that check into every method and every caller.

### `context.Context` first, always

Any function that does I/O, blocks, or may be cancelled takes `ctx` as its first
parameter. Never store a context in a struct.

## Errors

- **Wrap with `%w` and add what the caller does not know.** `fmt.Errorf("load config %q: %w", path, err)` — the path is the part `os.Open`'s error is missing.
- **Sentinel errors for conditions callers branch on**, exported and documented: `var ErrModuleNotRegistered = errors.New(...)`. A caller matching on a string is a caller you have broken by editing a message.
- **No panics in library code**, with one exception: a `Must*` helper whose name says so, used at init time where a failure means the program cannot start anyway.
- **Never discard an error silently.** `_ = f.Close()` when the value is genuinely irrelevant — the explicit assignment says you decided rather than forgot.

## Naming

- Package names: short, lowercase, no underscores, and **never** a stutter.
  `config.Load`, not `config.ConfigLoad`.
- No `util`, `helpers`, `common`, `misc`. A package name that does not say what is
  inside is a package that will accumulate anything.
- Interfaces: name the behaviour, not the implementation — `Store`, `Clock`,
  `Checker`.
- Test names: `Test<Type>_<Behaviour>`, e.g.
  `TestApp_RejectsDuplicateModuleNames`. The behaviour belongs in the name, because
  that is what a failure prints.

## Comments

Comment **why**, not what. The code says what.

```go
// Wrong: restates the code.
// Increment the counter.
counter++

// Right: says what a reader cannot deduce.
// Registration order matters: Stop runs in reverse, so a module can rely on its
// dependencies still being alive while it shuts down.
```

Doc comments on every exported symbol, starting with the symbol's name.

**Comment the non-obvious decision, especially when it looks wrong.** If the
straightforward thing does not work, the next person will try it — the comment is
what stops them spending an afternoon rediscovering why. This is also where the
value in this repo's gate scripts lives.

## Testing

- **The failing test comes first.** A doc claim with no test behind it is a wish.
- **Table-driven tests** for input/output variation; separate test functions for
  distinct behaviours. A table that needs an `if` per case is two tests.
- **Test the behaviour, not the implementation.** A test that breaks on a
  refactor with no behaviour change was testing the wrong thing.
- **Assert the property the spec claims, not a proxy for it.** `CORE-004` says
  `Stop` runs in **reverse order** — a test asserting only that `Stop` was called
  would pass on a broken implementation. Record the order and assert on the
  sequence.
- **`t.Parallel()`** where there is no shared state.
- **No `time.Sleep` for synchronisation.** Use a channel or `sync.WaitGroup`. A
  sleep is either slower than it needs to be or flaky on a loaded CI runner —
  usually both.
- **Prefer the standard library** — `testing`, `net/http/httptest`. A test
  dependency is still a dependency in `go.mod`.

## Concurrency

- Whoever creates a goroutine is responsible for ending it. A goroutine with no
  stop signal is a leak.
- Every long-lived goroutine selects on a context or a done channel.
- Prefer a channel or a mutex you can see over cleverness. `go test -race` runs in
  CI; keep it green.

## What not to do

- **No reflection where a function argument would do.** Reflection moves errors
  from compile time to run time, and turns "who calls this?" into a question no
  tool can answer.
- **No struct-tag DSL that encodes control flow.** Tags for field names and
  validation constraints, yes. Tags that decide *what happens*, no.
- **No package-level mutable state.** It makes tests order-dependent and is the
  reason Django needs `django.setup()`.
- **No `init()` with side effects.** Import order becomes load-bearing and
  invisible.
- **No global logger.** `logging` builds one and it is passed in. A library that
  writes to a global sink cannot be silenced by its user.

## Dependencies

Adding one to the framework's `go.mod` is a cost every consumer pays. Before
adding:

1. Can the standard library do it acceptably? Usually yes.
2. Is it maintained, and is its own dependency tree small?
3. Will its types appear in an exported signature? If so, stop — see above.

**Dev tooling goes in `tools/`**, which is a separate module. `apicheck` needs
`golang.org/x/tools`; no Fabrin user should carry that in their `go.sum` to run a
tool they will never invoke.

## Formatting

`gofmt` decides. `just format` applies it, `just lint` enforces it. There is
nothing to discuss here, which is the point.
