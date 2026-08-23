package admin

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"testing"

	"github.com/usefabrin/fabrin/orm"
)

type order struct {
	ID       int64
	Item     string
	Quantity int
}

type orderMemory struct {
	next int64
	rows map[string]order
}

func newOrderMemory() *orderMemory {
	return &orderMemory{next: 1, rows: map[string]order{}}
}

func (m *orderMemory) persistence() persistence[order] {
	return persistence[order]{
		list: func(context.Context) ([]order, error) {
			keys := make([]string, 0, len(m.rows))
			for key := range m.rows {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			out := make([]order, 0, len(keys))
			for _, key := range keys {
				out = append(out, m.rows[key])
			}
			return out, nil
		},
		get: func(_ context.Context, key string) (order, error) {
			got, ok := m.rows[key]
			if !ok {
				return order{}, fmt.Errorf("order %s not found", key)
			}
			return got, nil
		},
		create: func(_ context.Context, value order) (order, error) {
			value.ID = m.next
			m.next++
			m.rows[strconv.FormatInt(value.ID, 10)] = value
			return value, nil
		},
		update: func(_ context.Context, key string, value order) (order, error) {
			if _, ok := m.rows[key]; !ok {
				return order{}, fmt.Errorf("order %s not found", key)
			}
			value.ID, _ = strconv.ParseInt(key, 10, 64)
			m.rows[key] = value
			return value, nil
		},
		delete: func(_ context.Context, key string) error {
			delete(m.rows, key)
			return nil
		},
	}
}

func orderResource(t *testing.T, memory *orderMemory, actions *[]action, tokens *[]string) *resource[order] {
	t.Helper()

	got, err := newResource(resourceConfig[order]{
		model: orm.Registered{
			Module: "shop",
			Model: orm.Model{Table: "orders", Fields: []orm.Field{
				{Name: "id", Type: orm.Int64, PrimaryKey: true},
				{Name: "item", Type: orm.String, MaxLen: 64},
				{Name: "quantity", Type: orm.Int},
			}},
		},
		newRecord: func() order { return order{} },
		key: keyAdapter[order]{name: "id", read: func(value order) string {
			return strconv.FormatInt(value.ID, 10)
		}},
		fields: []fieldAdapter[order]{
			{
				name: "item",
				read: func(value order) string { return value.Item },
				write: func(value *order, input string) error {
					value.Item = input
					return nil
				},
			},
			{
				name: "quantity",
				read: func(value order) string { return strconv.Itoa(value.Quantity) },
				write: func(value *order, input string) error {
					quantity, err := strconv.Atoi(input)
					if err != nil {
						return fmt.Errorf("must be an integer: %w", err)
					}
					value.Quantity = quantity
					return nil
				},
			},
		},
		persistence: memory.persistence(),
		authorize: func(_ context.Context, got action, target target) error {
			if target.module != "shop" || target.model != "orders" {
				t.Fatalf("authorization target = %#v, want shop.orders", target)
			}
			*actions = append(*actions, got)
			return nil
		},
		validateCSRF: func(_ context.Context, token string) error {
			*tokens = append(*tokens, token)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("newResource: %v", err)
	}
	return got
}

func TestResource_CRUDFlowsFromMetadataFormsToPersistence(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	memory := newOrderMemory()
	var actions []action
	var tokens []string
	resource := orderResource(t, memory, &actions, &tokens)

	created, createForm, err := resource.create(ctx, "csrf-create", url.Values{
		"item":     {"widget"},
		"quantity": {"2"},
		"ignored":  {"must not be mass-assigned"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(createForm.errors) != 0 {
		t.Fatalf("create form errors = %v, want none", createForm.errors)
	}
	if want := []formField{{name: "item", value: "widget"}, {name: "quantity", value: "2"}}; created.key != "1" || !slices.Equal(created.fields, want) {
		t.Fatalf("created view = %#v, want key 1 and metadata-derived fields %#v", created, want)
	}

	listed, err := resource.list(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || listed[0].key != created.key || !slices.Equal(listed[0].fields, created.fields) {
		t.Fatalf("list = %#v, want the created record", listed)
	}

	updated, updateForm, err := resource.update(ctx, "1", "csrf-update", url.Values{
		"item":     {"updated"},
		"quantity": {"3"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(updateForm.errors) != 0 {
		t.Fatalf("update form errors = %v, want none", updateForm.errors)
	}
	if want := []formField{{name: "item", value: "updated"}, {name: "quantity", value: "3"}}; !slices.Equal(updated.fields, want) {
		t.Fatalf("updated view = %#v, want fields %#v", updated, want)
	}

	if err := resource.delete(ctx, "1", "csrf-delete"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	listed, err = resource.list(ctx)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("list after delete = %#v, want empty", listed)
	}

	if want := []action{createAction, listAction, updateAction, deleteAction, listAction}; !slices.Equal(actions, want) {
		t.Errorf("authorization actions = %v, want %v", actions, want)
	}
	if want := []string{"csrf-create", "csrf-update", "csrf-delete"}; !slices.Equal(tokens, want) {
		t.Errorf("CSRF tokens = %v, want %v", tokens, want)
	}
}

func TestResource_UnsafeActionsFailClosedBeforeBindingOrPersistence(t *testing.T) {
	t.Parallel()

	denied := errors.New("denied")
	tests := []struct {
		name       string
		rejectCSRF bool
		action     action
	}{
		{name: "create rejects CSRF", rejectCSRF: true, action: createAction},
		{name: "create rejects authorization", action: createAction},
		{name: "update rejects CSRF", rejectCSRF: true, action: updateAction},
		{name: "update rejects authorization", action: updateAction},
		{name: "delete rejects CSRF", rejectCSRF: true, action: deleteAction},
		{name: "delete rejects authorization", action: deleteAction},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			memory := newOrderMemory()
			var actions []action
			var tokens []string
			resource := orderResource(t, memory, &actions, &tokens)

			bound := 0
			originalWrite := resource.fields[0].adapter.write
			resource.fields[0].adapter.write = func(value *order, input string) error {
				bound++
				return originalWrite(value, input)
			}

			persisted := 0
			resource.persistence.create = func(context.Context, order) (order, error) {
				persisted++
				return order{}, nil
			}
			resource.persistence.get = func(context.Context, string) (order, error) {
				persisted++
				return order{}, nil
			}
			resource.persistence.update = func(context.Context, string, order) (order, error) {
				persisted++
				return order{}, nil
			}
			resource.persistence.delete = func(context.Context, string) error {
				persisted++
				return nil
			}

			resource.validateCSRF = func(context.Context, string) error {
				tokens = append(tokens, "checked")
				if tt.rejectCSRF {
					return denied
				}
				return nil
			}
			resource.authorize = func(context.Context, action, target) error {
				actions = append(actions, tt.action)
				return denied
			}

			var err error
			switch tt.action {
			case createAction:
				_, _, err = resource.create(t.Context(), "token", url.Values{"item": {"blocked"}})
			case updateAction:
				_, _, err = resource.update(t.Context(), "1", "token", url.Values{"item": {"blocked"}})
			case deleteAction:
				err = resource.delete(t.Context(), "1", "token")
			}

			if !errors.Is(err, denied) {
				t.Fatalf("unsafe action error = %v, want denied", err)
			}
			if len(tokens) != 1 {
				t.Errorf("CSRF checks = %d, want one", len(tokens))
			}
			wantAuth := 1
			if tt.rejectCSRF {
				wantAuth = 0
			}
			if len(actions) != wantAuth {
				t.Errorf("authorization checks = %d, want %d", len(actions), wantAuth)
			}
			if bound != 0 {
				t.Errorf("bound %d field(s) after a failed gate, want zero", bound)
			}
			if persisted != 0 {
				t.Errorf("called persistence %d time(s) after a failed gate, want zero", persisted)
			}
		})
	}
}

func TestResource_FormErrorsStayWithMetadataFieldsAndSkipPersistence(t *testing.T) {
	t.Parallel()

	memory := newOrderMemory()
	var actions []action
	var tokens []string
	resource := orderResource(t, memory, &actions, &tokens)

	persisted := 0
	resource.persistence.create = func(context.Context, order) (order, error) {
		persisted++
		return order{}, nil
	}

	_, bound, err := resource.create(t.Context(), "token", url.Values{
		"item":     {"this value is deliberately longer than the metadata limit of sixty-four characters"},
		"quantity": {"many"},
	})
	if err != nil {
		t.Fatalf("create invalid form: %v", err)
	}
	for _, name := range []string{"item", "quantity"} {
		if bound.errors[name] == "" {
			t.Errorf("form error for %q is empty; got %v", name, bound.errors)
		}
	}
	if want := []formField{
		{name: "item", value: "this value is deliberately longer than the metadata limit of sixty-four characters"},
		{name: "quantity", value: "many"},
	}; !slices.Equal(bound.fields, want) {
		t.Errorf("bound fields = %#v, want metadata order %#v", bound.fields, want)
	}
	if persisted != 0 {
		t.Errorf("persistence called %d time(s) for an invalid form, want zero", persisted)
	}
}
