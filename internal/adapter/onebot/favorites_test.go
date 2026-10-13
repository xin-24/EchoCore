package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/message"
	protocol "github.com/xin-24/EchoCore/internal/onebot"
)

func TestFavoriteDecode(t *testing.T) {
	data := json.RawMessage(`[{"resId":"a","url":"https://example/a?token=one","desc":"开心"},{"resId":"a","url":"https://example/a?token=two"},{"url":"x"},{"resId":"market","url":"x","isMarkFace":true},{"md5":"0123456789abcdef0123456789abcdef","url":"https://example/b"}]`)
	batch, err := decodeFavorites(data, 48)
	if err != nil || len(batch.Items) != 2 || batch.Skipped != 3 || batch.Items[0].SourceID != "a" {
		t.Fatal(batch, err)
	}
	for _, raw := range []string{`null`, `{}`, `"x"`} {
		if _, err := decodeFavorites(json.RawMessage(raw), 48); err == nil {
			t.Fatal("接受了未知结构")
		}
	}
	if _, err := decodeFavorites(json.RawMessage(`[]`), 48); err != nil {
		t.Fatal("空列表应可用")
	}
}

func TestCallerSharesEchoWithSenderAndClose(t *testing.T) {
	writer := &channelActionWriter{payloads: make(chan []byte, 2)}
	sender := NewActionSender(writer)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results := make(chan error, 2)
	go func() {
		_, e := sender.Call(ctx, "fetch_custom_face_detail", protocol.ActionParams{"count": 48})
		results <- e
	}()
	go func() {
		_, _, e := sender.Send(ctx, message.OutgoingMessage{Platform: message.PlatformQQ, UserID: "200", Text: "ok"})
		results <- e
	}()
	var first, second protocol.Action
	if json.Unmarshal(<-writer.payloads, &first) != nil || json.Unmarshal(<-writer.payloads, &second) != nil {
		t.Fatal("解析失败")
	}
	if string(first.Echo) == string(second.Echo) {
		t.Fatal("echo 冲突")
	}
	for _, a := range []protocol.Action{second, first} {
		sender.Resolve(protocol.ActionResponse{Echo: a.Echo, Status: protocol.ActionStatusOK, Data: json.RawMessage(`[]`)})
	}
	for range 2 {
		if e := <-results; e != nil {
			t.Fatal(e)
		}
	}
	go func() { _, e := sender.Call(ctx, "get_login_info", nil); results <- e }()
	<-writer.payloads
	sender.Close()
	if e := <-results; !errors.Is(e, ErrDisconnected) {
		t.Fatal(e)
	}
	if len(sender.pending) != 0 {
		t.Fatal("断线遗留 pending")
	}
}

func TestFavoriteResponseLogsHideData(t *testing.T) {
	raw := redactMessageContent([]byte(`{"status":"ok","retcode":0,"echo":"x","data":[{"url":"https://cdn.example?token=secret"}],"message":"secret"}`))
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "cdn.example") {
		t.Fatal("泄漏签名资源")
	}
}
