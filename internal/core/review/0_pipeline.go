package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/antlss/gitlab-review-agent/internal/config"
	"github.com/antlss/gitlab-review-agent/internal/core/agents/reviewer"
	"github.com/antlss/gitlab-review-agent/internal/core/prompt"
	"github.com/antlss/gitlab-review-agent/internal/pkg/git"
	"github.com/antlss/gitlab-review-agent/internal/pkg/llm"
	"github.com/antlss/gitlab-review-agent/internal/pkg/tools"
	"github.com/antlss/gitlab-review-agent/internal/shared"
)

type Pipeline struct {
	cfg           config.Config
	jobStore      shared.ReviewJobStore
	repoSettings  shared.RepositorySettingsStore
	recordStore   shared.ReviewRecordStore
	feedbackStore shared.FeedbackStore
	gitlabClient  shared.GitLabClient
	gitManager    *git.Manager
	gatherer      *ContextGatherer
	agent         *reviewer.Agent
}

type PipelineDeps struct {
	Config        config.Config
	JobStore      shared.ReviewJobStore
	RepoSettings  shared.RepositorySettingsStore
	RecordStore   shared.ReviewRecordStore
	FeedbackStore shared.FeedbackStore
	GitLabClient  shared.GitLabClient
	GitManager    *git.Manager
	Gatherer      *ContextGatherer
	Agent         *reviewer.Agent
}

func NewPipeline(deps PipelineDeps) *Pipeline {
	return &Pipeline{
		cfg:           deps.Config,
		jobStore:      deps.JobStore,
		repoSettings:  deps.RepoSettings,
		recordStore:   deps.RecordStore,
		feedbackStore: deps.FeedbackStore,
		gitlabClient:  deps.GitLabClient,
		gitManager:    deps.GitManager,
		gatherer:      deps.Gatherer,
		agent:         deps.Agent,
	}
}

