package group

import (
	"strconv"
	"sync"
	"testing"
)

func TestStateStoreLifecycle(t *testing.T) {
	store := NewStateStore()
	if store.Enabled("100") || store.SetEnabled("100", false) {
		t.Fatal("新群应默认为关闭")
	}
	if !store.SetEnabled("100", true) || !store.Enabled("100") {
		t.Fatal("开启后应记录本群状态")
	}
	if store.SetEnabled("100", true) {
		t.Fatal("重复开启不应视为状态变化")
	}
	if store.Enabled("200") {
		t.Fatal("开启一个群不应影响其他群")
	}
	if NewStateStore().Enabled("100") {
		t.Fatal("新的服务实例不应恢复旧群状态")
	}
	if !store.SetEnabled("100", false) || store.Enabled("100") {
		t.Fatal("关闭后应清除本群开启状态")
	}
	if store.SetEnabled("", true) || store.Enabled("") {
		t.Fatal("空群 ID 不应被开启")
	}
}

func TestStateStoreConcurrentAccess(t *testing.T) {
	store := NewStateStore()
	var workers sync.WaitGroup
	for worker := range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			id := strconv.Itoa(worker + 1)
			for range 100 {
				store.SetEnabled(id, true)
				if !store.Enabled(id) {
					t.Error("其他群的并发操作影响了本群状态")
				}
				store.SetEnabled("shared", true)
				store.Enabled("shared")
				store.SetEnabled("shared", false)
				store.SetEnabled(id, false)
			}
		}()
	}
	workers.Wait()
	for worker := range 16 {
		if store.Enabled(strconv.Itoa(worker + 1)) {
			t.Fatal("并发操作完成后应为关闭状态")
		}
	}
}

func TestAllowlist(t *testing.T) {
	ids := []string{"100", "100", ""}
	permissions := NewAllowlist(ids)
	ids[0] = "200"
	if !permissions.CanControl("100") || permissions.CanControl("200") || permissions.CanControl("") {
		t.Fatal("仅配置中的非空用户 ID 应获得权限，且不受原切片修改影响")
	}
	if NewAllowlist(nil).CanControl("100") {
		t.Fatal("空白名单不应授权任何人")
	}
}
