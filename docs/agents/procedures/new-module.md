# New module procedure

1. Declare the consumer-owned port before importing or implementing a provider.
2. Add planned requirement/spec/matrix entries.
3. Write failing route, boundary, and readiness tests as applicable.
4. Implement the required `Module` methods and only needed optional interfaces.
5. For a new public package, record the depguard boundary and API manifest entry.
6. Flip behavior specs to implemented only after exact tests pass.
7. Update parity, architecture, changelog, and examples last.

Module packages never import one another. `main` owns wiring and decides whether
a port is satisfied in-process or by a remote adapter.
