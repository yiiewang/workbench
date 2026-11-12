-- 000008_user_email_notify.down.sql
DROP TABLE IF EXISTS notify_log;
ALTER TABLE users DROP COLUMN email;
