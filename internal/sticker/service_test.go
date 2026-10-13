package sticker

import (
	"context"
	"errors"
	"github.com/xin-24/EchoCore/internal/llm"
	"net/netip"
	"net/url"
	"testing"
	"time"
)

type favoriteSourceFunc func(context.Context, int) (FavoriteBatch, error)

func (f favoriteSourceFunc) Fetch(ctx context.Context, n int) (FavoriteBatch, error) {
	return f(ctx, n)
}

func testFavorites(t *testing.T) (*Service, uint64, FavoriteSource) {
	t.Helper()
	cfg := DefaultFavoritesConfig()
	cfg.Enabled = true
	cfg.Directory = t.TempDir()
	s, err := OpenFavorites(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	data := testImage(t, true)
	s.download = func(context.Context, string) ([]byte, error) { return data, nil }
	token, _ := s.Bind(context.Background(), "100", func() {})
	source := favoriteSourceFunc(func(context.Context, int) (FavoriteBatch, error) {
		return FavoriteBatch{Items: []RemoteFavorite{{SourceID: "one", URL: "https://images.example/secret?token=private", Description: "开心猫猫"}}}, nil
	})
	return s, token, source
}

func TestFavoritesApprovalIsolationAndRevocation(t *testing.T) {
	s, token, source := testFavorites(t)
	if err := s.Sync(context.Background(), "100", token, source); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot("100") != nil {
		t.Fatal("新素材不应自动批准")
	}
	list := s.List("100")
	if len(list) != 1 {
		t.Fatal(list)
	}
	id := list[0].ID
	yes, no := true, false
	if err := s.Update("999", id, "", "", &yes); err == nil {
		t.Fatal("跨账号审批")
	}
	if err := s.Update("100", id, "猫咪", "表达开心", &yes); err != nil {
		t.Fatal(err)
	}
	view := s.Snapshot("100")
	if view == nil || len(view.Search("开心", 8)) != 1 || s.Snapshot("999") != nil {
		t.Fatal("账号或候选错误")
	}
	if _, data, err := view.Resolve(id); err != nil || !ValidImage(data) {
		t.Fatal("图片解析失败", err)
	}
	if err := s.Update("100", id, "", "", &no); err != nil {
		t.Fatal(err)
	}
	if _, _, err := view.Resolve(id); err == nil {
		t.Fatal("旧快照越过停用")
	}
	called := false
	if err := view.(GuardedLibrary).Guard(id, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("停用后仍发送")
	}
	if err := s.Update("100", id, "", "", &yes); err != nil {
		t.Fatal(err)
	}
	s.Bind(context.Background(), "100", func() {})
	if _, _, err := view.Resolve(id); err == nil {
		t.Fatal("重连后旧快照仍有效")
	}
}

func TestFavoritesFailureExpiryAndPersistence(t *testing.T) {
	s, token, source := testFavorites(t)
	if err := s.Sync(context.Background(), "100", token, source); err != nil {
		t.Fatal(err)
	}
	id := s.List("100")[0].ID
	yes := true
	if err := s.Update("100", id, "猫", "开心", &yes); err != nil {
		t.Fatal(err)
	}
	broken := favoriteSourceFunc(func(context.Context, int) (FavoriteBatch, error) { return FavoriteBatch{}, errors.New("private-url") })
	if err := s.Sync(context.Background(), "100", token, broken); err == nil || s.Snapshot("100") == nil {
		t.Fatal("失败破坏了快照")
	}
	status, _ := s.Status("100")
	if status.Error != "query_failed" {
		t.Fatal(status)
	}
	// 第二个实例只读取持久化结果；不恢复任何在线连接授权。
	reopened, err := OpenFavorites(s.config, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Snapshot("100") != nil {
		t.Fatal("未登录即恢复在线授权")
	}
	reopened.Bind(context.Background(), "100", func() {})
	if reopened.Snapshot("100") == nil || !reopened.List("100")[0].Approved {
		t.Fatal("审批没有持久化")
	}
	s.mu.Lock()
	r := s.records[id]
	r.FetchedAt = time.Now().Add(-25 * time.Hour)
	s.records[id] = r
	s.mu.Unlock()
	if s.Snapshot("100") != nil {
		t.Fatal("过期素材仍可选")
	}
}

func TestFavoritesDownloadFailureKeepsMetadataAndContentChangeRevokes(t *testing.T) {
	s, token, source := testFavorites(t)
	s.download = func(context.Context, string) ([]byte, error) { return nil, ErrDownload }
	if err := s.Sync(context.Background(), "100", token, source); err != nil {
		t.Fatal(err)
	}
	if len(s.List("100")) != 1 || s.List("100")[0].Hash != "" {
		t.Fatal("失败素材元数据丢失")
	}
	yes := true
	id := s.List("100")[0].ID
	if err := s.Update("100", id, "猫", "开心", &yes); err != nil {
		t.Fatal(err)
	}
	data := testImage(t, false)
	s.download = func(context.Context, string) ([]byte, error) { return data, nil }
	if err := s.Sync(context.Background(), "100", token, source); err != nil {
		t.Fatal(err)
	}
	// 首次看到实际内容必须重新批准，避免批准未知文件。
	if s.Snapshot("100") != nil {
		t.Fatal("未知内容沿用旧审批")
	}
	if err := s.Update("100", id, "", "", &yes); err != nil {
		t.Fatal(err)
	}
	data = testImage(t, true)
	if err := s.Sync(context.Background(), "100", token, source); err != nil {
		t.Fatal(err)
	}
	if s.Snapshot("100") != nil {
		t.Fatal("内容替换没有撤销审批")
	}
}

func TestFavoritesSyncCanceledAndMerged(t *testing.T) {
	s, token, _ := testFavorites(t)
	started := make(chan struct{})
	release := make(chan struct{})
	source := favoriteSourceFunc(func(context.Context, int) (FavoriteBatch, error) {
		close(started)
		<-release
		return FavoriteBatch{}, nil
	})
	done := make(chan error, 1)
	go func() { done <- s.Sync(context.Background(), "100", token, source) }()
	<-started
	if err := s.Sync(context.Background(), "100", token, source); err == nil {
		t.Fatal("并发同步未合并")
	}
	s.Bind(context.Background(), "100", func() {})
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("旧连接仍发布", err)
	}
}

func TestDownloaderRejectsUnsafeTargets(t *testing.T) {
	d := NewDownloader([]string{"cdn.example"})
	for _, raw := range []string{"http://cdn.example/a", "https://cdn.example:8443/a", "https://user:pass@cdn.example/a", "https://127.0.0.1/a", "https://cdn.example.evil/a", "file:///secret", "https://cdn.example./a"} {
		u, _ := url.Parse(raw)
		if d.allowed(u) {
			t.Fatal("接受危险 URL", raw)
		}
		if _, err := d.Download(context.Background(), raw); err == nil {
			t.Fatal("危险请求未拒绝")
		}
	}
	for _, address := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.1.1", "2001:db8::1", "224.1.1.1", "0.0.0.0", "64:ff9b::7f00:1"} {
		if publicIP(netip.MustParseAddr(address)) {
			t.Fatal("非公网 IP 未过滤", address)
		}
	}
	if !publicIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("公网判断错误")
	}
}

