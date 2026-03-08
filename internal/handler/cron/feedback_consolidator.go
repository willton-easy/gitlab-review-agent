package cron

import (
	"context"
	"log/slog"

	"ai-review-agent/internal/core/feedback"
	"ai-review-agent/internal/shared"
)

type FeedbackConsolidatorJob struct {
	repoSettings shared.RepositorySettingsStore
	consolidator *feedback.Consolidator
}

func NewFeedbackConsolidatorJob(
	repoSettings shared.RepositorySettingsStore,
	consolidator *feedback.Consolidator,
) *FeedbackConsolidatorJob {
	return &FeedbackConsolidatorJob{
		repoSettings: repoSettings,
		consolidator: consolidator,
	}
}

func (j *FeedbackConsolidatorJob) Run() {
	ctx := context.Background()

	repos, err := j.repoSettings.ListEnabled(ctx)
	if err != nil {
		slog.Error("list enabled repos", "error", err)
		return
	}

	consolidated := 0
	for _, repo := range repos {
		if err := j.consolidator.ConsolidateProject(ctx, repo.GitLabProjectID); err != nil {
			slog.Error("consolidate project", "project_id", repo.GitLabProjectID, "error", err)
			continue
		}
		consolidated++
	}

	slog.Info("feedback consolidation completed", "repos_processed", consolidated)
}
