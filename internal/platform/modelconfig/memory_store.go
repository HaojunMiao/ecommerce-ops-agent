package modelconfig

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

type MemoryStore struct {
	mu       sync.RWMutex
	versions map[string]*ModelConfigVersion
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{versions: make(map[string]*ModelConfigVersion)}
}

var _ Store = (*MemoryStore)(nil)

func (s *MemoryStore) CreateConfigVersion(_ context.Context, v *ModelConfigVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.versions {
		if existing.WorkspaceID == v.WorkspaceID && existing.Name == v.Name && existing.Version == v.Version {
			return fmt.Errorf("model config version already exists")
		}
	}
	cp := *v
	s.versions[v.ID] = &cp
	return nil
}

func (s *MemoryStore) ListConfigVersions(_ context.Context, workspaceID string) ([]*ModelConfigVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*ModelConfigVersion, 0)
	for _, v := range s.versions {
		if v.WorkspaceID == workspaceID {
			cp := *v
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Version > out[j].Version
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *MemoryStore) GetConfigVersion(_ context.Context, id string) (*ModelConfigVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.versions[id]
	if !ok {
		return nil, fmt.Errorf("model config version not found")
	}
	cp := *v
	return &cp, nil
}
