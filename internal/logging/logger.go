package logging

import (
	"bytes"
	"io"
	"log/slog"
	"sync"

	"github.com/xin-24/EchoCore/internal/config"
)

const (
	colorReset  = "\x1b[0m"
	colorCyan   = "\x1b[36m"
	colorGreen  = "\x1b[32m"
	colorYellow = "\x1b[33m"
	colorRed    = "\x1b[31m"
)

// New 根据运行环境创建 Logger。
// development 使用彩色 Console 日志并启用 DEBUG，production 使用 INFO 级别 JSON 日志。
func New(writer io.Writer, environment config.Environment) *slog.Logger {
	if environment == config.EnvironmentProduction {
		return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	colored := &levelColorWriter{writer: writer}
	handler := slog.NewTextHandler(colored, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(_ []string, attribute slog.Attr) slog.Attr {
			if attribute.Key == slog.TimeKey {
				return slog.String(slog.TimeKey, attribute.Value.Time().Format("15:04:05"))
			}
			return attribute
		},
	})
	return slog.New(handler)
}

type levelColorWriter struct {
	writer io.Writer
	mu     sync.Mutex
}

func (w *levelColorWriter) Write(payload []byte) (int, error) {
	colored := append([]byte(nil), payload...)
	colored = bytes.Replace(colored, []byte("level=DEBUG"), []byte("level="+colorCyan+"DEBUG"+colorReset), 1)
	colored = bytes.Replace(colored, []byte("level=INFO"), []byte("level="+colorGreen+"INFO"+colorReset), 1)
	colored = bytes.Replace(colored, []byte("level=WARN"), []byte("level="+colorYellow+"WARN"+colorReset), 1)
	colored = bytes.Replace(colored, []byte("level=ERROR"), []byte("level="+colorRed+"ERROR"+colorReset), 1)

	w.mu.Lock()
	defer w.mu.Unlock()
	written, err := w.writer.Write(colored)
	if err != nil {
		return 0, err
	}
	if written != len(colored) {
		return 0, io.ErrShortWrite
	}
	return len(payload), nil
}
