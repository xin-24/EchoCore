package sticker

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

var ErrDownload = errors.New("收藏图片下载失败或未通过安全校验")

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
		if err != nil || port != "443" || !d.hosts[host] {
			return nil, ErrDownload
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, ErrDownload
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, ErrDownload
			}
		}
		var dialer net.Dialer
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, ErrDownload
	}
	d.client = &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !d.allowed(req.URL) {
			return ErrDownload
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
	return u != nil && u.Scheme == "https" && u.User == nil && u.Fragment == "" && (u.Port() == "" || u.Port() == "443") && d.hosts[u.Hostname()] && !strings.HasSuffix(u.Hostname(), ".")
}

func (d *Downloader) Download(ctx context.Context, address string) ([]byte, error) {
	u, err := url.Parse(address)
	if err != nil || !d.allowed(u) {
		return nil, ErrDownload
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrDownload
	}
	response, err := d.client.Do(req)
	if err != nil {
		return nil, ErrDownload
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > MaxImageBytes {
		return nil, ErrDownload
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxImageBytes+1))
	if err != nil || !ValidImage(data) {
		return nil, ErrDownload
	}
	return data, nil
}
