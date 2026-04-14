package logx

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Format is the supported structured log output format.
type Format string

const (
	FormatJSON Format = "json"
	FormatText Format = "text"
)

// ParseFormat validates a configured log format.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(FormatJSON):
		return FormatJSON, nil
	case string(FormatText):
		return FormatText, nil
	default:
		return "", fmt.Errorf("LOG_FORMAT: unrecognized %q", s)
	}
}

// NewHandler constructs a slog handler for the requested format.
func NewHandler(w io.Writer, format Format, opts *slog.HandlerOptions) (slog.Handler, error) {
	switch format {
	case FormatJSON:
		return slog.NewJSONHandler(w, opts), nil
	case FormatText:
		return slog.NewTextHandler(w, opts), nil
	default:
		return nil, fmt.Errorf("unsupported log format %q", format)
	}
}

// NewLogger constructs a logger for the requested format.
func NewLogger(w io.Writer, format Format, opts *slog.HandlerOptions) (*slog.Logger, error) {
	h, err := NewHandler(w, format, opts)
	if err != nil {
		return nil, err
	}
	return slog.New(h), nil
}

// Log records a structured event with the required component and event fields.
func Log(
	ctx context.Context,
	logger *slog.Logger,
	level slog.Level,
	component string,
	event string,
	msg string,
	attrs ...slog.Attr,
) {
	if logger == nil {
		logger = slog.Default()
	}
	base := []slog.Attr{
		slog.String("component", component),
		slog.String("event", event),
	}
	logger.LogAttrs(ctx, level, msg, append(base, attrs...)...)
}

func Debug(
	ctx context.Context,
	logger *slog.Logger,
	component string,
	event string,
	msg string,
	attrs ...slog.Attr,
) {
	Log(ctx, logger, slog.LevelDebug, component, event, msg, attrs...)
}

func Info(
	ctx context.Context,
	logger *slog.Logger,
	component string,
	event string,
	msg string,
	attrs ...slog.Attr,
) {
	Log(ctx, logger, slog.LevelInfo, component, event, msg, attrs...)
}

func Warn(
	ctx context.Context,
	logger *slog.Logger,
	component string,
	event string,
	msg string,
	attrs ...slog.Attr,
) {
	Log(ctx, logger, slog.LevelWarn, component, event, msg, attrs...)
}

func Error(
	ctx context.Context,
	logger *slog.Logger,
	component string,
	event string,
	msg string,
	attrs ...slog.Attr,
) {
	Log(ctx, logger, slog.LevelError, component, event, msg, attrs...)
}
