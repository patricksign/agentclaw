-- 002_add_task_priority.sql
-- Add priority and dependency tracking to tasks

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS priority INT DEFAULT 50;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS depends_on TEXT[];
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS assigned_to TEXT;

CREATE INDEX IF NOT EXISTS idx_tasks_priority ON tasks(priority DESC);
