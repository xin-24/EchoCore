package settings

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/xin-24/EchoCore/internal/llm"
)

var ErrMissingJevKey = errors.New("缺少 Jev 环境密钥")

func compileDecision(base llm.Client, cfg llm.JevConfig, strict bool) (llm.Client, bool, error) {
	var primary llm.Decider
	reason := "jev_disabled"
	if cfg.Enabled {
		key := os.Getenv("ECHOCORE_JEV_API_KEY")
		if strings.TrimSpace(key) == "" {
			if strict {
				return nil, false, ErrMissingJevKey
			}
			reason = "jev_unavailable"
		} else {
			client, err := llm.NewJevClient(cfg, key)
			if err != nil {
				return nil, false, err
			}
			primary = client
		}
	}
	if base == nil {
		return nil, false, nil
	}
	return &llm.CascadeClient{Base: base, Primary: primary, Threshold: cfg.MinConfidence, Timeout: time.Duration(cfg.TimeoutMS) * time.Millisecond, UnavailableReason: reason}, primary != nil, nil
}
