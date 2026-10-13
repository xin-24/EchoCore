package sticker

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadValidatesBodiesAndRedirects(t *testing.T) {
	d := NewDownloader([]string{"cdn.example"})
	data := testImage(t, true)
	d.client.Transport = testTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("下载携带了凭据")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: -1, Header: make(http.Header)}, nil
	})
	if got, err := d.Download(context.Background(), "https://cdn.example/image"); err != nil || !bytes.Equal(got, data) {
		t.Fatal("有效图片下载失败", err)
	}
	for _, body := range [][]byte{[]byte("not an image"), make([]byte, MaxImageBytes+1)} {
		data = body
		if _, err := d.Download(context.Background(), "https://cdn.example/image"); err == nil {
			t.Fatal("非法响应被接受")
		}
	}
	u, _ := url.Parse("https://127.0.0.1/private")
	if d.client.CheckRedirect(&http.Request{URL: u}, nil) == nil {
		t.Fatal("跳转到未授权主机")
	}
	u, _ = url.Parse("https://cdn.example/next")
	if d.client.CheckRedirect(&http.Request{URL: u}, make([]*http.Request, 3)) == nil {
		t.Fatal("跳转无限制")
	}
}
