-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1 LIMIT 1;

-- name: VerifyUser :one
UPDATE users SET is_verified = $1 WHERE id = $2 RETURNING is_verified;

-- name: CreateUser :one
INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id;

-- name: GetUnverifiedUsers :many
SELECT * FROM users WHERE is_verified = false OR is_verified IS NULL ORDER BY id;
