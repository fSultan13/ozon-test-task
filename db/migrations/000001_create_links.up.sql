CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE links (
    original_url text COLLATE "C" NOT NULL CHECK (octet_length(original_url) BETWEEN 1 AND 8000),
    code text COLLATE "C" PRIMARY KEY CHECK (code ~ '^[a-zA-Z0-9_]{1,10}$')
);

-- A full URL can exceed the B-tree index tuple limit. Hashing bounds the key
-- size; the adapter checks the exact URL before returning an existing mapping.
CREATE UNIQUE INDEX links_original_sha256_key ON links (digest(original_url, 'sha256'));
CREATE INDEX links_original_url_hash_idx ON links USING hash (original_url);
CREATE INDEX links_code_hash_idx ON links USING hash (code);
