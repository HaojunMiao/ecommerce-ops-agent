package prompt

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/domain"
	pgstore "github.com/HaojunMiao/ecommerce-ops-agent/internal/infrastructure/postgres/sqlc"
)

// PostgresStore 用 sqlc 实现 prompt.Store。所有 ID 列是 UUID,uuid.Parse 接受
// 32-hex 与 canonical 两种形式并归一,故无需回写 canonical ID。
type PostgresStore struct {
	db *pgxpool.Pool
	q  *pgstore.Queries
}

func NewPostgresStore(db *pgxpool.Pool, q *pgstore.Queries) *PostgresStore {
	return &PostgresStore{db: db, q: q}
}

var _ Store = (*PostgresStore)(nil)

// ---- Prompt ----

func (s *PostgresStore) CreatePrompt(ctx context.Context, p *domain.Prompt) error {
	id, err := uuid.Parse(p.ID)
	if err != nil {
		return fmt.Errorf("parse prompt id: %w", err)
	}
	row, err := s.q.CreatePrompt(ctx, pgstore.CreatePromptParams{
		ID:          id,
		WorkspaceID: p.WorkspaceID,
		Name:        p.Name,
		Category:    p.Category,
		CreatedBy:   p.CreatedBy,
	})
	if err != nil {
		return fmt.Errorf("create prompt: %w", err)
	}
	// 写回 canonical ID + DB 时间戳,保证 Service 后续用 p.ID 与读回的 version.PromptID 同形(否则 Promote 的归属校验会因 32-hex vs canonical 失败)。
	*p = *promptFromRow(row)
	return nil
}

