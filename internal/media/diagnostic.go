package media

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"strings"
)

// DownloadError 只携带脱敏诊断，不保存原始 URL 或底层错误正文。
type DownloadError struct {
	Kind, Stage, Host, Scheme string
	HTTPStatus                int
}

func (e *DownloadError) Error() string { return "图片下载失败: " + e.Stage + "/" + e.Kind }
func (e *DownloadError) Unwrap() error { return ErrDownload }

func safeSource(u *url.URL) (string, string) {
	if u == nil {
		return "", ""
	}
	host := strings.ToLower(u.Hostname())
	if len(host) > 253 {
		host = ""
	}
	for _, r := range host {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune(".-:", r)) {
			host = ""
			break
		}
	}
	scheme := u.Scheme
	switch scheme {
	case "http", "https", "file", "data", "":
	default:
		scheme = "other"
	}
	return host, scheme
}

func downloadError(kind, stage string, u *url.URL, status int) *DownloadError {
	host, scheme := safeSource(u)
	return &DownloadError{kind, stage, host, scheme, status}
}

func networkError(err error, stage string, u *url.URL) *DownloadError {
	var known *DownloadError
	if errors.As(err, &known) {
		return known
	}
	kind := "network_error"
	var network net.Error
	var dns *net.DNSError
	var certificate *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var record tls.RecordHeaderError
	switch {
	case errors.Is(err, context.Canceled):
		kind = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		kind = "timeout"
	case errors.As(err, &network) && network.Timeout():
		kind = "timeout"
	case errors.As(err, &dns):
		kind = "dns_failed"
	case errors.As(err, &certificate), errors.As(err, &authority), errors.As(err, &record):
		kind = "tls_failed"
	}
	return downloadError(kind, stage, u, 0)
}

// DownloadLogFields 分开保留原始来源与实际失败目标（可能为重定向目标）。
func DownloadLogFields(address string, err error) []any {
	source, _ := url.Parse(address)
	host, scheme := safeSource(source)
	failure := networkError(err, "request", source)
	return []any{"source_host", host, "source_scheme", scheme,
		"error_kind", failure.Kind, "download_stage", failure.Stage,
		"target_host", failure.Host, "target_scheme", failure.Scheme,
		"http_status", failure.HTTPStatus}
}
