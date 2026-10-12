package llm

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestJevProxyValidation(t *testing.T) {
	for _, address := range []string{"", "socks5h://127.0.0.1:7897", "socks5://localhost:7897", "http://localhost:8080", "https://[::1]:8080"} {
		cfg := DefaultJevConfig()
		cfg.ProxyURL = address
		if err := cfg.Validate(); err != nil {
			t.Fatalf("合法代理被拒绝：%q", address)
		}
	}
	for _, address := range []string{"ftp://localhost:21", "socks5h://localhost", "socks5h://:7897", "socks5h://localhost:0", "socks5h://localhost:65536", "socks5h://user:secret@localhost:7897", "socks5h://localhost:7897/path", "socks5h://localhost:7897?x=1", "socks5h://localhost:7897#x"} {
		cfg := DefaultJevConfig()
		cfg.ProxyURL = address
		if err := cfg.Validate(); err != ErrJevConfig {
			t.Fatalf("非法代理未被拒绝：%q", address)
		}
	}
}

func TestJevSOCKSProxyResolvesDomain(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	finished := make(chan error, 1)
	go func() {
		finished <- func() error {
			conn, err := listener.Accept()
			if err != nil {
				return err
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			// 本地模拟 SOCKS5 服务，验证客户端传递域名而非本机解析的 IP。
			header := make([]byte, 2)
			if _, err := io.ReadFull(conn, header); err != nil {
				return err
			}
			if header[0] != 5 {
				return fmt.Errorf("不是 SOCKS5")
			}
			if _, err := io.CopyN(io.Discard, conn, int64(header[1])); err != nil {
				return err
			}
			if _, err := conn.Write([]byte{5, 0}); err != nil {
				return err
			}
			request := make([]byte, 5)
			if _, err := io.ReadFull(conn, request); err != nil {
				return err
			}
			if request[0] != 5 || request[1] != 1 || request[3] != 3 {
				return fmt.Errorf("未使用域名连接")
			}
			target := make([]byte, int(request[4])+2)
			if _, err := io.ReadFull(conn, target); err != nil {
				return err
			}
			if string(target[:len(target)-2]) != "localhost" {
				return fmt.Errorf("目标域名错误")
			}
			if _, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80}); err != nil {
				return err
			}
			r, err := http.ReadRequest(bufio.NewReader(conn))
			if err != nil {
				return err
			}
			defer r.Body.Close()
			if r.URL.Path != "/systemone" || r.Header.Get("Authorization") != "Bearer test-key" {
				return fmt.Errorf("代理请求格式错误")
			}
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				return err
			}
			body := `{"answers":{"action":{"type":"choice","choice":"reply","confidence":0.9}}}`
			_, err = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
			return err
		}()
	}()
	cfg := DefaultJevConfig()
	cfg.Enabled, cfg.BaseURL, cfg.Path = true, "http://localhost", "/systemone"
	cfg.ProxyURL = "socks5h://" + listener.Addr().String()
	client, err := NewJevClient(cfg, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	defer client.http.CloseIdleConnections()
	if client.http.Transport == http.DefaultTransport {
		t.Fatal("不得修改全局传输层")
	}
	response, err := client.Decide(context.Background(), DecideRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if response.Decision != DecisionReply {
		t.Fatal("代理响应解析失败")
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	// 相同客户端在代理关闭后必须报错，不允许静默直连。
	listener.Close()
	if _, err := client.Decide(context.Background(), DecideRequest{}); err == nil {
		t.Fatal("代理不可用时应失败")
	}
}
