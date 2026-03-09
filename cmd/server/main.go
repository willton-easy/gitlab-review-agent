package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"

	"github.com/antlss/gitlab-review-agent/internal/config"
	"github.com/antlss/gitlab-review-agent/internal/core/agents/replier"
	"github.com/antlss/gitlab-review-agent/internal/core/agents/reviewer"
	"github.com/antlss/gitlab-review-agent/internal/core/feedback"
	"github.com/antlss/gitlab-review-agent/internal/core/reply"
	"github.com/antlss/gitlab-review-agent/internal/core/review"
	"github.com/antlss/gitlab-review-agent/internal/handler/cron"
	"github.com/antlss/gitlab-review-agent/internal/handler/webhook"
	"github.com/antlss/gitlab-review-agent/internal/handler/worker"
	"github.com/antlss/gitlab-review-agent/internal/pkg/git"
	"github.com/antlss/gitlab-review-agent/internal/pkg/gitlab"
	"github.com/antlss/gitlab-review-agent/internal/pkg/llm"
	"github.com/antlss/gitlab-review-agent/internal/pkg/logger"
	"github.com/antlss/gitlab-review-agent/internal/pkg/queue"
	"github.com/antlss/gitlab-review-agent/internal/pkg/store"
)

func main() {
	_ = godotenv.Load()
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	logger.Setup(cfg.Log.Level, cfg.Log.Format)
	slog.Info("starting ai-review-agent server", "store_driver", cfg.Store.Driver)

	stores, err := store.New(cfg.Store)
	if err != nil {
		slog.Error("store initialization failed", "error", err)
		os.Exit(1)
	}
	defer stores.Close()

	gitlabClient := gitlab.NewClient(cfg.GitLab.BaseURL, cfg.GitLab.Token)
	gitManager := git.NewManager(cfg.Git.ReposDir, cfg.GitLab.BaseURL, cfg.GitLab.Token)
	jobQueue := queue.NewQueue()
	gatherer := review.NewContextGatherer(gitlabClient, stores.RepoSettings, stores.Feedbacks, cfg.GitLab.BotUserID)
	reviewAgent := reviewer.NewAgent()
	replyAgent := replier.NewAgent()

	reviewPipeline := review.NewPipeline(review.PipelineDeps{
		Config:        *cfg,
		JobStore:      stores.ReviewJobs,
		RepoSettings:  stores.RepoSettings,
		RecordStore:   stores.ReviewRecords,
		FeedbackStore: stores.Feedbacks,
		GitLabClient:  gitlabClient,
		GitManager:    gitManager,
		Gatherer:      gatherer,
		Agent:         reviewAgent,
	})

	replyPipeline := reply.NewPipeline(reply.PipelineDeps{
		Config:        *cfg,
		ReplyJobStore: stores.ReplyJobs,
		RepoSettings:  stores.RepoSettings,
		FeedbackStore: stores.Feedbacks,
		GitLabClient:  gitlabClient,
		ReplyAgent:    replyAgent,
		ReposDir:      cfg.Git.ReposDir,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workerPool := worker.NewWorkerPool(cfg.Worker.PoolSize, worker.WorkerDeps{
		Queue:          jobQueue,
		ReviewPipeline: reviewPipeline,
		ReplyPipeline:  replyPipeline,
		ReviewJobStore: stores.ReviewJobs,
		ReplyJobStore:  stores.ReplyJobs,
	})
	workerPool.Start(ctx)

	cronRunner := cron.NewRunner()
	consolidatorLLM, err := llm.NewBalancedClientFromConfig(cfg.LLM, nil)
	if err != nil {
		slog.Warn("feedback consolidator LLM init failed, cron disabled", "error", err)
	} else {
		consolidator := feedback.NewConsolidator(
			stores.Feedbacks, stores.RepoSettings, consolidatorLLM,
			cfg.Cron.FeedbackConsolidateMinCount,
			cfg.Cron.FeedbackConsolidateMinAgeDays,
			cfg.Cron.FeedbackCustomPromptMaxWords,
		)
		consolidatorJob := cron.NewFeedbackConsolidatorJob(stores.RepoSettings, consolidator)
		if err := cronRunner.Register(cfg.Cron.FeedbackConsolidateSchedule, "feedback_consolidator", consolidatorJob.Run); err != nil {
			slog.Error("register feedback consolidator cron", "error", err)
		}
	}

	staleRecovery := cron.NewStaleJobRecoveryJob(stores.ReviewJobs, 35)
	if err := cronRunner.Register("*/5 * * * *", "stale_job_recovery", staleRecovery.Run); err != nil {
		slog.Error("register stale job recovery cron", "error", err)
	}

	cronRunner.Start()
	defer cronRunner.Stop()

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)

	webhookHandler := webhook.NewHandler(webhook.HandlerDeps{
		WebhookSecret:      cfg.GitLab.WebhookSecret,
		BotUserID:          cfg.GitLab.BotUserID,
		ReviewTriggerLabel: cfg.Review.TriggerLabel,
		RepoSettings:       stores.RepoSettings,
		ReviewJobStore:     stores.ReviewJobs,
		ReplyJobStore:      stores.ReplyJobs,
		GitLabClient:       gitlabClient,
		Queue:              jobQueue,
		ServerCtx:          ctx,
	})

	r.Post("/webhook/gitlab", webhookHandler.HandleGitLabEvent)
	r.Get("/health", webhookHandler.HandleHealth)

	addr := fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port)
	server := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	slog.Info("server listening", "addr", addr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-sigCh
	slog.Info("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	server.Shutdown(shutdownCtx)
	cancel()                  // Stop workers and handler goroutines
	jobQueue.Close()          // Wake up any workers blocking on GetNextJob
	webhookHandler.Shutdown() // Wait for in-flight webhook goroutines

	workerDone := make(chan struct{})
	go func() { workerPool.Wait(); close(workerDone) }()
	select {
	case <-workerDone:
		slog.Info("all workers stopped gracefully")
	case <-shutdownCtx.Done():
		slog.Warn("shutdown timeout — some workers may still be running")
	}

	slog.Info("server stopped")
}
