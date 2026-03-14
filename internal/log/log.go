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
	levelAssign  slog.Level = 2
	levelWrite   slog.Level = 4
	levelTraffic slog.Level = 6
)

func (l *Log) Traffic(msg string, args ...any) {
	l.Log(context.Background(), levelTraffic, msg, args...)
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
				case levelAssign:
					a.Value = slog.StringValue("ASSIGNMENT")
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
