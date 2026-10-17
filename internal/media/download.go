// Package media 负责收到的图片的安全下载和预处理。
package media

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const MaxDownloadBytes = 5 << 20

var ErrDownload = errors.New("图片下载失败或未通过安全校验")

// Downloader 只接受明确允许的 HTTPS 主机，直连已校验 IP，不继承代理与凭据。
type Downloader struct {
	hosts  map[string]bool
	client *http.Client
}

func NewDownloader(hosts []string) *Downloader {
	d := &Downloader{hosts: map[string]bool{}}
	for _, host := range hosts {
		d.hosts[host] = true
	}
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, DisableKeepAlives: true}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		target := &url.URL{Scheme: "https", Host: host}
		if err != nil || port != "443" || !d.hosts[host] {
			return nil, downloadError("host_or_port_not_allowed", "dial", target, 0)
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, networkError(err, "dns", target)
		}
		if len(ips) == 0 {
			return nil, downloadError("dns_failed", "dns", target, 0)
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, downloadError("non_public_ip", "dns", target, 0)
			}
		}
		var dialer net.Dialer
		var lastErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, networkError(lastErr, "dial", target)
	}
	d.client = &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return downloadError("redirect_limit", "redirect", req.URL, 0)
		}
		if kind := d.rejection(req.URL); kind != "" {
			return downloadError(kind, "redirect", req.URL, 0)
		}
		return nil
	}}
	return d
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, block := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return true
}

func (d *Downloader) allowed(u *url.URL) bool {
	return d.rejection(u) == ""
}

func (d *Downloader) rejection(u *url.URL) string {
	if u == nil || u.Hostname() == "" || u.Opaque != "" {
		return "invalid_url"
	}
	if u.Scheme != "https" {
		return "scheme_not_allowed"
	}
	if u.User != nil || u.Fragment != "" {
		return "invalid_url"
	}
	if u.Port() != "" && u.Port() != "443" {
		return "port_not_allowed"
	}
	if !d.hosts[u.Hostname()] || strings.HasSuffix(u.Hostname(), ".") {
		return "host_not_allowed"
	}
	return ""
}

func (d *Downloader) Download(ctx context.Context, address string) ([]byte, error) {
	u, err := url.Parse(address)
	if address == "" {
		return nil, downloadError("missing_url", "validate", nil, 0)
	}
	if err != nil {
		return nil, downloadError("invalid_url", "validate", nil, 0)
	}
	if kind := d.rejection(u); kind != "" {
		return nil, downloadError(kind, "validate", u, 0)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, downloadError("invalid_url", "request", u, 0)
	}
	response, err := d.client.Do(req)
	if err != nil {
		return nil, networkError(err, "request", u)
	}
	defer response.Body.Close()
	target := u
	if response.Request != nil {
		target = response.Request.URL
	}
	if response.StatusCode != http.StatusOK {
		return nil, downloadError("http_error", "response", target, response.StatusCode)
	}
	if response.ContentLength > MaxDownloadBytes {
		return nil, downloadError("too_large", "response", target, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxDownloadBytes+1))
	if err != nil {
		return nil, networkError(err, "read_body", target)
	}
	if len(data) == 0 {
		return nil, downloadError("empty_body", "read_body", target, response.StatusCode)
	}
	if len(data) > MaxDownloadBytes {
		return nil, downloadError("too_large", "read_body", target, response.StatusCode)
	}
	return data, nil
}
