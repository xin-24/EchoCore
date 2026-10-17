package admin

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"net/http"
	"strconv"
	"strings"

	"github.com/xin-24/EchoCore/internal/sticker"
)

// WithStickers 复用机器人同一素材服务，不打开第二份数据库。
func (s *Server) WithStickers(favorites *sticker.Service, local *sticker.Catalog) {
	s.routes.HandleFunc("GET "+Prefix+"/stickers", func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		if page < 1 || page > 1000 {
			fail(w, 422, "invalid_page", "页码无效")
			return
		}
		items := local.AdminList()
		if favorites != nil {
			items = append(items, favorites.AdminList()...)
		}
		filtered := []sticker.AdminItem{}
		q := strings.ToLower(r.URL.Query().Get("q"))
		source := r.URL.Query().Get("source")
		account := r.URL.Query().Get("account")
		approval := r.URL.Query().Get("approval")
		if len(q) > 256 {
			fail(w, 422, "invalid_query", "搜索内容过长")
			return
		}
		for _, item := range items {
			if (source == "" || source == item.Source) && (account == "" || account == item.Account) && (approval == "" || approval == item.Approval) && strings.Contains(strings.ToLower(item.Name+" "+item.Description+" "+item.ID), q) {
				filtered = append(filtered, item)
			}
		}
		total := len(filtered)
		start := (page - 1) * 24
		if start > total {
			start = total
		}
		end := start + 24
		if end > total {
			end = total
		}
		accounts := []string{}
		if favorites != nil {
			accounts = favorites.AdminAccounts()
		}
		reply(w, 200, map[string]any{"items": filtered[start:end], "total": total, "page": page, "page_size": 24, "accounts": accounts, "favorites_enabled": favorites != nil})
	})
	s.routes.HandleFunc("GET "+Prefix+"/stickers/{id}/preview", func(w http.ResponseWriter, r *http.Request) {
		var data []byte
		var err error
		switch r.URL.Query().Get("source") {
		case "local":
			_, data, err = local.Resolve(r.PathValue("id"))
		case "qq":
			if favorites != nil {
				data, err = favorites.AdminPreview(r.URL.Query().Get("account"), r.PathValue("id"))
			} else {
				err = sticker.ErrUnavailable
			}
		default:
			err = sticker.ErrUnavailable
		}
		if err != nil {
			fail(w, 404, "image_unavailable", "缓存图片不可用，请同步后重试")
			return
		}
		// 缩略图解码第一帧为 PNG，避免整页 GIF 自动播放。
		if r.URL.Query().Get("still") == "1" {
			img, _, e := image.Decode(bytes.NewReader(data))
			if e != nil {
				fail(w, 404, "image_unavailable", "缓存图片无效")
				return
			}
			var b bytes.Buffer
			if png.Encode(&b, img) != nil {
				fail(w, 503, "image_error", "预览生成失败")
				return
			}
			data = b.Bytes()
		}
		w.Header().Set("Content-Type", http.DetectContentType(data))
		w.Write(data)
	})
	s.routes.HandleFunc("PATCH "+Prefix+"/stickers/{id}", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Account     string `json:"account"`
			Revision    string `json:"revision"`
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !decode(w, r, &b) {
			return
		}
		if favorites == nil {
			fail(w, 503, "disabled", "QQ 收藏来源未启用")
			return
		}
		if strings.TrimSpace(b.Name) == "" || strings.TrimSpace(b.Description) == "" {
			fail(w, 422, "invalid_description", "名称和描述不能为空")
			return
		}
		if err := favorites.AdminUpdate(b.Account, r.PathValue("id"), b.Name, b.Description, nil, b.Revision); err != nil {
			stickerError(w, err)
			return
		}
		reply(w, 200, map[string]bool{"updated": true})
	})
	s.routes.HandleFunc("POST "+Prefix+"/stickers/batch", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Approved *bool `json:"approved"`
			Items    []struct {
				ID       string `json:"id"`
				Account  string `json:"account"`
				Revision string `json:"revision"`
			} `json:"items"`
		}
		if !decode(w, r, &b) {
			return
		}
		if b.Approved == nil || len(b.Items) < 1 || len(b.Items) > 50 {
			fail(w, 422, "invalid_batch", "每批必须为 1～50 项并指定批准或停用")
			return
		}
		if favorites == nil {
			fail(w, 503, "disabled", "QQ 收藏来源未启用")
			return
		}
		result := []map[string]any{}
		for _, item := range b.Items {
			err := favorites.AdminUpdate(item.Account, item.ID, "", "", b.Approved, item.Revision)
			code := "ok"
			if err != nil {
				code = "unavailable"
				if errors.Is(err, sticker.ErrConflict) {
					code = "conflict"
				}
			}
			result = append(result, map[string]any{"id": item.ID, "account": item.Account, "code": code})
		}
		reply(w, 200, result)
	})
	s.routes.HandleFunc("GET "+Prefix+"/sticker-sync", func(w http.ResponseWriter, r *http.Request) {
		if favorites == nil {
			reply(w, 200, map[string]any{"enabled": false})
			return
		}
		status, busy := favorites.Status(r.URL.Query().Get("account"))
		reply(w, 200, map[string]any{"enabled": true, "busy": busy, "queued": favorites.AdminQueued(r.URL.Query().Get("account")), "at": status.At, "received": status.Received, "ready": status.Ready, "skipped": status.Skipped, "failed": status.Failed, "error": status.Error, "resource_hosts": status.ResourceHosts})
	})
	s.routes.HandleFunc("POST "+Prefix+"/sticker-sync", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Account string `json:"account"`
		}
		if !decode(w, r, &b) {
			return
		}
		if favorites == nil {
			fail(w, 503, "disabled", "QQ 收藏来源未启用")
			return
		}
		_, busy := favorites.Status(b.Account)
		if busy || favorites.AdminQueued(b.Account) {
			fail(w, 409, "busy", "同步进行中")
			return
		}
		if !favorites.RequestSync(b.Account) {
			fail(w, 503, "offline", "账号尚未连接或未确认身份")
			return
		}
		reply(w, 202, map[string]bool{"requested": true})
	})
}
func stickerError(w http.ResponseWriter, err error) {
	if errors.Is(err, sticker.ErrConflict) {
		fail(w, 409, "conflict", "素材已变更，请刷新后重试")
	} else if errors.Is(err, sticker.ErrFavoriteStore) {
		fail(w, 503, "storage_error", "素材保存失败")
	} else {
		fail(w, 422, "invalid_sticker", "素材、描述或缓存无效")
	}
}
