// Package admin contains the private vertical used to prove Fabrin's future
// metadata-driven admin seam. It intentionally exports nothing yet.
package admin

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/usefabrin/fabrin/orm"
)

type action string

const (
	listAction   action = "list"
	createAction action = "create"
	updateAction action = "update"
	deleteAction action = "delete"
)

// target carries everything an authorization policy needs to distinguish one
// model operation without giving the admin a user or session representation.
// The policy reads the authenticated principal from ctx, where the future auth
// middleware owns it.
type target struct {
	module string
	model  string
	key    string
}

type formField struct {
	name  string
	value string
}

type form struct {
	fields []formField
	errors map[string]string
}

func (f form) valid() bool { return len(f.errors) == 0 }

type recordView struct {
	key    string
	fields []formField
}

// A keyAdapter and fieldAdapter are the explicit link from Fabrin metadata to a
// concrete Go type. Functions rather than reflection keep a renamed or retyped
// field as a compile-time edit at the one resource registration site.
type keyAdapter[T any] struct {
	name string
	read func(T) string
}

type fieldAdapter[T any] struct {
	name  string
	read  func(T) string
	write func(*T, string) error
}

// persistence is deliberately private and resource-specific. It is the set of
// callbacks this proof exercises, not a Fabrin repository, query API, database
// service, transaction contract, or promise about pagination and filtering.
type persistence[T any] struct {
	list   func(context.Context) ([]T, error)
	get    func(context.Context, string) (T, error)
	create func(context.Context, T) (T, error)
	update func(context.Context, string, T) (T, error)
	delete func(context.Context, string) error
}

type resourceConfig[T any] struct {
	model        orm.Registered
	newRecord    func() T
	key          keyAdapter[T]
	fields       []fieldAdapter[T]
	persistence  persistence[T]
	authorize    func(context.Context, action, target) error
	validateCSRF func(context.Context, string) error
}

type fieldBinding[T any] struct {
	metadata orm.Field
	adapter  fieldAdapter[T]
}

type resource[T any] struct {
	model        orm.Registered
	newRecord    func() T
	key          keyAdapter[T]
	fields       []fieldBinding[T]
	persistence  persistence[T]
	authorize    func(context.Context, action, target) error
	validateCSRF func(context.Context, string) error
}

func newResource[T any](cfg resourceConfig[T]) (*resource[T], error) {
	if strings.TrimSpace(cfg.model.Module) == "" {
		return nil, fmt.Errorf("admin: resource for table %q has no owning module", cfg.model.Model.Table)
	}

	// Reuse the metadata registry's validation rather than teaching admin a
	// second definition of a valid table. The returned copy also prevents a
	// caller from mutating form behavior after construction.
	registry := orm.NewRegistry()
	if err := registry.Register(cfg.model.Module, cfg.model.Model); err != nil {
		return nil, fmt.Errorf("admin: resource metadata: %w", err)
	}
	registered := registry.Models()[0]

	if cfg.newRecord == nil {
		return nil, fmt.Errorf("admin: resource %q has no constructor", registered.Model.Table)
	}
	if cfg.key.read == nil {
		return nil, fmt.Errorf("admin: resource %q has no key reader", registered.Model.Table)
	}
	if err := validatePersistence(registered.Model.Table, cfg.persistence); err != nil {
		return nil, err
	}
	if cfg.authorize == nil {
		return nil, fmt.Errorf("admin: resource %q has no authorizer", registered.Model.Table)
	}
	if cfg.validateCSRF == nil {
		return nil, fmt.Errorf("admin: resource %q has no CSRF validator", registered.Model.Table)
	}

	adapters := make(map[string]fieldAdapter[T], len(cfg.fields))
	for _, adapter := range cfg.fields {
		if strings.TrimSpace(adapter.name) == "" {
			return nil, fmt.Errorf("admin: resource %q has an unnamed field adapter", registered.Model.Table)
		}
		if adapter.read == nil || adapter.write == nil {
			return nil, fmt.Errorf("admin: field adapter %q on resource %q must read and write", adapter.name, registered.Model.Table)
		}
		if _, duplicate := adapters[adapter.name]; duplicate {
			return nil, fmt.Errorf("admin: resource %q has two adapters for field %q", registered.Model.Table, adapter.name)
		}
		adapters[adapter.name] = adapter
	}

	bindings := make([]fieldBinding[T], 0, len(registered.Model.Fields)-1)
	var primary orm.Field
	for _, metadata := range registered.Model.Fields {
		if metadata.PrimaryKey {
			primary = metadata
			continue
		}
		adapter, ok := adapters[metadata.Name]
		if !ok {
			return nil, fmt.Errorf("admin: resource %q has no adapter for metadata field %q", registered.Model.Table, metadata.Name)
		}
		delete(adapters, metadata.Name)
		bindings = append(bindings, fieldBinding[T]{metadata: metadata, adapter: adapter})
	}
	if cfg.key.name != primary.Name {
		return nil, fmt.Errorf("admin: resource %q key adapter names %q, metadata primary key is %q", registered.Model.Table, cfg.key.name, primary.Name)
	}
	for extra := range adapters {
		return nil, fmt.Errorf("admin: resource %q has an adapter for unknown or primary-key field %q", registered.Model.Table, extra)
	}

	return &resource[T]{
		model:        registered,
		newRecord:    cfg.newRecord,
		key:          cfg.key,
		fields:       bindings,
		persistence:  cfg.persistence,
		authorize:    cfg.authorize,
		validateCSRF: cfg.validateCSRF,
	}, nil
}

