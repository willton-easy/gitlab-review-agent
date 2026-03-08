package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/antlss/gitlab-review-agent/internal/shared"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type ReplyJobStore struct {
	db *sqlx.DB
}

func NewReplyJobStore(db *sqlx.DB) *ReplyJobStore {
	return &ReplyJobStore{db: db}
}

func (s *ReplyJobStore) Create(ctx context.Context, job *shared.ReplyJob) error {
	if job.ID == (uuid.UUID{}) {
		return fmt.Errorf("reply job ID must be set before calling Create")
	}
	now := time.Now()
	job.CreatedAt = now
	job.UpdatedAt = now
	if job.QueuedAt.IsZero() {
		job.QueuedAt = now
	}

	_, err := s.db.NamedExecContext(ctx, `
		INSERT INTO reply_jobs (
			id, gitlab_project_id, mr_iid, discussion_id,
			trigger_note_id, trigger_note_content, trigger_note_author,
			bot_comment_id, bot_comment_content, bot_comment_file_path,
			bot_comment_line, status, queued_at, created_at, updated_at
		) VALUES (
			:id, :gitlab_project_id, :mr_iid, :discussion_id,
			:trigger_note_id, :trigger_note_content, :trigger_note_author,
			:bot_comment_id, :bot_comment_content, :bot_comment_file_path,
			:bot_comment_line, :status, :queued_at, :created_at, :updated_at
		)`, job)
	if err != nil {
		return fmt.Errorf("create reply job: %w", err)
	}
	return nil
}

func (s *ReplyJobStore) GetByID(ctx context.Context, id uuid.UUID) (*shared.ReplyJob, error) {
	var job shared.ReplyJob
	err := s.db.GetContext(ctx, &job,
		`SELECT * FROM reply_jobs WHERE id = $1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get reply job: %w", err)
	}
	return &job, nil
}

func (s *ReplyJobStore) UpdateStatus(ctx context.Context, id uuid.UUID, status shared.ReplyJobStatus, errMsg *string) error {
	query := `UPDATE reply_jobs SET status = $1, error_message = $2, updated_at = NOW()`
	if status == shared.ReplyJobStatusProcessing {
		query += `, started_at = NOW()`
	}
	if status == shared.ReplyJobStatusCompleted || status == shared.ReplyJobStatusFailed {
		query += `, completed_at = NOW()`
	}
	query += ` WHERE id = $3`
	_, err := s.db.ExecContext(ctx, query, status, errMsg, id)
	if err != nil {
		return fmt.Errorf("update reply job status: %w", err)
	}
	return nil
}

func (s *ReplyJobStore) UpdateCompleted(ctx context.Context, id uuid.UUID, reply string, intent shared.ReplyIntent, signal shared.FeedbackSignal) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE reply_jobs SET
			status = $1, reply_content = $2, intent_classified = $3,
			feedback_signal = $4, completed_at = NOW(), updated_at = NOW()
		WHERE id = $5`,
		shared.ReplyJobStatusCompleted, reply, intent, signal, id)
	if err != nil {
		return fmt.Errorf("update reply completed: %w", err)
	}
	return nil
}
