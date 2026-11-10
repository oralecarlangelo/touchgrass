-- 0022_user_must_change_password: a one-time password must be replaced
-- before the account can do anything else. Set on the bootstrap admin
-- when its password was generated on first boot (no
-- TOUCHGRASS_ADMIN_PASSWORD) and by `touchgrass users reset-password`;
-- cleared when the user changes their own password or an admin resets it.
ALTER TABLE users ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0;
