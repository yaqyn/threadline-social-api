-- name: CreateChirp :one
INSERT INTO chirps (id, created_at, updated_at, body, user_id)
VALUES (gen_random_uuid(), NOW(), NOW(), $1, $2)
RETURNING *;

-- name: GetChirps :many
SELECT * FROM chirps
ORDER BY created_at ASC;

-- name: GetChirp :one
SELECT * FROM chirps
WHERE id = $1;

-- name: DeleteChirp :execrows
DELETE FROM chirps
WHERE id = $1 AND user_id = $2;

-- name: GetChirpsByAuthor :many
SELECT * FROM chirps
WHERE user_id = $1
ORDER BY created_at ASC;

-- name: GetChirpsPage :many
SELECT * FROM chirps
WHERE (sqlc.narg(author_id)::uuid IS NULL OR user_id = sqlc.narg(author_id))
  AND (sqlc.arg(search)::text = '' OR body ILIKE '%' || sqlc.arg(search)::text || '%')
ORDER BY
    CASE WHEN sqlc.arg(descending)::boolean THEN created_at END DESC,
    CASE WHEN NOT sqlc.arg(descending)::boolean THEN created_at END ASC,
    id ASC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);
