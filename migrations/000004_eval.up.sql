CREATE TABLE eval_datasets (
    id text PRIMARY KEY,
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name text NOT NULL,
    target_kind text NOT NULL CHECK (target_kind IN ('agent')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE eval_cases (
    id text PRIMARY KEY,
    dataset_id text NOT NULL REFERENCES eval_datasets(id) ON DELETE CASCADE,
    input text NOT NULL,
    expected text NOT NULL,
    metadata text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX eval_cases_dataset_idx ON eval_cases (dataset_id, created_at, id);

CREATE TABLE eval_runs (
    id text PRIMARY KEY,
    dataset_id text NOT NULL REFERENCES eval_datasets(id) ON DELETE CASCADE,
    agent_id text NOT NULL,
    agent_version_id text NOT NULL,
    judge_kind text NOT NULL,
    threshold double precision NOT NULL CHECK (threshold >= 0 AND threshold <= 1),
    pass_rate double precision NOT NULL CHECK (pass_rate >= 0 AND pass_rate <= 1),
    passed boolean NOT NULL,
    report jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX eval_runs_dataset_idx ON eval_runs (dataset_id, created_at DESC, id DESC);
