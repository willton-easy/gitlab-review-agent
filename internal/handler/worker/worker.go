package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"

	"ai-review-agent/internal/pkg/queue"
	"ai-review-agent/internal/shared"
)

// ReviewPipelineExecutor is called for review jobs.
type ReviewPipelineExecutor interface {
	Execute(ctx context.Context, job *shared.ReviewJob) error
}

// ReplyPipelineExecutor is called for reply jobs.
type ReplyPipelineExecutor interface {
	Execute(ctx context.Context, job *shared.ReplyJob) error
}

type WorkerDeps struct {
	Queue          *queue.Queue
	ReviewPipeline ReviewPipelineExecutor
	ReplyPipeline  ReplyPipelineExecutor
	ReviewJobStore shared.ReviewJobStore
	ReplyJobStore  shared.ReplyJobStore
}

type Worker struct {
	id             string
	queue          *queue.Queue
	reviewPipeline ReviewPipelineExecutor
	replyPipeline  ReplyPipelineExecutor
	reviewJobStore shared.ReviewJobStore
	replyJobStore  shared.ReplyJobStore
}

type WorkerPool struct {
	workers []*Worker
	wg      sync.WaitGroup
}

func NewWorkerPool(n int, deps WorkerDeps) *WorkerPool {
	pool := &WorkerPool{}
	for i := 0; i < n; i++ {
		worker := &Worker{
			id:             uuid.New().String(),
			queue:          deps.Queue,
			reviewPipeline: deps.ReviewPipeline,
			replyPipeline:  deps.ReplyPipeline,
			reviewJobStore: deps.ReviewJobStore,
			replyJobStore:  deps.ReplyJobStore,
		}
		pool.workers = append(pool.workers, worker)
	}
	return pool
}

func (p *WorkerPool) Start(ctx context.Context) {
	for _, w := range p.workers {
		p.wg.Add(1)
		go func(w *Worker) {
			defer p.wg.Done()
			w.run(ctx)
		}(w)
	}
}

func (p *WorkerPool) Wait() {
	p.wg.Wait()
}

func (w *Worker) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// GetNextJob blocks until a job is available or ctx is cancelled
		job, projectID, err := w.queue.GetNextJob(ctx, w.id)
		if err != nil {
			if ctx.Err() != nil {
				return // shutting down
			}
			slog.Error("failed to get next job", "error", err)
			continue
		}
		if job == nil {
			// Queue closed or context cancelled
			return
		}

		if err := w.processJob(ctx, job); err != nil {
			slog.Error("job failed",
				"job_id", job.JobID.String(),
				"job_type", string(job.Type),
				"error", err,
			)
			_ = w.queue.SendToDLQ(ctx, *job, err.Error())
		}

		_ = w.queue.ReleaseLock(ctx, projectID, w.id)
	}
}

func (w *Worker) processJob(ctx context.Context, job *shared.QueueJob) error {
	switch job.Type {
	case shared.QueueJobTypeReview:
		reviewJob, err := w.reviewJobStore.GetByID(ctx, job.JobID)
		if err != nil {
			return fmt.Errorf("load review job: %w", err)
		}
		if reviewJob == nil {
			return fmt.Errorf("review job not found: %s", job.JobID)
		}
		return w.reviewPipeline.Execute(ctx, reviewJob)

	case shared.QueueJobTypeReply:
		replyJob, err := w.replyJobStore.GetByID(ctx, job.JobID)
		if err != nil {
			return fmt.Errorf("load reply job: %w", err)
		}
		if replyJob == nil {
			return fmt.Errorf("reply job not found: %s", job.JobID)
		}
		return w.replyPipeline.Execute(ctx, replyJob)

	default:
		return fmt.Errorf("unknown job type: %s", job.Type)
	}
}
