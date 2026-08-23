# Changelog

Notable changes to Fabrin. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## v0 and breaking changes

**Fabrin is pre-1.0. The public API is unstable and will break.**

SemVer permits that in `0.y.z`, but permission is not licence to break people
quietly. So the rule here is stricter than SemVer requires:

> **Every change to the exported surface is listed in this file, in the same
> commit that makes it.** `api/fabrin.txt` moving without a `CHANGELOG.md` update
> fails the `docs-guard` gate.

A v0 that breaks users without telling them is a v0 nobody can upgrade — they
learn what changed from a compile error and have to reverse-engineer the intent.
Breaking changes are fine; unannounced ones are not.

Once v1 lands, breaking changes wait for a major version like anywhere else.

---

## [Unreleased]

Milestone **F0** — repository, harness, agent system, and runnable core — is
**complete** ([#1](https://github.com/usefabrin/fabrin/issues/1)), and so is
**F1**, the `fabrin` command ([#32](https://github.com/usefabrin/fabrin/issues/32)).
Milestone **F2** — models, metadata, migrations — is in progress
([#51](https://github.com/usefabrin/fabrin/issues/51)).

Nothing is released yet, so both sit under `[Unreleased]` and entries are tagged
with their milestone rather than split into sections. Cutting a version is
[#27](https://github.com/usefabrin/fabrin/issues/27).

### Added

- **Private admin CRUD seam proof.** A new `admin` package exports no symbols yet,
  but proves one concrete record through existing ORM metadata, metadata-ordered
  private form state, typed field conversion, and resource-specific create,
  list, update, and delete callbacks without reflection or a framework-owned
  repository. Unknown inputs cannot be mass-assigned, invalid forms do not reach
  persistence, and mandatory CSRF then authorization gates fail closed before an
  unsafe action binds or persists. The public API snapshot and ORM metadata stay
  unchanged; rendering, auth, CSRF middleware, and a usable admin remain future
  work. `apicheck` now skips empty packages when rendering the snapshot, while an
  injected exported admin type still makes the gate fail; a private package no
  longer creates a blank-line-only API diff. ([ADR 0005], [#78])
- **Selection-before-construction module factories.** The new opaque
  `fabrin.ModuleFactory`, `fabrin.LazyModule`, and
  `fabrin.NewFromFactories` API validates the full named catalogue and selection
  before callbacks run, then builds only selected modules in registration order.
  `fabrin.New` remains the eager, source-compatible path. Typed closures keep
  dependency wiring compiler-checked; Fabrin introduces no service locator, and
  owned I/O stays in `Lifecycle.Start` so startup can unwind it. The runnable
  hello example proves a greet-only process never invokes the orders database
  opener. ([ADR 0004], [#77])
- **Platform-local multi-agent orchestration.** `docs/agents/` is the canonical
  contract for Lead authority, risk-based fan-out, isolated path ownership,
  serialized integration, task/result packets, and high-risk human review.
  Claude Code, Codex, and Cursor receive generated native adapters and may never
  invoke one another; `scripts/agents.sh check` enforces byte parity, native
  metadata is parsed in tests, and `agentcheck` mechanically rejects invalid
  dispatch/fan-in packets, overlapping ownership, and stale results. ([#71],
  [#75], [#76])
- **Structural spec and workflow hardening.** The tools-only `speccheck` parser
  rejects malformed YAML, invalid statuses, unknown requirements, prose-only
  matrix IDs, production functions, invalid test signatures, and prefix/comment
  test matches. Docs freshness now fails closed on bad ranges, accounts for both
  sides of renames and whole push ranges, and requires the owning documentation.
  Hook installation is linked-worktree-safe, workflow and composite actions are
  SHA-pinned with timeouts, and `just race` runs in CI. ([#72])

- Working agreement (`AGENTS.md`) with `CLAUDE.md` as a pointer to it, contribution
  flow, MIT licence, and a Go module that compiles from the first commit. ([#12])
- `just check` as the local validation gate and exact CI quality job, plus hygiene
  gates, boundary rules, and a fast pre-commit hook. ([#13])
- CI calling `just ci` rather than re-listing steps, `docs-guard` over the PR
  range, benchmarks on `main`, issue forms, and a PR template. ([#16])
- `ARCHITECTURE.md`, roadmap, Django parity table, coding guidelines, numbered
  requirements, and `specs/` with a validating gate.

- `examples/hello` — two modules, ports rather than imports, and process slicing,
  each covered by an executable test rather than by prose. ([#9])

  `greet` needs the current time and never imports the module that owns it: it
  declares a one-method `Clock` interface and `main.go` passes in whatever
  satisfies it. `TestModules_NeverImportEachOther` reads the **import graph**
  rather than behaviour, which is the only way to prove that negative — it
  catches even a blank import, which compiles cleanly and which no behavioural
  test could distinguish from correct code.

- **`api/fabrin.txt`** — a checked-in snapshot of every exported symbol, 130
  promises across `fabrin`, `config`, `health`, and `logging`. `just api`
  regenerates it; `just api-check` fails when the code and the record disagree,
  and it runs inside `just check`. ([#10])

  The snapshot is generated by `apicheck`, which lives in a **separate `tools/`
  Go module**. It needs `golang.org/x/tools` for type information, and Fabrin is
  a library: anything in its `go.mod` lands in every consumer's `go.sum`, for a
  tool they will never invoke.

  The gate answers two questions, not one. Alongside snapshot drift it fails on
  an **unblessed third-party type in any exported signature** — in a result,
  parameter, variadic, struct field, interface method, method signature, package
  variable, type argument, or nested inside containers to any depth. `apicheck`'s
  allowlist holds `github.com/gin-gonic/gin` and nothing else, and a test asserts
  exactly that, so adding a second entry fails until the ADR that hard rule 1
  requires actually happens.

  Type aliases are recorded **unexpanded** — `type Context = github.com/gin-gonic/gin.Context`,
  not Gin's forty-odd methods — so a Gin patch bump cannot churn the file. A gate
  that goes red on a dependency update that changed nothing about Fabrin is a
  gate people learn to ignore.

  `just test`, `just cover`, `just lint`, and `just format` now each invoke the
  `tools/` module explicitly. A separate module is not reached by `go test ./...`
  from the root, so without this `apicheck`'s own tests would never have run —
  locally or in CI — while every recipe printed success.

- **`examples/hello/orders`** — a module that reaches its data through a port it
  declares. ([#60])

  ```go
  // the module declares what it requires — no ORM named anywhere in this package
  package orders

  type Store interface {
      Find(ctx context.Context, id int64) (*Order, error)
      Create(ctx context.Context, o *Order) error
  }
  ```

  This is [ADR 0002](docs/adr/0002-database-sql-is-the-orm-seam.md) made
  concrete, and the reason it exists as code rather than as prose: the seam
  Fabrin ships for data is not a blessed ORM type, it is the interface the
  consuming module declares for itself — exactly as `greet` declares its `Clock`,
  one layer down.

  **The module imports no ORM, no driver, and not `database/sql`**, which
  `TestOrders_ImportsNoDatabaseHandleNorAnythingOutsideFabrin` reads off the
  **import graph**. No behavioural test can prove that negative: a module that
  imports a handle and never calls it passes every request-level assertion. The
  check is an **allowlist** — standard library plus Fabrin — rather than a deny
  list of ORMs, because a prefix deny list fails open against the one nobody
  wrote a rule for, and "no ORM" is a claim about all of them.

  **`main.go` is the only file that names SQL.** It opens SQLite, runs a
  hand-written `migrate.M` to create the table, and passes a `*sqlStore`.
  `sql.ErrNoRows` is translated to `orders.ErrNotFound` at that boundary, so the
  module answers 404 without importing `database/sql` to recognise the error —
  which is the difference between a port and an indirection.

  **Two implementations of the port**, because an interface with exactly one
  forever is a wrapper wearing a disguise. The in-memory one records its writes,
  since a response body cannot distinguish a handler that went through the port
  from one that echoed the request back.

  A `Modeler` implementation, so the migration generator will have something real
  to diff. What did **not** ship: migrations generated *by* `makemigrations`, and
  the regenerate-and-diff gate over them. Both need #59, and the on-disk format
  #56 has not decided — writing one now would invent the format the generator
  must then live with, which is the same reasoning `MIG-008` records.

- **`fabrin/migrate`** — the migration engine, over `*sql.DB`. ([#54])

  ```go
  applied, err := migrate.Run(ctx, db, []migrate.M{{
      Version: "20260801120000",
      Name:    "add orders",
      Up:      func(ctx context.Context, h migrate.Handle) error { … },
      Down:    func(ctx context.Context, h migrate.Handle) error { … },
  }})
  ```

  **Each migration and the row recording it commit in one transaction.** Writing
  the applied-state row outside it is the classic form of this bug: the migration
  half-runs, the row says it finished, and every later run skips the half that
  never happened. Mutation-checked — moving that `INSERT` out of the transaction
  turns the test red.

  This relies on the database rolling back DDL. SQLite and PostgreSQL do; **MySQL
  and MariaDB do not**, and the package documentation says so rather than
  promising atomicity nobody can deliver there.

  **`Down` is required, not optional** — a migration you cannot reverse is a
  deploy you cannot roll back, and the moment you need it is the worst moment to
  find out nobody wrote one. An irreversible change returns an error from `Down`
  explaining why, which is a decision recorded in the file rather than an
  omission. Django's `reverse_code` is optional; this is deliberately stricter.

  **An unapplied migration sorting before an applied one is an error.** That is
  the shape of a branch merged out of order, and applying it silently would run
  it against a schema it was never written against. Detected before anything
  runs, along with duplicate versions and missing steps.

  `Rollback` undoes newest-first to an **exclusive** target version, because a
  later migration may depend on an earlier one.

  **No driver, no Gin, no `net/http`** — enforced, not merely intended, so
  migrations run from a process that mounts no routes. The engine takes a
  `*sql.DB`; the application supplies the driver.

  **BREAKING — `Up` and `Down` take a `Handle`, not a `*sql.Tx`.** ([#67],
  [ADR 0003])

  ```go
  type Handle interface {
      ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
      QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
      QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
      PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
  }
  ```

  `*sql.Tx` answered "may a migration run outside a transaction?" with *no* — by
  construction, silently, as a side effect of picking the obvious type.
  `CREATE INDEX CONCURRENTLY` is the case that forces the question: PostgreSQL
  refuses it inside a transaction block, and it is how you index a large table
  without locking writes. Django ships `atomic = False` for exactly this.

  **The engine still passes a transaction, and the version row still commits
  with the body** — MIG-001 is unchanged. What changed is that the *type* no
  longer promises it, so a future opt-in needs no edit to anyone's migration.
  The package documentation states today's guarantee rather than hedging about a
  mode that does not exist yet.

  `*sql.Tx`, `*sql.DB` and `*sql.Conn` satisfy `Handle` **unmodified, with no
  adapter** — that property is what makes the change clean, and it is the first
  thing that would be lost if a signature drifted, so a test reads it.

  **The method set is frozen at four**, and that is the one genuinely
  irreversible part: users implement `Handle` with recording fakes, so a fifth
  method breaks all of them at once. It is checked by reflection rather than by a
  compile-time `var _ Handle = (*sql.Tx)(nil)`, because the fifth method someone
  would plausibly add — `QueryRow` — is one `*sql.Tx` and `*sql.DB` **already
  have**, so a satisfaction check keeps compiling on the day it lands while every
  user's fake breaks.

  Worth stating plainly, because the reverse was claimed while this was being
  designed: **a narrow interface restricts nothing.** `h.(*sql.Tx).Rollback()`
  still compiles. The interface turns an accident into a deliberate act — a
  footgun sitting in autocomplete becomes something nobody reaches by mistake —
  it does not remove the ability. For the same reason omitting `PrepareContext`
  would not have prevented preparing, only made it uglier, which is why the set
  is sized for ergonomics rather than for restriction.

  **Versions of unequal width are rejected.** `M` already documented that
  ordering is a plain lexicographic comparison and therefore needs a fixed-width
  version — "9 sorts after 10" — and nothing enforced it, so a set mixing widths
  was accepted, reordered into a sequence its author did not write, and applied
  with each half running against a schema it never saw. The error names **both**
  versions, because the fault is in the pair: `9` is unimpeachable next to `8`.

  It wraps `ErrInvalidMigration` rather than adding a sentinel. The set is
  unusable for the same reason a missing `Down` makes one unusable, and an
  exported sentinel is a permanent promise nothing yet needs to branch on.

  Mutation-checked in both halves: deleting the check turns the test red, and so
  does keeping it while dropping the two versions from the message. The message
  deliberately contains no literal `9` or `10` of its own — an illustrative "9
  sorts after 10" in the error text would have satisfied the test without naming
  the versions the caller actually passed. ([#55])

- **`Modeler`** — a module declares the tables it owns. ([#53])

  ```go
  func (m billing) Models() []orm.Model {
      return []orm.Model{{Table: "invoices", Fields: …}}
  }
  ```

  The fourth optional module interface, asserted at registration alongside
  `Checker`, `Lifecycle`, and `Commander`, and reported through
  `App.Capabilities()` like them. `App.Models()` is the collected schema, sorted
  by table, each entry carrying the module that declared it.

  **Nothing is scanned for.** Django discovers models because a Python import has
  side effects at runtime; the Go equivalent is a blank import whose absence is
  silent — and a model that quietly fails to register is a table the migration
  generator proposes to `DROP`. Handing them over makes the absence a
  compile-time one.

  **Collected from mounted modules only**, the same process-slicing rule
  readiness checks and commands follow. A migrate-only process that mounts one
  module is handed only its schema, not the whole application's.

  **Two modules claiming one table fails at construction**, naming both and the
  table. Propagated rather than logged, and wrapped with `%w` so
  `errors.Is(err, orm.ErrDuplicateTable)` still matches from the root package.

- **`fabrin/orm`** — the model-metadata registry, and the first piece of F2. ([#52])

  ```go
  r := orm.NewRegistry()
  err := r.Register("shop", orm.Model{
      Table: "orders",
      Fields: []orm.Field{
          {Name: "id", Type: orm.Int64, PrimaryKey: true},
          {Name: "reference", Type: orm.String, MaxLen: 32, Unique: true},
          {Name: "shipped_at", Type: orm.Time, Nullable: true},
      },
  })
  ```

  The admin (F4), forms (F5), and the migration generator all read **Fabrin**
  metadata rather than the ORM's. Read GORM's instead and the admin *is*
  GORM-shaped: swapping the ORM would mean rewriting the admin, the forms, and
  the generator. One layer of indirection now buys the ability to be wrong about
  the ORM later.

  **There is no database handle here, and there cannot be.** The package
  describes tables; it opens nothing, and depguard forbids it importing
  `database/sql` — the deny was injected and read before this landed, alongside a
  sibling-import deny that the compiler cannot catch because such an import is
  not a cycle. That is what lets a schema be read with nothing running, and it is
  why these tests finish in microseconds. The query API stays `database/sql` or
  whichever ORM the application chose, reached through an interface the module
  declares for itself
  ([ADR 0002](docs/adr/0002-database-sql-is-the-orm-seam.md)).

  **Registration is where a model is rejected**, not DDL time: an empty table
  name, no fields, an unnamed or untyped column, a duplicated column, a `MaxLen`
  on something that is not a string, no primary key, or two of them. The
  generator runs against a real database, and a mystery there is a mystery in the
  most expensive available place. `ErrDuplicateTable` names **both** modules,
  because "duplicate table" alone means grepping every module for the name.

  **Two ordering rules that look contradictory and are not.** `Models()` sorts by
  table, because registration order carries `FABRIN_MODULES` and the argument
  order in `main` into the output. Field order is preserved exactly as declared,
  because that is the author's intent about column layout. Both make the
  generator's output a function of the schema alone. `Models()` returns a deep
  copy for the same reason: three subsystems read this, and a caller that mutated
  its result would change what every later reader sees with nothing to blame.

  The zero `Type` is invalid on purpose — a forgotten type is an error rather
  than whichever constant happens to sort first.

  This shipped one PR before anything imported it, so for that window `orm`
  importing the root package compiled cleanly and `orm-is-standalone` was the only
  thing rejecting it. `Modeler` ([#53]) closed the window. FR-ORM-1 stays *in
  progress* until the admin and forms read the registry, which is the clause its
  text actually promises.

- **`just check` now generates a project, builds it, runs its tests, extends it
  with `startapp`, and boots it.** ([#38])

  The templates are `.tmpl` text, invisible to the compiler — so a change to
  `fabrin.New`, `config.Standard`, or the `Module` interface could break the
  scaffold with every other gate green. This is the gate that notices.

  **Against the working tree, not the published module.** A `replace` points the
  generated project at this checkout, so a change that breaks the scaffold fails
  here rather than going green until it is merged.

  Not fully offline, and the reason is worth recording: `GOPROXY=off` was tried
  first and failed in CI. Go 1.17+ prunes the module graph, so Fabrin's cache
  never holds `golang.org/x/sys@v0.6.0`'s `go.mod` — an edge reachable only
  through go-isatty's unpruned requirements — and the generated project's empty
  require list makes resolution walk it. It passed locally on a warm cache and
  failed on a runner holding exactly what Fabrin needs.

  Not an `examples/` entry, despite the roadmap's wording. A generated project
  has its own `go.mod`, which under `examples/` is a **nested module**:
  `go build ./examples/x` fails, and `build-examples.sh` globs `examples/*/`
  expecting packages of the root module. Committing a copy instead would let it
  drift from the templates it exists to prove. So it is generated fresh on every
  run and thrown away — there is no directory that can be silently absent.

  It also compiles what #37 could only re-parse: `startapp`'s edit to `main.go`
  is followed by a real build.

- **`fabrin startapp <name>`** — a module, wired in. ([#37])

  ```console
  $ fabrin startapp billing
  created billing/ and wired it into main.go

  $ go run . routes
  GET  /           home
  GET  /billing    billing
  GET  /healthz    (framework)
  ```

  Django's `startapp` scaffolds the app and leaves `INSTALLED_APPS` to you,
  because it is a list of strings. Fabrin's wiring is Go code, so the scaffold
  can finish the job — and must: a module that exists and is never registered
  compiles, serves nothing, and says nothing about why.

  **The AST locates; the edit is a splice.** `go/ast` finds the real
  `fabrin.New` call rather than a string that looks like one — a regex is
  fragile against a user who reformatted, added a comment, or wired a port, all
  of which the generated file's own comments encourage. But re-printing with
  `go/format` would reformat code the user owns and can move their comments,
  turning "add a module" into a diff nobody wants to review. So the parser is
  used only to find *where*, and the edit inserts at the byte offsets it
  reports: the correctness of a parser with the diff of a one-line insert.

  The edit is then **verified rather than assumed** — the result is re-parsed
  and checked for the `fabrin.New` call before anything is written, so a bug
  here cannot leave a broken `main.go` behind.

  `startapp` and `new` render the **same module templates**; `home` is simply
  the first module `new` generates, at `/` rather than at its own path.

- **`fabrin new <name>`** — a project that builds, tests, and boots. ([#36])

  ```console
  $ fabrin new demo
  created demo
  resolving dependencies (go mod tidy)…

      cd demo
      just run
  ```

  It emits `go.mod`, a `main.go` ending in `app.Execute`, one module (`home/`)
  with a passing test, a `justfile`, `.gitignore`, and a README, then runs
  `go mod tidy` so the project resolves Fabrin from the proxy. The generated
  `go.mod` names **no** dependency versions of its own: a version pinned in a
  template is wrong the first time Fabrin is tagged, and nothing fails when it
  is.

  Templates live in **`internal/scaffold`**, not a public package. Every exported
  symbol is a permanent promise, and a template's shape is the least stable thing
  in the repository; `cmd/fabrin` is `package main` and contributes nothing to
  `api/fabrin.txt`, so the whole scaffold stays off the public surface.

  The generated `go` directive is **Fabrin's own**, not the toolchain that
  happened to run the scaffold. Emitting the newer one would pin a project to
  whatever the generating developer had installed, so it stops building for a
  colleague on the older toolchain with nothing in the diff to say why. A test
  reads Fabrin's `go.mod` and fails if the two drift.

- **Flags are parsed wherever they appear in a command's arguments.** ([#36])

  Go's `flag` package stops at the first non-flag argument. That is right for a
  program's own arguments — `go run x.go -v` must hand `-v` to the program — and
  wrong for a subcommand, whose name has already been consumed. Without this,
  `fabrin new demo -module example.com/demo` treats the module path as a second
  project name and reports "takes exactly one name". Everything after a bare
  `--` is still passed through untouched.

- **`Commander`** — a module contributes subcommands to the application's own
  binary. Django's management commands. ([#35])

  Collected from the **mounted** modules only, the same process-slicing rule
  `collectChecks` follows for readiness: a module this process did not mount
  registered nothing, and offering its command would advertise work this process
  cannot do. `examples/hello` gains a `greet` command that reaches the clock
  through the same `Clock` port its HTTP handler uses — a module's command *is*
  the module, not a second implementation living nearby.

  A command name colliding with a built-in, or with another module's command, is
  an **error at construction**, naming both sides. Found at dispatch instead, a
  shadowed `routes` is a command that suddenly does something else, and which of
  the two wins depends on slice order. The reserved names are read from the
  built-in set itself rather than a second list, so the check cannot drift from
  what is actually dispatchable.

- **`(*App).Execute`** — the entry point a `main` hands `os.Args[1:]` to, with
  built-in `routes`, `serve`, and `version`. ([#34])

  Django's `manage.py` imports the project at runtime. Go compiles, so a separate
  tool cannot introspect an application it did not build — the binary that
  already has the modules linked in is the only one that can answer "which module
  owns this URL", and `Execute` is where it answers. `examples/hello` now uses
  it, so the path is exercised by `just check` rather than described in prose.

  **No arguments still means serve**, deliberately: anything else silently
  changes what `./myapp` does the moment its `main` switches from `Run`, and a
  container that used to serve would print usage and exit 0 — which every
  orchestrator reads as a successful run. A *leading flag* also still serves,
  because `config.Standard()` already parsed `-addr` and friends out of
  `os.Args`; dispatching it would break `./myapp -addr :9000`.

  `App.Routes()` attributes each route to its module by set-difference around
  each module's mount call. Not a slice of the tail: Gin builds its listing by
  walking one radix tree per HTTP method, so the order is neither registration
  order nor sorted, and slicing would hand routes to the wrong module as soon as
  two used different verbs. Output is sorted by path then method, because
  unstable output makes `routes` useless for diffing two deployments.

- **Gin's debug output is silenced unless `FABRIN_DEBUG` is set.** ([#34])

  Gin's zero configuration is debug mode: constructing an engine prints a warning
  banner and the entire route table to **stdout**. That was four lines of garbage
  above every `routes` listing, and in a container log it is a needless
  disclosure of internal handler paths. Same shape as `ReadHeaderTimeout` and
  `TrustedProxies` — differ from the library's default when the library's default
  is unsafe, now recorded as **NFR-7**.

  It is also what finally makes `Options.Debug` *do* something. Until now it
  resolved, validated, and changed nothing — the defect class `LOG-004` exists to
  catch. `GIN_MODE` overrides both, because `gin.SetMode` is process-global and a
  caller who set it has said something more specific than an application default.

- **`fabrin/cli`** — `Command` and `Dispatch`, the command surface `Commander`
  plugs into. ([#33])

  `Commander` returns `[]cli.Command`, so the **root package imports `cli`** —
  which means `cli` can never import root without cycling, and therefore cannot
  take an `*App` or serve anything itself. Assembling Fabrin's built-ins with a
  module's own commands is root's job, because root is the only layer that sees
  both. The constraint is a feature: a CLI that needs an HTTP stack constructed
  before it can print its own help is a CLI nobody can test, and `fabrin new`
  runs in a directory where no app exists at all.

  `Command` is Fabrin's own struct over stdlib `flag`, not a third-party command
  type — `apicheck`'s allowlist stays at exactly one entry and no CLI dependency
  reaches a consumer's `go.sum`. `*flag.FlagSet` is deliberately concrete rather
  than an interface Fabrin defines; wrapping the standard library for its own
  sake is the "narrow interface" alternative
  [ADR 0001](docs/adr/0001-gin-as-a-type-alias.md) rejected, and the accepted
  cost is that a command's flag definitions are coupled to `flag`'s API.

  Two behaviours worth naming. **An unknown command is an error** that suggests
  the closest match and lists the rest — a typo that exits 0 is the worst outcome
  available, because the user believes the command ran. And **`Dispatch` writes
  to no stream it does not own**: `flag.ContinueOnError` stops a `FlagSet`
  calling `os.Exit` but not printing to `os.Stderr`, so the set's output is
  discarded and the error is returned instead.

- **`docs/adr/`** — the directory `AGENTS.md` had been routing decisions to in
  three places without it existing, plus [ADR 0001](docs/adr/0001-gin-as-a-type-alias.md)
  recording Gin-as-a-type-alias retroactively. ([#28])

  The decision was already stated in `AGENTS.md`, `ARCHITECTURE.md`,
  `CONTRIBUTING.md`, and `router.go`; what none of them held is the **four
  alternatives that lost** — a wrapper struct, a narrow interface, Fabrin's own
  router, and the import-level restriction that got drafted before it turned out
  to make `health` and `logging` unwritable. That last one is why `apicheck`
  exists at all, and it lived nowhere until now.

  `docs/adr/README.md` states the format and, more usefully, what an ADR is
  **not**: not a changelog, not documentation, and not a retrospective ritual. A
  directory of ADRs nobody needed teaches people to skip the ones that matter, so
  backfilling one per past decision is explicitly out of scope.

- **Six agent charters in `.claude/agents/`, plus the `new-module` and
  `issue-to-pr` skills.** ([#11])

  Charters, not rules: rules stay in `AGENTS.md`, where every agent and every
  human sees them. Each file states its charter, its tools, and an explicit
  **hand-back condition** — an agent with no stated stopping point drifts into
  the next job, and that drift is invisible in the output.

  Three of them overlap a gate deliberately, and each is written as *what the
  gate cannot see*. `just docs-check` counts touched files and cannot read them,
  which is how an undefined `[#8]` link reference and a superseded "zero extra
  allocations" claim both survived it green. `just api-check` knows the surface
  moved, not whether it should have. Hard rule 4 says prove a gate bites; the
  half people skip is the negative control.

  **No gate enforces the charter format, deliberately.** Every gate here checks a
  machine-decidable property against a single source. A check that frontmatter
  and a hand-back section *exist* would pass on a charter that says nothing, and
  no injected violation could prove otherwise — which is precisely the "a rule
  that silently matches nothing looks identical to a rule that passes" failure
  hard rule 4 warns about.

### Fixed

- `-h`, `-help`, and `--help` anywhere in the initial configuration-flag prefix
  now print usage instead of panicking during config load or starting the server.
  Generated and example mains use `config.Load` so invalid leading flags return
  a normal startup error. ([#41])
- Readiness deadlines now bound checks that ignore context, with at most one
  outstanding invocation per check; panicking probes fail closed instead of
  crashing the process. Recovered handler panics are explicitly error-logged
  with the actual committed status and become 500 before commitment. ([#73])

- **The examples smoke gate bound and polled different addresses.** It started
  each example on `:$port` (wildcard) and polled `127.0.0.1:$port`. A wildcard
  bind normally accepts loopback, so it usually worked — but if anything on the
  machine already held that loopback address, `curl` reached *that* server and
  the gate reported on a process it did not start. Seen for real on `:8080`
  against an unrelated local app. Now binds loopback explicitly, which also stops
  CI examples being briefly reachable off-box. ([#9])
- **`FABRIN_LOG_FORMAT` and `FABRIN_LOG_LEVEL` were inert.** Both resolved from
  the environment and validated, and then `Options.WithDefaults` overwrote
  `Logger` with `slog.Default()` — so neither ever reached a handler. Same defect
  class as `FABRIN_ADDR` below: a settings key the surface advertises and nothing
  implements. `WithDefaults` now leaves `Logger` nil, documented, and
  `fabrin.New` builds the logger from both fields. ([#8])
- Three fail-open defects in the harness, all of which reported success while
  enforcing nothing. ([#15], [#14])
  - The public-package coverage gate matched **substrings**: `orm` matched
    `formatters:`, so `fabrin/orm` would have landed with no boundary rule
    recorded — a fail-open in the gate written to prevent exactly that. Replaced
    with an anchored `# boundary: <name>` marker that prose cannot satisfy.
  - `just install-hooks` created a **relative** symlink under `.git/hooks`,
    assuming `.git` is a directory. In a worktree it is a file, so the link
    dangled and git ran nothing while the recipe printed success.
  - `FABRIN_ADDR` was referenced by `smoke-examples.sh` but implemented nowhere,
    making a shell script the de-facto owner of part of the config contract.

### Public API

First exported surface. It is now also recorded line-by-line in
[`api/fabrin.txt`](api/fabrin.txt) ([#10]); this section stays the place the
*reasoning* lives, because a snapshot diff shows what moved and never why.

Changed in package `fabrin/migrate` ([#67]) — **breaking**:

- `Handle` — a new exported interface of exactly four methods, and `M.Up`/`M.Down`
  retyped from `func(ctx, *sql.Tx) error` to `func(ctx, Handle) error`. Nothing
  removed; `database/sql` is standard library, so `apicheck`'s allowlist stays at
  its single Gin entry.

  The reasoning is [ADR 0003] and the entry above. In short: `*sql.Tx` answered a
  question nobody asked — *may a migration run outside a transaction?* — with a
  silent no. The engine still passes a transaction; the **type** stopped
  promising one.

  The method set is the part that cannot be walked back, because users implement
  `Handle` in their own fakes. Four, frozen, and a test reads the set by
  reflection rather than asserting satisfaction — the fifth method anyone would
  plausibly add is one `*sql.Tx` and `*sql.DB` already have, so a
  `var _ Handle = (*sql.Tx)(nil)` check would keep compiling while every user's
  fake broke.

Added in package `fabrin/migrate` ([#54]):

- `M` — `Version`, `Name`, `Up`, `Down`. `Version` is a `string` rather than an
  int so a timestamp is the natural choice, which is what stops two branches
  colliding. Ordering is lexicographic, so the format must be fixed-width — `9`
  sorts after `10`.
- `Run(ctx, db, ms) ([]M, error)` and
  `Rollback(ctx, db, ms, toVersion string) ([]M, error)`.
- `Table` — the applied-state table's name. Exported because it appears in the
  user's schema dumps and backups; something they will see is something they
  should be able to name.
- Sentinels: `ErrDuplicateVersion`, `ErrInvalidMigration`, `ErrOutOfOrder`,
  `ErrMissingMigration`.

Both entry points return **what they applied or undid**, rather than only an
error. An empty slice means already up to date, and on failure the partial slice
says which migrations committed before the one that failed — "migration failed"
alone leaves the operator guessing at the database's actual state. That also
avoids a separate `Applied()` accessor for now; #59's status command can add one
when it needs it.

`*sql.DB` and `*sql.Tx` are standard library, so `apicheck`'s allowlist stays at
its single entry — the property [ADR 0002](docs/adr/0002-database-sql-is-the-orm-seam.md)
chose `database/sql` as the seam to get.

Added to package `fabrin` ([#53]):

- `Modeler` — `Models() []orm.Model`, the fourth optional module interface.
- `(*App).Models() []orm.Registered`.

`Models()` returns the models rather than the `*orm.Registry` holding them, and
that is the whole design decision. A registry would carry `Register` with it, so
any caller could add a table after construction — which is exactly what that
type's "built once, read afterwards" contract rules out, and what makes its lack
of a mutex sound rather than lucky. The migration generator (#57) only reads, so
nothing is lost. The result is a deep copy for the same reason.

Added in package `fabrin/orm` ([#52]):

- `Type` — a `string` type, with `String`, `Int`, `Int64`, `Float`, `Bool`,
  `Time`, `Bytes`, and `Valid() bool`. Fabrin's own vocabulary on purpose: a
  driver's types would pin the metadata to one database, and `reflect.Kind`
  cannot tell a timestamp from an `int64`. The set is the smallest the migration
  generator needs and may grow — a new constant is additive, where a changed
  meaning would not be. The zero value is invalid, so a forgotten type is an
  error rather than whichever constant sorts first.
- `Field` — `Name`, `Type`, `MaxLen`, `Nullable`, `PrimaryKey`, `Unique`,
  `Index`. Deliberately short: defaults and foreign keys are absent because the
  first migration does not need them, and a struct of exported fields may gain
  them later where it could never lose one.
- `Model` — `Table`, `Fields`. A **struct, not an interface** a user's type
  implements. The consumer in F2 is the migration generator, which wants a
  description and nothing else, and a description should not require a zero value
  of the user's type to answer questions about it. The cost is that nothing here
  links a table back to a Go type; the admin will need that and will add it
  rather than reshape this.
- `Registered` — `Module`, `Model`. The owning module is not decoration: a schema
  conflict has to name who declared each side, and registration is the only
  moment that is known for free.
- `Registry`, `NewRegistry()`, `(*Registry).Register(module string, models ...Model) error`,
  `(*Registry).Models() []Registered`.
- Sentinels: `ErrDuplicateTable`, `ErrInvalidModel`, `ErrInvalidField`.

`Registry` is not mutex-guarded, and that is the promise rather than an omission:
it is built once and read afterwards. A mutex would buy nothing against that
pattern and would imply the schema can change while an application runs.

A `Len()` was written and then **removed before this shipped**. `len(r.Models())`
answers the same question, nothing called it, and an exported symbol is
permanent — cheap to add later, breaking to withdraw.

No third-party type appears anywhere in this surface, so `apicheck`'s allowlist
stays at its single entry.

Added in package `fabrin/cli` ([#33]):

- `Command` — `Name`, `Short`, `Flags func(*flag.FlagSet)`, `Run func(context.Context, []string) error`.
  A struct of exported fields, so it may gain fields forever and lose none.
  `Flags` is optional; `Run` is not.
- `Dispatch(ctx, out io.Writer, cmds []Command, args []string) error`. `out` is a
  parameter rather than `os.Stdout` because a library must not write to a stream
  it does not own — and it makes the usage output testable without swapping
  globals.

Added to package `fabrin` ([#34]):

- `Route` — `Method`, `Path`, `Module`. `Module` is empty for a route the
  framework mounted itself, which is `/healthz` and `/readyz`.
- `Commander` — `Commands() []cli.Command`, the sixth optional module interface,
  asserted at registration alongside `Checker` and `Lifecycle` and reported
  through `App.Capabilities()` like them ([#35]).
- `(*App).Routes() []Route` and
  `(*App).Execute(ctx, out io.Writer, args []string) error`.

Added to package `fabrin`:

- `App`, `New(Options, ...Module) (*App, error)`, `Options`, `Module`, `Lifecycle`,
  `Router`.
- `App` methods: `Run`, `Start`, `Stop`, `Handler`, `Engine`, `Addr`, `Modules`,
  `Capabilities`, `Options`.
- **Gin aliases** — `Context = gin.Context`, `HandlerFunc = gin.HandlerFunc`,
  `H = gin.H`. Aliases, not wrappers: a `*fabrin.Context` and a `*gin.Context` are
  the same type, so every Gin middleware works unmodified. This is what puts Gin's
  v1 API inside Fabrin's semver contract, deliberately.
- Sentinels: `ErrDuplicateModule`, `ErrUnknownModule`, `ErrNoModules`,
  `ErrAlreadyRunning`. Exported so callers branch with `errors.Is` rather than
  matching message text.
- Defaults: `DefaultAddr`, `DefaultShutdownTimeout`, `DefaultReadHeaderTimeout`.

Two defaults differ from the underlying library's, both toward safety:

- `TrustedProxies` defaults to **none**. Gin trusts every proxy by default, which
  makes `ClientIP()` spoofable by any client sending `X-Forwarded-For`.
- `ReadHeaderTimeout` defaults to 10s. Go's `http.Server` zero value is *no
  timeout*, so a client can hold a goroutine open indefinitely by sending headers
  slowly.

`Options` is a plain struct rather than functional options because a config loader
produces it directly. Consequence: its fields may be **added**, never removed or
retyped, without a breaking change.

**`Options` will move to `fabrin/config` in #7, keeping `fabrin.Options` as an
alias.** Declaring it in the root package puts two commitments in direct conflict:
`FR-CONFIG` says the loader produces `Options`, while the `config-is-standalone`
boundary rule forbids `fabrin/config` from importing the root package — so
`config.Load() (fabrin.Options, error)` is both the obvious signature and a rule
violation. Settings are a lower-level concern than the application, so the type
belongs in `config` and the dependency runs root → config, which is the allowed
direction. The alias keeps every caller and the public surface unchanged.

Added — package `fabrin/config`:

- `Load(...Source) (Options, error)`, `MustLoad`, `Resolve(...Source) (*Resolved, error)`.
- Sources: `FromEnv(Lookup)`, `FromFile(path)`, `FromRequiredFile(path)`,
  `FromFlags(args)`. `Lookup`, `MapLookup`, `OSEnv()`.
- `Resolved` with `Get`, `SourceOf`, `String` — **provenance**, because on a
  misconfigured deploy you can see the wrong value but not where it came from, and
  that is most of the debugging time.
- `Standard() []Source` — the conventional stack (`.env` ← environment ← flags)
  as one value: `config.MustLoad(config.Standard()...)`. A **slice**, not a
  composite source, so per-layer provenance survives; collapsing the three would
  report every value as coming from "standard" and discard the answer to *which
  layer set this*. Reads `os.Args`, so it belongs in `main` — in a test binary the
  flag layer rejects `go test`'s own `-test.*` flags.
- `Defaults() Source` — a source supplying nothing, so "defaults only" is
  something a caller can say out loud.
- Settings keys as constants (`KeyAddr`, `KeyModules`, …), sentinels
  (`ErrInvalidValue`, `ErrUnknownKey`, `ErrBadFile`, `ErrNoSources`), and the
  `Default*` constants.
- `Options` **moved here** from the root package (see above), gaining `Debug`,
  `LogFormat`, and `LogLevel`, plus an exported `WithDefaults`.

Behaviour worth knowing:

- **`Load()` and `Resolve()` with NO sources are an error** (`ErrNoSources`),
  not a defaults-only load. ([#22])

  This is the one place the package's own "explicit sources" rule was working
  against its users. `config.MustLoad()` is the shape a reader reaches for first;
  it compiled, started, served, and ignored every `FABRIN_` variable with no
  diagnostic anywhere. Running the README's own example with
  `FABRIN_ADDR=127.0.0.1:18099` bound `:8080` instead — the only symptom being a
  port collision with an unrelated process.

  Sources stay explicit, because "nothing reads the environment behind your back"
  is worth keeping and a test needs a configuration the machine cannot influence.
  What changed is that the empty call now *says so*, and its error names both
  fixes. That matches how this codebase already treats an unknown settings key and
  an unknown module name: an error, never a silent no-op.

- **Later layers win**: defaults ← file ← env ← flags.
- **An unknown `FABRIN_`-prefixed key is an error**, with a near-miss suggestion.
  A silently ignored typo is how a setting "does not work" with no diagnostic
  anywhere. Unprefixed keys are ignored, since a real environment and a shared
  `.env` are full of unrelated variables.
- **A missing `FromFile` is not an error**; a missing `FromRequiredFile` is. The
  usual deployment has no settings file at all, so requiring one would make the
  common case the error case — but a path the user named explicitly should not be
  ignored.
- **Only flags actually passed contribute.** A flag layer writing its zero values
  over everything beneath it would let `FromFlags` erase the environment merely by
  being present.

Added — package `fabrin/health`:

- `Check` (the interface a module implements), `CheckFunc`, `Named(name, fn)`.
- `Registry` with `NewRegistry`, `Register`, `Len`, `Evaluate`; `Report`,
  `Result`, `Status` with `StatusUp` / `StatusDown`.
- `LivenessHandler()`, `ReadinessHandler(*Registry)`, `Mount(gin.IRouter, *Registry)`.
- `LivenessPath` (`/healthz`), `ReadinessPath` (`/readyz`), `DefaultTimeout` (2s).

**Liveness and readiness answer different questions, and conflating them is how a
deployment becomes a restart loop.** `/healthz` consults *nothing*: restarting is
the only remedy a liveness failure has, so the probe must fail only when
restarting would help. `/readyz` consults every mounted module's checks and
**fails closed** — a ready-but-broken instance takes traffic it cannot serve and
hides the fault behind a load balancer.

Added — package `fabrin/logging`:

- `New(io.Writer, format, level) *slog.Logger` — returned, never installed into a
  package-level global, so a consumer can silence or redirect Fabrin without
  affecting anything else in their process.
- `RequestID()` and `Logger(*slog.Logger)` middleware.
- `RequestIDFromContext(ctx)`, `ContextWithRequestID(ctx, id)`.
- `HeaderRequestID` (`X-Request-ID`), `LogKeyRequestID` (`request_id`),
  `MsgRequest` (`request`).

Added — package `fabrin`:

- `Checker` — the optional `Module` interface contributing `[]health.Check` to
  readiness. Reported by `App.Capabilities` like `Lifecycle`.

### Changed

- Deployment documentation now describes `FABRIN_MODULES` honestly as
  route/capability selection after construction, lifecycle ordering as
  caller-owned, and the current data seam as `database/sql` plus consumer-owned
  ports rather than an unwritten GORM default. Rendering/forms/CSRF now precede
  auth and admin in the paused stabilization roadmap. ([#74])

- **`modernc.org/sqlite` is no longer a test-only dependency of this
  repository.** ([#60])

  `examples/hello/main.go` imports the driver, because an example that describes
  opening a database without opening one is not the thing a user copies.

  The measurement, so the next person deciding has numbers rather than a feeling:
  `go list -deps ./...` now reaches `modernc.org/sqlite` **three** times — 30
  `modernc.org/*` packages in all — where it reached it **zero** before. It is
  reached only through `examples/hello`.

  **This supersedes two clauses of the #54 entry below**, which read
  "`go list -deps ./...` reaches it **zero** times" and "It is imported only from
  `migrate/migrate_test.go`". Both were true while the driver existed for
  `migrate`'s tests alone; neither is now.

  **What has not changed is the property that entry was measuring.**
  `go list -deps .` — the root package a consumer actually imports — is still
  **zero**, `examples/hello` is `package main` and unimportable, and
  `migrate-is-standalone` still denies the driver everywhere in the engine
  outside `_test.go`. Nothing new reaches a consumer's binary; what reaches their
  `go.sum` is what already did.

  [ADR 0002](docs/adr/0002-database-sql-is-the-orm-seam.md)'s Consequences
  section calls it "a test-only dependency", and that sentence is now stale. The
  ADR is **deliberately not edited**: it is accepted and dated, and
  `docs/adr/README.md` forbids amending an accepted ADR's substance — an ADR
  quietly rewritten to match today's code cannot tell you the code once did
  something else. The decision it records is untouched by this; only a
  second-order fact about it moved, and a fact that moved is a changelog entry.

- **`modernc.org/sqlite` enters `go.mod` as a test-only dependency.** ([#54])

  Flagged rather than absorbed quietly, because
  [ADR 0002](docs/adr/0002-database-sql-is-the-orm-seam.md) forecast this exact
  cost and asked for it to be revisited if it grew: *"it enters `go.mod` as a
  test-only dependency — Go does not distinguish those — so it reaches consumers'
  `go.sum` while never being linked into their binaries."*

  The measurement, so the next person deciding has a number rather than a
  feeling: **ten modules**, of which two (`golang.org/x/sys`, `mattn/go-isatty`)
  Fabrin already had through Gin.

  Nothing is linked into a consumer's binary, and that is checked rather than
  asserted — `go list -deps ./...` reaches it **zero** times, while
  `go list -deps -test ./migrate/` reaches it three. It is imported only from
  `migrate/migrate_test.go`, and `migrate-is-standalone` denies it everywhere
  else.

  Pure Go, not cgo, which is not incidental: `check-scaffold.sh` and the smoke
  gate build and boot real binaries in CI, and a cgo driver would make that
  toolchain-dependent for a dependency no user links.

  The alternative that stays open is the one the ADR already named — moving the
  worked adapter to `github.com/usefabrin/fabrin-gorm` — plus, for this
  specifically, a stub `database/sql/driver` written in-repo. That was rejected
  here: a fake driver cannot answer whether DDL rolls back, which is the single
  property MIG-001 exists to prove.

- **`fabrin new -dir` no longer creates a mistyped parent path.** ([#45])

  `os.MkdirAll` was building the whole chain, so `-dir ~/projcts` produced a
  project under a directory nobody meant and nothing failed, which is why
  nobody would look. The project directory itself is still created — that is
  what the user asked for; its parent is something they *named*, and a name
  that does not resolve is a typo worth reporting.

- **BREAKING — `cli.Command.Run` gained an `io.Writer`.** ([#35])

  ```diff
  -Run func(ctx context.Context, args []string) error
  +Run func(ctx context.Context, out io.Writer, args []string) error
  ```

  Shipped one PR earlier in #33, revised here. Without it, `Execute`'s writer
  reached the built-ins and stopped: a module's command that printed anything had
  to reach for `os.Stdout` itself, which is the least appropriate place in the
  whole framework to be writing to a process-global stream, and made the command
  untestable without capturing one. Django hands its management commands
  `self.stdout` and Cobra hands them `cmd.OutOrStdout()` for the same reason.

  Mechanical to fix: add the parameter, ignore it with `_` if the command prints
  nothing.

- **`fabrin.New` now installs three middleware and mounts two routes by default:**
  `gin.Recovery`, `logging.RequestID`, `logging.Logger`, plus `/healthz` and
  `/readyz`. Batteries included — an orchestrator's probes work against a stock
  app, and every response carries a request id a user can quote. A module that
  declares `/healthz` itself now panics at construction, which is Gin's duplicate-
  route behaviour and the right moment to find out.
- **Readiness consults only the modules this process *mounted*.** With
  `FABRIN_MODULES` in effect, an unmounted module's failing dependency does not
  gate this process — process slicing extends to readiness.
- **The request log message is now the constant `"request"`**, with method and
  path as attributes, rather than an interpolated `"GET /users/123"`. An
  interpolated message makes every distinct path its own message string, which
  defeats grouping and alerting in every aggregator.
- **`RequestID` no longer calls `c.Set(LogKeyRequestID, id)`.** It allocated Gin's
  `Keys` map on every request to hold a second copy of a string already reachable
  through `RequestIDFromContext`. `LogKeyRequestID` remains exported — it is the
  slog attribute key.
- **`config.Options.WithDefaults` leaves `Logger` nil.** Its default is not a
  constant but a logger built from `LogFormat` and `LogLevel`, and
  `config-is-standalone` forbids `fabrin/config` from importing `fabrin/logging`.
  `fabrin.New` fills it. A caller using `config.Load()` *without* going through
  `fabrin.New` must now supply a logger or check for nil before using
  `Options.Logger`.
- `fabrin.Options`, `fabrin.DefaultAddr`, `fabrin.DefaultShutdownTimeout`, and
  `fabrin.DefaultReadHeaderTimeout` are now **aliases** of the `fabrin/config`
  declarations. Not a breaking change — an alias is the same type and the same
  constant — and existing code keeps compiling unchanged.

### Performance

- The per-request baseline moved from **9 allocations to 22**, recorded with
  itemised attribution in [`perf/BASELINE.md`](perf/BASELINE.md). The framework's
  own machinery still adds **zero** — `BenchmarkFabrin_OneRoute` and
  `BenchmarkRequestIDAndLogger` land on the same 22 allocs and 1794 B, which is
  the evidence that the registry, route groups, and capability map all resolve at
  construction time. The 13 belong to request ids and structured logging, which
  are per-request work by definition. Attribution benchmarks now live in
  `logging/` so a future move is a reading rather than a bisect.

  This supersedes the earlier entry for #18, which read "zero extra allocations
  and zero extra bytes per request (9 allocs/op, 1040 B/op for both)". That was
  true when the only thing between a request and a handler was the router; it
  stopped being true the moment request ids and a log line became defaults. The
  half of it that survives is stated above and is now the tracked invariant.

[#8]: https://github.com/usefabrin/fabrin/issues/8
[#9]: https://github.com/usefabrin/fabrin/issues/9
[#10]: https://github.com/usefabrin/fabrin/issues/10
[#11]: https://github.com/usefabrin/fabrin/issues/11
[#28]: https://github.com/usefabrin/fabrin/issues/28
[#33]: https://github.com/usefabrin/fabrin/issues/33
[#34]: https://github.com/usefabrin/fabrin/issues/34
[#35]: https://github.com/usefabrin/fabrin/issues/35
[#36]: https://github.com/usefabrin/fabrin/issues/36
[#37]: https://github.com/usefabrin/fabrin/issues/37
[#38]: https://github.com/usefabrin/fabrin/issues/38
[#45]: https://github.com/usefabrin/fabrin/issues/45
[#52]: https://github.com/usefabrin/fabrin/issues/52
[#53]: https://github.com/usefabrin/fabrin/issues/53
[#54]: https://github.com/usefabrin/fabrin/issues/54
[#55]: https://github.com/usefabrin/fabrin/issues/55
[#60]: https://github.com/usefabrin/fabrin/issues/60
[#67]: https://github.com/usefabrin/fabrin/issues/67
[#71]: https://github.com/usefabrin/fabrin/issues/71
[#72]: https://github.com/usefabrin/fabrin/issues/72
[#73]: https://github.com/usefabrin/fabrin/issues/73
[#74]: https://github.com/usefabrin/fabrin/issues/74
[#75]: https://github.com/usefabrin/fabrin/issues/75
[#76]: https://github.com/usefabrin/fabrin/issues/76
[#77]: https://github.com/usefabrin/fabrin/issues/77
[#78]: https://github.com/usefabrin/fabrin/issues/78
[ADR 0004]: docs/adr/0004-module-factories-select-before-construction.md
[ADR 0005]: docs/adr/0005-admin-crud-seam-remains-private.md
[#41]: https://github.com/usefabrin/fabrin/issues/41
[ADR 0003]: docs/adr/0003-migrations-take-a-handle-not-a-transaction.md
[#12]: https://github.com/usefabrin/fabrin/pull/12
[#13]: https://github.com/usefabrin/fabrin/pull/13
[#14]: https://github.com/usefabrin/fabrin/issues/14
[#15]: https://github.com/usefabrin/fabrin/pull/15
[#16]: https://github.com/usefabrin/fabrin/pull/16
[#22]: https://github.com/usefabrin/fabrin/issues/22
