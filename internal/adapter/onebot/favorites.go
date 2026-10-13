package onebot

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	protocol "github.com/xin-24/EchoCore/internal/onebot"
	"github.com/xin-24/EchoCore/internal/sticker"
)

var ErrFavoriteResponse = errors.New("收藏接口返回结构不兼容，请核验 NapCat 版本")

// QQFavoriteSource 仅查询收藏，不提供任何写入 QQ 收藏的能力。
type QQFavoriteSource struct {
	Caller  Caller
	Account string
}

func (s QQFavoriteSource) Fetch(ctx context.Context, count int) (sticker.FavoriteBatch, error) {
	if err := s.verify(ctx); err != nil {
		return sticker.FavoriteBatch{}, err
	}
	response, err := s.Caller.Call(ctx, "fetch_custom_face_detail", protocol.ActionParams{"count": count})
	if err != nil {
		return sticker.FavoriteBatch{}, err
	}
	if err := s.verify(ctx); err != nil {
		return sticker.FavoriteBatch{}, err
	}
	return decodeFavorites(response.Data, count)
}

func (s QQFavoriteSource) verify(ctx context.Context) error {
	if s.Account == "" {
		return nil
	}
	r, err := s.Caller.Call(ctx, "get_login_info", nil)
	var login struct {
		UserID json.Number `json:"user_id"`
	}
	if err != nil {
		return err
	}
	if json.Unmarshal(r.Data, &login) != nil || string(login.UserID) != s.Account {
		return ErrFavoriteResponse
	}
	return nil
}

// 字段依据 NapCat NodeIKernelMsgService.fetchFavEmojiList；未知结构拒绝猜测。
// 不使用基本 URL 列表降级：它无法可靠提供审批所需的稳定来源身份。
func decodeFavorites(data json.RawMessage, limit int) (sticker.FavoriteBatch, error) {
	var rows []json.RawMessage
	if len(data) > 2<<20 || json.Unmarshal(data, &rows) != nil || rows == nil || len(rows) > 500 || limit < 1 {
		return sticker.FavoriteBatch{}, ErrFavoriteResponse
	}
	result := sticker.FavoriteBatch{}
	seen := map[string]bool{}
	for i, raw := range rows {
		if i >= limit {
			result.Skipped++
			continue
		}
		var row struct {
			ResID      string `json:"resId"`
			MD5        string `json:"md5"`
			URL        string `json:"url"`
			Desc       string `json:"desc"`
			IsMarkFace bool   `json:"isMarkFace"`
			IsAPNG     bool   `json:"isAPNG"`
		}
		if json.Unmarshal(raw, &row) != nil || row.IsMarkFace || row.IsAPNG || row.URL == "" || len(row.URL) > 4096 || !utf8.ValidString(row.Desc) {
			result.Skipped++
			continue
		}
		id := row.ResID
		if id == "" {
			if decoded, e := hex.DecodeString(row.MD5); e == nil && len(decoded) == 16 {
				id = "md5:" + strings.ToLower(row.MD5)
			}
		}
		if id == "" || len(id) > 512 || seen[id] {
			result.Skipped++
			continue
		}
		seen[id] = true
		if utf8.RuneCountInString(row.Desc) > 200 {
			row.Desc = string([]rune(row.Desc)[:200])
		}
		result.Items = append(result.Items, sticker.RemoteFavorite{SourceID: id, URL: row.URL, Description: strings.TrimSpace(row.Desc)})
	}
	return result, nil
}
