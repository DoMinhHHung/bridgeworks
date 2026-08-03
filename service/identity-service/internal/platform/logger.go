package platform

import (
	"io"
	"log/slog"
	"os"
)

func NewLogger(serviceName string, level slog.Level) *slog.Logger {
	return NewLoggerWithWriter(os.Stdout, serviceName, level)
}

func NewLoggerWithWriter(writer io.Writer, serviceName string, level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})
	return slog.New(handler).With("service", serviceName)
}
