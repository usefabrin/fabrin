// Package orders is a Fabrin module that owns a table and reaches it through a
// port.
//
// # This package is the point of the F2 example
//
// orders needs to read and write orders. It does NOT import an ORM, a driver, or
// database/sql. It declares [Store] — the two methods it actually calls — and
// takes it as a constructor argument, leaving main as the only place that knows
// what is behind it.
//
// That is [ADR 0002] made concrete. The seam Fabrin ships for data is not a
// blessed ORM type; it is the interface the consuming module declares for itself,
// exactly as greet declares its Clock. Swapping SQLite for Postgres, for GORM, or
// for an HTTP call to a service that owns orders is a change in main and nowhere
// else, and this package cannot tell the difference.
//
// The cost is honest and worth seeing in code rather than in prose: there is no
// Model.objects here, no ambient handle, and nobody wrote the store for you.
// Fabrin's answer is that the wiring is one obvious line in main — which is what
// makes it swappable at all.
//
// [ADR 0002]: https://github.com/usefabrin/fabrin/blob/main/docs/adr/0002-database-sql-is-the-orm-seam.md
package orders

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/usefabrin/fabrin"
	"github.com/usefabrin/fabrin/orm"
)

// Order is one row of the table this module owns.
type Order struct {
	ID   int64  `json:"id"`
	Item string `json:"item"`
}

// Store is the capability orders needs, declared in the package that needs it.
//
// Two methods, because the interface describes what the CONSUMER requires rather
// than what a database happens to offer. A wide interface copied from an ORM is
// a direct import wearing a disguise: it makes the seam expensive to satisfy any
// other way, which is the one thing it exists to make cheap. There are two
// implementations in this repository — main's SQLite one, and the in-memory one
// the tests use — because an interface with exactly one implementation forever is
// a wrapper wearing a disguise.
//
// Create assigns the id and writes it back to o.
type Store interface {
	Find(ctx context.Context, id int64) (*Order, error)
	Create(ctx context.Context, o *Order) error
}

// ErrNotFound lets a Store say "no such order" without this package caring how it
// found out. Returning a bare error would leave the handler unable to tell a
// missing row from a database that fell over — a 404 and a 500 are different
// answers, and guessing between them is how a broken dependency gets reported as
// a typo.
var ErrNotFound = errors.New("orders: not found")

// Module owns the orders table.
type Module struct {
	store Store
}

// New returns the module, taking its dependency rather than reaching for it, so
// a missing one is a compile error at the call site in main and not a nil
// dereference on the first request.
func New(s Store) *Module { return &Module{store: s} }

// Name implements [fabrin.Module]. It is what FABRIN_MODULES selects on, and
// what App.Models attributes this module's tables to.
func (m *Module) Name() string { return "orders" }

// Models implements the optional [fabrin.Modeler] interface: the module declares
// the tables it owns, and nothing scans for them.
//
// This is Fabrin's metadata, not a driver's and not an ORM's, which is what lets
// a migration generator and — later — the admin read a schema with no database
// running anywhere.
func (m *Module) Models() []orm.Model {
	return []orm.Model{{
		Table: "orders",
		Fields: []orm.Field{
			{Name: "id", Type: orm.Int64, PrimaryKey: true},
			{Name: "item", Type: orm.String, MaxLen: 200},
		},
	}}
}

// Routes implements [fabrin.Module].
func (m *Module) Routes(r fabrin.Router) {
	r.POST("/orders", m.create)
	r.GET("/orders/:id", m.find)
}

func (m *Module) create(c *fabrin.Context) {
	var o Order
	if err := c.ShouldBindJSON(&o); err != nil {
		c.JSON(http.StatusBadRequest, fabrin.H{"error": "body must be {\"item\": string}"})
		return
	}
	if o.Item == "" {
		c.JSON(http.StatusBadRequest, fabrin.H{"error": "item is required"})
		return
	}

	if err := m.store.Create(c.Request.Context(), &o); err != nil {
		// The reason is logged, not returned: a storage error's text is for the
		// operator reading logs, and handing it to a caller leaks schema.
		_ = c.Error(err)
		c.JSON(http.StatusInternalServerError, fabrin.H{"error": "could not store the order"})
		return
	}

	c.JSON(http.StatusCreated, o)
}

func (m *Module) find(c *fabrin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		// An unparseable id cannot name a row, so it is the same answer as a row
		// that is not there. Reporting 400 here would tell a scanner that ids are
		// integers, for no benefit to anyone who mistyped one.
		c.JSON(http.StatusNotFound, fabrin.H{"error": "no such order"})
		return
	}

	o, err := m.store.Find(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			c.JSON(http.StatusNotFound, fabrin.H{"error": "no such order"})
			return
		}
		_ = c.Error(err)
		c.JSON(http.StatusInternalServerError, fabrin.H{"error": "could not read the order"})
		return
	}

	c.JSON(http.StatusOK, o)
}