func (p *Pipeline) Execute(ctx context.Context, job *shared.ReviewJob) error {
	log := slog.With("job_id", job.ID.String(), "project_id", job.GitLabProjectID, "mr_iid", job.MrIID)

	if err := p.jobStore.UpdateStatus(ctx, job.ID, shared.ReviewJobStatusReviewing, nil); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	// Pre-flight: check if review is enabled for this repo
	settings, err := p.repoSettings.GetByProjectID(ctx, job.GitLabProjectID)
	if err != nil {
		return p.failJob(ctx, job, "get repo settings: "+err.Error())
	}
	if settings != nil && !settings.ReviewEnabled {
		log.Info("review skipped as disabled in settings")
		return p.jobStore.UpdateStatus(ctx, job.ID, shared.ReviewJobStatusSkippedDisabled, nil)
	}
	projectPath := ""
	if settings != nil {
		projectPath = settings.ProjectPath
	}
	if err := p.acquireAndFetch(ctx, job, projectPath); err != nil {
		return p.failJob(ctx, job, err.Error())
	}
	log.Debug("git checkout completed", "head_sha", job.HeadSHA, "project_path", projectPath)

	// Determine base SHA: use previous review's HeadSHA for incremental, or target branch
	fullRecheck := job.IsForcePush
	baseSHA, err := p.determineBaseSHA(ctx, job, fullRecheck)
	if err != nil {
		return p.failJob(ctx, job, "determine base SHA: "+err.Error())
	}
	log.Info("base SHA determined", "base_sha", baseSHA, "force_push", fullRecheck)
	if err := p.jobStore.UpdateBaseSHA(ctx, job.ID, baseSHA); err != nil {
		log.Warn("failed to update base SHA", "error", err)
	}

	diffFiles, err := p.gitManager.Diff(ctx, job.GitLabProjectID, baseSHA, job.HeadSHA)
	if err != nil {
		return p.failJob(ctx, job, "git diff: "+err.Error())
	}

	excludePatterns := append(DefaultExcludePatterns(), job.ExcludePatternList()...)
	var filteredFiles []shared.DiffFile
	for i := range diffFiles {
		f := &diffFiles[i]
		if f.Status == "D" {
			continue
		}
		if ShouldExclude(f.Path, excludePatterns) {
			continue
		}
		ScoreRisk(f)
		filteredFiles = append(filteredFiles, *f)
	}

	log.Info("diff files filtered", "total", len(diffFiles), "filtered", len(filteredFiles))

	if len(filteredFiles) > p.cfg.Review.MaxFilesBeforeSample {
		if p.cfg.Review.LargePRAction == "block" {
			msg := fmt.Sprintf("MR has %d files (max %d). Skipping review.", len(filteredFiles), p.cfg.Review.MaxFilesBeforeSample)
			p.gitlabClient.PostThreadComment(ctx, job.GitLabProjectID, job.MrIID, msg)
			return p.jobStore.UpdateStatus(ctx, job.ID, shared.ReviewJobStatusSkippedSize, nil)
		}
		sort.Slice(filteredFiles, func(i, j int) bool {
			return filteredFiles[i].RiskScore > filteredFiles[j].RiskScore
		})
		if len(filteredFiles) > p.cfg.Review.SampleFileCount {
			filteredFiles = filteredFiles[:p.cfg.Review.SampleFileCount]
		}
	}

	sort.Slice(filteredFiles, func(i, j int) bool {
		return filteredFiles[i].RiskScore > filteredFiles[j].RiskScore
	})

	reviewCtx, err := p.gatherer.Gather(ctx, job, filteredFiles)
	if err != nil {
		return p.failJob(ctx, job, "context gathering: "+err.Error())
	}

	llmClient, err := llm.NewBalancedClientFromConfig(p.cfg.LLM, job.RepoModelOverride)
	if err != nil {
		return p.failJob(ctx, job, "create LLM client: "+err.Error())
	}
	p.jobStore.UpdateModelUsed(ctx, job.ID, llmClient.ModelName())

	repoPath := p.gitManager.RepoPath(job.GitLabProjectID)
	toolCfg := p.cfg.Tool
	toolCfg.BaseSHA = baseSHA
	toolCfg.HeadSHA = job.HeadSHA
	registry := tools.NewRegistry(repoPath, filteredFiles, toolCfg)

	// Pre-load HIGH-risk diffs into user message; for small MRs, pre-load all files
	var highRiskFiles, otherFiles []shared.DiffFile
	for _, f := range filteredFiles {
		if f.RiskTier == shared.RiskHigh {
			highRiskFiles = append(highRiskFiles, f)
		} else {
			otherFiles = append(otherFiles, f)
		}
	}

	preloadMaxBytes := p.cfg.Review.PreloadDiffMaxKB * 1024
	highContent, highIncluded := p.computePreloadedDiffs(ctx, job.GitLabProjectID, highRiskFiles, baseSHA, job.HeadSHA, preloadMaxBytes)
	preloadedDiffs := highContent
	allDiffsPreloaded := false

	if len(filteredFiles) <= p.cfg.Review.PreloadDiffThreshold {
		remaining := preloadMaxBytes - len(preloadedDiffs)
		otherContent, otherIncluded := p.computePreloadedDiffs(ctx, job.GitLabProjectID, otherFiles, baseSHA, job.HeadSHA, remaining)
		preloadedDiffs += otherContent
		allDiffsPreloaded = (highIncluded + otherIncluded) == len(filteredFiles)
	}

	maxIter, softWarn := CalculateBudgetWithPreload(len(filteredFiles), preloadedDiffs != "")
	log.Info("diff preload complete",
		"preloaded_bytes", len(preloadedDiffs),
		"all_diffs_preloaded", allDiffsPreloaded,
		"max_iterations", maxIter,
	)

	log.Info("starting agent loop", "max_iterations", maxIter, "files", len(filteredFiles))
	diffStat := formatDiffStat(filteredFiles)
	lang := prompt.ParseLanguage(p.cfg.Review.ResponseLanguage)
	agentInput := reviewer.AgentInput{
		Job:                  job,
		ReviewCtx:            reviewCtx,
		FilteredFiles:        filteredFiles,
		DiffStatFormatted:    diffStat,
		MaxIterations:        maxIter,
		SoftWarnAt:           softWarn,
		CompressionThreshold: p.cfg.LLM.CompressionThreshold,
		Registry:             registry,
		LLMClient:            llmClient,
		PreloadedDiffs:       preloadedDiffs,
		AllDiffsPreloaded:    allDiffsPreloaded,
		ResponseLanguage:     lang,
	}

	result, err := p.agent.Run(ctx, agentInput)
	if err != nil {
		return p.failJob(ctx, job, "agent: "+err.Error())
	}

	parsed, err := Parse(result.RawOutput)
	if err != nil {
		p.jobStore.UpdateAIOutput(ctx, job.ID, result.RawOutput, nil, result.IterationsUsed, result.TokensEstimated)
		return p.jobStore.UpdateStatus(ctx, job.ID, shared.ReviewJobStatusParseFailed, shared.StrPtr("parse failed: "+err.Error()))
	}

	comments := ValidateAndFilter(parsed, filteredFiles, reviewCtx.ExistingUnresolvedComments)
	p.jobStore.UpdateAIOutput(ctx, job.ID, result.RawOutput, comments, result.IterationsUsed, result.TokensEstimated)

	if job.DryRun {
		return p.jobStore.UpdateCompleted(ctx, job.ID, 0, 0)
	}

	p.jobStore.UpdateStatus(ctx, job.ID, shared.ReviewJobStatusPosting, nil)
	posted, suppressed := 0, 0
	for i := range comments {
		c := &comments[i]
		if c.Suppressed {
			suppressed++
			continue
		}

		body := FormatComment(c, lang)
		resp, err := p.gitlabClient.PostInlineComment(ctx, shared.PostInlineCommentRequest{
			ProjectID: job.GitLabProjectID,
			MrIID:     job.MrIID,
			Body:      body,
			FilePath:  c.FilePath,
			NewLine:   c.LineNumber,
			BaseSHA:   baseSHA,
			HeadSHA:   job.HeadSHA,
			StartSHA:  baseSHA,
		})
		if err != nil {
			log.Warn("failed to post comment", "file", c.FilePath, "line", c.LineNumber, "error", err)
			continue
		}

		c.GitLabNoteID = &resp.NoteID
		c.GitLabDiscussionID = &resp.DiscussionID
		posted++

		fb := &shared.ReviewFeedback{
			GitLabProjectID:    job.GitLabProjectID,
			ReviewJobID:        &job.ID,
			GitLabDiscussionID: resp.DiscussionID,
			GitLabNoteID:       resp.NoteID,
			FilePath:           &c.FilePath,
			LineNumber:         &c.LineNumber,
			Category:           &c.Category,
			CommentSummary:     shared.StrPtr(shared.Truncate(c.ReviewComment, 200)),
			Language:           shared.StrPtr(reviewCtx.DetectedLanguage),
			ModelUsed:          shared.StrPtr(llmClient.ModelName()),
		}
		if err := p.feedbackStore.Create(ctx, fb); err != nil {
			log.Warn("failed to create feedback", "error", err)
		}
	}

	// Auto-resolve previous bot threads where the flagged line was modified in this diff
	resolved := p.autoResolveFixedThreads(ctx, job, reviewCtx.BotUnresolvedComments, filteredFiles)
	if resolved > 0 {
		log.Info("auto-resolved fixed threads", "count", resolved)
	}

	summary := buildSummaryComment(posted, suppressed, len(comments), resolved, result, llmClient.ModelName(), lang)
	p.gitlabClient.PostThreadComment(ctx, job.GitLabProjectID, job.MrIID, summary)

	p.jobStore.UpdateCompleted(ctx, job.ID, posted, suppressed)

	reviewedFiles := extractFilePaths(filteredFiles)
	filesJSON, err := json.Marshal(reviewedFiles)
	if err != nil {
		log.Warn("failed to marshal reviewed files", "error", err)
		filesJSON = []byte("[]")
	}
	p.recordStore.Upsert(ctx, &shared.ReviewRecord{
		GitLabProjectID: job.GitLabProjectID,
		MrIID:           job.MrIID,
		ReviewJobID:     job.ID,
		HeadSHA:         job.HeadSHA,
		ReviewedFiles:   filesJSON,
		CommentsPosted:  posted,
	})

	p.repoSettings.IncrementFeedbackCount(ctx, job.GitLabProjectID, posted)

	log.Info("review completed", "posted", posted, "suppressed", suppressed, "iterations", result.IterationsUsed)
	return nil
}

