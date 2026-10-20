package admin

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/memory"
	"github.com/xin-24/EchoCore/internal/message"
	"github.com/xin-24/EchoCore/internal/sticker"
)

// 仅由隔离浏览器验收显式启动；不加载真实配置、QQ、模型或用户数据库。
func TestE2EServer(t *testing.T) {
	if os.Getenv("ECHOCORE_ADMIN_E2E") != "1" {
		t.Skip("仅用于显式浏览器验收")
	}
	directory := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 160, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 160; x++ {
			img.Set(x, y, color.RGBA{uint8(80 + x/2), uint8(120 + y/3), 125, 255})
		}
	}
	f, err := os.Create(filepath.Join(directory, "demo.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	manifest := []byte("stickers:\n  - id: demo\n    name: 测试欢迎图\n    description: 仅用于隔离验收，不是真实 QQ 素材\n    file: demo.png\n    enabled: true\n")
	if err = os.WriteFile(filepath.Join(directory, "catalog.yaml"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := sticker.Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Origin: "http://127.0.0.1:15173", BackendHost: "127.0.0.1:18081", Key: testKey})
	if err != nil {
		t.Fatal(err)
	}
	s.WithStickers(nil, catalog)
	// 在临时目录播种一个未批准的收藏缓存，不请求 QQ 或外网。
	cfg := sticker.DefaultFavoritesConfig()
	cfg.Enabled = true
	cfg.Directory = filepath.Join(directory, "favorites")
	favorites, err := sticker.OpenFavorites(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	favorites.Close()
	data, err := os.ReadFile(filepath.Join(directory, "demo.png"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if err = os.WriteFile(filepath.Join(cfg.Directory, hash+".img"), data, 0600); err != nil {
		t.Fatal(err)
	}
	record := sticker.FavoriteRecord{Candidate: sticker.Candidate{ID: "qq_demo", Name: "测试收藏图", Description: "隔离测试的欢迎表情"}, Account: "100", Hash: hash, Size: int64(len(data)), FetchedAt: time.Now()}
	raw, _ := json.Marshal(record)
	db, err := sql.Open("sqlite3", filepath.Join(cfg.Directory, "favorites.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO favorite_records(id,value) VALUES(?,?)", record.ID, string(raw))
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	favorites, err = sticker.OpenFavorites(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer favorites.Close()
	// 上面注册的处理器替换为使用隔离收藏服务的新实例。
	s, err = New(Options{Origin: "http://127.0.0.1:15173", BackendHost: "127.0.0.1:18081", Key: testKey})
	if err != nil {
		t.Fatal(err)
	}
	s.WithStickers(favorites, catalog)
	mcfg := memory.DefaultConfig()
	mcfg.Enabled = true
	mcfg.Database = filepath.Join(directory, "memory.db")
	mcfg.AllowedPrivateUsers = []string{"200", "201"}
	mcfg.Auto.Enabled = true
	mcfg.Auto.Mode = "review_all"
	mcfg.Auto.TriggerCount = 1
	mcfg.Auto.Grants = []memory.Grant{{SelfID: "100", Type: "private", ConversationID: "201", UserIDs: []string{"201"}}}
	memories, err := memory.Open(context.Background(), mcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer memories.Close()
	yes, no := true, false
	if err = memories.StartAuto(context.Background(), memory.AutoOptions{
		Extractor:  memory.FakeExtractor{Items: []memory.Proposal{{Kind: "preference", Topic: "code_explanation", Content: "用户偏好中文代码解释", Sources: []memory.Evidence{{Ref: "s1", Quote: "以后讲代码用中文吧"}}, SelfReport: &yes, Sensitive: &no, Uncertain: &no}}},
		Reconciler: memory.FakeReconciler{Items: []memory.ReconcileItem{{Index: 0, Decision: "new"}}}, Now: func() time.Time { return time.Now().Add(-2 * time.Second) },
	}); err != nil {
		t.Fatal(err)
	}
	memories.Collect(message.IncomingMessage{Platform: "qq", SelfID: "100", UserID: "201", MessageID: "browser-only", Text: "以后讲代码用中文吧", Timestamp: time.Now()})
	s.WithMemory(memories)
	server := &http.Server{Addr: "127.0.0.1:18081", Handler: s}
	t.Log("隔离管理测试服务启动：18081")
	if err = server.ListenAndServe(); err != nil {
		t.Fatal(err)
	}
}
