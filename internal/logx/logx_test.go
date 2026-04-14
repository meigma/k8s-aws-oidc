package logx

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNewLogger_JSONAndTextCarryEventFields(t *testing.T) {
	tests := []struct {
		name   string
		format Format
		assert func(t *testing.T, raw string)
	}{
		{
			name:   "json",
			format: FormatJSON,
			assert: func(t *testing.T, raw string) {
				t.Helper()
				var record map[string]any
				if err := json.Unmarshal([]byte(raw), &record); err != nil {
					t.Fatalf("unmarshal json log: %v", err)
				}
				if got := record["component"]; got != "test_component" {
					t.Fatalf("component = %v", got)
				}
				if got := record["event"]; got != "test_event" {
					t.Fatalf("event = %v", got)
				}
				if got := record["answer"]; got != float64(42) {
					t.Fatalf("answer = %v", got)
				}
			},
		},
		{
			name:   "text",
			format: FormatText,
			assert: func(t *testing.T, raw string) {
				t.Helper()
				for _, want := range []string{
					"component=test_component",
					"event=test_event",
					"answer=42",
				} {
					if !strings.Contains(raw, want) {
						t.Fatalf("log %q missing %q", raw, want)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger, err := NewLogger(&buf, tt.format, &slog.HandlerOptions{Level: slog.LevelInfo})
			if err != nil {
				t.Fatalf("NewLogger: %v", err)
			}

			Info(context.Background(), logger, "test_component", "test_event", "test message", slog.Int("answer", 42))
			raw := strings.TrimSpace(buf.String())
			if raw == "" {
				t.Fatal("empty log output")
			}
			tt.assert(t, raw)
		})
	}
}
