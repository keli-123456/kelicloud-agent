package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newLoopbackListener(t *testing.T, network string) net.Listener {
	t.Helper()

	addr := "127.0.0.1:0"
	if network == "tcp6" {
		addr = "[::1]:0"
	}

	ln, err := net.Listen(network, addr)
	if err != nil {
		t.Skipf("skip %s listener: %v", network, err)
	}
	return ln
}

func closeAcceptedConnections(t *testing.T, ln net.Listener) func() {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	return func() {
		_ = ln.Close()
		<-done
	}
}

func newHTTPServer(t *testing.T, network string, statusCode int) (*httptest.Server, string) {
	t.Helper()

	ln := newLoopbackListener(t, network)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(statusCode)
	}))
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, strings.TrimPrefix(srv.URL, "http://")
}

func shouldSkipICMP(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "operation not permitted") ||
		strings.Contains(message, "permission denied") ||
		strings.Contains(message, "no packets received")
}

func TestResolveIP(t *testing.T) {
	targets := []string{"127.0.0.1", "localhost"}
	if ip := net.ParseIP("::1"); ip != nil {
		targets = append(targets, "::1")
	}

	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			resolved, err := resolveIP(target)
			if err != nil {
				t.Fatalf("resolveIP(%q) error: %v", target, err)
			}
			if net.ParseIP(resolved) == nil {
				t.Fatalf("resolveIP(%q) returned non-IP value %q", target, resolved)
			}
		})
	}
}

func TestICMPPingLoopback(t *testing.T) {
	timeout := time.Second
	targets := []string{"127.0.0.1"}
	if ln, err := net.Listen("tcp6", "[::1]:0"); err == nil {
		_ = ln.Close()
		targets = append(targets, "::1")
	}

	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			latency, err := icmpPing(target, timeout)
			if shouldSkipICMP(err) {
				t.Skipf("icmp not available in this environment: %v", err)
			}
			if err != nil {
				t.Fatalf("icmpPing(%q) error: %v", target, err)
			}
			if latency < 0 {
				t.Fatalf("icmpPing(%q) returned invalid latency %d", target, latency)
			}
		})
	}
}

func TestTCPPingLoopback(t *testing.T) {
	timeout := time.Second

	testCases := []struct {
		name    string
		network string
	}{
		{name: "ipv4", network: "tcp4"},
		{name: "ipv6", network: "tcp6"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ln := newLoopbackListener(t, tc.network)
			defer closeAcceptedConnections(t, ln)()

			latency, err := tcpPing(ln.Addr().String(), timeout)
			if err != nil {
				t.Fatalf("tcpPing(%q) error: %v", ln.Addr().String(), err)
			}
			if latency < 0 {
				t.Fatalf("tcpPing(%q) returned invalid latency %d", ln.Addr().String(), latency)
			}
		})
	}
}

func TestHasScriptShebang(t *testing.T) {
	if !hasScriptShebang("#!/bin/bash\necho ok\n") {
		t.Fatal("expected bash shebang to be detected")
	}
	if hasScriptShebang("echo ok") {
		t.Fatal("expected plain command to skip shebang mode")
	}
}

func TestBuildTaskCommandUsesScriptFileForShebangScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not available: %v", err)
	}

	cmd, cleanup, err := buildTaskCommand("#!/bin/bash\nprintf 'ok'")
	if err != nil {
		t.Fatalf("buildTaskCommand returned error: %v", err)
	}
	defer cleanup()

	if len(cmd.Args) == 0 {
		t.Fatal("expected script path in command args")
	}
	scriptPath := cmd.Args[0]
	if !strings.Contains(scriptPath, "komari-task-") {
		t.Fatalf("expected temp script path, got %q", scriptPath)
	}
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("expected temp script file to exist: %v", err)
	}

	output, runErr := cmd.Output()
	if runErr != nil {
		t.Fatalf("expected shebang script to execute successfully, got %v", runErr)
	}
	if string(output) != "ok" {
		t.Fatalf("expected output %q, got %q", "ok", string(output))
	}
}

func TestBuildTaskCommandFallsBackToShellForPlainCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}

	cmd, cleanup, err := buildTaskCommand("printf 'ok'")
	if err != nil {
		t.Fatalf("buildTaskCommand returned error: %v", err)
	}
	defer cleanup()

	if bashPath, err := exec.LookPath("bash"); err == nil {
		if got := strings.Join(cmd.Args, " "); got != bashPath+" -lc printf 'ok'" {
			t.Fatalf("expected bash -lc command, got %q", got)
		}
		return
	}

	if got := strings.Join(cmd.Args, " "); got != "sh -c printf 'ok'" {
		t.Fatalf("expected sh -c fallback, got %q", got)
	}
}

func TestBuildTaskCommandRunsBashSyntaxWhenBashAvailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not available: %v", err)
	}

	cmd, cleanup, err := buildTaskCommand("items=(ok); printf '%s' \"${items[0]}\"")
	if err != nil {
		t.Fatalf("buildTaskCommand returned error: %v", err)
	}
	defer cleanup()

	output, runErr := cmd.Output()
	if runErr != nil {
		t.Fatalf("expected bash syntax to execute successfully, got %v", runErr)
	}
	if string(output) != "ok" {
		t.Fatalf("expected output %q, got %q", "ok", string(output))
	}
}

func TestBuildTaskCommandFallsBackToShWhenBashUnavailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}

	original := lookPath
	lookPath = func(file string) (string, error) {
		if file == "bash" {
			return "", exec.ErrNotFound
		}
		return exec.LookPath(file)
	}
	defer func() {
		lookPath = original
	}()

	cmd, cleanup, err := buildTaskCommand("printf 'ok'")
	if err != nil {
		t.Fatalf("buildTaskCommand returned error: %v", err)
	}
	defer cleanup()

	if got := strings.Join(cmd.Args, " "); got != "sh -c printf 'ok'" {
		t.Fatalf("expected sh -c fallback, got %q", got)
	}
}

func TestHTTPPingLoopback(t *testing.T) {
	timeout := time.Second

	testCases := []struct {
		name    string
		network string
	}{
		{name: "ipv4", network: "tcp4"},
		{name: "ipv6", network: "tcp6"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srv, target := newHTTPServer(t, tc.network, http.StatusNoContent)

			for _, input := range []string{srv.URL, target} {
				t.Run(input, func(t *testing.T) {
					latency, err := httpPing(input, timeout)
					if err != nil {
						t.Fatalf("httpPing(%q) error: %v", input, err)
					}
					if latency < 0 {
						t.Fatalf("httpPing(%q) returned invalid latency %d", input, latency)
					}
				})
			}
		})
	}
}

func TestHTTPPingReturnsErrorOnServerFailure(t *testing.T) {
	timeout := time.Second
	_, target := newHTTPServer(t, "tcp4", http.StatusInternalServerError)

	latency, err := httpPing(target, timeout)
	if err == nil {
		t.Fatal("expected httpPing to return an error for non-2xx status")
	}
	if latency < 0 {
		t.Fatalf("httpPing(%q) returned invalid latency %d", target, latency)
	}
}
