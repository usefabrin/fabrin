package auth

import "context"

type identityContextKey struct{}

// WithIdentity returns a child context carrying one authenticated identity.
// Authentication middleware calls it only after validating a live session.
func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, identity)
}

// IdentityFromContext returns the authenticated identity carried by ctx.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	return identity, ok
}
