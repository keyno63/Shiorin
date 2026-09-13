-- Migration 1: run through cmd/migrate. Once applied, add a new migration
-- rather than editing this file; the migration runner verifies its checksum.
CREATE TABLE users (
    id text PRIMARY KEY,
    username text NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9_-]{3,32}$'),
    password_salt bytea NOT NULL CHECK (octet_length(password_salt) = 16),
    password_hash bytea NOT NULL CHECK (octet_length(password_hash) = 32),
    password_algorithm text NOT NULL DEFAULT 'argon2id',
    password_version integer NOT NULL DEFAULT 19,
    password_memory_kib integer NOT NULL DEFAULT 19456,
    password_iterations integer NOT NULL DEFAULT 2,
    password_parallelism integer NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id text PRIMARY KEY,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    device_label text NOT NULL DEFAULT '' CHECK (octet_length(device_label)<=100),
    user_agent text NOT NULL DEFAULT '' CHECK (octet_length(user_agent)<=512),
    revoked_at timestamptz,
    CHECK (expires_at>created_at)
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);
CREATE INDEX sessions_revoked_at_idx ON sessions (revoked_at) WHERE revoked_at IS NOT NULL;

CREATE TABLE bookmarks (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title text NOT NULL CHECK (length(trim(title)) > 0),
    url text NOT NULL,
    note text NOT NULL DEFAULT '',
    tags text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX bookmarks_user_created_at_idx ON bookmarks (user_id, created_at DESC, id DESC);
CREATE INDEX bookmarks_tags_idx ON bookmarks USING gin (tags);

-- Repository queries MUST scope by the authenticated user before counting/paging:
-- SELECT ... FROM bookmarks WHERE user_id = $1 AND ...;
-- Existing installations need a migration and an explicit owner for old rows;
-- do not assign existing bookmarks to an arbitrary or shared default user.
