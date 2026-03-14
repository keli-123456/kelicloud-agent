package monitoring

import (
	"reflect"
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
	UpdateCNConnectivityProbeConfig(true, "223.5.5.5\n119.29.29.29", 90)

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

	result := GetCNConnectivityProbeResult()
	if result == nil {
		t.Fatal("expected initial probe result")
	}
	if result.Target != "223.5.5.5" {
		t.Fatalf("unexpected initial target: got %q want %q", result.Target, "223.5.5.5")
	}
}
