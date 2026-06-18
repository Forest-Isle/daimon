package netdial

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
	"golang.org/x/net/proxy"
)

// DialContext dials addr ("host:port"), routing through the HTTP/HTTPS proxy
// from the environment (HTTPS_PROXY/HTTP_PROXY/ALL_PROXY, honoring NO_PROXY)
// when one applies to the target, otherwise dialing directly. Used for raw
// IMAP/SMTP TLS connections that net/http's transport-level proxy does not cover.
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	cfg := httpproxy.FromEnvironment()
	if cfg.HTTPSProxy == "" {
		cfg.HTTPSProxy = cfg.HTTPProxy
	}
	if cfg.HTTPSProxy == "" {
		cfg.HTTPSProxy = getEnvAny("ALL_PROXY", "all_proxy")
	}
	proxyURL, err := cfg.ProxyFunc()(&url.URL{Scheme: "https", Host: addr})
	if err != nil {
		return nil, fmt.Errorf("netdial: proxy env: %w", err)
	}
	if proxyURL == nil {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, fmt.Errorf("netdial: dial direct: %w", err)
		}
		return conn, nil
	}

	switch strings.ToLower(proxyURL.Scheme) {
	case "http", "https":
		conn, err := dialHTTPConnect(ctx, proxyURL, addr)
		if err != nil {
			return nil, err
		}
		return conn, nil
	case "socks5":
		conn, err := dialSOCKS5(ctx, network, addr, proxyURL)
		if err != nil {
			return nil, err
		}
		return conn, nil
	default:
		return nil, fmt.Errorf("netdial: unsupported proxy scheme %q", proxyURL.Scheme)
	}
}

func getEnvAny(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func dialHTTPConnect(ctx context.Context, proxyURL *url.URL, addr string) (net.Conn, error) {
	pconn, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxyURL.Host)
	if err != nil {
		return nil, fmt.Errorf("netdial: dial proxy: %w", err)
	}

	// For an https-scheme proxy the CONNECT request (including the
	// Proxy-Authorization credential) must travel over TLS to the proxy, not
	// raw TCP. Wrap the proxy connection before writing anything to it.
	var pc net.Conn = pconn
	if strings.EqualFold(proxyURL.Scheme, "https") {
		tlsConn := tls.Client(pconn, &tls.Config{ServerName: proxyURL.Hostname()})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = pconn.Close()
			return nil, fmt.Errorf("netdial: proxy tls: %w", err)
		}
		pc = tlsConn
	}

	stop := context.AfterFunc(ctx, func() {
		_ = pc.Close()
	})
	defer stop()

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if proxyURL.User != nil {
		req.Header.Set("Proxy-Authorization", "Basic "+basicAuth(proxyURL.User))
	}
	if err := req.Write(pc); err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("netdial: write CONNECT: %w", err)
	}

	br := bufio.NewReader(pc)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("netdial: read CONNECT response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = pc.Close()
		return nil, fmt.Errorf("netdial: proxy CONNECT %s: %s", addr, resp.Status)
	}

	if !stop() {
		if err := ctx.Err(); err != nil {
			_ = pc.Close()
			return nil, fmt.Errorf("netdial: context canceled: %w", err)
		}
	}
	_ = pc.SetDeadline(time.Time{})
	return &bufferedConn{Reader: br, Conn: pc}, nil
}

func dialSOCKS5(ctx context.Context, network, addr string, proxyURL *url.URL) (net.Conn, error) {
	var auth *proxy.Auth
	if proxyURL.User != nil {
		auth = &proxy.Auth{User: proxyURL.User.Username()}
		if password, ok := proxyURL.User.Password(); ok {
			auth.Password = password
		}
	}
	dialer, err := proxy.SOCKS5("tcp", proxyURL.Host, auth, &net.Dialer{})
	if err != nil {
		return nil, fmt.Errorf("netdial: socks5 proxy: %w", err)
	}
	if ctxDialer, ok := dialer.(proxy.ContextDialer); ok {
		conn, err := ctxDialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, fmt.Errorf("netdial: socks5 dial: %w", err)
		}
		return conn, nil
	}
	conn, err := dialer.Dial(network, addr)
	if err != nil {
		return nil, fmt.Errorf("netdial: socks5 dial: %w", err)
	}
	return conn, nil
}

func basicAuth(user *url.Userinfo) string {
	username := user.Username()
	password, _ := user.Password()
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}

type bufferedConn struct {
	*bufio.Reader
	net.Conn
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.Reader.Read(p)
}
