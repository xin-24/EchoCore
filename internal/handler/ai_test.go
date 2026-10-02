package handler_test

import (
	"strings"
	"testing"

	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/handler"
	"github.com/xin-24/EchoCore/internal/message"
)

func TestAICommandLifecycle(t *testing.T) {
	states := group.NewStateStore()
	dispatcher := message.NewDispatcher(handler.NewAI(states, group.NewAllowlist([]string{"20001"})))
	incoming := message.IncomingMessage{
		Platform: message.PlatformQQ, UserID: "20001", GroupID: "30001", IsGroup: true, Mentioned: true,
	}
	steps := []struct {
		command string
		want    string
		enabled bool
	}{
		{"/ai off", "本群 AI 参与开关已经关闭。", false},
		{" /ai  \t on ", "本群 AI 参与开关已开启。", true},
		{"/ai on", "本群 AI 参与开关已经开启。", true},
		{"/ai off", "本群 AI 参与开关已关闭。", false},
		{"/ai off", "本群 AI 参与开关已经关闭。", false},
	}
	for _, step := range steps {
		incoming.Text = step.command
		outgoing, handled := dispatcher.Dispatch(incoming)
		want := message.OutgoingMessage{Platform: message.PlatformQQ, GroupID: "30001", Text: step.want}
		if !handled || outgoing != want {
			t.Fatalf("Dispatch(%q) = %#v, %v; want %#v, true", step.command, outgoing, handled, want)
		}
		if states.Enabled("30001") != step.enabled || states.Enabled("30002") {
			t.Fatalf("%q 后群状态不符合预期", step.command)
		}
	}
}

func TestAICommandRejectsInvalidRequestsWithoutChangingState(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		user        string
		groupID     string
		private     bool
		unmentioned bool
		emptyList   bool
		want        string
	}{
		{name: "未授权开启", text: "/ai on", user: "90001", want: "没有操作"},
		{name: "未授权关闭", text: "/ai off", user: "90001", want: "没有操作"},
		{name: "空白名单", text: "/ai on", emptyList: true, want: "没有操作"},
		{name: "私聊不能改变群状态", text: "/ai on", private: true, want: "仅限群聊"},
		{name: "缺少群ID", text: "/ai on", groupID: "empty", want: "仅限群聊"},
		{name: "未提及机器人", text: "/ai off", unmentioned: true},
		{name: "缺少参数", text: "/ai", want: "用法"},
		{name: "未知参数", text: "/ai status", want: "用法"},
		{name: "多余参数", text: "/ai off 30002", want: "用法"},
		{name: "大小写错误", text: "/ai ON", want: "用法"},
		{name: "非命令前缀", text: "/aion"},
		{name: "普通文本", text: "你好"},
	}
	for _, test := range tests {
		for _, initiallyEnabled := range []bool{false, true} {
			t.Run(test.name+"/"+map[bool]string{false: "关闭", true: "开启"}[initiallyEnabled], func(t *testing.T) {
				states := group.NewStateStore()
				states.SetEnabled("30001", initiallyEnabled)
				ids := []string{"20001"}
				if test.emptyList {
					ids = nil
				}
				dispatcher := message.NewDispatcher(handler.NewAI(states, group.NewAllowlist(ids)))
				incoming := message.IncomingMessage{
					Platform: message.PlatformQQ, UserID: "20001", GroupID: "30001",
					IsGroup: !test.private, Mentioned: !test.unmentioned, Text: test.text,
				}
				if test.user != "" {
					incoming.UserID = test.user
				}
				if test.groupID == "empty" || test.private {
					incoming.GroupID = ""
				}
				outgoing, handled := dispatcher.Dispatch(incoming)
				if test.want == "" {
					if handled {
						t.Fatalf("消息应被忽略，实际回复：%#v", outgoing)
					}
				} else if !handled || !strings.Contains(outgoing.Text, test.want) {
					t.Fatalf("回复 = %#v, handled=%v；应包含 %q", outgoing, handled, test.want)
				}
				if states.Enabled("30001") != initiallyEnabled {
					t.Fatal("无效或未授权的请求修改了群状态")
				}
			})
		}
	}
}
