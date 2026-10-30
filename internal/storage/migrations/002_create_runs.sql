-- Migration 002: Create runs table
CREATE TABLE IF NOT EXISTS runs (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    agent_version INTEGER NOT NULL,
    trigger_type TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at DATETIME NOT NULL,
    completed_at DATETIME,
    exit_code INTEGER,
    output_ref TEXT,
    error_message TEXT,
    container_id TEXT,
    cancel_reason TEXT,
    FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_runs_agent_id ON runs(agent_id);
CREATE INDEX IF NOT EXISTS idx_runs_started_at ON runs(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status);