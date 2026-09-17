# Shiorin

Shiorin is a self-hosted bookmark application written in Go, with a simple HTML interface and a JSON API. It stores technical articles in PostgreSQL and provides bearer-token authentication, per-user data isolation, tags, and text search.

## Features

- Browser UI for account registration, login, bookmark creation, and search
- Account registration with Argon2id password hashing
- Independent, revocable login sessions
- Private bookmarks scoped to the authenticated user
- Text search, exact tag filtering, and pagination
- PostgreSQL persistence with explicit transactional migrations
- Optional in-memory storage for temporary development

## Requirements

- Go 1.27.1 or later
- Docker-compatible container runtime for the included PostgreSQL 18 database, or an existing PostgreSQL server

Go downloads module dependencies on the first run. Start Docker Desktop, Rancher Desktop, or another compatible runtime before using the included Compose configuration.

## Quick start

Start PostgreSQL, apply the schema, and run the API.

### PowerShell

```powershell
docker compose up -d --wait db
$env:DATABASE_URL = "postgres://shiorin:shiorin_dev@127.0.0.1:54329/shiorin?sslmode=disable"
go run ./cmd/migrate
go run ./cmd/api
```

### macOS and Linux

```bash
docker compose up -d --wait db
export DATABASE_URL="postgres://shiorin:shiorin_dev@127.0.0.1:54329/shiorin?sslmode=disable"
go run ./cmd/migrate
go run ./cmd/api
```

The server listens on <http://127.0.0.1:8080> by default. Check that it is running:

```console
$ curl http://127.0.0.1:8080/healthz
{"status":"ok"}
```

The Compose credentials are for local development only. PostgreSQL data is stored in a named volume and survives container and API restarts.

## Browser interface

Open <http://127.0.0.1:8080/> after starting the server. Create an account, log in,
and save bookmarks with optional notes and comma-separated tags. Search by text
or an exact tag, and use Previous / Next to browse results. Saving a bookmark
clears the search filters so you can see the latest entries.

The UI uses plain HTML, CSS, and JavaScript embedded in the Go binary. No frontend
build tools or separate web server are required. Rebuild or restart `go run` after
editing files in `internal/httpapi/web`.

The bearer token is stored in browser `sessionStorage`, keeping login across
reloads in the same tab. Browser session restoration or tab duplication may retain
or copy that storage; use **Log out** to revoke the server session explicitly.
If browser storage is unavailable, login lasts until the page is reloaded.
Tokens remain accessible to same-origin JavaScript. The UI uses a restrictive
Content Security Policy, no third-party scripts, and text-only rendering of saved
content. Passwords are not stored in browser storage. Expired or revoked sessions
return the UI to the login form on the next authenticated request.

For a temporary demo without PostgreSQL, run:

```powershell
$env:STORAGE = "memory"
go run ./cmd/api
```

In-memory accounts and bookmarks are lost when the server stops. Unset `STORAGE`
or set it to `postgres` to return to persistent storage.

## Try the API

This PowerShell example registers a user, logs in, creates and searches for a bookmark, and logs out. Run it in a separate terminal while the API is running.

```powershell
$credentials = @{
    username = "alice"
    password = "replace-with-your-own-long-passphrase"
} | ConvertTo-Json

Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/auth/register -ContentType "application/json" -Body $credentials
$login = Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/auth/login -ContentType "application/json" -Body $credentials
$headers = @{ Authorization = "Bearer $($login.access_token)" }

$bookmark = @{
    title = "Building a search API in Go"
    url = "https://example.com/go-search"
    note = "Implementation notes on database search"
    tags = @("Go", "database")
} | ConvertTo-Json

Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/bookmarks -Headers $headers -ContentType "application/json; charset=utf-8" -Body ([Text.Encoding]::UTF8.GetBytes($bookmark))
Invoke-RestMethod "http://127.0.0.1:8080/bookmarks?q=go&tag=database&limit=20&offset=0" -Headers $headers
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/auth/logout -Headers $headers
```

## API overview

