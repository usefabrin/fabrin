// Package orders_test drives the orders module through a store the module does
// not ship.
//
// That is the whole point of the file. The interface orders declares has two
// implementations in this repository — the SQLite one main wires in, and the
// in-memory one below — and an interface with exactly one implementation forever
// is a wrapper wearing a disguise. Nothing here opens a database, which is the
// property a module that reaches its data through a port is supposed to have.
package orders_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/usefabrin/fabrin"
	"github.com/usefabrin/fabrin/examples/hello/orders"
)

// memStore is the second implementation of the port: orders.Store, satisfied
// with a map.
//
// It records its writes, because the response body alone cannot tell a handler
// that used the store from one that echoed the request back — see the assertion
// in the test below.
type memStore struct {
	mu     sync.Mutex
	nextID int64
	byID   map[int64]orders.Order
	writes int
}

func newMemStore() *memStore { return &memStore{byID: map[int64]orders.Order{}} }

func (s *memStore) Create(_ context.Context, o *orders.Order) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	o.ID = s.nextID
	s.byID[o.ID] = *o
	s.writes++
	return nil
}

func (s *memStore) Find(_ context.Context, id int64) (*orders.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	o, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("no order %d", id)
	}
	return &o, nil
}

// do drives the app's handler directly, so this test needs no port and no
// listener.
func do(t *testing.T, app *fabrin.App, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	return rec
}

func TestModule_ReachesItsDataOnlyThroughTheStoreItWasGiven(t *testing.T) {
	t.Parallel()

	// No database, no driver, no migration — the module is mounted on a real App
	// and answers real requests with a map behind it. A module that could only be
	// exercised against SQLite would have the ORM welded to it, whatever its
	// import list said.
	store := newMemStore()
	app, err := fabrin.New(fabrin.Options{}, orders.New(store))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	rec := do(t, app, http.MethodPost, "/orders", `{"item":"widget"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /orders = %d, want 201, body %s", rec.Code, rec.Body.String())
	}

	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("POST /orders returned %s, which is not the created order: %v", rec.Body.String(), err)
	}
	if created.ID == 0 {
		t.Fatalf("POST /orders must return the id the store assigned, got %s", rec.Body.String())
	}

	// The response could be the request echoed back; the store's state could not.
	// This is the assertion that distinguishes a handler which used the port from
	// one that only looks as though it did.
	if store.writes != 1 {
		t.Errorf("store recorded %d writes, want 1 — the handler answered without going through the port", store.writes)
	}

	rec = do(t, app, http.MethodGet, "/orders/"+strconv.FormatInt(created.ID, 10), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /orders/%d = %d, want 200, body %s", created.ID, rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "widget") {
		t.Errorf("GET /orders/%d returned %s, want the order that was written", created.ID, rec.Body.String())
	}
}
