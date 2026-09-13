# Shiorin

A Go starter project for an API that stores and searches technical article bookmarks.

Requires Go 1.27.1 or later and PostgreSQL (the development configuration uses PostgreSQL 18).
Database access uses `pgx`; password hashing uses Argon2id from `golang.org/x/crypto`.
Go downloads dependencies on the first run.

## Run

```powershell
docker compose up -d --wait db
$env:DATABASE_URL = "postgres://shiorin:shiorin_dev@127.0.0.1:54329/shiorin?sslmode=disable"
go run ./cmd/migrate
go run ./cmd/api
```

Start Docker or Rancher Desktop first. The Compose credentials are for local development only.
PostgreSQL data lives in a named volume and survives container and API restarts.
For an existing PostgreSQL server, set `DATABASE_URL` to a fresh database and run the same migration command.
Use TLS when connecting to a remote database.

The server listens on http://127.0.0.1:8080 by default. Press Ctrl+C to stop it.
To use a different address, set the environment variable before starting the server:

```powershell
$env:HTTP_ADDR = "127.0.0.1:9090"
go run ./cmd/api
```

`.env.example` provides an example configuration. The application does not load `.env` files automatically.
`STORAGE` defaults to `postgres`; a missing connection string or schema causes startup to fail.
It never silently falls back to memory after a database error.

For an explicitly temporary instance without PostgreSQL:

```powershell
$env:STORAGE = "memory"
go run ./cmd/api
```

Memory mode loses all accounts, sessions, and bookmarks on restart. Set `STORAGE` back to `postgres`
to use persistent storage. Multiple API instances must use the same database for shared authentication and data.

## Try the API

Run these commands in a separate PowerShell session while the server is running at its default address:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/healthz

$credentials = @{
    username = "alice"
    password = "replace-with-your-own-long-passphrase"
} | ConvertTo-Json
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/auth/register -ContentType "application/json" -Body $credentials
$login = Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/auth/login -ContentType "application/json" -Body $credentials
$headers = @{ Authorization = "Bearer $($login.access_token)" }
Invoke-RestMethod http://127.0.0.1:8080/me -Headers $headers

$body = @{
    title = "Building a search API in Go"
    url = "https://example.com/go-search"
    note = "Implementation notes on database search"
    tags = @("Go", "database")
} | ConvertTo-Json
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/bookmarks -Headers $headers -ContentType "application/json; charset=utf-8" -Body ([Text.Encoding]::UTF8.GetBytes($body))

Invoke-RestMethod "http://127.0.0.1:8080/bookmarks?q=go&tag=database&limit=20&offset=0" -Headers $headers

Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/auth/logout -Headers $headers
```

| Method | Path | Description |
|---|---|---|
| GET | /healthz | Check whether the API process is running |
| POST | /auth/register | Register a username and password; returns the user (201) |
| POST | /auth/login | Exchange credentials for a bearer token (200) |
| POST | /auth/logout | Revoke the current bearer token (204; authentication required) |
| GET | /me | Return the authenticated user |
| GET | /me/sessions | List the current user's active sessions, including the current session |
| PATCH | /me/sessions/{id} | Rename an owned session using device_label (204) |
| DELETE | /me/sessions/{id} | Revoke one owned session, including the current one (204) |
| POST | /me/sessions/revoke-others | Revoke all other sessions while keeping the current one (204) |
| POST | /bookmarks | Create a bookmark owned by the authenticated user |
| GET | /bookmarks | Search only the authenticated user's bookmarks |

## Accounts and ownership

Anyone can register a separate account using `username` and `password`.
Usernames are case-insensitive, normalized to lowercase with surrounding whitespace removed,
and must contain 3-32 ASCII letters, digits, underscores, or hyphens. Duplicate usernames return 409.
Passwords are not trimmed or normalized. They must contain at least 15 characters and no more than 1024 UTF-8 bytes.

Registration does not log the user in. Login returns `user`, `access_token`, `token_type`, `expires_at`, and `session_id`.
Send the token in the `Authorization: Bearer <access_token>` header. Sessions expire after 24 hours;
logout revokes only the supplied token. Missing, invalid, expired, or revoked tokens return 401.
Tokens are accepted only in the authorization header, not as query parameters or cookies.

Each bookmark contains a server-assigned `user_id`. Clients cannot assign ownership:
`user_id` in a creation body or search query is rejected with 400. Search filters, `total`,
and pagination apply only to the current user's records. There is no public or cross-user search.
To try isolation, register and log in as `bob`, then search with Bob's token: his results are empty
until he creates his own bookmarks.

Passwords use independently salted Argon2id hashes (19 MiB memory, two passes, one lane).
Session tokens contain 32 random bytes and are stored only as SHA-256 digests.
Account and authenticated responses use `Cache-Control: no-store`.
At most two password hashes run concurrently; excess authentication work returns 429 with `Retry-After`.
The hashing approach follows the [Go Argon2 documentation](https://pkg.go.dev/golang.org/x/crypto/argon2)
and [OWASP password storage guidance](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).

## Manage logged-in sessions

Each login creates an independent session. This represents a login in an app or browser, not a verified physical device.
Login optionally accepts `device_label` (up to 100 UTF-8 bytes). The server also records the `User-Agent` header
(up to 512 UTF-8 bytes). Both are descriptive, client-provided information and are never used for authentication.
Escape these values when rendering them in a future UI.

After registering Alice, log in from another client and inspect sessions:

```powershell
$credentials = @{
    username = "alice"
    password = "replace-with-your-own-long-passphrase"
    device_label = "Work laptop"
} | ConvertTo-Json
$login = Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/auth/login -ContentType "application/json" -Body $credentials
$headers = @{ Authorization = "Bearer $($login.access_token)" }
$sessions = Invoke-RestMethod http://127.0.0.1:8080/me/sessions -Headers $headers
$sessions.items | Format-Table id, device_label, user_agent, last_seen_at, current

