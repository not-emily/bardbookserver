# Bard API

<!-- Generated from docs/api/openapi.yaml by scripts/docs. Do not edit. -->

A self-hosted server for ebooks and audiobooks.

Version `0.1.0`.

Bard serves its admin UI, this JSON API and the media itself from one
origin on one port. A client points at the host and nothing else.

**Authentication is a per-device bearer token.** `POST /auth/login`
exchanges a username and password for one, and that token is presented as
`Authorization: Bearer <token>` on every later request. Tokens are opaque
and revocable: there is nothing to verify offline, because revocation that
takes effect on the next request is worth more than statelessness here.

**CSRF does not apply to this API.** It guards the cookie-authenticated
browser session only, since a cross-site form cannot set an
`Authorization` header. A native client never sends a CSRF token.

**Content a reader may not see is absent rather than refused.** Where a
source hides age-locked books, requesting one gives `404`, not `403` with
a reason — a reason code would confirm the book exists.

## Servers

- `https://bard.example.com` — Your own server, behind whichever transport you chose.

## Endpoints

- [`POST /api/v1/auth/login`](#post-apiv1authlogin) — Exchange credentials for a device token

## `POST /api/v1/auth/login`

**Exchange credentials for a device token**

The only unauthenticated endpoint. Rate limited per resolved client
address, so a server whose `trusted_proxies` is wrong throttles every
client as one — see `/diagnostics` in the admin UI.

`device_name` is what the account holder sees in their authorized
devices list, so it should name the device rather than the app.

No authentication required.

### Request body

[`LoginRequest`](#loginrequest), required

### Responses

| Status | Body | Description |
| --- | --- | --- |
| `200` | [`LoginResponse`](#loginresponse) | A new device token. Store it; it is not retrievable again. |
| `400` | [`Error`](#error) | Username or password missing, or a malformed body. |
| `401` | [`Error`](#error) | Invalid credentials. Deliberately not distinguished from an unknown username. |
| `429` | [`Error`](#error) | Too many attempts. `Retry-After` carries the wait in seconds. |

## Schemas

### Denied

An authorization failure that the reader is allowed to know about.
`reason` is the stable code to branch on; extra properties carry
whatever that reason needs, such as the age a book requires.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `error` | string | yes |  |
| `reason` | string | yes | A stable code; `error` is the sentence for a person. |

### Device

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `active_user_id` | integer (int64) | yes | The profile this device is currently reading as, which may be a managed user. |
| `created_at` | string (date-time) | yes |  |
| `id` | integer (int64) | yes |  |
| `last_seen_at` | string (date-time) |  |  |
| `last_seen_ip` | string |  | Recorded for the owner's devices list. Never used as identity. |
| `name` | string | yes |  |

### Error

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `error` | string | yes | Human-readable, and not intended for matching on. |

### LoginRequest

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `device_name` | string |  | Shown in the authorized devices list, e.g. "Emily's iPhone". |
| `password` | string (password) | yes |  |
| `username` | string | yes |  |

### LoginResponse

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `device` | [`Device`](#device) | yes |  |
| `token` | string | yes | Opaque. Shown once, stored hashed, and revocable. |
| `user` | [`User`](#user) | yes |  |

### User

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `age` | integer |  | Present only when a birth date is set. Absent is not zero. |
| `can_upload` | boolean | yes |  |
| `display_name` | string | yes |  |
| `has_pin` | boolean | yes | Whether switching into this profile asks for a PIN. |
| `id` | integer (int64) | yes |  |
| `is_admin` | boolean | yes |  |
| `managed` | boolean | yes | A passwordless profile switched into from its owner's device. |
| `restricted` | boolean | yes | Subject to age gating. |
| `username` | string | yes |  |

