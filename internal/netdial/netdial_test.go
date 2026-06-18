package netdial

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestDialContextHTTPConnectProxy(t *testing.T) {
	targetAddr, stopTarget := startEchoTarget(t)
	defer stopTarget()
	proxyAddr, stopProxy := startConnectProxy(t, httpConnectOK, targetAddr)
	defer stopProxy()
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://"+proxyAddr)

	conn, err := DialContext(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("echo = %q, want ping", string(buf))
	}
}

func TestDialContextDirect(t *testing.T) {
	targetAddr, stopTarget := startEchoTarget(t)
	defer stopTarget()
	clearProxyEnv(t)

	conn, err := DialContext(context.Background(), "tcp", targetAddr)
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	_ = conn.Close()
}

func TestDialContextHTTPConnectNon200(t *testing.T) {
	proxyAddr, stopProxy := startConnectProxy(t, httpConnectForbidden, "")
	defer stopProxy()
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://"+proxyAddr)

	_, err := DialContext(context.Background(), "tcp", "example.com:443")
	if err == nil {
		t.Fatal("DialContext() error is nil, want CONNECT failure")
	}
	if !strings.Contains(err.Error(), "proxy CONNECT example.com:443: 403 Forbidden") {
		t.Fatalf("DialContext() error = %v", err)
	}
}

func TestDialContextNoProxyBypassesProxy(t *testing.T) {
	targetAddr, stopTarget := startRoutableEchoTarget(t)
	defer stopTarget()
	clearProxyEnv(t)
	host, _, err := net.SplitHostPort(targetAddr)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", host)

	conn, err := DialContext(context.Background(), "tcp", targetAddr)
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	_ = conn.Close()
}

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "all_proxy", "no_proxy",
	} {
		t.Setenv(name, "")
	}
}

func startEchoTarget(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	done := make(chan struct{})
	go serveEcho(ln, done)
	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

type connectStatus int

const (
	httpConnectOK connectStatus = iota
	httpConnectForbidden
)

func startRoutableEchoTarget(t *testing.T) (string, func()) {
	t.Helper()
	host := firstNonLoopbackIPv4(t)
	ln, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", "0"))
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		_ = ln.Close()
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	done := make(chan struct{})
	go serveEcho(ln, done)
	return net.JoinHostPort(host, port), func() {
		_ = ln.Close()
		<-done
	}
}

func firstNonLoopbackIPv4(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatalf("InterfaceAddrs() error = %v", err)
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP.To4()
		if ip != nil && !ip.IsLoopback() {
			return ip.String()
		}
	}
	t.Skip("no non-loopback IPv4 address available for NO_PROXY test")
	return ""
}

func serveEcho(ln net.Listener, done chan<- struct{}) {
	defer close(done)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}()
	}
}

func startConnectProxy(t *testing.T, status connectStatus, targetAddr string) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleProxyConn(conn, status, targetAddr)
		}
	}()
	return ln.Addr().String(), func() {
		_ = ln.Close()
		<-done
	}
}

func handleProxyConn(conn net.Conn, status connectStatus, targetAddr string) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	for {
		header, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if header == "\r\n" {
			break
		}
	}
	if status == httpConnectForbidden {
		_, _ = io.WriteString(conn, "HTTP/1.1 403 Forbidden\r\n\r\n")
		return
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		_, _ = io.WriteString(conn, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return
	}
	if _, _, err := net.SplitHostPort(parts[1]); err != nil {
		_, _ = io.WriteString(conn, "HTTP/1.1 400 Bad Request\r\n\r\n")
		return
	}
	target, err := net.Dial("tcp", targetAddr)
	if err != nil {
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return
	}
	defer target.Close()
	_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
	_ = conn.SetDeadline(time.Time{})

	errc := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(target, br)
		errc <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(conn, target)
		errc <- struct{}{}
	}()
	<-errc
}
