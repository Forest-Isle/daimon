-- 013_cleanup_legacy.sql: Remove legacy tables from V1/V2/V2.5 memory systems
-- and rename fact_access_* tables to memory_access_* for naming consistency.

-- V1: memories table (001_init.sql, no Go code references)
DROP TABLE IF EXISTS memories;

-- V2: cognitive_cycles (002_cognitive.sql, no Go code references)
DROP TABLE IF EXISTS cognitive_cycles;

-- V2: memory_facts + FTS (003_memory_v2.sql, replaced by file-based storage)
DROP TABLE IF EXISTS memory_facts_fts;
DROP TABLE IF EXISTS memory_facts;

-- V2.5: fact_embeddings + FTS (006_file_memory.sql, replaced by memory_index)
DROP TABLE IF EXISTS fact_embeddings_fts;
DROP TABLE IF EXISTS fact_embeddings;

-- agent_traces (006_agent_traces.sql, no Go code references)
DROP TABLE IF EXISTS agent_traces;

-- Rename fact_access_* to memory_access_* for naming consistency.
-- Both fresh and existing databases have these tables from 008_access_log.sql.
ALTER TABLE fact_access_log RENAME TO memory_access_log;
ALTER TABLE fact_access_stats RENAME TO memory_access_stats;
