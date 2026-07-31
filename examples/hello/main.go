// Command hello is the smallest Fabrin app that demonstrates the two claims F0
// makes: ports rather than imports, and process slicing.
//
// Run it:
//
//	go run ./examples/hello                      # both modules
//	FABRIN_MODULES=greet go run ./examples/hello # only greet; /time is a 404
//
// Then:
//
//	curl localhost:8080/greet?name=you
//	curl localhost:8080/time
//	curl localhost:8080/healthz    # liveness, consults nothing
//	curl localhost:8080/readyz     # readiness, aggregates module checks
package main

import (
	"context"
	"log"

	"github.com/usefabrin/fabrin"
	"github.com/usefabrin/fabrin/config"
	"github.com/usefabrin/fabrin/examples/hello/clock"
	"github.com/usefabrin/fabrin/examples/hello/greet"
)

func main() {
	// The conventional stack, later layers winning: defaults ← .env ← env ← flags.
	// Load with no sources at all is an error rather than a silent no-op.
	cfg := config.MustLoad(config.Standard()...)

	app, err := newApp(cfg)
	if err != nil {
		log.Fatal(err)
	}

	// Serves the mounted modules plus /healthz and /readyz, logs every request
	// with an id, and shuts down gracefully on SIGINT or SIGTERM.
	if err := app.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// newApp is where the wiring lives, separate from main so the tests exercise the
// same construction the binary does. A test that builds a different app proves
// nothing about the example anyone actually runs.
//
// This function is also the whole of the "ports, not imports" story. It is the
// ONLY place that knows both modules exist: greet asks for a greet.Clock, the
// clock module happens to satisfy it, and neither package names the other.
// Swapping in a remote clock is a change here and nowhere else.
func newApp(opts fabrin.Options) (*fabrin.App, error) {
	clk := clock.New()

	return fabrin.New(opts,
		greet.New(clk), // the port, satisfied in-process by a direct call
		clk,
	)
}