func (s *PostgresStore) DeletePrompt(ctx context.Context, promptID string) error {
	id, err := uuid.Parse(promptID)
	if err != nil {
		return fmt.Errorf("parse prompt id: %w", err)
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM prompts WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete prompt: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetPrompt(ctx context.Context, promptID string) (*domain.Prompt, error) {
	id, err := uuid.Parse(promptID)
	if err != nil {
		return nil, fmt.Errorf("parse prompt id: %w", err)
	}
	row, err := s.q.GetPrompt(ctx, id)
	if err != nil {
		return nil, notFound("prompt", err)
	}
	return promptFromRow(row), nil
}

func (s *PostgresStore) ListPrompts(ctx context.Context, workspaceID string) ([]*domain.Prompt, error) {
	rows, err := s.q.ListPrompts(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list prompts: %w", err)
	}
	out := make([]*domain.Prompt, 0, len(rows))
	for _, r := range rows {
		out = append(out, promptFromRow(r))
	}
	return out, nil
}

// ---- PromptVersion(immutable) ----

func (s *PostgresStore) CreatePromptVersion(ctx context.Context, v *domain.PromptVersion) error {
	// CreatePromptVersion 返回的基础表行不包含版本化模型配置。先保存调用方
	// 传入的配置，避免用数据库 canonical row 回填实体时把它们清空。
	modelConfigVersionID := v.ModelConfigVersionID
	generationConfig := v.GenerationConfig
	id, err := uuid.Parse(v.ID)
	if err != nil {
		return fmt.Errorf("parse version id: %w", err)
	}
	promptID, err := uuid.Parse(v.PromptID)
	if err != nil {
		return fmt.Errorf("parse prompt id: %w", err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin prompt version: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := s.q.WithTx(tx)
	row, err := q.CreatePromptVersion(ctx, pgstore.CreatePromptVersionParams{
		ID:              id,
		PromptID:        promptID,
		Version:         int32(v.Version),
		Template:        v.Template,
		VariablesSchema: v.VariablesSchema,
		Hash:            v.Hash,
		TokenEstimate:   int32(v.TokenEstimate),
		CreatedBy:       v.CreatedBy,
	})
	if err != nil {
		return fmt.Errorf("create prompt version: %w", err)
	}
	*v = *versionFromRow(row) // 写回 canonical ID/PromptID,与 p.ID 同形
	v.ModelConfigVersionID = modelConfigVersionID
	v.GenerationConfig = generationConfig
	if modelConfigVersionID != "" || !emptyGenerationConfig(generationConfig) {
		var configID any
		if modelConfigVersionID != "" {
			parsed, err := uuid.Parse(modelConfigVersionID)
			if err != nil {
				return fmt.Errorf("parse model config version id: %w", err)
			}
			configID = parsed
		}
		configJSON, err := json.Marshal(generationConfig)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO prompt_version_configs
			  (prompt_version_id,model_config_version_id,generation_config)
			VALUES ($1,$2,$3)`, row.ID, configID, configJSON); err != nil {
			return fmt.Errorf("create prompt version config: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit prompt version: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetPromptVersion(ctx context.Context, versionID string) (*domain.PromptVersion, error) {
	id, err := uuid.Parse(versionID)
	if err != nil {
		return nil, fmt.Errorf("parse version id: %w", err)
	}
	row, err := s.q.GetPromptVersion(ctx, id)
	if err != nil {
		return nil, notFound("prompt version", err)
	}
	v := versionFromRow(row)
	if err := s.loadVersionConfig(ctx, v); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *PostgresStore) GetPromptVersionByNumber(ctx context.Context, promptID string, version int) (*domain.PromptVersion, error) {
	id, err := uuid.Parse(promptID)
	if err != nil {
		return nil, fmt.Errorf("parse prompt id: %w", err)
	}
	row, err := s.q.GetPromptVersionByNumber(ctx, pgstore.GetPromptVersionByNumberParams{
		PromptID: id,
		Version:  int32(version),
	})
	if err != nil {
		return nil, notFound("prompt version", err)
	}
	v := versionFromRow(row)
	if err := s.loadVersionConfig(ctx, v); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *PostgresStore) ListPromptVersions(ctx context.Context, promptID string) ([]*domain.PromptVersion, error) {
	id, err := uuid.Parse(promptID)
	if err != nil {
		return nil, fmt.Errorf("parse prompt id: %w", err)
	}
	rows, err := s.q.ListPromptVersions(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list prompt versions: %w", err)
	}
	out := make([]*domain.PromptVersion, 0, len(rows))
	for _, r := range rows {
		v := versionFromRow(r)
		if err := s.loadVersionConfig(ctx, v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ---- Env 指针 ----

func (s *PostgresStore) SetEnvBinding(ctx context.Context, promptID, env, versionID string) error {
	pid, err := uuid.Parse(promptID)
	if err != nil {
		return fmt.Errorf("parse prompt id: %w", err)
	}
	vid, err := uuid.Parse(versionID)
	if err != nil {
		return fmt.Errorf("parse version id: %w", err)
	}
	if err := s.q.UpsertPromptEnv(ctx, pgstore.UpsertPromptEnvParams{
		PromptID:  pid,
		Env:       env,
		VersionID: vid,
	}); err != nil {
		return fmt.Errorf("upsert prompt env: %w", err)
	}
	return nil
}

func (s *PostgresStore) GetEnvBinding(ctx context.Context, promptID, env string) (string, error) {
	pid, err := uuid.Parse(promptID)
	if err != nil {
		return "", fmt.Errorf("parse prompt id: %w", err)
	}
	vid, err := s.q.GetPromptEnv(ctx, pgstore.GetPromptEnvParams{PromptID: pid, Env: env})
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", fmt.Errorf("no version bound for env %s", env)
		}
		return "", fmt.Errorf("get prompt env: %w", err)
	}
	return vid.String(), nil
}

func (s *PostgresStore) loadVersionConfig(ctx context.Context, v *domain.PromptVersion) error {
	var configID pgtype.UUID
	var configJSON []byte
	err := s.db.QueryRow(ctx, `
		SELECT model_config_version_id,generation_config
		FROM prompt_version_configs WHERE prompt_version_id=$1`, uuid.MustParse(v.ID)).
		Scan(&configID, &configJSON)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get prompt version config: %w", err)
	}
	if configID.Valid {
		v.ModelConfigVersionID = uuid.UUID(configID.Bytes).String()
	}
	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &v.GenerationConfig); err != nil {
			return fmt.Errorf("decode generation config: %w", err)
		}
	}
	return nil
}

func emptyGenerationConfig(v domain.GenerationConfig) bool {
	return v.Temperature == nil && v.TopP == nil && v.MaxOutputTokens == nil &&
		len(v.Stop) == 0 && v.Seed == nil
}

// ---- 行 → domain ----

func promptFromRow(r pgstore.Prompt) *domain.Prompt {
	return &domain.Prompt{
		ID:          r.ID.String(),
		WorkspaceID: r.WorkspaceID,
		Name:        r.Name,
		Category:    r.Category,
		CreatedBy:   r.CreatedBy,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

func versionFromRow(r pgstore.PromptVersion) *domain.PromptVersion {
	return &domain.PromptVersion{
		ID:              r.ID.String(),
		PromptID:        r.PromptID.String(),
		Version:         int(r.Version),
		Template:        r.Template,
		VariablesSchema: r.VariablesSchema,
		Hash:            r.Hash,
		TokenEstimate:   int(r.TokenEstimate),
		CreatedBy:       r.CreatedBy,
		CreatedAt:       r.CreatedAt,
	}
}

func notFound(what string, err error) error {
	if err == pgx.ErrNoRows {
		return fmt.Errorf("%s not found", what)
	}
	return fmt.Errorf("get %s: %w", what, err)
}