func validatePersistence[T any](table string, p persistence[T]) error {
	switch {
	case p.list == nil:
		return fmt.Errorf("admin: resource %q has no list persistence callback", table)
	case p.get == nil:
		return fmt.Errorf("admin: resource %q has no get persistence callback", table)
	case p.create == nil:
		return fmt.Errorf("admin: resource %q has no create persistence callback", table)
	case p.update == nil:
		return fmt.Errorf("admin: resource %q has no update persistence callback", table)
	case p.delete == nil:
		return fmt.Errorf("admin: resource %q has no delete persistence callback", table)
	}
	return nil
}

func (r *resource[T]) list(ctx context.Context) ([]recordView, error) {
	if err := r.authorizeAction(ctx, listAction, ""); err != nil {
		return nil, err
	}
	records, err := r.persistence.list(ctx)
	if err != nil {
		return nil, fmt.Errorf("admin: list %s.%s: %w", r.model.Module, r.model.Model.Table, err)
	}
	views := make([]recordView, 0, len(records))
	for _, record := range records {
		views = append(views, r.view(record))
	}
	return views, nil
}

func (r *resource[T]) create(ctx context.Context, token string, input url.Values) (recordView, form, error) {
	if err := r.guardWrite(ctx, createAction, "", token); err != nil {
		return recordView{}, form{}, err
	}
	value, bound := r.bind(r.newRecord(), input)
	if !bound.valid() {
		return recordView{}, bound, nil
	}
	created, err := r.persistence.create(ctx, value)
	if err != nil {
		return recordView{}, bound, fmt.Errorf("admin: create %s.%s: %w", r.model.Module, r.model.Model.Table, err)
	}
	return r.view(created), bound, nil
}

func (r *resource[T]) update(ctx context.Context, key, token string, input url.Values) (recordView, form, error) {
	if err := r.guardWrite(ctx, updateAction, key, token); err != nil {
		return recordView{}, form{}, err
	}
	current, err := r.persistence.get(ctx, key)
	if err != nil {
		return recordView{}, form{}, fmt.Errorf("admin: load %s.%s %q: %w", r.model.Module, r.model.Model.Table, key, err)
	}
	value, bound := r.bind(current, input)
	if !bound.valid() {
		return recordView{}, bound, nil
	}
	updated, err := r.persistence.update(ctx, key, value)
	if err != nil {
		return recordView{}, bound, fmt.Errorf("admin: update %s.%s %q: %w", r.model.Module, r.model.Model.Table, key, err)
	}
	return r.view(updated), bound, nil
}

func (r *resource[T]) delete(ctx context.Context, key, token string) error {
	if err := r.guardWrite(ctx, deleteAction, key, token); err != nil {
		return err
	}
	if err := r.persistence.delete(ctx, key); err != nil {
		return fmt.Errorf("admin: delete %s.%s %q: %w", r.model.Module, r.model.Model.Table, key, err)
	}
	return nil
}

func (r *resource[T]) guardWrite(ctx context.Context, action action, key, token string) error {
	// CSRF sits outside authorization in the eventual HTTP stack. Keeping the
	// same order here means no unsafe form is bound and no policy detail is
	// exposed until the request proves it came through the trusted form flow.
	if err := r.validateCSRF(ctx, token); err != nil {
		return fmt.Errorf("admin: %s %s.%s: CSRF: %w", action, r.model.Module, r.model.Model.Table, err)
	}
	return r.authorizeAction(ctx, action, key)
}

func (r *resource[T]) authorizeAction(ctx context.Context, action action, key string) error {
	identity := target{module: r.model.Module, model: r.model.Model.Table, key: key}
	if err := r.authorize(ctx, action, identity); err != nil {
		return fmt.Errorf("admin: authorize %s %s.%s: %w", action, identity.module, identity.model, err)
	}
	return nil
}

func (r *resource[T]) bind(value T, input url.Values) (T, form) {
	bound := form{fields: make([]formField, 0, len(r.fields)), errors: map[string]string{}}
	for _, field := range r.fields {
		raw := input.Get(field.metadata.Name)
		bound.fields = append(bound.fields, formField{name: field.metadata.Name, value: raw})

		if field.metadata.MaxLen > 0 && utf8.RuneCountInString(raw) > field.metadata.MaxLen {
			bound.errors[field.metadata.Name] = fmt.Sprintf("must be at most %d characters", field.metadata.MaxLen)
			continue
		}
		if err := field.adapter.write(&value, raw); err != nil {
			bound.errors[field.metadata.Name] = err.Error()
		}
	}
	return value, bound
}

func (r *resource[T]) view(value T) recordView {
	fields := make([]formField, 0, len(r.fields))
	for _, field := range r.fields {
		fields = append(fields, formField{name: field.metadata.Name, value: field.adapter.read(value)})
	}
	return recordView{key: r.key.read(value), fields: fields}
}
