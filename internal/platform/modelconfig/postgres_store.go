package modelconfig

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ db *pgxpool.Pool }

func NewPostgresStore(db *pgxpool.Pool) *PostgresStore { return &PostgresStore{db: db} }

var _ Store = (*PostgresStore)(nil)

func (s *PostgresStore) CreateConfigVersion(ctx context.Context, v *ModelConfigVersion) error {
	id, err := uuid.Parse(v.ID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO model_config_versions
		  (id,workspace_id,name,version,provider_kind,base_url,model_name,credential_ref,
		   timeout_ms,max_retries,input_price_per_million,output_price_per_million,
		   cached_input_price_per_million,created_by,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		id, v.WorkspaceID, v.Name, v.Version, v.ProviderKind, v.BaseURL, v.ModelName, v.CredentialRef,
		v.TimeoutMS, v.MaxRetries, v.InputPricePerMillion, v.OutputPricePerMillion,
		v.CachedInputPricePerMillion, v.CreatedBy, v.CreatedAt)
	if err != nil {
		return fmt.Errorf("create model config version: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListConfigVersions(ctx context.Context, workspaceID string) ([]*ModelConfigVersion, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id::text,workspace_id,name,version,provider_kind,base_url,model_name,credential_ref,
		       timeout_ms,max_retries,input_price_per_million,output_price_per_million,
		       cached_input_price_per_million,created_by,created_at
		FROM model_config_versions WHERE workspace_id=$1 ORDER BY name,version DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*ModelConfigVersion, 0)
	for rows.Next() {
		v, err := scanConfigVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetConfigVersion(ctx context.Context, id string) (*ModelConfigVersion, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	v, err := scanConfigVersion(s.db.QueryRow(ctx, `
		SELECT id::text,workspace_id,name,version,provider_kind,base_url,model_name,credential_ref,
		       timeout_ms,max_retries,input_price_per_million,output_price_per_million,
		       cached_input_price_per_million,created_by,created_at
		FROM model_config_versions WHERE id=$1`, uid))
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("model config version not found")
	}
	return v, err
}

type rowScanner interface{ Scan(...any) error }

func scanConfigVersion(row rowScanner) (*ModelConfigVersion, error) {
	v := &ModelConfigVersion{}
	err := row.Scan(&v.ID, &v.WorkspaceID, &v.Name, &v.Version, &v.ProviderKind, &v.BaseURL,
		&v.ModelName, &v.CredentialRef, &v.TimeoutMS, &v.MaxRetries, &v.InputPricePerMillion,
		&v.OutputPricePerMillion, &v.CachedInputPricePerMillion, &v.CreatedBy, &v.CreatedAt)
	if err != nil {
		return nil, err
	}
	return v, nil
}
