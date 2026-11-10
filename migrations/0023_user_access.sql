-- 0023_user_access: roles with a service scope (S31, ADR-0014).
-- A role decides what a user may do; a scope decides on which services.
-- The scope is either every service (all_services = 1, which also
-- covers services created later and the host-level screens) or the
-- rows of user_services.
--
-- SQLite cannot alter a CHECK, so users is rebuilt to admit the
-- reserved 'developer' role. ORDER MATTERS: the rebuild comes before
-- user_services exists. Migrations run in one transaction with foreign
-- keys on, and DROP TABLE performs an implicit DELETE that fires
-- ON DELETE CASCADE, so rebuilding users after the child table exists
-- would silently wipe every grant. Any later rebuild of users must copy
-- user_services aside first.
CREATE TABLE users_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('admin', 'developer', 'viewer')),
  all_services INTEGER NOT NULL DEFAULT 1 CHECK (all_services IN (0, 1)),
  must_change_password INTEGER NOT NULL DEFAULT 0,
  disabled_at TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  -- An admin is never scoped: every power an admin holds is fleet-wide.
  CHECK (role <> 'admin' OR all_services = 1)
);
-- Every existing account keeps its role and gets the all-services
-- scope, so it behaves exactly as before.
INSERT INTO users_new (id, username, password_hash, role, all_services, must_change_password, disabled_at, created_at)
  SELECT id, username, password_hash, role, 1, must_change_password, disabled_at, created_at FROM users;
-- Keep the id high-water mark so a deleted user's id is never reused.
UPDATE sqlite_sequence
  SET seq = (SELECT seq FROM sqlite_sequence WHERE name = 'users')
  WHERE name = 'users_new'
    AND EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'users');
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;
-- One row per granted service. Deleting a user drops their grants;
-- deleting a service clears them in ServiceStore.Delete, so a service
-- re-created under the same id never inherits old grants.
CREATE TABLE user_services (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  service_id TEXT NOT NULL REFERENCES services(id),
  granted_by TEXT NOT NULL,
  granted_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  PRIMARY KEY (user_id, service_id)
);
CREATE INDEX idx_user_services_service ON user_services (service_id);
