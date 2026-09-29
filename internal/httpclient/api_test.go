package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakeSOCKS5 is a SOCKS5 proxy for tests. It accepts no-auth CONNECT
// requests, records the requested host and connects every tunnel to backend.
type fakeSOCKS5 struct {
	ln      net.Listener
	backend string

	mu    sync.Mutex
	hosts []string
}

func startFakeSOCKS5(t *testing.T, backend string) *fakeSOCKS5 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeSOCKS5{ln: ln, backend: backend}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go p.serve(conn)
		}
	}()
	return p
}

func (p *fakeSOCKS5) requestedHosts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.hosts...)
}

func (p *fakeSOCKS5) serve(conn net.Conn) {
	defer conn.Close()
	// Greeting: version, method count, methods. Reply "no auth".
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, head[1])); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}
	// Request: version, CONNECT, reserved, address type, address, port.
	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil {
		return
	}
	var host string
	switch req[3] {
	case 1:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 3:
		n := make([]byte, 1)
		if _, err := io.ReadFull(conn, n); err != nil {
			return
		}
		name := make([]byte, n[0])
		if _, err := io.ReadFull(conn, name); err != nil {
			return
		}
		host = string(name)
	default:
		return
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(conn, port); err != nil {
		return
	}
	p.mu.Lock()
	p.hosts = append(p.hosts, net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port)))))
	p.mu.Unlock()

	up, err := net.Dial("tcp", p.backend)
	if err != nil {
		return
	}
	defer up.Close()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go func() { _, _ = io.Copy(up, conn) }()
	_, _ = io.Copy(conn, up)
}

func TestNewAPIProxyAndUserAgent(t *testing.T) {
	uaCh := make(chan string, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uaCh <- r.Header.Get("User-Agent")
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(backend.Close)
	t.Cleanup(apiTransport.transport.CloseIdleConnections)
	backendAddr := backend.Listener.Addr().String()

	tests := []struct {
		name string
		// allProxy is the ALL_PROXY scheme. Empty means no proxy.
		allProxy string
		// url is the request URL. A proxied request uses a host that does
		// not resolve, so only the proxy can reach the backend.
		url string
		// setUA sets userAgent on the request before the call.
		setUA     bool
		userAgent string
		wantUA    string
		wantHost  string
	}{
		{
			name:   "direct request gets the cliamp User-Agent",
			url:    backend.URL + "/api",
			wantUA: UserAgent,
		},
		{
			name:     "ALL_PROXY socks5 carries the request",
			allProxy: "socks5",
			url:      "http://api.example.test:8096/api",
			wantUA:   UserAgent,
			wantHost: "api.example.test:8096",
		},
		{
			name:      "ALL_PROXY socks5h keeps a caller User-Agent",
			allProxy:  "socks5h",
			url:       "http://feeds.example.test/api",
			setUA:     true,
			userAgent: "podcast/2",
			wantUA:    "podcast/2",
			wantHost:  "feeds.example.test:80",
		},
		{
			name:      "an empty caller User-Agent sends none",
			url:       backend.URL + "/api",
			setUA:     true,
			userAgent: "",
			wantUA:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearProxyEnv(t)
			var proxy *fakeSOCKS5
			if tt.allProxy != "" {
				proxy = startFakeSOCKS5(t, backendAddr)
				t.Setenv("ALL_PROXY", tt.allProxy+"://"+proxy.ln.Addr().String())
			}

			req, err := http.NewRequest(http.MethodGet, tt.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.setUA {
				req.Header.Set("User-Agent", tt.userAgent)
			}
			resp, err := NewAPI(5 * time.Second).Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if string(body) != "ok" {
				t.Fatalf("body = %q, want ok", body)
			}
			if got := <-uaCh; got != tt.wantUA {
				t.Errorf("User-Agent = %q, want %q", got, tt.wantUA)
			}
			if got := req.Header.Get("User-Agent"); got != tt.userAgent {
				t.Errorf("NewAPI changed the caller's request headers: %v", req.Header)
			}
			if proxy == nil {
				return
			}
			if hosts := proxy.requestedHosts(); len(hosts) != 1 || hosts[0] != tt.wantHost {
				t.Errorf("proxy CONNECT hosts = %v, want [%s]", hosts, tt.wantHost)
			}
		})
	}
}

func TestNewAPIClient(t *testing.T) {
	if c := NewAPI(7 * time.Second); c.Timeout != 7*time.Second {
		t.Errorf("Timeout = %v, want 7s", c.Timeout)
	}

	// The API transport keeps HTTP/2, which Streaming turns off for Icecast.
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	clearProxyEnv(t)
	tr := newAPITransport()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	tr.TLSClientConfig = &tls.Config{RootCAs: roots}
	client := &http.Client{Transport: &socks5RoundTripper{transport: tr}}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Errorf("Proto = %s, want HTTP/2", resp.Proto)
	}
}