All API endpoints except `/healthz`, `/auth/register`, and `/auth/login` require an `Authorization: Bearer <access_token>` header. The browser page at `/` and its CSS/JavaScript assets are public; bookmark data still requires authentication.

| Method | Path | Auth | Success | Description |
|---|---|:---:|:---:|---|
| GET | `/healthz` | No | 200 | Check whether the API process is running |
| POST | `/auth/register` | No | 201 | Register a username and password |
| POST | `/auth/login` | No | 200 | Exchange credentials for a bearer token |
| POST | `/auth/logout` | Yes | 204 | Revoke the current bearer token |
| GET | `/me` | Yes | 200 | Return the authenticated user |
| GET | `/me/sessions` | Yes | 200 | List the current user's active sessions |
| PATCH | `/me/sessions/{id}` | Yes | 204 | Rename an owned session using `device_label` |
| DELETE | `/me/sessions/{id}` | Yes | 204 | Revoke an owned session |
| POST | `/me/sessions/revoke-others` | Yes | 204 | Revoke every session except the current one |
| POST | `/bookmarks` | Yes | 201 | Create a bookmark |
| GET | `/bookmarks` | Yes | 200 | Search the current user's bookmarks |

### Search

`GET /bookmarks` accepts these query parameters:

| Parameter | Behavior |
|---|---|
| `q` | Case-insensitive substring match across title, URL, and note |
| `tag` | Exact match after trimming whitespace and normalizing case |
| `limit` | Page size from 1 to 100; defaults to 20 |
| `offset` | Number of records to skip, from 0 to 1,000,000; defaults to 0 |

When both `q` and `tag` are present, a bookmark must match both. Responses contain `items`, `total`, `limit`, and `offset`. PostgreSQL results are ordered by creation time descending, then ID descending. Substring matching currently scans within the owner's records; a dedicated search index is a future enhancement.

Only HTTP and HTTPS bookmark URLs are accepted. The application does not visit submitted URLs or fetch article content. POST request bodies are limited to 1 MiB.

## Configuration

The application reads configuration from environment variables. `.env.example` contains example values, but `.env` files are not loaded automatically.

| Variable | Default | Description |
|---|---|---|
| `HTTP_ADDR` | `127.0.0.1:8080` | HTTP listen address |
| `STORAGE` | `postgres` | Storage backend: `postgres` or `memory` |
| `DATABASE_URL` | None | PostgreSQL connection string; required for PostgreSQL storage |
| `TEST_DATABASE_URL` | None | Enables PostgreSQL integration tests |

To use a different listen address:

```powershell
$env:HTTP_ADDR = "127.0.0.1:9090"
go run ./cmd/api
```

To run an explicitly temporary instance without PostgreSQL:

```powershell
$env:STORAGE = "memory"
go run ./cmd/api
```

Memory mode loses all accounts, sessions, and bookmarks on restart. A missing PostgreSQL connection string or schema causes startup to fail; Shiorin never silently falls back to memory after a database error. Multiple API instances must share the same database to share authentication and data.

For an existing PostgreSQL server, set `DATABASE_URL` to a fresh database and run `go run ./cmd/migrate`. Use TLS when connecting to a remote database.

## Accounts and sessions

Usernames are case-insensitive, converted to lowercase, and stripped of surrounding whitespace. They must contain 3–32 ASCII letters, digits, underscores, or hyphens. Duplicate usernames return 409.

Passwords are not trimmed or normalized. They must contain at least 15 characters and no more than 1,024 UTF-8 bytes. Registration does not log the user in. Login returns `user`, `access_token`, `token_type`, `expires_at`, and `session_id`.

Each login creates an independent session with a fixed 24-hour lifetime. Login optionally accepts a `device_label` of up to 100 UTF-8 bytes. The server also records up to 512 UTF-8 bytes from the client-provided `User-Agent` header. These values are descriptive only and must be escaped if rendered in a UI.

The session list returns `id`, `created_at`, `expires_at`, `last_seen_at`, `device_label`, `user_agent`, and `current`; it never exposes access tokens or token digests. `last_seen_at` updates at most once every five minutes and does not extend expiry. Inactive session records are removed seven days after expiry or revocation.

