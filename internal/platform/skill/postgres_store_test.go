//go:build integration

package skill_test

// PG 版 Skill Store 契约测试。需 Docker(或 KBOT_TEST_DATABASE_URL)。

import (
	"context"
	"testing"

	pgstore "github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres/sqlc"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres/testpg"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/skill"
)

func TestPostgresSkillStore_Contract(t *testing.T) {
	pool := testpg.Start(t)
	runSkillStoreContract(t, func(t *testing.T) skill.Store {
		if _, err := pool.Exec(context.Background(),
			`TRUNCATE skills, skill_versions, skill_subscriptions CASCADE`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		return skill.NewPostgresStore(pgstore.New(pool))
	})
}
