// Package health provides liveness and readiness endpoints, and the [Check]
// interface a module implements to contribute to readiness.
//
// # Liveness and readiness are different questions
//
// The distinction is the whole reason this package exists as more than one
// handler:
//
//   - **Liveness** (/healthz) asks "is this process alive?" It consults NOTHING.
//     A liveness probe that fails when the database is slow causes an orchestrator
//     to restart the process, which cannot fix the database and drops whatever
//     requests the process was still serving. Restarting is the only remedy a
//     liveness failure has, so the probe must only fail when restarting would
//     help.
//
//   - **Readiness** (/readyz) asks "should this process receive traffic?" It
//     consults every module's checks and FAILS CLOSED. Reporting ready while a
//     dependency is unreachable takes traffic the process cannot serve, which is
//     worse than being removed from the pool — the request fails either way, but a
//     ready-but-broken instance also hides the problem behind a load balancer.
//
// Conflating the two is the single most common way a Kubernetes deployment
// becomes a restart loop under load.
package health

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Status is the outcome of a readiness evaluation.
type Status string

const (
	// StatusUp means every check passed.
	StatusUp Status = "up"

	// StatusDown means at least one check failed or timed out.
	StatusDown Status = "down"
)

// Check is a readiness probe contributed by a module. A module supplies these via
// the optional Checker interface.
//
// Probe must respect ctx: readiness is evaluated on a request, so a check that
// blocks indefinitely turns the readiness endpoint itself into an outage. Return
// an error to report not-ready; the error text reaches the response, so write it
// for whoever is paged at 3am.
type Check interface {
	// Name identifies the check in the readiness response. Combined with the
	// module's name it must be enough to know what to go and look at.
	Name() string

	// Probe reports whether the dependency is usable right now.
	Probe(ctx context.Context) error
}

// CheckFunc adapts a function to [Check].
type CheckFunc struct {
	CheckName string
	Fn        func(ctx context.Context) error
}

// Name implements [Check].
func (c CheckFunc) Name() string { return c.CheckName }

// Probe implements [Check].
func (c CheckFunc) Probe(ctx context.Context) error {
	if c.Fn == nil {
		return errors.New("check has no probe function")
	}
	return c.Fn(ctx)
}

// Named builds a [Check] from a name and a function.
func Named(name string, fn func(ctx context.Context) error) Check {
	return CheckFunc{CheckName: name, Fn: fn}
}

// DefaultTimeout bounds how long readiness waits for all checks together.
//
// Bounded because an orchestrator's own probe timeout is typically a few seconds:
// a readiness handler that outlives it is reported as a failure anyway, but with
// no detail about which check hung. Failing here instead names the culprit.
const DefaultTimeout = 2 * time.Second

// Result is one check's outcome, as rendered in the readiness response.
type Result struct {
	Module string `json:"module"`
	Check  string `json:"check"`
	Status Status `json:"status"`
	// Error is the failure text, omitted when the check passed.
	Error string `json:"error,omitempty"`
	// Duration is how long the probe took, in milliseconds — usually the first
	// clue when readiness is flapping.
	DurationMS int64 `json:"duration_ms"`
}

// Report is the readiness response body.
type Report struct {
	Status Status   `json:"status"`
	Checks []Result `json:"checks"`
}

// Registry holds the checks contributed by modules and evaluates them.
//
// Safe for concurrent use: Register happens at construction, Evaluate on requests.
type Registry struct {
	// Timeout bounds a whole Evaluate call. Zero means [DefaultTimeout].
	Timeout time.Duration

	mu     sync.RWMutex
	checks []registered
}

type registered struct {
	module string
	check  Check
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{} }

// Register adds a module's checks.
//
// A check with an empty name is rejected rather than registered, because an
// anonymous failing check tells whoever is paged nothing about where to look.
func (r *Registry) Register(module string, checks ...Check) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, c := range checks {
		if c == nil {
			return errors.New("health: nil check registered by module " + module)
		}
		if c.Name() == "" {
			return errors.New("health: module " + module + " registered a check with no name")
		}
		r.checks = append(r.checks, registered{module: module, check: c})
	}
	return nil
}

// Len reports how many checks are registered.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.checks)
}

// Evaluate runs every check concurrently and reports the aggregate.
//
// Concurrently because checks are I/O bound and independent: run serially, N slow
// dependencies cost the sum of their latencies and blow the orchestrator's probe
// timeout for reasons unrelated to any single one of them.
//
// The result is sorted by module then check, so a diff between two readiness
// responses shows a real change rather than map iteration order.
func (r *Registry) Evaluate(ctx context.Context) Report {
	r.mu.RLock()
	checks := append([]registered(nil), r.checks...)
	r.mu.RUnlock()

	timeout := r.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	results := make([]Result, len(checks))
	var wg sync.WaitGroup

	for i, rc := range checks {
		wg.Add(1)
		go func(i int, rc registered) {
			defer wg.Done()

			start := time.Now()
			err := rc.check.Probe(ctx)
			res := Result{
				Module:     rc.module,
				Check:      rc.check.Name(),
				Status:     StatusUp,
				DurationMS: time.Since(start).Milliseconds(),
			}
			if err != nil {
				res.Status = StatusDown
				res.Error = err.Error()
			}
			results[i] = res
		}(i, rc)
	}
	wg.Wait()

	sort.Slice(results, func(a, b int) bool {
		if results[a].Module != results[b].Module {
			return results[a].Module < results[b].Module
		}
		return results[a].Check < results[b].Check
	})

	report := Report{Status: StatusUp, Checks: results}
	for _, res := range results {
		if res.Status != StatusUp {
			report.Status = StatusDown
			break
		}
	}
	return report
}

// LivenessHandler answers "is this process alive?" and consults nothing.
//
// It returns 200 as long as the process can serve a request at all. That is not
// laziness: restarting is the only remedy a liveness failure has, so the probe
// must fail only when restarting would help. Checking a dependency here converts
// every dependency blip into a restart loop.
func LivenessHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": StatusUp})
	}
}

// ReadinessHandler answers "should this process receive traffic?" from the
// registry's checks, and FAILS CLOSED — any failing or timed-out check yields 503.
//
// The body names every check with its status, error, and duration, so the response
// itself is the diagnostic rather than a signal to go and find one.
func ReadinessHandler(r *Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		report := r.Evaluate(c.Request.Context())

		code := http.StatusOK
		if report.Status != StatusUp {
			code = http.StatusServiceUnavailable
		}
		c.JSON(code, report)
	}
}

// Mount registers the liveness and readiness routes on router.
//
// Kept here rather than in the root package so the paths are defined once
// alongside the handlers that serve them.
func Mount(router gin.IRouter, r *Registry) {
	router.GET(LivenessPath, LivenessHandler())
	router.GET(ReadinessPath, ReadinessHandler(r))
}

// The conventional paths, exported so a caller can reference them rather than
// retyping the string in a probe configuration.
const (
	LivenessPath  = "/healthz"
	ReadinessPath = "/readyz"
)
