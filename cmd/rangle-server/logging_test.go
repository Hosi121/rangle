package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestCloudLogSeverityAndMessage(t *testing.T) {
	for _, test := range []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug, "DEBUG"},
		{slog.LevelInfo, "INFO"},
		{slog.LevelWarn, "WARNING"},
		{slog.LevelError, "ERROR"},
		{slog.LevelWarn + 1, "WARNING"},
	} {
		t.Run(test.level.String(), func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{
				ReplaceAttr: cloudLogAttribute,
				Level:       slog.LevelDebug,
			})).With("service", "rangle-server")
			logger.Log(context.Background(), test.level, "generation job submitted",
				"job_id", "job-123", "provider_name", "projects/example/locations/asia-northeast1/customJobs/456",
				slog.Group("detail", "level", "unchanged", "msg", "unchanged"))
			var entry map[string]any
			if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			if entry["severity"] != test.want || entry["message"] != "generation job submitted" ||
				entry["service"] != "rangle-server" || entry["job_id"] != "job-123" || entry["provider_name"] == nil {
				t.Fatalf("Cloud Logging identity/severity lost: %v", entry)
			}
			if _, exists := entry["level"]; exists {
				t.Fatal("Cloud Logging severity was left in the payload level field")
			}
			if detail := entry["detail"].(map[string]any); detail["level"] != "unchanged" || detail["msg"] != "unchanged" {
				t.Fatalf("nested attributes were renamed: %v", detail)
			}
		})
	}
}

func TestCloudLogAttributeAllowsAnOrdinaryLevelAttribute(t *testing.T) {
	attr := slog.String("level", "application-defined")
	if got := cloudLogAttribute(nil, attr); !got.Equal(attr) {
		t.Fatalf("application attribute changed: %v", got)
	}
}
