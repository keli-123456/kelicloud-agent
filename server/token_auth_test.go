package server

import (
	"errors"
	"testing"
)

func TestIndicatesInvalidClientTokenResponse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		statusCode int
		body       string
		want       bool
	}{
		{name: "invalid token", statusCode: 401, body: `{"error":"invalid token"}`, want: true},
		{name: "missing token", statusCode: 401, body: `{"error":"token is required"}`, want: true},
		{name: "validation failure", statusCode: 401, body: `{"error":"failed to validate token"}`, want: true},
		{name: "empty unauthorized body", statusCode: 401, body: "", want: false},
		{name: "other unauthorized body", statusCode: 401, body: "access denied", want: false},
		{name: "other status", statusCode: 403, body: `{"error":"invalid token"}`, want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := indicatesInvalidClientTokenResponse(tc.statusCode, tc.body); got != tc.want {
				t.Fatalf("indicatesInvalidClientTokenResponse(%d, %q) = %v, want %v", tc.statusCode, tc.body, got, tc.want)
			}
		})
	}
}

func TestClassifyClientTokenResponseReturnsInvalidTokenError(t *testing.T) {
	t.Parallel()

	err := classifyClientTokenResponse("upload basic info", "token-old", 401, `{"error":"invalid token"}`)
	if !IsInvalidClientTokenError(err) {
		t.Fatalf("expected invalid client token error, got %v", err)
	}

	var invalidTokenErr *InvalidClientTokenError
	if !errors.As(err, &invalidTokenErr) {
		t.Fatalf("expected InvalidClientTokenError, got %T", err)
	}

	if invalidTokenErr.Operation != "upload basic info" {
		t.Fatalf("unexpected operation: %q", invalidTokenErr.Operation)
	}
	if invalidTokenErr.Token != "token-old" {
		t.Fatalf("unexpected token: %q", invalidTokenErr.Token)
	}
	if invalidTokenErr.StatusCode != 401 {
		t.Fatalf("unexpected status code: %d", invalidTokenErr.StatusCode)
	}
}

func TestClassifyClientTokenResponseKeepsGenericErrors(t *testing.T) {
	t.Parallel()

	err := classifyClientTokenResponse("connect websocket", "token-old", 403, "forbidden")
	if err == nil {
		t.Fatal("expected an error")
	}
	if IsInvalidClientTokenError(err) {
		t.Fatalf("expected generic error, got invalid token error: %v", err)
	}
}
