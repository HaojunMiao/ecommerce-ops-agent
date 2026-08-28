CREATE TABLE integration_replays (
    replay_key text PRIMARY KEY,
    expires_at timestamptz NOT NULL
);

CREATE INDEX integration_replays_expiry_idx ON integration_replays (expires_at);

CREATE TABLE teams (
    id text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name text NOT NULL,
    mode text NOT NULL CHECK (mode IN ('pipeline','supervisor')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, workspace_id)
);

CREATE TABLE team_versions (
    id text PRIMARY KEY,
    team_id text NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    version integer NOT NULL CHECK (version > 0),
    members jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (team_id, version),
    UNIQUE (id, team_id)
);

CREATE TABLE team_promotions (
    team_id text NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    environment text NOT NULL CHECK (environment IN ('dev','staging','prod')),
    team_version_id text NOT NULL,
    promoted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, environment),
    FOREIGN KEY (team_version_id, team_id) REFERENCES team_versions(id, team_id)
);
