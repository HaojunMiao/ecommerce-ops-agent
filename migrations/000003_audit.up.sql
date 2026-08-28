CREATE TABLE audit_events (
    id text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    actor_id text NOT NULL,
    action text NOT NULL,
    resource_id text NOT NULL,
    data jsonb NOT NULL DEFAULT '{}'::jsonb,
    previous_hash text NOT NULL DEFAULT '',
    hash text NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (workspace_id, hash)
);

CREATE INDEX audit_events_chain_idx ON audit_events (workspace_id, created_at, id);
