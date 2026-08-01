# Django parity

What Fabrin has, what it plans, and — the part that matters most — **where the
direct port of a Django idea would read badly in Go.**

The design question for every row is *"what problem does Django solve here, and
what is the idiomatic Go answer?"* Never *"how do we transliterate Python?"*
Fabrin should feel like Go that happens to come with batteries. A framework that
feels like Django-in-Go would be worse than either.

Status: ✅ shipped · 🚧 in progress · 📋 planned (with milestone) · ❌ deliberately not

---

## Project structure and configuration

| Django | Fabrin | Status |
|---|---|---|
| `INSTALLED_APPS` | `fabrin.Module` — a name plus routes; everything else optional | ✅ F0 |
| `settings.py` | `fabrin/config` — defaults ← file ← env ← flags, each value reporting its source | ✅ F0 |
| `django-admin startproject` | `fabrin new` — go.mod, main, one module, justfile, README; `go mod tidy` run for you | ✅ F1 |
| `django-admin startapp` | `fabrin startapp` — and it wires the module into `newApp`, which Django cannot do because `INSTALLED_APPS` is a list of strings | ✅ F1 |
| `manage.py runserver` | `./myapp serve` — or no arguments at all — with graceful shutdown | ✅ F1 |
| `show_urls` (django-extensions) | `./myapp routes` — every mounted route with the module that owns it | ✅ F1 |
| Management commands | `Commander` on a module — collected from *mounted* modules, so slicing drops them with the routes | ✅ F1 |
| `python manage.py check` | `Checker` on a module → `/readyz` | ✅ F0 |
| `LOGGING` (dictConfig) | `fabrin/logging` — `log/slog`, JSON by default, request ids | ✅ F0 |
| `settings.DEBUG` | `FABRIN_DEBUG` — off by default, and off means Gin's construction-time route dump is silenced | ✅ F1 |
| `AppConfig.ready()` | `Lifecycle.Start` / `Stop`, reverse order on the way down | ✅ F0 |

**Where the port would read badly.** Django's settings are a *module you import*,
which makes them a mutable global. That is why Django needs
`django.setup()`, why test settings are a separate file, and why import order
matters. Fabrin's config is a **value you pass in**. The dependency is visible in
the type system and a test constructs its own without touching the environment.

Same reasoning for `INSTALLED_APPS`: Django names apps as **strings** and imports
them by reflection, so a typo is a runtime `ImportError` and no tool can find an
app's usages. Fabrin's modules are **values in a slice** — the compiler checks
them, and jump-to-definition works.

## Models and the database

| Django | Fabrin | Status |
|---|---|---|
| `models.Model` | `orm.Model` + Fabrin's metadata registry — a description, with no database handle anywhere near it | 🚧 F2 |
| Models found by importing each app in `INSTALLED_APPS` | `Modeler` on a module — models are handed over, never scanned for | ✅ F2 |
| `makemigrations` / `migrate` | `fabrin makemigrations` / `fabrin migrate` | 📋 F2 |
| `QuerySet` | GORM, or anything else, behind an interface **your module declares** — `database/sql` is Fabrin's seam ([ADR 0002](adr/0002-database-sql-is-the-orm-seam.md)) | 📋 F2 |
| `DATABASES` | One config block, one place for pool limits | 📋 F2 |
| `select_related` / `prefetch_related` | GORM preloading | 📋 F2 |
| `Model.objects` manager | Explicit repository or store, passed in | 📋 F2 |
| Lazy querysets | ❌ | ❌ |

**Where the port would read badly.** Django's `Model.objects` is an implicit
global connection: a model knows how to find the database by itself. In Go that
makes tests order-dependent and hides the dependency from the type system. Fabrin
passes a handle.

**Lazy querysets are the one Django feature deliberately rejected.** They are
elegant in Python and a trap in Go: an expression that looks like data but fires a
query when iterated makes the N+1 problem invisible, and Go has no `__iter__` to
make the laziness read naturally. Explicit `.Find(ctx, &out)` is more typing and
far less debugging.

**Why Fabrin owns the metadata registry rather than reading GORM's.** If the admin
read GORM's metadata, the admin would *be* GORM-shaped, and swapping the ORM would
mean rewriting the admin, the forms, and the migration generator. One layer of
indirection now buys the ability to be wrong about the ORM later.

**Why discovery does not port.** Django finds models by importing every app in
`INSTALLED_APPS` and letting the metaclass register each class as a side effect of
import. That works because a Python import runs code. Go's nearest equivalent is
`init()` reached through a blank import — and a blank import that nobody wrote is
*silent*. The failure is not a missing model; it is a model absent from the
registry while its table exists, which a diff-based generator reads as "this table
should be dropped."

So `Modeler` hands models over explicitly. The module that owns the table names
it, which also gives Fabrin something Django's registry does not have: every table
knows which module declared it, so a conflict names both sides and the admin can
group by owner. This is the same reasoning `INSTALLED_APPS` gets above — Go's
answer to "list your components" is a value, not a string to be resolved later.

**The split Django does not make.** A Django `Model` is two things at once: a
description of a table *and* the way you query it. `orm.Model` is only the first
— a plain struct of exported fields, with no handle, no driver, and no import of
`database/sql`. The query half is whatever the application chose, reached through
an interface the module declares for itself.

The gain is concrete rather than architectural taste: a schema can be read with
no database running, so the admin renders and the migration generator diffs
without one, and this package's tests finish in microseconds. Django pays the
opposite way — `django.setup()` before anything, which is why its test suite
needs a database to ask what a field is called.

The cost is that nothing here links a table back to a Go type, which the admin
(F4) will need. It will add that rather than reshape this: `Model` is a struct of
exported fields, so it may gain fields forever and lose none.

## Requests, responses, routing

