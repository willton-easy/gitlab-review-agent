package logger

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

func Setup(level, format string) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level:     lvl,
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.SourceKey {
				source, ok := a.Value.Any().(*slog.Source)
				if ok && source != nil {
					a.Value = slog.StringValue(formatSource(source))
				}
			}
			return a
		},
	}

	var handler slog.Handler
	format = strings.ToLower(format)
	switch format {
	case "beauty":
		handler = NewBeautyHandler(os.Stdout, opts)
	case "console":
		handler = slog.NewTextHandler(os.Stdout, opts)
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	default:
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
}

func formatSource(s *slog.Source) string {
	// Show folder/file.go:line
	dir := filepath.Base(filepath.Dir(s.File))
	file := filepath.Base(s.File)
	return fmt.Sprintf("%s/%s:%d", dir, file, s.Line)
}
