// Package greet is a Fabrin module that greets a caller, and stamps the greeting
// with the current time.
//
// # This package is the point of the example
//
// greet needs the current time, which another module owns. It does NOT import
// that module. It declares [Clock] — the single method it actually needs — and
// takes it as a constructor argument, leaving main as the only place that knows
// who provides it.
//
// That interface is the extraction seam. Moving the clock into its own service
// changes nothing in this package: main passes an HTTP client adapter instead of
// a direct call, and greet cannot tell the difference. A direct import would weld
// the two together permanently, and the weld stays invisible until someone tries
// to split them — by which point it is load-bearing.
package greet

import (
	"net/http"
	"time"

	"github.com/usefabrin/fabrin"
)

// Clock is the capability greet needs, declared in the package that needs it.
//
// One method, because the interface should describe what the CONSUMER requires,
// not what the provider happens to offer. A wide interface copied from the
// provider is a direct import wearing a disguise: it makes the seam expensive to
// satisfy any other way, which is the one thing it exists to make cheap.
type Clock interface {
	Now() time.Time
}

// Module greets a caller.
type Module struct {
	clock Clock
}

// New returns the module, taking its dependency rather than reaching for it.
//
// A missing dependency is therefore a compile error at the call site in main,
// not a nil dereference on the first request.
func New(clock Clock) *Module { return &Module{clock: clock} }

// Name implements [fabrin.Module].
func (m *Module) Name() string { return "greet" }

// Routes implements [fabrin.Module].
func (m *Module) Routes(r fabrin.Router) {
	r.GET("/greet", func(c *fabrin.Context) {
		name := c.DefaultQuery("name", "world")
		c.JSON(http.StatusOK, fabrin.H{
			"greeting": "hello, " + name,
			"at":       m.clock.Now().Format(time.RFC3339),
		})
	})
}