$label = @{ device_label = "Personal laptop" } | ConvertTo-Json
Invoke-RestMethod -Method Patch -Uri "http://127.0.0.1:8080/me/sessions/$($login.session_id)" -Headers $headers -ContentType "application/json" -Body $label

# Revoke all other logins, keeping this one.
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/me/sessions/revoke-others -Headers $headers

# Revoke the current login. A listed session ID can be used to revoke another login instead.
Invoke-RestMethod -Method Delete -Uri "http://127.0.0.1:8080/me/sessions/$($login.session_id)" -Headers $headers
```

The list returns `items` containing `id`, `created_at`, `expires_at`, `last_seen_at`, `device_label`,
`user_agent`, and `current`. It never returns access tokens or token digests. Other users' sessions cannot
be listed, renamed, or revoked; an inaccessible or inactive session ID returns 404.

API instances check PostgreSQL on every authenticated request, without a local authentication cache.
Once revocation commits, subsequent authentication checks on any instance reject the token.
Requests that already passed authentication may still finish. Database failures fail closed with 500
rather than granting access or treating an outage as an incorrect password.

Session expiry is fixed at 24 hours after login. `last_seen_at` updates at most every five minutes per session,
so it is approximate and does not extend expiry. The server runs cleanup at startup and every 15 minutes,
deleting records seven days after expiry or revocation. Inactive records never authenticate during retention.
Session history is not exposed in the active-session list. Keep clocks synchronized across API hosts.

## Search behavior

`q` performs a case-insensitive substring search across the title, URL, and note.
`tag` performs an exact match after trimming surrounding whitespace and normalizing case.
When both `q` and `tag` are provided, a bookmark must match both conditions.
PostgreSQL results are ordered by creation time descending, then ID descending to break ties.
Memory mode uses reverse insertion order. Responses contain `items`, `total`, `limit`, and `offset` fields.
PostgreSQL computes the count and page in one consistent snapshot. Substring matching currently uses a scan
within the owner's records; dedicated full-text indexes are a future enhancement.

`limit` accepts values from 1 to 100 (default: 20), and `offset` accepts values from 0 to 1,000,000 (default: 0).
POST request bodies are limited to 1 MiB. Only HTTP and HTTPS URLs are accepted.
The application does not visit submitted URLs or fetch article content.

## Project structure

- `cmd/api`: Server startup, timeouts, and graceful shutdown
- `cmd/migrate`: Explicit transactional schema migration command
- `internal/httpapi`: HTTP handlers, input validation, and API tests
- `internal/auth`: Registration, password hashing, session management, repository contract, and test memory store
- `internal/bookmark`: Data types, repository interface, and concurrency-safe in-memory storage
- `internal/postgres`: Persistent account, session, and bookmark repositories; database integration tests
- `db/schema.sql`: Initial managed schema, embedded in the migration runner
- `compose.yaml`: Local PostgreSQL instance with persistent storage
- `.github/workflows/ci.yml`: Formatting checks, vet, race detection tests, and builds

## Validation

```powershell
go test ./...
go vet ./...
go build -o bin/shiorin.exe ./cmd/api
```

To include real PostgreSQL integration tests:

```powershell
$env:TEST_DATABASE_URL = "postgres://shiorin:shiorin_dev@127.0.0.1:54329/shiorin?sslmode=disable"
go test -count=1 ./...
```

The database integration test creates and drops only a randomly named test schema and requires permission
to create schemas. It checks persistence across repository recreation, multi-instance revocation, ownership,
pagination, expiry, cleanup, and concurrent migration execution. Without `TEST_DATABASE_URL`, that test is skipped.
GitHub Actions provisions PostgreSQL and runs `go test -race ./...` on Linux, including integration tests.

## Database migrations

`go run ./cmd/migrate` applies schema changes explicitly. The API does not migrate tables at startup.
The runner uses a transaction, a PostgreSQL advisory lock, and a checksum recorded in `shiorin_migrations`.
Repeated runs are safe; a changed already-applied migration is rejected. The checksum normalizes Windows line endings.

The initial migration expects a fresh database. If an earlier draft of `db/schema.sql` was applied manually,
do not drop existing data: prepare an explicit upgrade/import migration, including ownership for old bookmarks.
Once migration 1 has been applied, preserve it and add future schema changes as new migration versions.

## Current scope and next steps

Accounts, sessions, and bookmarks persist in PostgreSQL by default. The HTTP server listens on loopback by default.
Before hosting it for other users, configure HTTPS and sustained authentication rate limiting.
The concurrency limit bounds simultaneous password hashing; it is not a login-attempt rate limit.
Account verification and password recovery are not implemented.

1. Add a browser UI for login and session management.
2. Add bookmark updates, deletion, favorites, and saved searches.
3. Evaluate search quality and index performance using real data.

Cookie authentication, JWTs, automatic token refresh, and device fingerprinting are not implemented.
Every repository query must continue to filter by the authenticated user before counting or paging.
The module path in `go.mod` matches the existing Git remote.
