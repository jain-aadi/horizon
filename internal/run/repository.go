package run

import (
	"context"

	"github.com/jain-aadi/horizon/internal/storage"
)

// RunRepository defines the persistence interface for runs.
type RunRepository interface {
	Save(ctx context.Context, rec *RunRecord) error
	GetByID(ctx context.Context, id string) (*RunRecord, error)
	ListByAgentID(ctx context.Context, agentID string) ([]*RunRecord, error)
	DeleteByAgentID(ctx context.Context, agentID string) error
}

// StorageRepository adapts SQLite rows to the run-domain repository contract.
type StorageRepository struct {
	repository *storage.RunRepository
}

// NewStorageRepository creates a run repository backed by SQLite.
func NewStorageRepository(repository *storage.RunRepository) *StorageRepository {
	return &StorageRepository{repository: repository}
}

func (r *StorageRepository) Save(ctx context.Context, record *RunRecord) error {
	return r.repository.Save(ctx, record.ToStorageRow())
}

func (r *StorageRepository) GetByID(ctx context.Context, id string) (*RunRecord, error) {
	row, err := r.repository.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return FromStorageRow(row), nil
}

func (r *StorageRepository) ListByAgentID(ctx context.Context, agentID string) ([]*RunRecord, error) {
	rows, err := r.repository.ListByAgentID(ctx, agentID)
	if err != nil {
		return nil, err
	}
	records := make([]*RunRecord, len(rows))
	for i, row := range rows {
		records[i] = FromStorageRow(row)
	}
	return records, nil
}

func (r *StorageRepository) DeleteByAgentID(ctx context.Context, agentID string) error {
	return r.repository.DeleteByAgentID(ctx, agentID)
}