func TestFavoriteSelectorUsesOnlyApprovedSnapshot(t *testing.T) {
	s, token, source := testFavorites(t)
	if err := s.Sync(context.Background(), "100", token, source); err != nil {
		t.Fatal(err)
	}
	id := s.List("100")[0].ID
	yes := true
	if err := s.Update("100", id, "", "", &yes); err != nil {
		t.Fatal(err)
	}
	view := s.Snapshot("100")
	g := &favoriteGenerator{id: id}
	plan, err := (Selector{Library: view, Config: DefaultConfig()}).Generate(context.Background(), g, llm.GenerateRequest{})
	if err != nil || plan.StickerID != id || plan.Text != "哈哈" {
		t.Fatal(plan, err)
	}
	if _, data, err := view.Resolve(plan.StickerID); err != nil || !ValidImage(data) {
		t.Fatal("选择后无法解析")
	}
	if err := s.Forget("100", id); err != nil {
		t.Fatal(err)
	}
	if err := view.(GuardedLibrary).Guard(id, func() error { t.Error("删除后发送"); return nil }); err == nil {
		t.Fatal("旧快照绕过删除")
	}
}

type favoriteGenerator struct{ id string }

func (g *favoriteGenerator) Generate(_ context.Context, r llm.GenerateRequest) (llm.GenerateResponse, error) {
	if len(r.ToolHistory) == 0 {
		return llm.GenerateResponse{ToolCalls: []llm.ToolCall{{ID: "list", Name: "list_stickers", Arguments: `{"query":""}`}}}, nil
	}
	return llm.GenerateResponse{ToolCalls: []llm.ToolCall{{ID: "select", Name: "select_sticker", Arguments: `{"id":"` + g.id + `","text":"哈哈"}`}}}, nil
}
