package media

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadSafety(t *testing.T) {
	d := NewDownloader([]string{"p.qpic.cn"})
	for _, address := range []string{"http://p.qpic.cn/a", "https://p.qpic.cn.evil/a", "https://user:pass@p.qpic.cn/a", "https://127.0.0.1/a", "file:///etc/passwd", "https://p.qpic.cn:444/a", "https://p.qpic.cn/a#secret"} {
		if _, err := d.Download(context.Background(), address); err == nil {
			t.Fatalf("危险地址被接收: %s", address)
		}
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "::ffff:192.168.1.1", "100.64.0.1", "2001:db8::1", "198.18.0.1"} {
		if publicIP(netip.MustParseAddr(ip)) {
			t.Fatal("未拒绝非公网地址", ip)
		}
	}
	// 生产传输层即使允许列表配置 localhost，也必须在 DNS 解析后拒绝回环。
	local := NewDownloader([]string{"localhost"})
	transport := local.client.Transport.(*http.Transport)
	if conn, err := transport.DialContext(context.Background(), "tcp", net.JoinHostPort("localhost", "443")); err == nil {
		conn.Close()
		t.Fatal("DNS 回环绕过")
	}
	bad, _ := url.Parse("https://127.0.0.1/a")
	if d.client.CheckRedirect(&http.Request{URL: bad}, nil) == nil {
		t.Fatal("重定向绕过")
	}
	for _, size := range []int{0, MaxDownloadBytes + 1, 42} {
		d.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, ContentLength: -1, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", size)))}, nil
		})
		data, err := d.Download(context.Background(), "https://p.qpic.cn/test")
		if (size == 42) != (err == nil) || len(data) > MaxDownloadBytes {
			t.Fatal("流式下载大小限制失效")
		}
	}
}

func TestImagePreprocessing(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2000, 10)))
	data, err := Prepare(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" || cfg.Width != 1600 {
		t.Fatal(cfg, format, err)
	}
	for _, raw := range [][]byte{nil, []byte("GIF89a"), []byte("not-image"), make([]byte, MaxDownloadBytes+1)} {
		if _, err := Prepare(raw); err == nil {
			t.Fatal("非法图片未拒绝")
		}
	}
}
