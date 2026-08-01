<img width="1280" height="640" alt="fabrin-banner Large-1280x640" src="https://github.com/user-attachments/assets/8cb44f47-f422-4b5f-a2cf-816bf021264e" />

<h1>Fabrin</h1>

**A batteries-included web framework for Go, built on [Gin](https://github.com/gin-gonic/gin)
and inspired by Django's development philosophy.**

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](go.mod)
[![Status: v0](https://img.shields.io/badge/status-v0%20unstable-orange.svg)](#status)

Go has excellent HTTP routers. What it does not have is Django's answer to
*everything after routing* — settings, models, migrations, auth, an admin site
you get for free, management commands — with one convention for how they fit
together.

Fabrin is that layer. It does not hide Gin; it builds on it, and every Gin
middleware you already use keeps working.

---

## Status

**v0 — the API is unstable and will break.** Fabrin is being built in public,
milestone by milestone. See [docs/TODO.md](docs/TODO.md) for the roadmap and
[docs/DJANGO_PARITY.md](docs/DJANGO_PARITY.md) for what exists today versus what
Django offers. Breaking changes in v0 are allowed but are always listed in
[CHANGELOG.md](CHANGELOG.md).

Do not build production systems on Fabrin yet. Do try it, file issues, and tell
us where the Django instinct maps badly onto Go.

## The idea in thirty seconds

A Fabrin app is a set of **modules** — Fabrin's answer to Django's
`INSTALLED_APPS`. A module owns its routes, its models, its migrations, and its
management commands. You compose modules in `main()`; Fabrin handles the rest.

```go
package main

import (
    "context"
    "log"

    "github.com/usefabrin/fabrin"
    "github.com/usefabrin/fabrin/config"
)

func main() {
    // The conventional stack, later layers winning: defaults ← .env ← env ← flags.
    // Sources are explicit — Load with none is an error, never a silent no-op.
    cfg := config.MustLoad(config.Standard()...)

    app, err := fabrin.New(cfg,
        blog.New(),
        auth.New(),
    )
    if err != nil {
        log.Fatal(err) // a duplicate module name, or a selection naming nothing
    }

    // Serves your modules' routes plus /healthz and /readyz, logs every request
    // with an id, and shuts down gracefully on SIGINT/SIGTERM.
    if err := app.Run(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

A module is one small interface:

```go
type Blog struct{ posts PostStore }

func (b *Blog) Name() string { return "blog" }

func (b *Blog) Routes(r fabrin.Router) {
    r.GET("/posts", func(c *fabrin.Context) {
        c.JSON(200, b.posts.All(c.Request.Context()))
    })
}
```

`*fabrin.Context` **is** `*gin.Context` — a type alias, not a wrapper. So
`c.ShouldBindJSON`, `c.Param`, `r.Use(anyGinMiddleware)` and the whole Gin
ecosystem work with no adapter and no ceremony. Blessing Gin publicly is a
deliberate trade: Fabrin's compatibility is tied to Gin v1 (stable since 2015)
in exchange for never standing between you and the router you chose.

## Microservices, without a second framework

Fabrin is a **modular monolith by default, extractable by design**. Three
mechanisms, and deliberately no more:

**1. Ports, not imports.** A module never imports another module. It declares the
interface it needs and takes it as a dependency:

```go
// Inside the blog module — blog does not know who provides this.
type Clock interface{ Now() time.Time }

func New(clock Clock) *Blog { return &Blog{clock: clock} }
```

That interface is the extraction seam. Nothing needs to change in `blog` for its
dependency to move to another process.

**2. Process slicing.** One binary, many deployment shapes:

```bash
fabrin serve                          # everything in one process
FABRIN_MODULES=blog,auth fabrin serve # only these two
FABRIN_MODULES=reports fabrin serve   # the reports service
```

Splitting a monolith into services becomes a deploy-config change, not a rewrite.
An unknown module name is an error, never a silent no-op.

**3. Swappable satisfaction.** A port satisfied in-process by a direct call can
instead be satisfied by an HTTP client adapter. The module cannot tell the
difference, and its tests do not change.

**What Fabrin deliberately does not ship:** service discovery, a service mesh, an
RPC framework, or a remote-client code generator. Those are solved elsewhere and
solved better. Fabrin ships the seam plus service-ready defaults — structured
logging, liveness and readiness endpoints, config from the environment, graceful
shutdown.

## Django parity

The design question for every feature is *"what problem does Django solve here,
and what is the idiomatic **Go** answer?"* — not *"how do we transliterate
Python?"* Fabrin should feel like Go that happens to come with batteries.

| Django | Fabrin | Status |
|---|---|---|
| `INSTALLED_APPS` | `fabrin.Module` | ✅ F0 |
| `settings.py` | `fabrin/config` (defaults ← file ← env ← flags) | ✅ F0 |
| `runserver`, graceful shutdown | `fabrin.App.Run` | ✅ F0 |
| System checks | `Module.Checks()` → `/readyz` | ✅ F0 |
| `LOGGING` | `fabrin/logging` — `log/slog`, JSON by default, request ids | ✅ F0 |
| `django-admin startproject` / `startapp` | `fabrin new` / `fabrin startapp` | ✅ F1 |
| Management commands | `Module.Commands()` | ✅ F1 |
| Models + `makemigrations` / `migrate` | `fabrin/orm` metadata + `Modeler`, `fabrin/migrate` engine — the commands and on-disk migration files are still to come | 🚧 F2 |
| `django.contrib.auth` | `fabrin/auth` | F3 |
| **`django.contrib.admin`** | `fabrin/admin` (html/template + htmx, embedded) | F4 |
| Templates, forms, static files | `fabrin/render`, `fabrin/forms` | F5 |
| Signals, Celery | `fabrin/signals`, `fabrin/tasks` | F6 |
| — *(no Django equivalent)* | Remote ports, bus backends, OpenTelemetry | F7 |
| Cache, mail, i18n, throttling | `fabrin/cache`, `fabrin/mail`, … | F8 |

Full table with rationale: [docs/DJANGO_PARITY.md](docs/DJANGO_PARITY.md).

## Design commitments

- **Gin is public, everything else is ours.** Gin is the only third-party type
  allowed in an exported signature. `api/fabrin.txt` is a checked-in snapshot of
  the entire public surface, and a gate fails if it moves by accident.
- **`internal/` means internal.** If you need it, it is a root-level package.
- **Batteries are removable.** Every battery sits behind an interface you can
  replace. Defaults exist so you do not *have* to choose, not so you cannot.
- **No magic that a debugger cannot follow.** No struct-tag DSL that
  reimplements control flow, no reflection where a function argument would do.

## Getting started

**Pre-1.0 and unreleased** — there is no tagged version yet, so `fabrin new`
resolves the framework from `main`. The API will break; the
[CHANGELOG](CHANGELOG.md) records every change to the exported surface.

```bash
go install github.com/usefabrin/fabrin/cmd/fabrin@main

fabrin new demo
cd demo
fabrin startapp billing  # a module, wired into main.go for you
just run                 # serves on :8080

curl localhost:8080/
curl localhost:8080/healthz    # liveness — consults nothing
curl localhost:8080/readyz     # readiness — fails closed
```

Your app's own binary is the CLI, because Go compiles and no separate tool can
introspect an application it did not build:

```bash
go run . routes          # every mounted route, and the module that owns it
go run . help
```

To work on Fabrin itself:

```bash
git clone https://github.com/usefabrin/fabrin
cd fabrin
just setup     # deps, pinned tools, git hooks
just check     # the one gate — exactly what CI runs
```

## Contributing

Issues first, small PRs, squash merge. Read [CONTRIBUTING.md](CONTRIBUTING.md),
and if you are an AI agent, read [AGENTS.md](AGENTS.md) — it is the working
agreement for humans and agents alike.

The most useful contribution right now is not code. It is telling us which Django
convenience you miss most in Go, and why the obvious Go answer is not good
enough.

## Licence

[MIT](LICENSE) © Fabrin contributors
