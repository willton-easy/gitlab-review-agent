package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

// sqliteSchema defines all tables in a single string for auto-migration.
const sqliteSchema = `
CREATE TABLE IF NOT EXISTS repository_settings (
    id                  TEXT PRIMARY KEY,
    gitlab_project_id   INTEGER NOT NULL UNIQUE,
    project_path        TEXT NOT NULL,
    model_override      TEXT,
    language            TEXT,
    framework           TEXT,
    custom_prompt       TEXT,
    exclude_patterns    TEXT,
    feedback_count      INTEGER NOT NULL DEFAULT 0,
    is_archived         INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_repo_settings_project_id ON repository_settings(gitlab_project_id);

CREATE TABLE IF NOT EXISTS review_jobs (
    id                        TEXT PRIMARY KEY,
    gitlab_project_id         INTEGER NOT NULL,
    mr_iid                    INTEGER NOT NULL,
    head_sha                  TEXT NOT NULL,
    base_sha                  TEXT,
    target_branch             TEXT NOT NULL DEFAULT '',
    source_branch             TEXT NOT NULL DEFAULT '',
    is_force_push             INTEGER NOT NULL DEFAULT 0,
    dry_run                   INTEGER NOT NULL DEFAULT 0,
    trigger_source            TEXT NOT NULL,
    status                    TEXT NOT NULL DEFAULT 'PENDING',
    model_used                TEXT,
    repo_model_override       TEXT,
    repo_language             TEXT,
    repo_framework            TEXT,
    repo_exclude_patterns     TEXT,
    ai_output_raw             TEXT,
    ai_output_parsed          TEXT,
    iterations_used           INTEGER,
    tokens_estimated          INTEGER,
    total_comments_posted     INTEGER,
    total_comments_suppressed INTEGER,
    queued_at                 TEXT NOT NULL,
    started_at                TEXT,
    completed_at              TEXT,
    error_message             TEXT,
    created_at                TEXT NOT NULL,
    updated_at                TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_review_jobs_project_mr_sha ON review_jobs(gitlab_project_id, mr_iid, head_sha);
CREATE INDEX IF NOT EXISTS idx_review_jobs_status ON review_jobs(status);

CREATE TABLE IF NOT EXISTS reply_jobs (
    id                      TEXT PRIMARY KEY,
    gitlab_project_id       INTEGER NOT NULL,
    mr_iid                  INTEGER NOT NULL,
    discussion_id           TEXT NOT NULL,
    trigger_note_id         INTEGER NOT NULL,
    trigger_note_content    TEXT NOT NULL,
    trigger_note_author     TEXT NOT NULL,
    bot_comment_id          INTEGER NOT NULL,
    bot_comment_content     TEXT NOT NULL,
    bot_comment_file_path   TEXT,
    bot_comment_line        INTEGER,
    status                  TEXT NOT NULL DEFAULT 'PENDING',
    reply_content           TEXT,
    intent_classified       TEXT,
    feedback_signal         TEXT,
    error_message           TEXT,
    queued_at               TEXT NOT NULL,
    started_at              TEXT,
    completed_at            TEXT,
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_reply_jobs_project ON reply_jobs(gitlab_project_id, mr_iid);

CREATE TABLE IF NOT EXISTS review_feedbacks (
    id                   TEXT PRIMARY KEY,
    gitlab_project_id    INTEGER NOT NULL,
    review_job_id        TEXT,
    gitlab_discussion_id TEXT NOT NULL,
    gitlab_note_id       INTEGER NOT NULL UNIQUE,
    file_path            TEXT,
    line_number          INTEGER,
    category             TEXT,
    comment_summary      TEXT,
    language             TEXT,
    signal               TEXT,
    signal_reply_content TEXT,
    model_used           TEXT,
    consolidated_at      TEXT,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_feedbacks_project ON review_feedbacks(gitlab_project_id);
CREATE INDEX IF NOT EXISTS idx_feedbacks_note_id ON review_feedbacks(gitlab_note_id);

CREATE TABLE IF NOT EXISTS review_records (
    id                  TEXT PRIMARY KEY,
    gitlab_project_id   INTEGER NOT NULL,
    mr_iid              INTEGER NOT NULL,
    review_job_id       TEXT NOT NULL,
    head_sha            TEXT NOT NULL,
    reviewed_files      TEXT NOT NULL,
    comments_posted     INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT NOT NULL,
    UNIQUE(gitlab_project_id, mr_iid)
);
CREATE INDEX IF NOT EXISTS idx_review_records_mr ON review_records(gitlab_project_id, mr_iid);
`

// Connect opens a SQLite database and auto-creates tables.
func Connect(dbPath string) (*sqlx.DB, error) {
	db, err := sqlx.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite is not designed for high concurrency; limit connections.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec(sqliteSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create sqlite schema: %w", err)
	}
	return db, nil
}

// NullTimeToString is a helper that converts sql.NullTime to a *string for SQLite text columns.
func NullTimeToString(t sql.NullTime) *string {
	if !t.Valid {
		return nil
	}
	s := t.Time.Format("2006-01-02T15:04:05Z07:00")
	return &s
}
