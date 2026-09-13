# Shiorin

A Go starter project for an API that stores and searches technical article bookmarks.

Requires Go 1.27.1 or later, as specified in `go.mod`. Uses only the standard library, with no third-party dependencies.

## Run

```powershell
go run ./cmd/api
```

The server listens on http://127.0.0.1:8080 by default. Press Ctrl+C to stop it.
To use a different address, set the environment variable before starting the server:

```powershell
$env:HTTP_ADDR = "127.0.0.1:9090"
go run ./cmd/api
```

`.env.example` provides an example configuration. The application does not load `.env` files automatically.

## Try the API

Run these commands in a separate PowerShell session while the server is running at its default address:

```powershell
Invoke-RestMethod http://127.0.0.1:8080/healthz

$body = @{
    title = "Building a search API in Go"
    url = "https://example.com/go-search"
    note = "Implementation notes on database search"
    tags = @("Go", "database")
} | ConvertTo-Json
Invoke-RestMethod -Method Post -Uri http://127.0.0.1:8080/bookmarks -ContentType "application/json; charset=utf-8" -Body ([Text.Encoding]::UTF8.GetBytes($body))

Invoke-RestMethod "http://127.0.0.1:8080/bookmarks?q=go&tag=database&limit=20&offset=0"
```

| Method | Path | Description |
|---|---|---|
| GET | /healthz | Check whether the API process is running |
| POST | /bookmarks | Create a bookmark with title, url, note, and tags |
| GET | /bookmarks | Search using q, tag, limit, and offset |

`q` performs a case-insensitive substring search across the title, URL, and note.
`tag` performs an exact match after trimming surrounding whitespace and normalizing case.
When both `q` and `tag` are provided, a bookmark must match both conditions.
Results are returned in reverse insertion order, with `items`, `total`, `limit`, and `offset` fields.

`limit` accepts values from 1 to 100 (default: 20), and `offset` accepts values from 0 to 1,000,000 (default: 0).
POST request bodies are limited to 1 MiB. Only HTTP and HTTPS URLs are accepted.
The application does not visit submitted URLs or fetch article content.

## Project structure

- `cmd/api`: Server startup, timeouts, and graceful shutdown
- `internal/httpapi`: HTTP handlers, input validation, and API tests
- `internal/bookmark`: Data types, repository interface, and concurrency-safe in-memory storage
- `db/schema.sql`: Table definitions for a future PostgreSQL implementation
- `.github/workflows/ci.yml`: Formatting checks, vet, race detection tests, and builds

## Validation

```powershell
go test ./...
go vet ./...
go build -o bin/shiorin.exe ./cmd/api
```

GitHub Actions also runs `go test -race ./...` on Linux.

## Current scope and next steps

Storage is currently in memory, so all data is lost when the server restarts.
PostgreSQL connectivity, authentication, updates, and deletion are not yet implemented.
This starter is intended for local development and listens only on the loopback address by default.

1. Add a PostgreSQL repository that implements `internal/bookmark.Repository`.
2. Add connection settings and migrations, and select the repository at startup.
3. Add updates, deletion, favorites, and saved searches.
4. Evaluate search quality and index performance using real data.

`db/schema.sql` is not applied automatically. Full-text search indexes will be added after the search approach is chosen.
The module path in `go.mod` matches the existing Git remote.
