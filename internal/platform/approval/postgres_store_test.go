//go:build integration

package approval_test

// PG 版 Approval Store 契约测试。需 Docker(或 KBOT_TEST_DATABASE_URL)。

import (
	"context"
	"testing"

	"github.com/google/uuid"

	pgstore "github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres/sqlc"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres/testpg"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/approval"
)

func TestPostgresApprovalStore_Contract(t *testing.T) {
	pool := testpg.Start(t)
	runApprovalContract(t, func(t *testing.T) (approval.Store, string, string) {
		ctx := context.Background()
		if _, err := pool.Exec(ctx, `TRUNCATE approvals, checkpoints, conversations CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		// checkpoints.conversation_id 外键 → 先建一个会话。
		workspaceID := "approval-contract"
		if _, err := pool.Exec(ctx, `INSERT INTO workspaces (id, name) VALUES ($1, 'Approval Contract') ON CONFLICT (id) DO NOTHING`, workspaceID); err != nil {
			t.Fatalf("insert workspace: %v", err)
		}
		convID := uuid.New()
		if _, err := pool.Exec(ctx,
			`INSERT INTO conversations (id, workspace_id, status) VALUES ($1, $2, 'active')`, convID, workspaceID); err != nil {
			t.Fatalf("insert conversation: %v", err)
		}
		return approval.NewPostgresStore(pgstore.New(pool)), convID.String(), workspaceID
	})
}

func TestRejectReleasesAwaitingConversation(t *testing.T) {
	pool := testpg.Start(t)
	ctx := context.Background()
	workspaceID := "approval-reject"
	_, _ = pool.Exec(ctx, `INSERT INTO workspaces (id,name) VALUES ($1,'Approval Reject') ON CONFLICT (id) DO NOTHING`, workspaceID)
	convID, approvalID := uuid.New(), uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO conversations (id,workspace_id,status) VALUES ($1,$2,'awaiting_approval')`, convID, workspaceID)
	if err == nil {
		_, err = pool.Exec(ctx, `INSERT INTO approvals (id,workspace_id,conversation_id,action,payload,status)
			VALUES ($1,$2,$3,'update_order','{}','pending')`, approvalID, workspaceID, convID)
	}
	if err != nil {
		t.Fatal(err)
	}
	store := approval.NewPostgresStore(pgstore.New(pool))
	if _, err := store.ResolvePending(ctx, approvalID.String(), workspaceID, approval.StatusRejected, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM conversations WHERE id=$1`, convID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		t.Fatalf("conversation status = %q, want active", status)
	}
}
