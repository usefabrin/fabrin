// Package clock is a Fabrin module that reports the current time.
//
// It also happens to be what satisfies the greet module's Clock port in this
// process — but nothing here knows that. The dependency runs greet → an
// interface greet declares, and main is the only place that knows both sides
// exist. See [github.com/usefabrin/fabrin/examples/hello/greet].
package clock

import (
	"context"
	"net/http"
	"time"

	"github.com/usefabrin/fabrin"
	"github.com/usefabrin/fabrin/health"
)

// Module serves the current time.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Name implements [fabrin.Module]. It is how FABRIN_MODULES selects this module
// and how a failing check names it.
func (m *Module) Name() string { return "clock" }

// Routes implements [fabrin.Module].
func (m *Module) Routes(r fabrin.Router) {
	r.GET("/time", func(c *fabrin.Context) {
		c.JSON(http.StatusOK, fabrin.H{"now": m.Now().Format(time.RFC3339)})
	})
}

// Now is the capability the greet module depends on.
//
// Note what it is not: it is not registered anywhere, not fetched from a service
// locator, and not exposed through a Fabrin API. It is an ordinary method, and
// greet reaches it through an interface greet declares for itself.
func (m *Module) Now() time.Time { return time.Now() }

// Checks implements the optional fabrin.Checker interface, contributing to
// /readyz.
//
// A clock has no dependency that can be down, so this always passes — it is here
// to show the shape. A real one would probe the thing whose absence should take
// this process out of the load balancer: a database, a queue, a cache it cannot
// serve without.
//
// It must never be added to /healthz. Readiness asks "should I get traffic";
// liveness asks "would restarting help", and restarting cannot reach a database.
func (m *Module) Checks() []health.Check {
	return []health.Check{
		health.Named("tick", func(context.Context) error { return nil }),
	}
}