// acquireAndFetch wraps git lock acquisition + fetch/checkout in a single
// function so defer correctly scopes the lock release to only the git operations.
func (p *Pipeline) acquireAndFetch(ctx context.Context, job *shared.ReviewJob, projectPath string) error {
	if err := p.gitManager.AcquireGitLock(ctx, job.GitLabProjectID); err != nil {
		return err
	}
	defer p.gitManager.ReleaseGitLock(ctx, job.GitLabProjectID)
	return p.gitManager.FetchAndCheckout(ctx, job.GitLabProjectID, projectPath, job.HeadSHA)
}

func (p *Pipeline) determineBaseSHA(ctx context.Context, job *shared.ReviewJob, fullRecheck bool) (string, error) {
	if fullRecheck {
		return p.gitManager.RevParse(ctx, job.GitLabProjectID, "origin/"+job.TargetBranch)
	}

	record, err := p.recordStore.GetLastCompleted(ctx, job.GitLabProjectID, job.MrIID)
	if err != nil {
		return "", err
	}
	if record == nil {
		return p.gitManager.RevParse(ctx, job.GitLabProjectID, "origin/"+job.TargetBranch)
	}

	if p.gitManager.SHAExists(ctx, job.GitLabProjectID, record.HeadSHA) {
		return record.HeadSHA, nil
	}

	slog.Info("incremental base SHA not found, using target branch",
		"project_id", job.GitLabProjectID, "mr_iid", job.MrIID)
	return p.gitManager.RevParse(ctx, job.GitLabProjectID, "origin/"+job.TargetBranch)
}