Only the authorization header is accepted for tokens—not query parameters or cookies. Logout revokes only the supplied token. Missing, invalid, expired, or revoked tokens return 401. Other users' sessions cannot be listed, renamed, or revoked; an inaccessible or inactive session ID returns 404.

## Security and ownership

- Every bookmark is assigned to the authenticated user by the server. Clients cannot set or query `user_id`.
- Search filters, totals, and pagination apply only to the current user's records. There is no public or cross-user search.
- Passwords use independently salted Argon2id hashes with 19 MiB of memory, two passes, and one lane.
- Session tokens contain 32 random bytes and are stored only as SHA-256 digests.
- Account and authenticated responses include `Cache-Control: no-store`.
- At most two password hashes run concurrently. Excess authentication work returns 429 with `Retry-After`.
- PostgreSQL is checked on every authenticated request. Database failures fail closed with 500.
- Revocation is shared across API instances after its transaction commits. Requests already authenticated may still finish.

The password hashing approach follows the [Go Argon2 documentation](https://pkg.go.dev/golang.org/x/crypto/argon2) and [OWASP password storage guidance](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).

## Development

Run the standard checks:

```console
go test ./...
go vet ./...
go build ./cmd/api
```

To include PostgreSQL integration tests:

### PowerShell

```powershell
$env:TEST_DATABASE_URL = "postgres://shiorin:shiorin_dev@127.0.0.1:54329/shiorin?sslmode=disable"
go test -count=1 ./...
```

### macOS and Linux

```bash
TEST_DATABASE_URL="postgres://shiorin:shiorin_dev@127.0.0.1:54329/shiorin?sslmode=disable" go test -count=1 ./...
```

The integration test creates and drops only a randomly named test schema, so the database user needs permission to create schemas. It verifies persistence, multi-instance revocation, ownership, pagination, expiry, cleanup, and concurrent migrations. Without `TEST_DATABASE_URL`, it is skipped. CI runs formatting checks, vet, builds, and `go test -race ./...` with PostgreSQL on Linux.

## Database migrations

`go run ./cmd/migrate` applies schema changes explicitly; the API never migrates tables at startup. The runner uses a transaction, a PostgreSQL advisory lock, and checksums stored in `shiorin_migrations`. Repeated runs are safe, while changes to an applied migration are rejected. Checksums normalize Windows line endings.

The initial migration expects a fresh database. If an earlier draft of `db/schema.sql` was applied manually, preserve the data and prepare an explicit upgrade or import migration, including ownership for existing bookmarks. Once migration 1 is applied, do not modify it; add subsequent changes as new migration versions.

## Project structure

- `cmd/api`: server startup, timeouts, and graceful shutdown
- `cmd/migrate`: explicit schema migration command
- `internal/httpapi`: HTTP handlers, validation, and API tests
- `internal/auth`: registration, password hashing, sessions, and repository contract
- `internal/bookmark`: bookmark types, repository interface, and in-memory storage
- `internal/postgres`: persistent repositories and database integration tests
- `db/schema.sql`: initial managed schema embedded in the migration runner
- `compose.yaml`: local PostgreSQL instance with persistent storage
- `.github/workflows/ci.yml`: formatting, vet, race tests, and builds

## Production considerations

The default configuration listens on loopback and is intended for development. Before exposing Shiorin to other users:

- terminate connections with HTTPS;
- add sustained authentication attempt rate limiting—the hash concurrency limit is not a login rate limit;
- keep clocks synchronized across API hosts;
- plan account verification and password recovery if required.

Cookie authentication, JWTs, automatic token refresh, and device fingerprinting are not implemented.

## Roadmap

1. Add device session management to the browser UI.
2. Add bookmark updates, deletion, favorites, and saved searches.
3. Evaluate search quality and index performance using real data.

## LICENSE

This application is licensed under the Elastic License 2.0 (ELv2).
See [LICENSE](LICENSE) for details.
