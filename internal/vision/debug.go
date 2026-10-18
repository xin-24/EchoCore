package vision

import (
	"context"
	"log/slog"

	"github.com/xin-24/EchoCore/internal/media"
)

// debugMedia 仅缓存有界元数据，不保留帧字节、Base64 或来源 URL。
type debugMedia struct {
	Type                                  string
	OriginalFrames, DurationMS, LoopCount int
	TimesMS                               []int
}

func debugMetadata(p media.Prepared) debugMedia {
	m := debugMedia{Type: p.Type, OriginalFrames: p.OriginalFrames, DurationMS: p.DurationMS, LoopCount: p.LoopCount}
	for _, frame := range p.Frames[:min(len(p.Frames), 8)] {
		m.TimesMS = append(m.TimesMS, frame.TimeMS)
	}
	return m
}

// logLocalDebug 输出视觉模型最终描述，不输出原始 API 响应或思考正文。
// 日志处理器负责转义换行等控制字符；生产配置禁止打开此开关。
func (s *Service) logLocalDebug(ctx context.Context, source string, m debugMedia, description string, correlation ...any) {
	if !s.config.DebugLocal || ctx.Err() != nil || !s.logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	fields := []any{"source", source, "media_type", m.Type, "original_frames", m.OriginalFrames, "sampled_frames", len(m.TimesMS), "frame_times_ms", m.TimesMS}
	if m.Type == "gif" {
		fields = append(fields, "duration_ms", m.DurationMS, "loop_count", m.LoopCount)
	}
	fields = append(fields, "description", description)
	fields = append(fields, correlation...)
	s.logger.DebugContext(ctx, "Vision local debug", fields...)
}
