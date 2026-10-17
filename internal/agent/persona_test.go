package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/message"
	"github.com/xin-24/EchoCore/internal/settings"
)

func TestFilePersonaReplacesHardcodedStyleForBothConversations(t *testing.T) {
	role := "# 角色设定\n你是小喵，自然聊天，不刻意凑字数。"
	for _, isGroup := range []bool{false, true} {
		executor, _, fake := executorTest(t, llm.FakeOptions{Response: llm.GenerateResponse{Text: "你好"}}, ExecutorOptions{PersonaPrompt: role, PersonaStyle: "不应叠加的旧风格"})
		incoming := message.IncomingMessage{Platform: message.PlatformQQ, SelfID: "1", UserID: "2", GroupID: "group", IsGroup: isGroup, Mentioned: true, Text: "你好"}
		work := executor.Prepare(incoming)
		if work.Execute == nil {
			t.Fatal("未创建任务")
		}
		_, ok := work.Execute(context.Background())
		work.Finish()
		if !ok {
			t.Fatal("生成失败")
		}
		_, requests := fake.Requests()
		prompt := requests[0].SystemPrompt
		if !strings.HasPrefix(prompt, role) || !strings.Contains(prompt, "EchoCore 运行约束") {
			t.Fatal("未使用角色文件或缺少运行约束")
		}
		for _, old := range []string{"不超过 25 字", "不超过 100 字", "鸽子娘", "不应叠加的旧风格"} {
			if strings.Contains(prompt, old) {
				t.Fatalf("残留旧人设：%s", old)
			}
		}
		if !isGroup && !strings.Contains(prompt, "当前会话是私聊") {
			t.Fatal("私聊场景未覆盖文件背景")
		}
	}
	options := optionsFromSettings(ExecutorOptions{PersonaPrompt: role}, settings.Values{})
	if options.PersonaPrompt != role {
		t.Fatal("运行配置覆盖了文件人设快照")
	}
}
