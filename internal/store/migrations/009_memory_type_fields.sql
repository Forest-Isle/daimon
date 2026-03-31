-- Add type, emotion, and sensitivity fields to memory_index
ALTER TABLE memory_index ADD COLUMN memory_type TEXT NOT NULL DEFAULT 'semantic';
ALTER TABLE memory_index ADD COLUMN emotion TEXT NOT NULL DEFAULT 'neutral';
ALTER TABLE memory_index ADD COLUMN sensitivity TEXT NOT NULL DEFAULT 'public';