// autoResolveFixedThreads resolves previous bot comment threads where the
// flagged file+line was modified in the current diff (likely fixed by new commit).
func (p *Pipeline) autoResolveFixedThreads(ctx context.Context, job *shared.ReviewJob, botComments []shared.BotUnresolvedComment, diffFiles []shared.DiffFile) int {
	if len(botComments) == 0 {
		return 0
	}

	// Build set of modified lines per file
	modifiedLines := make(map[string]map[int]bool)
	for _, f := range diffFiles {
		if len(f.AddedLines) == 0 {
			continue
		}
		lineSet := make(map[int]bool, len(f.AddedLines))
		for _, ln := range f.AddedLines {
			lineSet[ln] = true
		}
		modifiedLines[f.Path] = lineSet
	}

	resolved := 0
	for _, bc := range botComments {
		lines, ok := modifiedLines[bc.FilePath]
		if !ok {
			continue
		}
		if !lines[bc.LineNumber] {
			continue
		}
		if err := p.gitlabClient.ResolveDiscussion(ctx, job.GitLabProjectID, job.MrIID, bc.DiscussionID); err != nil {
			slog.Warn("failed to auto-resolve discussion", "discussion_id", bc.DiscussionID, "error", err)
			continue
		}
		resolved++
	}
	return resolved
}

