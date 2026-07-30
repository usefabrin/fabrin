// Package fabrin is a batteries-included web framework for Go, built on Gin and
// inspired by Django's development philosophy.
//
// A Fabrin application is a set of modules — Fabrin's answer to Django's
// INSTALLED_APPS. A module owns its routes, and optionally its models,
// migrations, management commands, and health checks. Modules are composed in
// main, and the App handles registration, lifecycle, and graceful shutdown.
//
// # Gin is public
//
// Context and HandlerFunc are type aliases for the corresponding Gin types,
// not wrappers around them. Every Gin middleware works unmodified, and there is
// no adapter layer between your handler and the router. The trade this makes is
// explicit: Fabrin's compatibility is tied to Gin's v1 API. Gin is also the only
// third-party package Fabrin permits in an exported signature.
//
// # Deployment shapes
//
// Fabrin is a modular monolith by default and extractable by design. A module
// never imports another module: it declares the interface it needs and receives
// it as a dependency, and that interface is the seam along which the module can
// later move into its own process. Setting FABRIN_MODULES selects which modules
// this process mounts, so one binary serves many deployment shapes.
//
// Fabrin deliberately ships no service discovery, service mesh, or RPC
// framework. It ships the seam and service-ready defaults.
//
// # Status
//
// v0: the API is unstable and will break. See the CHANGELOG for breaking
// changes and docs/TODO.md for the roadmap.
package fabrin
