package logger

import (
	"context"
	"io"
	"log/slog"
)

type Log struct {
	*slog.Logger
}

const (
	levelSched    slog.Level = 1
	levelAssign   slog.Level = 2
	levelPipeline slog.Level = 3
	levelWrite    slog.Level = 4
	levelTraffic  slog.Level = 6
)

func (l *Log) Traffic(msg string, args ...any) {
	l.Log(context.Background(), levelTraffic, msg, args...)
}

// Sched logs scheduler-side events: piece pool state, peer registry, assignments.
func (l *Log) Sched(msg string, args ...any) {
	l.Log(context.Background(), levelSched, msg, args...)
}

// Pipe logs per-peer pipeline state: active piece, block window, in-flight requests.
func (l *Log) Pipe(msg string, args ...any) {
	l.Log(context.Background(), levelPipeline, msg, args...)
}

func (l *Log) Assignment(msg string, args ...any) {
	l.Log(context.Background(), levelAssign, msg, args...)
}

func (l *Log) Write(msg string, args ...any) {
	l.Log(context.Background(), levelWrite, msg, args...)
}

func New(w io.Writer) *Log {
	opts := &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				level := a.Value.Any().(slog.Level)
				switch level {
				case levelSched:
					a.Value = slog.StringValue("SCHED")
				case levelAssign:
					a.Value = slog.StringValue("ASSIGNMENT")
				case levelPipeline:
					a.Value = slog.StringValue("PIPELINE")
				case levelWrite:
					a.Value = slog.StringValue("WRITE")
				case levelTraffic:
					a.Value = slog.StringValue("TRAFFIC")
				}
			}

			if a.Key == slog.TimeKey && len(groups) == 0 {
				return slog.Attr{}
			}

			return a
		},
	}

	logger := slog.New(slog.NewTextHandler(w, opts))
	slog.SetDefault(logger)

	return &Log{logger}
}
