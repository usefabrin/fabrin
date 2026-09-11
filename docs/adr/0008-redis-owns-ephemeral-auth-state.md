# 0008. Redis owns ephemeral authentication state

- **Status:** Accepted
- **Date:** 2026-09-11
- **Deciders:** Fabrin maintainer
- **Requirement / issue:** FR-AUTH-2, FR-AUTH-5, FR-AUTH-6, FR-AUTH-7, #105

## Context

OTP challenges, rolling abuse budgets, browser pre-authentication state and
sessions are short-lived, highly concurrent and shared across application
instances. PostgreSQL can implement them correctly, but row/event tables require
continuous cleanup and put request-path coordination beside durable relational
identity data. Redis supplies server-side expiry and atomic scripts for this
state.

Identities, invitations, groups, permissions and profile relationships need
durability, uniqueness, queryability and foreign keys. Moving those records to
Redis would weaken the application data model. A Redis and PostgreSQL operation
cannot share one transaction, so verified signup needs an explicit recovery
protocol rather than an implied distributed transaction.

## Decision

Redis is Fabrin v1's default store for challenges, abuse budgets, verification
leases, browser pre-authentication, sessions, rotation and revocation. PostgreSQL
stores identities, eligibility, authorization and audit records. The Redis
adapter owns an official Go client configured by URL, but no client type appears
in Fabrin's exported API. V1 supports a standalone Redis primary.

Verification uses a bounded Redis redemption lease. Redis atomically validates
and leases the protected challenge; PostgreSQL idempotently resolves the verified
identity and eligibility; Redis atomically completes redemption and creates the
digest-only session. A transient PostgreSQL failure releases the lease for retry.
Eligibility denial consumes the challenge. Completion records remain briefly so
the same in-flight finalization is idempotent after an ambiguous Redis result.

Redis server time governs budgets and expiry. Keys contain digests rather than
email or source values. Every temporary record has a TTL; correctness does not
depend on cleanup. The application runs PostgreSQL migrations explicitly, while
constructing either adapter performs no network or schema I/O.

## Consequences

Multi-instance limits, challenge consumption and session revocation use one
atomic authority with automatic expiry. PostgreSQL remains the durable source of
identity and authorization truth. Redis becomes a required production dependency
for v1 auth, and operators must configure persistence, authentication, TLS,
memory policy, monitoring and recovery appropriate to session availability.

The verification path is a documented two-store workflow. A verified identity
may exist without a returned session after an outage, but no identity is created
before a valid challenge. Retrying resolves the same identity; the user may need
a fresh OTP after an ambiguous completed response. Identity disable and privilege
changes must revoke Redis sessions before reporting success and reconcile safely
after partial failure.

## Alternatives considered

- Keep all auth state in PostgreSQL: transactionally simple, but retains growing
  event/session tables and makes the durable database the hot coordination path.
- Put all auth data in Redis: gives one atomic store but loses relational identity,
  authorization and application-profile integrity.
- Use Redis only for OTP limits: leaves sessions and revocation split across two
  ephemeral-state designs and preserves most duplicated coordination logic.
- Support Sentinel and Cluster in v1: expands key-slot, failover and test scope
  before the standalone security and recovery behavior is proven.
