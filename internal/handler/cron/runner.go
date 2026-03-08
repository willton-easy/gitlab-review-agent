package cron

import (
	"log/slog"

	"github.com/robfig/cron/v3"
)

type Runner struct {
	c *cron.Cron
}

func NewRunner() *Runner {
	return &Runner{
		c: cron.New(),
	}
}

func (r *Runner) Register(schedule, name string, job func()) error {
	_, err := r.c.AddFunc(schedule, func() {
		defer func() {
			if v := recover(); v != nil {
				slog.Error("cron job panicked", "job", name, "panic", v)
			}
		}()
		slog.Info("cron job started", "job", name)
		job()
		slog.Info("cron job finished", "job", name)
	})
	return err
}

func (r *Runner) Start() { r.c.Start() }
func (r *Runner) Stop()  { r.c.Stop() }
