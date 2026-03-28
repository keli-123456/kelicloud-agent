package monitoring

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	ping "github.com/prometheus-community/pro-bing"
)

const (
	defaultCNConnectivityInterval       = 60
	defaultCNConnectivityRetryAttempts  = 3
	defaultCNConnectivityRetryDelaySecs = 1
	defaultCNConnectivityTimeoutSecs    = 5
	cnConnectivityFailureLimit          = 2
)

type CNConnectivityProbeConfig struct {
	Enabled           bool
	Targets           []string
	TargetsKey        string
	Interval          int
	RetryAttempts     int
	RetryDelaySeconds int
	TimeoutSeconds    int
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

func UpdateCNConnectivityProbeConfig(enabled bool, target string, interval, retryAttempts, retryDelaySeconds, timeoutSeconds int) {
	targets := parseCNConnectivityTargets(target)
	if interval <= 0 {
		interval = defaultCNConnectivityInterval
	}
	if retryAttempts <= 0 {
		retryAttempts = defaultCNConnectivityRetryAttempts
	}
	if retryDelaySeconds <= 0 {
		retryDelaySeconds = defaultCNConnectivityRetryDelaySecs
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultCNConnectivityTimeoutSecs
	}

	cnConnectivityState.mu.Lock()
	defer cnConnectivityState.mu.Unlock()

	cnConnectivityState.config = CNConnectivityProbeConfig{
		Enabled:           enabled && len(targets) > 0,
		Targets:           targets,
		TargetsKey:        strings.Join(targets, "\n"),
		Interval:          interval,
		RetryAttempts:     retryAttempts,
		RetryDelaySeconds: retryDelaySeconds,
		TimeoutSeconds:    timeoutSeconds,
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

		result := probeCNConnectivityTargets(config)
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
		a.RetryAttempts == b.RetryAttempts &&
		a.RetryDelaySeconds == b.RetryDelaySeconds &&
		a.TimeoutSeconds == b.TimeoutSeconds &&
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

func probeCNConnectivityTargets(config CNConnectivityProbeConfig) CNConnectivityProbeResult {
	if len(config.Targets) == 0 {
		return CNConnectivityProbeResult{
			Status:    "degraded",
			Message:   "no targets configured",
			CheckedAt: time.Now(),
		}
	}

	failures := make([]string, 0, len(config.Targets))
	for _, target := range config.Targets {
		result := probeCNConnectivity(
			target,
			config.RetryAttempts,
			time.Duration(config.RetryDelaySeconds)*time.Second,
			time.Duration(config.TimeoutSeconds)*time.Second,
		)
		if result.Status == "ok" {
			return result
		}
		failures = append(failures, target+": "+result.Message)
	}

	return CNConnectivityProbeResult{
		Status:    "degraded",
		Target:    strings.Join(config.Targets, ", "),
		Message:   strings.Join(failures, "; "),
		CheckedAt: time.Now(),
	}
}

func probeCNConnectivity(target string, retryAttempts int, retryDelay, timeout time.Duration) CNConnectivityProbeResult {
	return probeCNConnectivityWithProber(target, func(target string) (int64, error) {
		return icmpPingTarget(target, timeout)
	}, func() {
		time.Sleep(retryDelay)
	}, retryAttempts)
}

func probeCNConnectivityWithProber(target string, prober func(string) (int64, error), waitRetry func(), retryAttempts int) CNConnectivityProbeResult {
	result := CNConnectivityProbeResult{
		Target:    target,
		CheckedAt: time.Now(),
	}

	attempts := retryAttempts
	if attempts < 1 {
		attempts = defaultCNConnectivityRetryAttempts
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		latency, err := prober(target)
		if err == nil {
			result.Status = "ok"
			result.Latency = latency
			result.Message = "icmp reachable"
			result.CheckedAt = time.Now()
			return result
		}

		lastErr = err
		if attempt < attempts && waitRetry != nil {
			waitRetry()
		}
	}

	result.Status = "degraded"
	if lastErr != nil {
		if attempts == 1 {
			result.Message = lastErr.Error()
		} else {
			result.Message = fmt.Sprintf("%s after %d attempts", lastErr.Error(), attempts)
		}
	} else {
		result.Message = "connectivity probe failed"
	}
	result.CheckedAt = time.Now()
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
