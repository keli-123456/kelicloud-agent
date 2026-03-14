package monitoring

import (
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	ping "github.com/prometheus-community/pro-bing"
)

const (
	defaultCNConnectivityInterval = 60
	cnConnectivityFailureLimit    = 2
	cnConnectivityTimeout         = 5 * time.Second
)

type CNConnectivityProbeConfig struct {
	Enabled    bool
	Targets    []string
	TargetsKey string
	Interval   int
}

type CNConnectivityProbeResult struct {
	Status              string    `json:"status"`
	Target              string    `json:"target,omitempty"`
	Latency             int64     `json:"latency,omitempty"`
	Message             string    `json:"message,omitempty"`
	CheckedAt           time.Time `json:"checked_at,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures,omitempty"`
}

var (
	cnConnectivityState struct {
		mu       sync.RWMutex
		config   CNConnectivityProbeConfig
		result   *CNConnectivityProbeResult
		failures int
	}
	cnConnectivityLoopOnce sync.Once
	cnConnectivityNotifyCh = make(chan struct{}, 1)
)

func StartCNConnectivityProbeLoop() {
	cnConnectivityLoopOnce.Do(func() {
		go runCNConnectivityProbeLoop()
	})
}

func UpdateCNConnectivityProbeConfig(enabled bool, target string, interval int) {
	targets := parseCNConnectivityTargets(target)
	if interval <= 0 {
		interval = defaultCNConnectivityInterval
	}

	cnConnectivityState.mu.Lock()
	defer cnConnectivityState.mu.Unlock()

	cnConnectivityState.config = CNConnectivityProbeConfig{
		Enabled:    enabled && len(targets) > 0,
		Targets:    targets,
		TargetsKey: strings.Join(targets, "\n"),
		Interval:   interval,
	}

	cnConnectivityState.failures = 0
	if !cnConnectivityState.config.Enabled {
		cnConnectivityState.result = nil
	} else {
		cnConnectivityState.result = &CNConnectivityProbeResult{
			Status:  "unknown",
			Target:  targets[0],
			Message: "waiting for probe",
		}
	}

	select {
	case cnConnectivityNotifyCh <- struct{}{}:
	default:
	}
}

func GetCNConnectivityProbeResult() *CNConnectivityProbeResult {
	cnConnectivityState.mu.RLock()
	defer cnConnectivityState.mu.RUnlock()

	if cnConnectivityState.result == nil {
		return nil
	}

	resultCopy := *cnConnectivityState.result
	return &resultCopy
}

func runCNConnectivityProbeLoop() {
	for {
		config := getCNConnectivityProbeConfig()
		if !config.Enabled {
			<-cnConnectivityNotifyCh
			continue
		}

		result := probeCNConnectivityTargets(config.Targets)
		storeCNConnectivityProbeResult(config, result)

		timer := time.NewTimer(time.Duration(config.Interval) * time.Second)
		select {
		case <-cnConnectivityNotifyCh:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
		}
	}
}

func getCNConnectivityProbeConfig() CNConnectivityProbeConfig {
	cnConnectivityState.mu.RLock()
	defer cnConnectivityState.mu.RUnlock()
	return cnConnectivityState.config
}

func storeCNConnectivityProbeResult(config CNConnectivityProbeConfig, result CNConnectivityProbeResult) {
	cnConnectivityState.mu.Lock()
	defer cnConnectivityState.mu.Unlock()

	if !sameCNConnectivityProbeConfig(cnConnectivityState.config, config) ||
		!cnConnectivityState.config.Enabled {
		return
	}

	if result.Status == "ok" {
		cnConnectivityState.failures = 0
	} else {
		cnConnectivityState.failures++
		result.ConsecutiveFailures = cnConnectivityState.failures
		if cnConnectivityState.failures >= cnConnectivityFailureLimit {
			result.Status = "blocked_suspected"
		} else {
			result.Status = "degraded"
		}
	}

	result.ConsecutiveFailures = cnConnectivityState.failures
	cnConnectivityState.result = &result
}

func sameCNConnectivityProbeConfig(a, b CNConnectivityProbeConfig) bool {
	return a.Enabled == b.Enabled &&
		a.Interval == b.Interval &&
		a.TargetsKey == b.TargetsKey
}

func parseCNConnectivityTargets(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';'
	})
	targets := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))

	for _, part := range parts {
		target := strings.TrimSpace(part)
		if target == "" {
			continue
		}
		if _, exists := seen[target]; exists {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}

	return targets
}

func probeCNConnectivityTargets(targets []string) CNConnectivityProbeResult {
	if len(targets) == 0 {
		return CNConnectivityProbeResult{
			Status:    "degraded",
			Message:   "no targets configured",
			CheckedAt: time.Now(),
		}
	}

	failures := make([]string, 0, len(targets))
	for _, target := range targets {
		result := probeCNConnectivity(target)
		if result.Status == "ok" {
			return result
		}
		failures = append(failures, target+": "+result.Message)
	}

	return CNConnectivityProbeResult{
		Status:    "degraded",
		Target:    strings.Join(targets, ", "),
		Message:   strings.Join(failures, "; "),
		CheckedAt: time.Now(),
	}
}

func probeCNConnectivity(target string) CNConnectivityProbeResult {
	latency, err := icmpPingTarget(target, cnConnectivityTimeout)
	result := CNConnectivityProbeResult{
		Target:    target,
		CheckedAt: time.Now(),
	}

	if err != nil {
		result.Status = "degraded"
		result.Message = err.Error()
		return result
	}

	result.Status = "ok"
	result.Latency = latency
	result.Message = "icmp reachable"
	return result
}

func icmpPingTarget(target string, timeout time.Duration) (int64, error) {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}
	host = strings.Trim(host, "[]")

	ip, err := resolveProbeIP(host)
	if err != nil {
		return -1, err
	}

	pinger, err := ping.NewPinger(ip)
	if err != nil {
		return -1, err
	}
	pinger.Count = 1
	pinger.Timeout = timeout
	pinger.SetPrivileged(true)
	if err := pinger.Run(); err != nil {
		return -1, err
	}

	stats := pinger.Statistics()
	if stats.PacketsRecv == 0 {
		return -1, errors.New("no packets received")
	}

	return stats.AvgRtt.Milliseconds(), nil
}

func resolveProbeIP(target string) (string, error) {
	if ip := net.ParseIP(target); ip != nil {
		return target, nil
	}

	addrs, err := net.LookupHost(target)
	if err != nil || len(addrs) == 0 {
		return "", errors.New("failed to resolve target")
	}

	return addrs[0], nil
}
