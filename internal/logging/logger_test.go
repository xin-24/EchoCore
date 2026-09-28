package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/xin-24/EchoCore/internal/config"
)

func TestDevelopmentLoggerUsesColoredConsoleAndDebugLevel(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, config.EnvironmentDevelopment)

	logger.Debug("raw event", "component", "onebot.websocket")
	logger.Info("message received", "user_id", "20002000")
	logger.Warn("queue busy")
	logger.Error("send failed")

	logs := output.String()
	if !strings.Contains(logs, "level="+colorCyan+"DEBUG"+colorReset) {
		t.Fatalf("DEBUG color missing from %q", logs)
	}
	if !strings.Contains(logs, "level="+colorGreen+"INFO"+colorReset) {
		t.Fatalf("INFO color missing from %q", logs)
	}
	if !strings.Contains(logs, "level="+colorYellow+"WARN"+colorReset) {
		t.Fatalf("WARN color missing from %q", logs)
	}
	if !strings.Contains(logs, "level="+colorRed+"ERROR"+colorReset) {
		t.Fatalf("ERROR color missing from %q", logs)
	}
	if !strings.Contains(logs, "component=onebot.websocket") {
		t.Fatalf("component missing from %q", logs)
	}
	if json.Valid(bytes.TrimSpace(output.Bytes())) {
		t.Fatalf("development log unexpectedly uses JSON: %q", logs)
	}
}

func TestProductionLoggerUsesJSONAndHidesDebug(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, config.EnvironmentProduction)

	logger.Debug("hidden raw event")
	logger.Info("service ready", "address", "127.0.0.1:8080")

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("log line count = %d, want 1; logs: %q", len(lines), output.String())
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("json.Unmarshal(log) error = %v; log: %q", err, lines[0])
	}
	if got, want := entry[slog.MessageKey], "service ready"; got != want {
		t.Fatalf("message = %v, want %q", got, want)
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("production log contains ANSI color: %q", output.String())
	}
}
