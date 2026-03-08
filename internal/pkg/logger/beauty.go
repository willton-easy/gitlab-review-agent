package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"
)

type BeautyHandler struct {
	opts  slog.HandlerOptions
	out   io.Writer
	attrs []slog.Attr
}

func NewBeautyHandler(out io.Writer, opts *slog.HandlerOptions) *BeautyHandler {
	return &BeautyHandler{
		opts: *opts,
		out:  out,
	}
}

func (h *BeautyHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= h.opts.Level.Level()
}

func (h *BeautyHandler) Handle(ctx context.Context, r slog.Record) error {
	level := r.Level.String()
	levelColor := ""
	reset := "\033[0m"

	switch r.Level {
	case slog.LevelDebug:
		levelColor = "\033[34m" // Blue
	case slog.LevelInfo:
		levelColor = "\033[32m" // Green
	case slog.LevelWarn:
		levelColor = "\033[33m" // Yellow
	case slog.LevelError:
		levelColor = "\033[31m" // Red
	}

	timeStr := r.Time.Format("15:04:05.000")

	// Get source information
	sourceStr := ""
	if h.opts.AddSource {
		fs := runtime.CallersFrames([]uintptr{r.PC})
		f, _ := fs.Next()
		if f.File != "" {
			sourceStr = formatSource(&slog.Source{
				Function: f.Function,
				File:     f.File,
				Line:     f.Line,
			})
		}
	}

	// Basic log line: TIME LEVEL MESSAGE
	fmt.Fprintf(h.out, "%s %s%-5s%s \033[2m%s\033[0m %s",
		timeStr,
		levelColor, level, reset,
		sourceStr,
		r.Message,
	)

	// Append pre-set attributes
	for _, a := range h.attrs {
		fmt.Fprintf(h.out, " \033[36m%s\033[0m=%v", a.Key, a.Value.Any())
	}

	// Append record attributes
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(h.out, " \033[36m%s\033[0m=%v", a.Key, a.Value.Any())
		return true
	})

	if r.Level >= slog.LevelError {
		fmt.Fprint(h.out, "\n  \033[31mStacktrace:\033[0m\n")
		pcs := make([]uintptr, 20)
		// Skip frames: runtime.Callers + our Handle + internal slog
		n := runtime.Callers(3, pcs)
		frames := runtime.CallersFrames(pcs[:n])
		frameCount := 0
		recording := false
		for {
			frame, more := frames.Next()
			if !recording {
				// Prevent logging 'log/slog' internals and our own logger.go
				if !strings.Contains(frame.File, "log/slog") && !strings.HasSuffix(frame.File, "logger.go") {
					recording = true
				}
			}
			if recording {
				fmt.Fprintf(h.out, "    \033[36m%s\033[0m\n      \033[2m%s:%d\033[0m\n", frame.Function, frame.File, frame.Line)
				frameCount++
				if frameCount >= 5 {
					break
				}
			}
			if !more {
				break
			}
		}
	}

	fmt.Fprintln(h.out)
	return nil
}

func (h *BeautyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	copy(newAttrs[len(h.attrs):], attrs)
	return &BeautyHandler{
		opts:  h.opts,
		out:   h.out,
		attrs: newAttrs,
	}
}

func (h *BeautyHandler) WithGroup(name string) slog.Handler {
	// Grouping not implemented for BeautyHandler for simplicity
	return h
}
