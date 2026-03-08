package file

import (
	"context"
	"fmt"
	"os"
	"time"

	"ai-review-agent/internal/shared"

	"github.com/google/uuid"
)

type ReplyJobStore struct {
	b *base
}

func NewReplyJobStore(dataDir string) (*ReplyJobStore, error) {
	b, err := newBase(dataDir, "reply_jobs")
	if err != nil {
		return nil, err
	}
	return &ReplyJobStore{b: b}, nil
}

func replyJobFilename(id uuid.UUID) string {
	return id.String() + ".json"
}

func (s *ReplyJobStore) Create(_ context.Context, job *shared.ReplyJob) error {
	if job.ID == (uuid.UUID{}) {
		return fmt.Errorf("reply job ID must be set before calling Create")
	}
	s.b.mu.Lock()
	defer s.b.mu.Unlock()

	now := time.Now()
	job.CreatedAt = now
	job.UpdatedAt = now
	if job.QueuedAt.IsZero() {
		job.QueuedAt = now
	}
	return s.b.writeJSON(replyJobFilename(job.ID), job)
}

func (s *ReplyJobStore) GetByID(_ context.Context, id uuid.UUID) (*shared.ReplyJob, error) {
	s.b.mu.RLock()
	defer s.b.mu.RUnlock()

	var job shared.ReplyJob
	err := s.b.readJSON(replyJobFilename(id), &job)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read reply job: %w", err)
	}
	return &job, nil
}

func (s *ReplyJobStore) updateJob(id uuid.UUID, fn func(*shared.ReplyJob)) error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()

	fname := replyJobFilename(id)
	var job shared.ReplyJob
	if err := s.b.readJSON(fname, &job); err != nil {
		return fmt.Errorf("read reply job for update: %w", err)
	}
	fn(&job)
	job.UpdatedAt = time.Now()
	return s.b.writeJSON(fname, &job)
}

func (s *ReplyJobStore) UpdateStatus(_ context.Context, id uuid.UUID, status shared.ReplyJobStatus, errMsg *string) error {
	return s.updateJob(id, func(job *shared.ReplyJob) {
		job.Status = status
		job.ErrorMessage = errMsg
		now := time.Now()
		if status == shared.ReplyJobStatusProcessing {
			job.StartedAt = &now
		}
		if status == shared.ReplyJobStatusCompleted || status == shared.ReplyJobStatusFailed {
			job.CompletedAt = &now
		}
	})
}

func (s *ReplyJobStore) UpdateCompleted(_ context.Context, id uuid.UUID, reply string, intent shared.ReplyIntent, signal shared.FeedbackSignal) error {
	return s.updateJob(id, func(job *shared.ReplyJob) {
		job.Status = shared.ReplyJobStatusCompleted
		job.ReplyContent = &reply
		job.IntentClassified = &intent
		job.FeedbackSignal = &signal
		now := time.Now()
		job.CompletedAt = &now
	})
}
