-- 000011_guard:敏感工具人工审批持久化。

CREATE TABLE approvals (
    id              UUID PRIMARY KEY,
    conversation_id UUID,
    action          TEXT NOT NULL DEFAULT '',
    payload         JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'pending',       -- pending / approved / rejected
    approver_id     UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at     TIMESTAMPTZ
);
CREATE INDEX approvals_status ON approvals (status, created_at DESC);
