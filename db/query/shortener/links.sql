-- name: FindByOriginal :one
SELECT original_url, code FROM links WHERE original_url = $1;

-- name: FindByCode :one
SELECT original_url, code FROM links WHERE code = $1;

-- name: InsertLink :one
INSERT INTO links (original_url, code) VALUES ($1, $2)
ON CONFLICT DO NOTHING
RETURNING original_url, code;

-- name: FindByOriginalHash :one
SELECT original_url, code FROM links
WHERE digest(original_url, 'sha256') = digest(sqlc.arg(original_url)::text, 'sha256');
