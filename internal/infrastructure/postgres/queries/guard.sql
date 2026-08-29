-- 人在环审批。

-- name: CreateApproval :one
INSERT INTO approvals (id, workspace_id, conversation_id, action, payload, status, created_at)
VALUES ($1, $2, $3, $4, $5, 'pending', now())
RETURNING *;

-- name: GetApproval :one
SELECT * FROM approvals WHERE id = $1 LIMIT 1;

-- name: ResolvePendingApproval :one
WITH resolved AS (
    UPDATE approvals
    SET status = sqlc.arg(target_status), approver_id = sqlc.arg(actor_id), resolved_at = now()
    WHERE approvals.id = sqlc.arg(approval_id)
      AND approvals.workspace_id = sqlc.arg(target_workspace_id)
      AND status = 'pending'
    RETURNING *
), released AS (
    UPDATE conversations
    SET status = 'active', updated_at = now()
    WHERE id = (SELECT conversation_id FROM resolved)
      AND (SELECT status FROM resolved) = 'rejected'
      AND status = 'awaiting_approval'
)
SELECT approvals.*
FROM approvals
JOIN resolved ON resolved.id = approvals.id;

-- name: ListPendingApprovals :many
SELECT * FROM approvals
WHERE workspace_id = $1 AND status = 'pending'
ORDER BY created_at DESC;

-- name: ListApprovalsByConversation :many
SELECT * FROM approvals WHERE conversation_id = $1 ORDER BY created_at;

-- name: BeginApprovalExecution :one
UPDATE approvals
SET execution_status = 'executing', execution_started_at = now(), execution_completed_at = NULL,
    execution_error = '', execution_lease_until = now() + interval '2 minutes',
    execution_attempts = execution_attempts + 1, execution_token = sqlc.arg(execution_token)
WHERE id = sqlc.arg(id)
  AND conversation_id = sqlc.arg(conversation_id)
  AND status = 'approved'
  AND execution_attempts < 5
  AND (
      execution_status = 'not_started'
      OR execution_status = 'failed'
      OR (execution_status = 'executing' AND execution_lease_until < now())
  )
RETURNING *;

-- name: RenewApprovalExecution :execrows
UPDATE approvals
SET execution_lease_until = now() + interval '2 minutes'
WHERE id = sqlc.arg(id)
  AND execution_status = 'executing'
  AND execution_token = sqlc.arg(execution_token);

-- name: CompleteApprovalExecution :execrows
UPDATE approvals
SET execution_status = 'completed', execution_completed_at = now(), execution_error = '',
    execution_lease_until = NULL, execution_token = NULL
WHERE id = sqlc.arg(id) AND execution_status = 'executing'
  AND execution_token = sqlc.arg(execution_token);

-- name: FailApprovalExecution :execrows
UPDATE approvals
SET execution_status = 'failed', execution_completed_at = now(), execution_error = sqlc.arg(execution_error),
    execution_lease_until = NULL, execution_token = NULL
WHERE id = sqlc.arg(id) AND execution_status = 'executing'
  AND execution_token = sqlc.arg(execution_token);

-- name: ListReadyApprovalResumes :many
SELECT * FROM approvals
WHERE status = 'approved'
  AND execution_attempts < 5
  AND (
      execution_status = 'not_started'
      OR execution_status = 'failed'
      OR (execution_status = 'executing' AND execution_lease_until < now())
  )
ORDER BY resolved_at ASC
LIMIT $1;
