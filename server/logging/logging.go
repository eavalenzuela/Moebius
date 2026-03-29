package logging

import (
	"io"
	"log/slog"
	"os"

	"github.com/eavalenzuela/Moebius/shared/version"
)

// New creates a configured slog.Logger.
//   - format: "json" (default) or "text"
//   - level: "debug", "info" (default), "warn", "error"
//   - service: identifies the binary (e.g. "moebius-api")
func New(format, level, service string) *slog.Logger {
	return NewWithWriter(os.Stdout, format, level, service)
}

// NewWithWriter is like New but writes to w instead of stdout. Useful for testing.
func NewWithWriter(w io.Writer, format, level, service string) *slog.Logger {
	lvl := parseLevel(level)
	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	if format == "text" {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}

	return slog.New(handler).With(
		slog.String("service", service),
		slog.String("version", version.Version),
	)
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
