package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sync/atomic"
	"time"
)

var errLimit = errors.New("response_limit")

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, block := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return true
}
func secureTransport(endpoint string) *http.Transport {
	u, _ := url.Parse(endpoint)
	t := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 16 << 10}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil || host != u.Hostname() || port != "443" {
			return nil, errors.New("endpoint_denied")
		}
		ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil || len(ips) == 0 {
			return nil, errors.New("dns_failed")
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, errors.New("address_denied")
			}
		}
		var d net.Dialer
		for _, ip := range ips {
			conn, e := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return conn, nil
			}
		}
		return nil, errors.New("connect_failed")
	}
	return t
}

// boundedTransport 对整个操作累计计数，并禁止重放 tools/call。
type boundedTransport struct {
	base            http.RoundTripper
	endpoint, token string
	ctx             context.Context
	remaining       atomic.Int64
	calls           atomic.Int32
	status          atomic.Int32
}

func (t *boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.ctx.Err() != nil {
		return nil, t.ctx.Err()
	}
	if r.URL.String() != t.endpoint {
		return nil, errors.New("endpoint_denied")
	}
	if r.Method != "POST" && r.Method != "DELETE" {
		return nil, errors.New("method_denied")
	}
	r = r.Clone(r.Context())
	if r.Method == "POST" {
		raw, e := io.ReadAll(io.LimitReader(r.Body, 65537))
		r.Body.Close()
		if e != nil || len(raw) > 65536 {
			return nil, errors.New("request_limit")
		}
		var msg struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(raw, &msg) != nil {
			return nil, errors.New("invalid_request")
		}
		if msg.Method == "tools/call" && t.calls.Add(1) > 1 {
			return nil, errors.New("replay_denied")
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
	}
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	r = r.WithContext(ctx)
	if t.token != "" {
		r.Header.Set("Authorization", "Bearer "+t.token)
	}
	response, e := t.base.RoundTrip(r)
	if e != nil {
		stop()
		cancel()
		return nil, e
	}
	t.status.Store(int32(response.StatusCode))
	if response.ContentLength > t.remaining.Load() {
		response.Body.Close()
		stop()
		cancel()
		return nil, errLimit
	}
	response.Body = &limitedBody{body: response.Body, budget: &t.remaining, done: func() { stop(); cancel() }}
	return response, nil
}

type limitedBody struct {
	body   io.ReadCloser
	budget *atomic.Int64
	done   func()
}

func (b *limitedBody) Read(p []byte) (int, error) {
	left := b.budget.Load()
	if left < 0 {
		return 0, errLimit
	}
	if int64(len(p)) > left+1 {
		p = p[:left+1]
	}
	n, e := b.body.Read(p)
	if b.budget.Add(-int64(n)) < 0 {
		b.Close()
		return 0, errLimit
	}
	return n, e
}
func (b *limitedBody) Close() error { e := b.body.Close(); b.done(); return e }