func (p *Pipeline) failJob(ctx context.Context, job *shared.ReviewJob, msg string) error {
	slog.Error("review job failed", "job_id", job.ID.String(), "error", msg)
	p.jobStore.UpdateStatus(ctx, job.ID, shared.ReviewJobStatusFailed, &msg)
	return errors.New(msg)
}

func formatDiffStat(files []shared.DiffFile) string {
	var sb strings.Builder
	for _, f := range files {
		icon := "🟢"
		if f.RiskTier == shared.RiskHigh {
			icon = "🔴"
		} else if f.RiskTier == shared.RiskMedium {
			icon = "🟡"
		}
		sb.WriteString(fmt.Sprintf("%s %s (+%d/-%d) [%s]\n", icon, f.Path, f.LinesAdded, f.LinesRemoved, f.RiskTier))
	}
	return sb.String()
}

func FormatComment(c *shared.ParsedComment, lang prompt.ResponseLanguage) string {
	badge := SeverityBadge(c.Severity)
	comment := fmt.Sprintf("%s **[%s]** %s", badge, strings.ToUpper(string(c.Category)), c.ReviewComment)
	if c.Suggestion != "" {
		comment += fmt.Sprintf("\n\n💡 **%s**\n```suggestion\n%s\n```", prompt.SuggestionLabel(lang), c.Suggestion)
	}
	return comment
}

func SeverityBadge(s shared.CommentSeverity) string {
	switch s {
	case shared.SeverityCritical:
		return "🔴 `CRITICAL`"
	case shared.SeverityHigh:
		return "🟠 `HIGH`"
	case shared.SeverityMedium:
		return "🟡 `MEDIUM`"
	default:
		return "🔵 `LOW`"
	}
}

func buildSummaryComment(posted, suppressed, total, resolved int, result *reviewer.AgentResult, model string, lang prompt.ResponseLanguage) string {
	var sb strings.Builder
	sb.WriteString("## AI Review Summary\n\n")

	if total == 0 {
		sb.WriteString(prompt.SummaryLGTM())
	} else if posted == 0 && suppressed > 0 {
		sb.WriteString(prompt.SummaryAllFiltered(lang, suppressed))
	} else {
		sb.WriteString(prompt.SummaryPostedCount(lang, posted))
		if suppressed > 0 {
			sb.WriteString(prompt.SummaryFilteredCount(lang, suppressed))
		}
		sb.WriteString("\n")
	}

	if resolved > 0 {
		sb.WriteString(prompt.SummaryAutoResolved(lang, resolved))
	}

	sb.WriteString(fmt.Sprintf("- **Model:** %s\n", model))
	sb.WriteString(fmt.Sprintf("- **Iterations:** %d (stop: %s)\n", result.IterationsUsed, result.StopReason))

	if posted > 0 {
		sb.WriteString(prompt.SummaryReplyHint(lang))
	}
	return sb.String()
}

func extractFilePaths(files []shared.DiffFile) []string {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	return paths
}

func (p *Pipeline) computePreloadedDiffs(ctx context.Context, projectID int64, files []shared.DiffFile, baseSHA, headSHA string, maxBytes int) (string, int) {
	if len(files) == 0 || maxBytes <= 0 {
		return "", 0
	}
	var sb strings.Builder
	totalBytes := 0
	included := 0
	for _, f := range files {
		out, err := p.gitManager.DiffFile(ctx, projectID, baseSHA, headSHA, f.Path)
		if err != nil || len(out) == 0 {
			continue
		}
		header := fmt.Sprintf("--- %s ---\n", f.Path)
		entry := header + out + "\n"
		if totalBytes+len(entry) > maxBytes {
			sb.WriteString(fmt.Sprintf("--- %s: (diff omitted — size limit reached) ---\n", f.Path))
			continue
		}
		sb.WriteString(entry)
		totalBytes += len(entry)
		included++
	}
	return sb.String(), included
}
