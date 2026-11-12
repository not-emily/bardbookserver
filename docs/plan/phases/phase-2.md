# Phase 2: Auth & Policy Core

> **Depends on:** Phase 1
> **Enables:** Phase 6 (scoped reads), Phase 9 (age gating), Phase 10 (upload permission), Phase 11 (admin UI)
>
> See: [Full Plan](../plan.md)

## Goal

Stand up accounts, revocable device tokens, and the policy package — early, because remote internet access is a stated requirement and these are foundations rather than hardening.

## Key Deliverables

- `users` table: username, Argon2id hash, `is_admin`, `restricted`, `birth_date`, `managed`, `pin_hash`
- `devices` table: hashed token, user, device name, last-seen IP, last-seen at, created at
- Login, logout, device list, device revoke
- Managed-user switching: a device token declares which managed users it may assume
- Login rate limiting keyed on the trusted-proxy-resolved address
- Policy package: `Result`, `Reason` constants, `allow`/`deny`/`denyWith`, explicit registry, single-point message resolution
- Reason-coverage test
- `bard user add` / `bard user passwd` CLI, and first-run admin bootstrap

## Files to Create

- `internal/db/migrations/0002_users_devices.sql`
- `internal/model/user.go` — includes `Age()` computed from `birth_date`
- `internal/auth/password.go` — Argon2id hash and verify
- `internal/auth/token.go` — generate, hash, look up, revoke
- `internal/auth/ratelimit.go` — per-address login throttle
- `internal/policy/policy.go` — `Result`, `Reason`, helpers, registry, `Message()`
- `internal/policy/policy_test.go` — reason coverage
- `internal/api/auth_handlers.go`
- `internal/api/middleware_auth.go` — resolve device token to a user
- `cmd/bard/user.go`

## Dependencies

**Internal:** Phase 1 config, database, and trusted-proxy middleware (the rate limiter depends on correct address resolution).

**External:**
- `golang.org/x/crypto/argon2`

## Implementation Notes

Tokens are 256 bits from `crypto/rand`, returned to the client once and stored only as a hash. Compare with `crypto/subtle.ConstantTimeCompare`. Revocation is a row delete, which is why Decision 9 chose opaque tokens over JWTs.

The policy package ports `rails/policies/advanced` plus the `Scope` half of `rails/policies/basic` from the pattern library. Follow its rules as written: action methods return `Result` and **never** a bare bool, results are built only via the helpers, role helpers stay unexported and boolean, and the registry is an explicit map — never reflection-based lookup, which is Go's equivalent of the `.constantize` CWE-470 hazard the pattern calls out.

The upgrade over the Rails original: `policy_test.go` enumerates every `Reason` constant and asserts `Message()` returns a non-default string. The Rails pattern notes a missing case degrades silently to "Not authorized"; here it fails the build.

Managed users have no password. Switching requires a device token already authenticated as the owner, plus the PIN when set. The server returns a session scoped to the managed user; whether the client auto-selects or shows a picker is a client setting.

Only `BookPolicy` needs a `Scope`; add scopes to other resources only when a listing endpoint needs filtering.

## Sub-phases

### 2.1: Accounts and passwords
**Goal:** users table, Argon2id, CLI user management, first-run admin bootstrap.
**Deliverables:** migration, `model/user.go`, `auth/password.go`, `cmd/bard/user.go`.

### 2.2: Device tokens and sessions
**Goal:** issue, resolve, list, and revoke per-device tokens.
**Deliverables:** `devices` table, `auth/token.go`, auth middleware, login/logout/device endpoints.

### 2.3: Managed users
**Goal:** profile switching on shared devices.
**Deliverables:** `managed` and `pin_hash` columns, switchable-user set on a token, switch endpoint.

### 2.4: Policy package
**Goal:** the authorization contract everything else depends on.
**Deliverables:** `policy.go`, registry, `Message()`, reason-coverage test.

### 2.5: Login rate limiting
**Goal:** make the server safe to expose.
**Deliverables:** per-address throttle with backoff, keyed on the resolved address from Phase 1.

## Validation

- [ ] Admin can be created on first run and can log in
- [ ] A device token authenticates requests; revoking it returns 401 on the next call
- [ ] The device list shows name, last-seen IP, and last-seen time
- [ ] Repeated failed logins trip the rate limiter and recover after backoff
- [ ] Behind a trusted proxy, two different forwarded addresses are throttled independently
- [ ] A managed user cannot be assumed from an unrelated device token
- [ ] A wrong PIN is rejected
- [ ] `go test ./internal/policy` fails if a `Reason` constant has no message
- [ ] Password hashes and token hashes never appear in logs or API responses
