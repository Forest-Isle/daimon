-- 025_scheduled_tasks.sql: Add last_status column to scheduled_tasks (table created in 001_init.sql)
ALTER TABLE scheduled_tasks ADD COLUMN last_status TEXT;
