# Authentication availability

**Built-in login is not available yet.** Do not expose the private OTP proof as
an authentication endpoint or use it to issue production sessions. There is no
public `auth.New`, login route, identity store, session middleware or admin login
to configure in this revision.

The maintainer has approved the [email OTP contract](../AUTH_CONTRACT.md) for
implementation. It commits to code-based signup/login, framework-owned minimal
identities extended through application profiles, browser cookie sessions and
native bearer sessions. Approval does not mean these capabilities have shipped.

## Implemented foundations

- [Generated PostgreSQL data](generated-data.md) provides typed create/get stores
  and schema metadata. Stores do not authorize requests.
- [Capture email](testing-email.md) provides a bounded in-memory test inbox. It
  does not deliver real email or expose an HTTP inbox.
- The private challenge state machine verifies HMAC-bound credentials, canonical
  email, expiry, attempt exhaustion and one-time consumption under concurrent
  calls. This has no exported user API and is not a complete authentication store.

From the framework checkout, `go test -race ./auth` runs the primitive tests.
It does not need a database or email provider. Passing it does not establish the
transactional and distributed properties required for a working login system.

## Work still required

Production OTP needs shared address/source abuse budgets, atomic reservation and
resend replacement, deadline-bounded delivery and cleanup, then identity and
session creation in the same transaction as successful verification. Transport
must add CSRF, Origin checks, browser pre-auth state, native-purpose separation,
revocation and authorization. These must not be assembled ad hoc from the private
primitive: consuming a code and separately creating a session is not atomic.

The [v1 progress record](../V1_PLAN.md) tracks these dependencies. The September 10
preview target is incomplete, and no production auth or stable-v1 release is
claimed. Public setup instructions will be added with the tested integration,
rather than documenting configuration calls that do not exist.
