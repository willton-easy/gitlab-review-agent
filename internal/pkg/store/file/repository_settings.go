package file

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/antlss/gitlab-review-agent/internal/shared"

	"github.com/google/uuid"
)

type RepositorySettingsStore struct {
	b *base
}

func NewRepositorySettingsStore(dataDir string) (*RepositorySettingsStore, error) {
	b, err := newBase(dataDir, "repository_settings")
	if err != nil {
		return nil, err
	}
	return &RepositorySettingsStore{b: b}, nil
}

func filename(projectID int64) string {
	return fmt.Sprintf("%d.json", projectID)
}

func (s *RepositorySettingsStore) GetByProjectID(_ context.Context, projectID int64) (*shared.RepositorySettings, error) {
	s.b.mu.RLock()
	defer s.b.mu.RUnlock()

	var rs shared.RepositorySettings
	err := s.b.readJSON(filename(projectID), &rs)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read repository settings: %w", err)
	}
	return &rs, nil
}

func (s *RepositorySettingsStore) GetOrCreate(_ context.Context, projectID int64, projectPath string) (*shared.RepositorySettings, error) {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()

	fname := filename(projectID)
	var existing shared.RepositorySettings
	err := s.b.readJSON(fname, &existing)
	if err == nil {
		return &existing, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read repository settings: %w", err)
	}

	// Auto-create
	now := time.Now()
	rs := &shared.RepositorySettings{
		ID:              uuid.New(),
		GitLabProjectID: projectID,
		ProjectPath:     projectPath,
		ReviewEnabled:   true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.b.writeJSON(fname, rs); err != nil {
		return nil, err
	}
	return rs, nil
}

func (s *RepositorySettingsStore) Upsert(_ context.Context, settings *shared.RepositorySettings) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()

	fname := filename(settings.GitLabProjectID)
	var existing shared.RepositorySettings
	err := s.b.readJSON(fname, &existing)
	if err == nil {
		// Update only path and unarchive
		existing.ProjectPath = settings.ProjectPath
		existing.IsArchived = false
		existing.UpdatedAt = time.Now()
		return s.b.writeJSON(fname, &existing)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("read settings for upsert: %w", err)
	}

	// Insert new
	if settings.ID == (uuid.UUID{}) {
		settings.ID = uuid.New()
	}
	now := time.Now()
	settings.CreatedAt = now
	settings.UpdatedAt = now
	return s.b.writeJSON(fname, settings)
}

func (s *RepositorySettingsStore) IncrementFeedbackCount(_ context.Context, projectID int64, delta int) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()

	fname := filename(projectID)
	var rs shared.RepositorySettings
	if err := s.b.readJSON(fname, &rs); err != nil {
		return fmt.Errorf("read settings: %w", err)
	}
	rs.FeedbackCount += delta
	rs.UpdatedAt = time.Now()
	return s.b.writeJSON(fname, &rs)
}

func (s *RepositorySettingsStore) ResetFeedbackCount(_ context.Context, projectID int64) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()

	fname := filename(projectID)
	var rs shared.RepositorySettings
	if err := s.b.readJSON(fname, &rs); err != nil {
		return fmt.Errorf("read settings: %w", err)
	}
	rs.FeedbackCount = 0
	rs.UpdatedAt = time.Now()
	return s.b.writeJSON(fname, &rs)
}

func (s *RepositorySettingsStore) UpdateCustomPrompt(_ context.Context, projectID int64, prompt string) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()

	fname := filename(projectID)
	var rs shared.RepositorySettings
	if err := s.b.readJSON(fname, &rs); err != nil {
		return fmt.Errorf("read settings: %w", err)
	}
	rs.CustomPrompt = &prompt
	rs.UpdatedAt = time.Now()
	return s.b.writeJSON(fname, &rs)
}

func (s *RepositorySettingsStore) ListEnabled(_ context.Context) ([]*shared.RepositorySettings, error) {
	s.b.mu.RLock()
	defer s.b.mu.RUnlock()

	files, err := s.b.listFiles()
	if err != nil {
		return nil, fmt.Errorf("list settings files: %w", err)
	}
	var results []*shared.RepositorySettings
	for _, f := range files {
		var rs shared.RepositorySettings
		if err := s.b.readJSON(f, &rs); err != nil {
			continue
		}
		if rs.ReviewEnabled && !rs.IsArchived {
			results = append(results, &rs)
		}
	}
	return results, nil
}

func (s *RepositorySettingsStore) MarkArchived(_ context.Context, projectID int64) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()

	fname := filename(projectID)
	var rs shared.RepositorySettings
	if err := s.b.readJSON(fname, &rs); err != nil {
		return fmt.Errorf("read settings: %w", err)
	}
	rs.IsArchived = true
	rs.UpdatedAt = time.Now()
	return s.b.writeJSON(fname, &rs)
}
