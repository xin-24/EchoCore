package sticker

import (
	"context"
	"errors"
	"testing"
)

func TestAdminPreviewRevisionAndApproval(t *testing.T) {
	s, token, source := testFavorites(t)
	if err := s.Sync(context.Background(), "100", token, source); err != nil {
		t.Fatal(err)
	}
	item := s.AdminList()[0]
	if item.Approval != "pending" || item.Revision == "" {
		t.Fatal(item)
	}
	if data, err := s.AdminPreview("100", item.ID); err != nil || !ValidImage(data) {
		t.Fatal("未批准素材应可审核", err)
	}
	if s.Snapshot("100") != nil {
		t.Fatal("预览授予了发送权限")
	}
	if _, err := s.AdminPreview("999", item.ID); err == nil {
		t.Fatal("跨账号预览")
	}
	yes, no := true, false
	if err := s.AdminUpdate("100", item.ID, "", "", &yes, item.Revision); err != nil {
		t.Fatal(err)
	}
	if err := s.AdminUpdate("100", item.ID, "", "", &no, item.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("旧版本未拒绝", err)
	}
	item = s.AdminList()[0]
	if err := s.Update("100", item.ID, "新名称", "新描述", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AdminUpdate("100", item.ID, "", "", &no, item.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("QQ 修改未触发冲突", err)
	}
	item = s.AdminList()[0]
	if err := s.AdminUpdate("100", item.ID, "", "", &no, item.Revision); err != nil {
		t.Fatal(err)
	}
	if s.AdminList()[0].Approval != "disabled" {
		t.Fatal("停用与待批准未区分")
	}
}
