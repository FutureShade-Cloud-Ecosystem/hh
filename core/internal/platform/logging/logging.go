// Package logging configures the global slog JSON logger.
package logging

import (
	"log/slog"
	"os"
)

func New(env string) *slog.Logger {
	level := slog.LevelInfo
	if env == "dev" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
