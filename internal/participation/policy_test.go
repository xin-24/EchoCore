package participation

import "testing"

func TestPolicyBounds(t *testing.T) {
	for _, p := range []Policy{{}, {CooldownMS: 1000, WindowMS: 60000, MaxReplies: 2, RepeatWindowMS: 500}} {
		if p.Validate() != nil {
			t.Fatal("有效策略被拒绝")
		}
	}
	for _, p := range []Policy{{MaxReplies: 1}, {CooldownMS: -1}, {WindowMS: 3600001}, {MaxReplies: 1001, WindowMS: 1}, {RepeatWindowMS: 60001}} {
		if p.Validate() == nil {
			t.Fatal("无效策略被接受")
		}
	}
}
