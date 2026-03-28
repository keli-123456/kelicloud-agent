package monitoring

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseCNConnectivityTargets(t *testing.T) {
	targets := parseCNConnectivityTargets(
		"223.5.5.5\n119.29.29.29, dns.alidns.com ; 223.5.5.5",
	)

	expected := []string{"223.5.5.5", "119.29.29.29", "dns.alidns.com"}
	if !reflect.DeepEqual(targets, expected) {
		t.Fatalf("unexpected targets: got %v want %v", targets, expected)
	}
}

func TestUpdateCNConnectivityProbeConfigUsesMultipleTargets(t *testing.T) {
	UpdateCNConnectivityProbeConfig(true, "223.5.5.5\n119.29.29.29", 90, 4, 2, 8)

	config := getCNConnectivityProbeConfig()
	if !config.Enabled {
		t.Fatal("expected config to be enabled")
	}
	if config.Interval != 90 {
		t.Fatalf("unexpected interval: got %d want 90", config.Interval)
	}

	expectedTargets := []string{"223.5.5.5", "119.29.29.29"}
	if !reflect.DeepEqual(config.Targets, expectedTargets) {
		t.Fatalf("unexpected targets: got %v want %v", config.Targets, expectedTargets)
	}
	if config.RetryAttempts != 4 {
		t.Fatalf("unexpected retry attempts: got %d want 4", config.RetryAttempts)
	}
	if config.RetryDelaySeconds != 2 {
		t.Fatalf("unexpected retry delay seconds: got %d want 2", config.RetryDelaySeconds)
	}
	if config.TimeoutSeconds != 8 {
		t.Fatalf("unexpected timeout seconds: got %d want 8", config.TimeoutSeconds)
	}

	result := GetCNConnectivityProbeResult()
	if result == nil {
		t.Fatal("expected initial probe result")
	}
	if result.Target != "223.5.5.5" {
		t.Fatalf("unexpected initial target: got %q want %q", result.Target, "223.5.5.5")
	}
}

func TestProbeCNConnectivityRetriesUntilSuccess(t *testing.T) {
	attempts := 0
	waitCalls := 0
	result := probeCNConnectivityWithProber("223.5.5.5", func(target string) (int64, error) {
		attempts++
		if attempts < 3 {
			return -1, errors.New("no packets received")
		}
		return 42, nil
	}, func() {
		waitCalls++
	}, 3)

	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if waitCalls != 2 {
		t.Fatalf("expected 2 retry waits, got %d", waitCalls)
	}
	if result.Status != "ok" {
		t.Fatalf("expected ok status, got %q", result.Status)
	}
	if result.Latency != 42 {
		t.Fatalf("expected latency 42, got %d", result.Latency)
	}
	if result.Message != "icmp reachable" {
		t.Fatalf("unexpected success message: %q", result.Message)
	}
}

func TestProbeCNConnectivityFailsAfterRetryLimit(t *testing.T) {
	attempts := 0
	waitCalls := 0
	result := probeCNConnectivityWithProber("223.5.5.5", func(target string) (int64, error) {
		attempts++
		return -1, errors.New("no packets received")
	}, func() {
		waitCalls++
	}, defaultCNConnectivityRetryAttempts)

	if attempts != defaultCNConnectivityRetryAttempts {
		t.Fatalf("expected %d attempts, got %d", defaultCNConnectivityRetryAttempts, attempts)
	}
	if waitCalls != defaultCNConnectivityRetryAttempts-1 {
		t.Fatalf("expected %d retry waits, got %d", defaultCNConnectivityRetryAttempts-1, waitCalls)
	}
	if result.Status != "degraded" {
		t.Fatalf("expected degraded status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "after 3 attempts") {
		t.Fatalf("expected retry detail in message, got %q", result.Message)
	}
}
