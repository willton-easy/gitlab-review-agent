package cron

import (
	"context"
	"log/slog"

	"github.com/antlss/gitlab-review-agent/internal/shared"
)

type StaleJobRecoveryJob struct {
	jobStore         shared.ReviewJobStore
	olderThanMinutes int
}

func NewStaleJobRecoveryJob(jobStore shared.ReviewJobStore, olderThanMinutes int) *StaleJobRecoveryJob {
	return &StaleJobRecoveryJob{
		jobStore:         jobStore,
		olderThanMinutes: olderThanMinutes,
	}
}

func (j *StaleJobRecoveryJob) Run() {
	ctx := context.Background()

	staleJobs, err := j.jobStore.ListStale(ctx, j.olderThanMinutes)
	if err != nil {
		slog.Error("list stale jobs", "error", err)
		return
	}

	if len(staleJobs) == 0 {
		return
	}

	recovered := 0
	for _, job := range staleJobs {
		errMsg := "stale job recovered: exceeded maximum processing time"
		if err := j.jobStore.UpdateStatus(ctx, job.ID, shared.ReviewJobStatusFailed, &errMsg); err != nil {
			slog.Error("recover stale job", "job_id", job.ID, "error", err)
			continue
		}
		slog.Warn("recovered stale job", "job_id", job.ID, "project_id", job.GitLabProjectID, "mr_iid", job.MrIID)
		recovered++
	}

	slog.Info("stale job recovery completed", "recovered", recovered)
}
