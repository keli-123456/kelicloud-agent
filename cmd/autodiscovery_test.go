package cmd

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	agentserver "github.com/komari-monitor/komari-agent/server"
)

func newAutoDiscoveryTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create ipv4 listener: %v", err)
	}

	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func TestRecoverAutoDiscoveryFromInvalidTokenReRegisters(t *testing.T) {
	originalPathOverride := autoDiscoveryFilePathOverride
	originalAutoDiscoveryKey := flags.AutoDiscoveryKey
	originalEndpoint := flags.Endpoint
	originalToken := flags.Token
	defer func() {
		autoDiscoveryFilePathOverride = originalPathOverride
		flags.AutoDiscoveryKey = originalAutoDiscoveryKey
		flags.Endpoint = originalEndpoint
		flags.Token = originalToken
	}()

	tempDir := t.TempDir()
	autoDiscoveryFilePathOverride = filepath.Join(tempDir, "auto-discovery.json")
	flags.AutoDiscoveryKey = "autodiscovery-key-123456"
	flags.Token = "stale-token"

	if err := saveAutoDiscoveryConfig(&AutoDiscoveryConfig{UUID: "old-uuid", Token: "stale-token"}); err != nil {
		t.Fatalf("failed to seed stale auto-discovery config: %v", err)
	}

	var registerCalls int32
	testServer := newAutoDiscoveryTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/clients/register" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+flags.AutoDiscoveryKey {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		atomic.AddInt32(&registerCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"uuid":"new-uuid","token":"new-token"}}`))
	}))

	flags.Endpoint = testServer.URL

	recovered := recoverAutoDiscoveryFromInvalidToken(&agentserver.InvalidClientTokenError{
		Operation:  "connect websocket",
		StatusCode: http.StatusUnauthorized,
		Token:      "stale-token",
		Detail:     "invalid token",
	})
	if !recovered {
		t.Fatal("expected invalid token recovery to succeed")
	}

	if got := atomic.LoadInt32(&registerCalls); got != 1 {
		t.Fatalf("expected 1 re-registration call, got %d", got)
	}
	if flags.Token != "new-token" {
		t.Fatalf("expected token to rotate to new-token, got %q", flags.Token)
	}

	config, err := loadAutoDiscoveryConfig()
	if err != nil {
		t.Fatalf("failed to reload auto-discovery config: %v", err)
	}
	if config == nil {
		t.Fatal("expected auto-discovery config to exist after recovery")
	}
	if config.UUID != "new-uuid" || config.Token != "new-token" {
		t.Fatalf("unexpected recovered config: %+v", config)
	}
}

func TestRecoverAutoDiscoveryFromInvalidTokenSkipsIfTokenAlreadyRotated(t *testing.T) {
	originalPathOverride := autoDiscoveryFilePathOverride
	originalAutoDiscoveryKey := flags.AutoDiscoveryKey
	originalEndpoint := flags.Endpoint
	originalToken := flags.Token
	defer func() {
		autoDiscoveryFilePathOverride = originalPathOverride
		flags.AutoDiscoveryKey = originalAutoDiscoveryKey
		flags.Endpoint = originalEndpoint
		flags.Token = originalToken
	}()

	tempDir := t.TempDir()
	autoDiscoveryFilePathOverride = filepath.Join(tempDir, "auto-discovery.json")
	flags.AutoDiscoveryKey = "autodiscovery-key-123456"
	flags.Token = "fresh-token"

	var registerCalls int32
	testServer := newAutoDiscoveryTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&registerCalls, 1)
		http.Error(w, "unexpected register", http.StatusInternalServerError)
	}))

	flags.Endpoint = testServer.URL

	recovered := recoverAutoDiscoveryFromInvalidToken(&agentserver.InvalidClientTokenError{
		Operation:  "upload basic info",
		StatusCode: http.StatusUnauthorized,
		Token:      "stale-token",
		Detail:     "invalid token",
	})
	if !recovered {
		t.Fatal("expected stale invalid-token event to be treated as already recovered")
	}

	if got := atomic.LoadInt32(&registerCalls); got != 0 {
		t.Fatalf("expected no re-registration calls, got %d", got)
	}
	if _, err := os.Stat(autoDiscoveryFilePathOverride); !os.IsNotExist(err) {
		t.Fatalf("expected no auto-discovery file writes, got err=%v", err)
	}
}
