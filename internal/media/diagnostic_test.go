package media

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestDownloadDiagnosticsValidation(t *testing.T) {
	d := NewDownloader([]string{"p.qpic.cn"})
	for _, tc := range []struct{ address, kind, host, scheme string }{
		{"", "missing_url", "", ""},
		{"https://other.example/private?key=SECRET", "host_not_allowed", "other.example", "https"},
		{"http://p.qpic.cn/private?key=SECRET", "scheme_not_allowed", "p.qpic.cn", "http"},
		{"https://p.qpic.cn:444/private", "port_not_allowed", "p.qpic.cn", "https"},
		{"https://user:SECRET@p.qpic.cn/private", "invalid_url", "p.qpic.cn", "https"},
		{"https://p.qpic.cn/%zz?key=SECRET", "invalid_url", "", ""},
	} {
		_, err := d.Download(context.Background(), tc.address)
		var failure *DownloadError
		if !errors.Is(err, ErrDownload) || !errors.As(err, &failure) || failure.Kind != tc.kind || failure.Host != tc.host || failure.Scheme != tc.scheme {
			t.Fatalf("诊断错误: %+v", failure)
		}
		fields := fmt.Sprint(DownloadLogFields(tc.address, err))
		if strings.Contains(fields, "SECRET") || strings.Contains(fields, "private") || strings.Contains(err.Error(), "SECRET") {
			t.Fatal("诊断泄漏链接内容", fields)
		}
	}
}

func TestDownloadDiagnosticsNetworkAndResponse(t *testing.T) {
	d := NewDownloader([]string{"p.qpic.cn"})
	for _, tc := range []struct {
		err  error
		kind string
	}{
		{context.DeadlineExceeded, "timeout"},
		{context.Canceled, "canceled"},
		{&net.DNSError{Err: "SECRET", Name: "private.example"}, "dns_failed"},
		{tls.RecordHeaderError{Msg: "SECRET"}, "tls_failed"},
		{errors.New("SECRET"), "network_error"},
	} {
		d.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, tc.err })
		_, err := d.Download(context.Background(), "https://p.qpic.cn/private?key=SECRET")
		var failure *DownloadError
		if !errors.As(err, &failure) || failure.Kind != tc.kind || strings.Contains(err.Error(), "SECRET") {
			t.Fatal(err)
		}
	}
	for _, status := range []int{403, 404, 500} {
		d.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Request: r, Body: io.NopCloser(strings.NewReader("SECRET"))}, nil
		})
		_, err := d.Download(context.Background(), "https://p.qpic.cn/private")
		var failure *DownloadError
		if !errors.As(err, &failure) || failure.Kind != "http_error" || failure.HTTPStatus != status {
			t.Fatal(err)
		}
	}
	target, _ := url.Parse("https://redirect.example/private?key=SECRET")
	err := d.client.CheckRedirect(&http.Request{URL: target}, nil)
	// 模拟 net/http 包装，仍能取出重定向失败目标，而不输出包装错误里的 URL。
	wrapped := &url.Error{Op: "Get", URL: target.String(), Err: err}
	fields := fmt.Sprint(DownloadLogFields("https://p.qpic.cn/private", wrapped))
	if !strings.Contains(fields, "redirect.example") || !strings.Contains(fields, "host_not_allowed") || !strings.Contains(fields, "redirect") || strings.Contains(fields, "SECRET") || strings.Contains(fields, "private") {
		t.Fatal(fields)
	}
}
