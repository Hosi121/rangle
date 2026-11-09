package main

import "log/slog"

// Cloud Run promotes these JSON keys to LogEntry.severity and its display
// message. Plain slog level/msg would remain payload fields. Keep slog itself
// and its context/attributes; no collector SDK or network writer is needed.
func cloudLogAttribute(groups []string, attr slog.Attr) slog.Attr {
	if len(groups) != 0 {
		return attr
	}
	switch attr.Key {
	case slog.LevelKey:
		level, ok := attr.Value.Any().(slog.Level)
		if !ok {
			return attr
		}
		severity := "DEBUG"
		switch {
		case level >= slog.LevelError:
			severity = "ERROR"
		case level >= slog.LevelWarn:
			severity = "WARNING"
		case level >= slog.LevelInfo:
			severity = "INFO"
		}
		return slog.String("severity", severity)
	case slog.MessageKey:
		attr.Key = "message"
	}
	return attr
}
