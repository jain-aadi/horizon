-- Migration 001: Create agents table
CREATE TABLE IF NOT EXISTS agents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT,
    goal TEXT NOT NULL,
    instructions TEXT NOT NULL,
    runtime_config TEXT NOT NULL DEFAULT '{}',
    model_config TEXT NOT NULL DEFAULT '{}',
    capabilities TEXT NOT NULL DEFAULT '[]',
    permissions TEXT NOT NULL DEFAULT '{}',
    schedule TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    version INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_agents_created_at ON agents(created_at DESC);