ALTER TABLE users RENAME COLUMN username TO email;
DROP INDEX IF EXISTS idx_users_username;
CREATE INDEX IF NOT EXISTS idx_users_email on users(email);
