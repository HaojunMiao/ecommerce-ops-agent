CREATE TABLE approval_requests (
    id text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    run_id text NOT NULL,
    tool_call_id text NOT NULL,
    tool_version_id text NOT NULL,
    arguments jsonb NOT NULL,
    arguments_hash bytea NOT NULL,
    checkpoint bytea NOT NULL,
    status text NOT NULL CHECK (status IN ('pending','approved','rejected','executing','completed','failed')),
    decided_by text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    lease_owner text NOT NULL DEFAULT '',
    lease_until timestamptz,
    fencing_token bigint NOT NULL DEFAULT 0,
    attempts integer NOT NULL DEFAULT 0,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, run_id, tool_call_id)
);

CREATE INDEX approval_ready_idx ON approval_requests (status, lease_until, created_at);
