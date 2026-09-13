-- Planned PostgreSQL schema. The current server uses memory storage.
-- This file is not applied automatically.
CREATE TABLE bookmarks (
    id text PRIMARY KEY,
    title text NOT NULL CHECK (length(trim(title)) > 0),
    url text NOT NULL,
    note text NOT NULL DEFAULT '',
    tags text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX bookmarks_created_at_idx ON bookmarks (created_at DESC, id DESC);
CREATE INDEX bookmarks_tags_idx ON bookmarks USING gin (tags);