| Django | Fabrin | Status |
|---|---|---|
| `urls.py` / `path()` | `Module.Routes(r Router)` | ✅ F0 |
| View functions | `fabrin.HandlerFunc` = `gin.HandlerFunc` | ✅ F0 |
| `HttpRequest` / `HttpResponse` | `*fabrin.Context` = `*gin.Context` | ✅ F0 |
| Middleware | Gin middleware — **the entire existing ecosystem works** | ✅ F0 |
| Class-based generic views | ❌ | ❌ |
| `reverse()` / named URLs | `./myapp routes` for discovery ✅ F1; named reverse still under consideration, with no milestone | 🚧 |

**Where the port would read badly.** Django's class-based views solve template-method
reuse through inheritance depth — `ListView` → `MultipleObjectMixin` →
`ContextMixin` — and answering "what runs on GET?" means walking an MRO. Go has no
inheritance and is better for it. The Go answer to shared view behaviour is a
middleware or a helper function you can read in one place.

**This row is why Gin is public.** Fabrin could wrap `gin.Context` and own its
handler type. It would then need an adapter for every Gin middleware, and users
would learn two APIs for one job. Aliasing means `fabrin.Context` *is*
`gin.Context`, so every Gin middleware works unmodified. The cost — Gin's v1 API
is part of Fabrin's semver contract — was accepted deliberately.

## The admin site

| Django | Fabrin | Status |
|---|---|---|
| `django.contrib.admin` | `fabrin/admin`, generated from metadata | 📋 F4 |
| `ModelAdmin` customisation | Per-model overrides | 📋 F4 |
| `list_display`, `list_filter`, `search_fields` | Same concepts | 📋 F4 |
| Admin actions | 📋 F4 | 📋 F4 |
| Admin templates overridable | Overridable `html/template` blocks | 📋 F4 |

**The one place Django is simply ahead, and honestly so.** Django's admin is the
product of eighteen years of iteration on a language with runtime introspection
of everything. Fabrin's admin will be less capable at v1.

**Where the port would read badly.** Django's admin discovers models by importing
every app's `admin.py` for side effects. Fabrin's modules *declare* their models,
so the wiring is greppable.

`html/template` + htmx rather than a JS framework is the other deliberate
divergence: an admin that requires Node or Bun to build is an admin most Go teams
will not adopt. Server-rendered and `go:embed`ded means the admin ships inside
your binary.

## Auth, forms, templates

| Django | Fabrin | Status |
|---|---|---|
| `django.contrib.auth` | `fabrin/auth` | 📋 F3 |
| `User` model, swappable | Replaceable user model | 📋 F3 |
| Permissions and groups | Same | 📋 F3 |
| Sessions | Server-side, pluggable store | 📋 F3 |
| `forms.Form` / `ModelForm` | `fabrin/forms` | 📋 F5 |
| Django template language | `html/template`, or `templ` if you prefer | 📋 F5 |
| `{% csrf_token %}` | CSRF middleware + template helper | 📋 F3 |
| Static files, `collectstatic` | `fabrin/render` static serving + embedding | 📋 F5 |

**Where the port would read badly.** Django's template language deliberately
restricts logic to keep designers out of trouble. `html/template` already does
that, and Go's answer to "the template needs a computed value" is a function in
the template's FuncMap — not a new mini-language. Fabrin will not ship a template
DSL.

## Signals, tasks, caching

| Django | Fabrin | Status |
|---|---|---|
| Signals | `fabrin/signals` + `Subscriber` | 📋 F6 |
| Celery | `fabrin/tasks` | 📋 F6 |
| Cache framework | `fabrin/cache` | 📋 F8 |
| Email backends | `fabrin/mail` | 📋 F8 |
| i18n / l10n | 📋 F8 | 📋 F8 |

**Where the port would read badly.** Django's signals can be connected from
anywhere to anything, which makes "what happens when a User is saved?" answerable
only by grepping the whole codebase. Fabrin's modules *declare* their
subscriptions via `Subscriber`, so the receivers of an event are enumerable from
the module list.

## What Fabrin has that Django does not

Not parity — the reason a Go framework can be worth building.

| Capability | Why it matters |
|---|---|
| **Process slicing** — `FABRIN_MODULES=blog,auth` | One binary, N deployment shapes. Splitting a monolith is a deploy-config change, not a rewrite. Django has no equivalent; you split by creating another project. |
| **Ports, not imports** | A module declares the interface it needs, so it is extractable by construction. Django apps import each other's models freely, which is why "extract this app into a service" is a rewrite. |
| **A single static binary** | The admin, templates, and static files are `go:embed`ded. No virtualenv, no WSGI server, no `collectstatic` step in the deploy. |
| **A checked public API surface** | `api/fabrin.txt` plus a gate. Django's public/private boundary is documentation and convention. |
| **Compile-time module wiring** | A missing dependency is a build error, not a runtime `ImproperlyConfigured`. |
| **Liveness and readiness as separate endpoints** | `/healthz` consults nothing; `/readyz` aggregates module checks and fails closed. Django's `manage.py check` is a *command*, not a probe an orchestrator can poll, so every Django deployment writes this by hand — usually conflating the two and earning a restart loop. |

---

## How to read the "reads badly" notes

They are not criticisms of Django. Nearly every one describes a design that is
*correct in Python* — runtime introspection, mutable module globals, and duck
typing make the string-based, side-effect-driven approach genuinely pleasant
there.

They are load-bearing for Fabrin because the same design in Go produces the
opposite result: hidden dependencies, order-dependent tests, and tooling that
cannot follow the wiring. When a Fabrin feature starts feeling like ceremony
compared to its Django equivalent, this file is where to check whether the
ceremony is buying something — or whether we have transliterated when we should
have translated.
