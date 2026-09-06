package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNewUsesJSONAndConfiguredLevel(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, slog.LevelInfo)
	logger.Debug("hidden")
	logger.Info("visible", "component", "test")

	got := output.String()
	if strings.Contains(got, "hidden") || !strings.Contains(got, `"msg":"visible"`) || !strings.Contains(got, `"component":"test"`) {
		t.Fatalf("log output = %q", got)
	}
}
