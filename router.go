package fabrin

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Context is Gin's request context. It is a type ALIAS, not a wrapper: a
// *fabrin.Context and a *gin.Context are the same type, so a handler written for
// one works as the other with no conversion and no adapter.
//
// This is the single most consequential decision in Fabrin's public API. Blessing
// Gin means every middleware in Gin's ecosystem works here unmodified, and there
// is nothing to learn twice. The cost, accepted deliberately, is that Gin's v1
// API is part of Fabrin's semver contract — if Gin ever ships a v2 with a changed
// Context, Fabrin inherits a breaking change it did not choose. That risk is
// bounded: Gin has been on v1 since 2015.
//
// It is not a licence to bless anything else. Gin is the only third-party package
// permitted in a Fabrin exported signature, and apicheck's allowlist is the single
// reviewable record of it. See ARCHITECTURE.md.
type Context = gin.Context

// HandlerFunc is Gin's handler type. See [Context] for why this is an alias.
type HandlerFunc = gin.HandlerFunc

// H is Gin's shorthand map for JSON responses, re-exported so a handler need not
// import Gin directly for the common case.
type H = gin.H

// Router is the surface a module uses to mount its routes. It is the subset of
// Gin's *gin.RouterGroup that a module legitimately needs, which is deliberately
// narrower than the whole engine: a module must not be able to reconfigure the
// server, add another module's routes under its own prefix, or replace the
// engine's error handling.
//
// Since Context and HandlerFunc are Gin's own types, a gin.HandlerFunc satisfies
// HandlerFunc exactly, so stock Gin middleware can be passed to Use unchanged.
type Router interface {
	// Use adds middleware scoped to this module's routes.
	Use(middleware ...HandlerFunc) gin.IRoutes

	// Group creates a nested prefix within this module's routes.
	Group(relativePath string, handlers ...HandlerFunc) *gin.RouterGroup

	// Handle and the per-method helpers mount a route.
	Handle(httpMethod, relativePath string, handlers ...HandlerFunc) gin.IRoutes
	GET(relativePath string, handlers ...HandlerFunc) gin.IRoutes
	POST(relativePath string, handlers ...HandlerFunc) gin.IRoutes
	PUT(relativePath string, handlers ...HandlerFunc) gin.IRoutes
	PATCH(relativePath string, handlers ...HandlerFunc) gin.IRoutes
	DELETE(relativePath string, handlers ...HandlerFunc) gin.IRoutes
	HEAD(relativePath string, handlers ...HandlerFunc) gin.IRoutes
	OPTIONS(relativePath string, handlers ...HandlerFunc) gin.IRoutes

	// StaticFS serves an embedded or on-disk filesystem.
	StaticFS(relativePath string, fs http.FileSystem) gin.IRoutes
}
