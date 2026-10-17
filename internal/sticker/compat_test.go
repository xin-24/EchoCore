package sticker

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestFavoriteRevisionCompatibility(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"{}", ""},
		{`{"Revision":1}`, "1"},
		{`{"Revision":0}`, "0"},
		{`{"Revision":9007199254740993}`, "9007199254740993"},
		{`{"Revision":"nonce-123"}`, "nonce-123"},
		{`{"Revision":""}`, ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			record, err := decodeFavoriteRecord([]byte(tc.input))
			if err != nil || record.Revision != tc.want {
				t.Fatalf("版本号不兼容: %q, %v", record.Revision, err)
			}
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			var revision string
			if err := json.Unmarshal(fields["Revision"], &revision); err != nil || revision != tc.want {
				t.Fatal("保存格式必须统一为字符串")
			}
		})
	}
	for _, input := range []string{"null", "true", "{}", "[]", "-1", "1.5", "1e2"} {
		_, err := decodeFavoriteRecord([]byte(`{"Revision":` + input + `}`))
		if !errors.Is(err, ErrFavoriteStore) || !strings.Contains(err.Error(), "Revision") {
			t.Fatalf("非法版本号缺少明确错误: %s: %v", input, err)
		}
	}
}

func TestFavoritesReopenLegacyRevisionPreservesData(t *testing.T) {
	s, _, _ := testFavorites(t)
	raw := `{"id":"qq_legacy","name":"旧表情","description":"开心","Account":"100","Approved":true,"Override":true,"Revision":1,"Size":123,"FetchedAt":"2026-10-14T17:25:52+08:00"}`
	if _, err := s.db.Exec("INSERT INTO favorite_records(id,value) VALUES(?,?)", "qq_legacy", raw); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenFavorites(s.config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	record := reopened.records["qq_legacy"]
	if record.Revision != "1" || !record.Approved || !record.Override || record.Name != "旧表情" || record.Description != "开心" || record.Account != "100" || record.Size != 123 {
		t.Fatalf("旧收藏数据丢失: %+v", record)
	}
	var stored string
	if err := s.db.QueryRow("SELECT value FROM favorite_records WHERE id=?", "qq_legacy").Scan(&stored); err != nil || stored != raw {
		t.Fatal("读取兼容不得改写原始记录", err)
	}
}

func TestFavoritesStartupErrorIsSpecificAndPrivate(t *testing.T) {
	for _, tc := range []struct{ raw, stage string }{
		{`{"id":"qq_bad","Revision":true,"description":"secret-content"}`, "Revision"},
		{`{"id":"qq_bad","Account":false,"description":"secret-content"}`, "JSON"},
		{`{"id":"qq_bad","Account":"invalid","description":"secret-content"}`, "校验"},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			s, _, _ := testFavorites(t)
			if _, err := s.db.Exec("INSERT INTO favorite_records(id,value) VALUES(?,?)", "qq_bad", tc.raw); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenFavorites(s.config, nil)
			if reopened != nil {
				reopened.Close()
				t.Fatal("无效数据不应加载成功")
			}
			if !errors.Is(err, ErrFavoriteStore) || !strings.Contains(err.Error(), tc.stage) || strings.Contains(err.Error(), "secret-content") || strings.Contains(err.Error(), s.config.Directory) {
				t.Fatalf("错误不明确或泄漏数据: %v", err)
			}
		})
	}
}
