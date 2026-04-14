package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	providerName        = "mock-lab"
	modelName           = "gpt-4.1"
	activeProbeInterval = 1 * time.Second
	waitTimeout         = 8 * time.Second
)

var keyIDs = map[string]string{
	"site-a-ok":       "site-a-ok-id",
	"site-b-slow":     "site-b-slow-id",
	"site-d-hardfail": "site-d-hardfail-id",
}

type routeTarget struct {
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	KeyID    string  `json:"key_id,omitempty"`
	Weight   float64 `json:"weight"`
}

type routeGroup struct {
	Name       string        `json:"name"`
	RetryLimit int           `json:"retry_limit"`
	Targets    []routeTarget `json:"targets"`
}

type healthPolicy struct {
	FailureThreshold     int `json:"failure_threshold"`
	FailureWindowSeconds int `json:"failure_window_seconds"`
	CooldownSeconds      int `json:"cooldown_seconds"`
	ConsecutiveFailures  int `json:"consecutive_failures"`
}

type scenarioResult struct {
	Name          string            `json:"name"`
	Goal          string            `json:"goal"`
	Passed        bool              `json:"passed"`
	DurationMs    int64             `json:"duration_ms"`
	SuccessCount  int               `json:"success_count"`
	ErrorCount    int               `json:"error_count"`
	MockCounts    map[string]int    `json:"mock_counts,omitempty"`
	Observations  []string          `json:"observations"`
	HealthSummary map[string]string `json:"health_summary,omitempty"`
	ArtifactDir   string            `json:"artifact_dir"`
}

type runSummary struct {
	StartedAt  string           `json:"started_at"`
	FinishedAt string           `json:"finished_at"`
	BifrostURL string           `json:"bifrost_url"`
	MockAdmin  string           `json:"mock_admin"`
	OutputDir  string           `json:"output_dir"`
	Scenarios  []scenarioResult `json:"scenarios"`
}

type mockProfileAction struct {
	Type         string `json:"type"`
	Status       int    `json:"status,omitempty"`
	DelayMs      int    `json:"delay_ms,omitempty"`
	Message      string `json:"message,omitempty"`
	ResponseText string `json:"response_text,omitempty"`
}

type mockProfile struct {
	Default mockProfileAction   `json:"default"`
	Series  []mockProfileAction `json:"series,omitempty"`
	Loop    bool                `json:"loop,omitempty"`
}

type mockProfilesPayload struct {
	Profiles map[string]mockProfile `json:"profiles"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type targetHealthSnapshot struct {
	Key                     string  `json:"key"`
	Status                  string  `json:"status"`
	FailureCount            int     `json:"failure_count"`
	ConsecutiveFailures     int     `json:"consecutive_failures"`
	CooldownUntil           *string `json:"cooldown_until,omitempty"`
	LastFailureMsg          string  `json:"last_failure_msg,omitempty"`
	LastObservedAt          *string `json:"last_observed_at,omitempty"`
	LastObservedRequestType string  `json:"last_observed_request_type,omitempty"`
	LastObservationSource   string  `json:"last_observation_source,omitempty"`
}

type healthAPIResponse struct {
	Rules []struct {
		RuleID   string                 `json:"rule_id"`
		RuleName string                 `json:"rule_name"`
		Targets  []targetHealthSnapshot `json:"targets"`
	} `json:"rules"`
}

type mockEvent struct {
	Timestamp  string `json:"timestamp"`
	Path       string `json:"path"`
	Key        string `json:"key"`
	Model      string `json:"model"`
	User       string `json:"user"`
	ActionType string `json:"action_type"`
	StatusCode int    `json:"status_code"`
}

type mockEventsResponse struct {
	Events []mockEvent    `json:"events"`
	Counts map[string]int `json:"counts"`
}

type ruleCreateResponse struct {
	Rule struct {
		ID string `json:"id"`
	} `json:"rule"`
}

type requestResult struct {
	User       string `json:"user"`
	StatusCode int    `json:"status_code"`
	Text       string `json:"text,omitempty"`
	ErrorText  string `json:"error_text,omitempty"`
	LatencyMs  int64  `json:"latency_ms"`
}

type lab struct {
	bifrostURL string
	mockAdmin  string
	outputDir  string
	client     *http.Client
}

func main() {
	bifrostURL := flag.String("bifrost-url", "http://127.0.0.1:18080", "Bifrost base URL")
	mockAdmin := flag.String("mock-admin-url", "http://127.0.0.1:19101", "mock admin URL")
	outputDir := flag.String("output-dir", "", "output directory")
	flag.Parse()

	if *outputDir == "" {
		fmt.Fprintln(os.Stderr, "-output-dir is required")
		os.Exit(2)
	}
	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create output dir: %v\n", err)
		os.Exit(1)
	}

	l := &lab{
		bifrostURL: strings.TrimRight(*bifrostURL, "/"),
		mockAdmin:  strings.TrimRight(*mockAdmin, "/"),
		outputDir:  *outputDir,
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
	}

	startedAt := time.Now().UTC()

	if err := l.setupProvider(); err != nil {
		fmt.Fprintf(os.Stderr, "setup provider: %v\n", err)
		os.Exit(1)
	}

	scenarios := []struct {
		name string
		goal string
		run  func(*lab) (scenarioResult, error)
	}{
		{"passive_freshness_skips_probe", "最近有被动流量时不主动探测", runPassiveFreshnessSkipsProbe},
		{"stale_target_triggers_active_probe", "静默超过新鲜时间后触发主动探测", runStaleTargetTriggersActiveProbe},
		{"active_probe_success_recovers_cooldown", "主动探测成功后恢复主目标", runActiveProbeSuccessRecoversCooldown},
		{"active_probe_failure_keeps_cooldown", "主动探测失败后继续跳过异常目标", runActiveProbeFailureKeepsCooldown},
		{"health_status_observation_fields", "健康状态接口返回观测元数据", runHealthStatusObservationFields},
	}

	results := make([]scenarioResult, 0, len(scenarios))
	for _, sc := range scenarios {
		res, err := sc.run(l)
		if err != nil {
			res.Name = sc.name
			res.Goal = sc.goal
			res.Passed = false
			res.Observations = append(res.Observations, "执行异常: "+err.Error())
		}
		results = append(results, res)
	}

	summary := runSummary{
		StartedAt:  startedAt.Format(time.RFC3339),
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
		BifrostURL: l.bifrostURL,
		MockAdmin:  l.mockAdmin,
		OutputDir:  l.outputDir,
		Scenarios:  results,
	}
	if err := writeJSONFile(filepath.Join(l.outputDir, "results.json"), summary); err != nil {
		fmt.Fprintf(os.Stderr, "write results.json: %v\n", err)
		os.Exit(1)
	}

	passed := 0
	for _, res := range results {
		if res.Passed {
			passed++
		}
	}
	fmt.Printf("lab completed: %d/%d scenarios passed\n", passed, len(results))
	if passed != len(results) {
		os.Exit(1)
	}
}

func runPassiveFreshnessSkipsProbe(l *lab) (scenarioResult, error) {
	start := time.Now()
	dir := l.scenarioDir("passive_freshness_skips_probe")
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-a-ok": {Default: mockProfileAction{Type: "success", DelayMs: 80, ResponseText: "fresh passive success"}},
	}); err != nil {
		return scenarioResult{}, err
	}

	ruleID, err := l.createRule("lab-passive-freshness", defaultHealthPolicy(), []routeGroup{
		group("primary", 0, target("site-a-ok", 1)),
	})
	if err != nil {
		return scenarioResult{}, err
	}
	defer l.deleteRule(ruleID)

	reqRes, err := l.sendChat("fresh-001")
	if err != nil {
		return scenarioResult{}, err
	}
	time.Sleep(activeProbeInterval + 500*time.Millisecond)

	events, err := l.getMockEvents()
	if err != nil {
		return scenarioResult{}, err
	}
	health, err := l.getHealth(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}
	targetSnap, ok := findTarget(health, targetKey("site-a-ok"))
	if !ok {
		return scenarioResult{}, fmt.Errorf("target snapshot not found for %s", targetKey("site-a-ok"))
	}

	probeHits := countProbeHits(events, "site-a-ok")
	passed := reqRes.StatusCode == http.StatusOK &&
		totalEventCount(events.Counts) == 1 &&
		probeHits == 0 &&
		targetSnap.LastObservationSource == "passive"

	if err := writeScenarioArtifacts(dir, map[string]any{
		"request_result": reqRes,
		"mock_events":    events,
		"health":         health,
	}); err != nil {
		return scenarioResult{}, err
	}

	return scenarioResult{
		Name:         "passive_freshness_skips_probe",
		Goal:         "最近有被动流量时不主动探测",
		Passed:       passed,
		DurationMs:   time.Since(start).Milliseconds(),
		SuccessCount: boolToInt(reqRes.StatusCode == http.StatusOK),
		ErrorCount:   boolToInt(reqRes.StatusCode != http.StatusOK),
		MockCounts:   events.Counts,
		Observations: []string{
			fmt.Sprintf("总命中 %d 次", totalEventCount(events.Counts)),
			fmt.Sprintf("无用户主动探测命中 %d 次", probeHits),
			fmt.Sprintf("最后观测来源 %s", targetSnap.LastObservationSource),
		},
		HealthSummary: map[string]string{targetSnap.Key: targetSnap.Status},
		ArtifactDir:   dir,
	}, nil
}

func runStaleTargetTriggersActiveProbe(l *lab) (scenarioResult, error) {
	start := time.Now()
	dir := l.scenarioDir("stale_target_triggers_active_probe")
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-a-ok": {Default: mockProfileAction{Type: "success", DelayMs: 80, ResponseText: "stale active probe success"}},
	}); err != nil {
		return scenarioResult{}, err
	}

	ruleID, err := l.createRule("lab-stale-probe", defaultHealthPolicy(), []routeGroup{
		group("primary", 0, target("site-a-ok", 1)),
	})
	if err != nil {
		return scenarioResult{}, err
	}
	defer l.deleteRule(ruleID)

	reqRes, err := l.sendChat("stale-001")
	if err != nil {
		return scenarioResult{}, err
	}
	events, err := l.waitForProbeHit("site-a-ok", 1, waitTimeout)
	if err != nil {
		return scenarioResult{}, err
	}
	targetSnap, err := l.waitForTargetState(ruleID, targetKey("site-a-ok"), "active", "available", waitTimeout)
	if err != nil {
		return scenarioResult{}, err
	}
	health, err := l.getHealth(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}

	probeHits := countProbeHits(events, "site-a-ok")
	passed := reqRes.StatusCode == http.StatusOK &&
		probeHits >= 1 &&
		targetSnap.LastObservationSource == "active" &&
		targetSnap.LastObservedRequestType == "chat_completion"

	if err := writeScenarioArtifacts(dir, map[string]any{
		"request_result": reqRes,
		"mock_events":    events,
		"health":         health,
	}); err != nil {
		return scenarioResult{}, err
	}

	return scenarioResult{
		Name:         "stale_target_triggers_active_probe",
		Goal:         "静默超过新鲜时间后触发主动探测",
		Passed:       passed,
		DurationMs:   time.Since(start).Milliseconds(),
		SuccessCount: 1,
		ErrorCount:   0,
		MockCounts:   events.Counts,
		Observations: []string{
			fmt.Sprintf("无用户主动探测命中 %d 次", probeHits),
			fmt.Sprintf("最后观测来源 %s", targetSnap.LastObservationSource),
			fmt.Sprintf("最后观测请求类型 %s", targetSnap.LastObservedRequestType),
		},
		HealthSummary: map[string]string{targetSnap.Key: targetSnap.Status},
		ArtifactDir:   dir,
	}, nil
}

func runActiveProbeSuccessRecoversCooldown(l *lab) (scenarioResult, error) {
	start := time.Now()
	dir := l.scenarioDir("active_probe_success_recovers_cooldown")
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-d-hardfail": {Default: mockProfileAction{Type: "error", Status: 502, Message: "trip cooldown"}},
		"site-a-ok":       {Default: mockProfileAction{Type: "success", DelayMs: 60, ResponseText: "backup success"}},
	}); err != nil {
		return scenarioResult{}, err
	}

	ruleID, err := l.createRule("lab-probe-recover", defaultHealthPolicy(), []routeGroup{
		group("primary", 0, target("site-d-hardfail", 1)),
		group("backup", 0, target("site-a-ok", 1)),
	})
	if err != nil {
		return scenarioResult{}, err
	}
	defer l.deleteRule(ruleID)

	firstReq, err := l.sendChat("recover-001")
	if err != nil {
		return scenarioResult{}, err
	}
	secondReq, err := l.sendChat("recover-002")
	if err != nil {
		return scenarioResult{}, err
	}
	cooldownTarget, err := l.waitForTargetState(ruleID, targetKey("site-d-hardfail"), "", "cooldown", 2*time.Second)
	if err != nil {
		return scenarioResult{}, err
	}

	if err := l.setProfiles(map[string]mockProfile{
		"site-d-hardfail": {Default: mockProfileAction{Type: "success", DelayMs: 70, ResponseText: "primary recovered"}},
		"site-a-ok":       {Default: mockProfileAction{Type: "success", DelayMs: 60, ResponseText: "backup still healthy"}},
	}); err != nil {
		return scenarioResult{}, err
	}
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}

	probeEvents, err := l.waitForProbeHit("site-d-hardfail", 1, waitTimeout)
	if err != nil {
		return scenarioResult{}, err
	}
	afterProbeTarget, err := l.waitForTargetState(ruleID, targetKey("site-d-hardfail"), "active", "available", waitTimeout)
	if err != nil {
		return scenarioResult{}, err
	}

	finalReq, err := l.sendChat("recover-003")
	if err != nil {
		return scenarioResult{}, err
	}
	finalEvents, err := l.getMockEvents()
	if err != nil {
		return scenarioResult{}, err
	}
	finalHealth, err := l.getHealth(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}

	passed := firstReq.StatusCode == http.StatusOK &&
		secondReq.StatusCode == http.StatusOK &&
		finalReq.StatusCode == http.StatusOK &&
		cooldownTarget.Status == "cooldown" &&
		afterProbeTarget.Status == "available" &&
		countProbeHits(probeEvents, "site-d-hardfail") >= 1 &&
		countUserHits(finalEvents, "site-d-hardfail") == 1 &&
		countUserHits(finalEvents, "site-a-ok") == 0

	if err := writeScenarioArtifacts(dir, map[string]any{
		"first_request":      firstReq,
		"second_request":     secondReq,
		"probe_events":       probeEvents,
		"after_probe_target": afterProbeTarget,
		"final_request":      finalReq,
		"final_events":       finalEvents,
		"final_health":       finalHealth,
	}); err != nil {
		return scenarioResult{}, err
	}

	return scenarioResult{
		Name:         "active_probe_success_recovers_cooldown",
		Goal:         "主动探测成功后恢复主目标",
		Passed:       passed,
		DurationMs:   time.Since(start).Milliseconds(),
		SuccessCount: 3,
		ErrorCount:   0,
		MockCounts:   finalEvents.Counts,
		Observations: []string{
			fmt.Sprintf("冷却前状态 %s", cooldownTarget.Status),
			fmt.Sprintf("主动探测后状态 %s", afterProbeTarget.Status),
			fmt.Sprintf("恢复后真实请求命中主目标 %d 次", countUserHits(finalEvents, "site-d-hardfail")),
		},
		HealthSummary: map[string]string{afterProbeTarget.Key: afterProbeTarget.Status},
		ArtifactDir:   dir,
	}, nil
}

func runActiveProbeFailureKeepsCooldown(l *lab) (scenarioResult, error) {
	start := time.Now()
	dir := l.scenarioDir("active_probe_failure_keeps_cooldown")
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-d-hardfail": {Default: mockProfileAction{Type: "error", Status: 502, Message: "trip cooldown again"}},
		"site-a-ok":       {Default: mockProfileAction{Type: "success", DelayMs: 60, ResponseText: "backup survives"}},
	}); err != nil {
		return scenarioResult{}, err
	}

	ruleID, err := l.createRule("lab-probe-failure", defaultHealthPolicy(), []routeGroup{
		group("primary", 0, target("site-d-hardfail", 1)),
		group("backup", 0, target("site-a-ok", 1)),
	})
	if err != nil {
		return scenarioResult{}, err
	}
	defer l.deleteRule(ruleID)

	if _, err := l.sendChat("probe-fail-001"); err != nil {
		return scenarioResult{}, err
	}
	if _, err := l.sendChat("probe-fail-002"); err != nil {
		return scenarioResult{}, err
	}
	if _, err := l.waitForTargetState(ruleID, targetKey("site-d-hardfail"), "", "cooldown", 2*time.Second); err != nil {
		return scenarioResult{}, err
	}
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}

	probeEvents, err := l.waitForProbeHit("site-d-hardfail", 1, waitTimeout)
	if err != nil {
		return scenarioResult{}, err
	}
	afterProbeTarget, err := l.waitForTargetState(ruleID, targetKey("site-d-hardfail"), "active", "cooldown", waitTimeout)
	if err != nil {
		return scenarioResult{}, err
	}

	finalReq, err := l.sendChat("probe-fail-003")
	if err != nil {
		return scenarioResult{}, err
	}
	finalEvents, err := l.getMockEvents()
	if err != nil {
		return scenarioResult{}, err
	}
	finalHealth, err := l.getHealth(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}

	passed := finalReq.StatusCode == http.StatusOK &&
		countProbeHits(probeEvents, "site-d-hardfail") >= 1 &&
		countUserHits(finalEvents, "site-d-hardfail") == 0 &&
		countUserHits(finalEvents, "site-a-ok") == 1 &&
		afterProbeTarget.Status == "cooldown"

	if err := writeScenarioArtifacts(dir, map[string]any{
		"probe_events":       probeEvents,
		"after_probe_target": afterProbeTarget,
		"final_request":      finalReq,
		"final_events":       finalEvents,
		"final_health":       finalHealth,
	}); err != nil {
		return scenarioResult{}, err
	}

	return scenarioResult{
		Name:         "active_probe_failure_keeps_cooldown",
		Goal:         "主动探测失败后继续跳过异常目标",
		Passed:       passed,
		DurationMs:   time.Since(start).Milliseconds(),
		SuccessCount: 1,
		ErrorCount:   0,
		MockCounts:   finalEvents.Counts,
		Observations: []string{
			fmt.Sprintf("失败后最后观测来源 %s", afterProbeTarget.LastObservationSource),
			fmt.Sprintf("失败后状态 %s", afterProbeTarget.Status),
			fmt.Sprintf("后续真实请求命中备用目标 %d 次", countUserHits(finalEvents, "site-a-ok")),
		},
		HealthSummary: map[string]string{afterProbeTarget.Key: afterProbeTarget.Status},
		ArtifactDir:   dir,
	}, nil
}

func runHealthStatusObservationFields(l *lab) (scenarioResult, error) {
	start := time.Now()
	dir := l.scenarioDir("health_status_observation_fields")
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-a-ok": {Default: mockProfileAction{Type: "success", DelayMs: 70, ResponseText: "observation metadata"}},
	}); err != nil {
		return scenarioResult{}, err
	}

	ruleID, err := l.createRule("lab-health-metadata", defaultHealthPolicy(), []routeGroup{
		group("primary", 0, target("site-a-ok", 1)),
	})
	if err != nil {
		return scenarioResult{}, err
	}
	defer l.deleteRule(ruleID)

	reqRes, err := l.sendChat("meta-001")
	if err != nil {
		return scenarioResult{}, err
	}
	passiveTarget, err := l.waitForTargetState(ruleID, targetKey("site-a-ok"), "passive", "available", 2*time.Second)
	if err != nil {
		return scenarioResult{}, err
	}
	if passiveTarget.LastObservedAt == nil {
		return scenarioResult{}, fmt.Errorf("passive observation timestamp missing")
	}

	events, err := l.waitForProbeHit("site-a-ok", 1, waitTimeout)
	if err != nil {
		return scenarioResult{}, err
	}
	activeTarget, err := l.waitForTargetState(ruleID, targetKey("site-a-ok"), "active", "available", waitTimeout)
	if err != nil {
		return scenarioResult{}, err
	}
	health, err := l.getHealth(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}

	passiveAt, err := time.Parse(time.RFC3339, *passiveTarget.LastObservedAt)
	if err != nil {
		return scenarioResult{}, fmt.Errorf("parse passive observation time: %w", err)
	}
	activeAt, err := time.Parse(time.RFC3339, valueOrEmpty(activeTarget.LastObservedAt))
	if err != nil {
		return scenarioResult{}, fmt.Errorf("parse active observation time: %w", err)
	}

	passed := reqRes.StatusCode == http.StatusOK &&
		passiveTarget.LastObservedRequestType == "chat_completion" &&
		passiveTarget.LastObservationSource == "passive" &&
		activeTarget.LastObservedRequestType == "chat_completion" &&
		activeTarget.LastObservationSource == "active" &&
		!activeAt.Before(passiveAt) &&
		countProbeHits(events, "site-a-ok") >= 1

	if err := writeScenarioArtifacts(dir, map[string]any{
		"request_result": reqRes,
		"mock_events":    events,
		"health":         health,
		"passive_target": passiveTarget,
		"active_target":  activeTarget,
	}); err != nil {
		return scenarioResult{}, err
	}

	return scenarioResult{
		Name:         "health_status_observation_fields",
		Goal:         "健康状态接口返回观测元数据",
		Passed:       passed,
		DurationMs:   time.Since(start).Milliseconds(),
		SuccessCount: 1,
		ErrorCount:   0,
		MockCounts:   events.Counts,
		Observations: []string{
			fmt.Sprintf("被动观测来源 %s", passiveTarget.LastObservationSource),
			fmt.Sprintf("主动观测来源 %s", activeTarget.LastObservationSource),
			fmt.Sprintf("最后观测请求类型 %s", activeTarget.LastObservedRequestType),
		},
		HealthSummary: map[string]string{activeTarget.Key: activeTarget.Status},
		ArtifactDir:   dir,
	}, nil
}

func (l *lab) setupProvider() error {
	payload := map[string]any{
		"provider": providerName,
		"keys": []map[string]any{
			keyPayload("site-a-ok", keyIDs["site-a-ok"]),
			keyPayload("site-b-slow", keyIDs["site-b-slow"]),
			keyPayload("site-d-hardfail", keyIDs["site-d-hardfail"]),
		},
		"network_config": map[string]any{
			"base_url":                           l.mockAdmin,
			"default_request_timeout_in_seconds": 2,
			"max_retries":                        2,
			"retry_backoff_initial":              100,
			"retry_backoff_max":                  200,
			"max_conns_per_host":                 100,
		},
		"concurrency_and_buffer_size": map[string]any{
			"concurrency": 100,
			"buffer_size": 100,
		},
		"custom_provider_config": map[string]any{
			"is_key_less":        false,
			"base_provider_type": "openai",
		},
	}
	return l.postJSON(l.bifrostURL+"/api/providers", payload, nil)
}

func keyPayload(name, id string) map[string]any {
	return map[string]any{
		"id":      id,
		"name":    name,
		"value":   name,
		"models":  []string{modelName},
		"weight":  1,
		"enabled": true,
	}
}

func target(name string, weight float64) routeTarget {
	return routeTarget{
		Provider: providerName,
		Model:    modelName,
		KeyID:    keyIDs[name],
		Weight:   weight,
	}
}

func targetKey(name string) string {
	return fmt.Sprintf("%s:%s:%s", providerName, modelName, keyIDs[name])
}

func group(name string, retry int, targets ...routeTarget) routeGroup {
	return routeGroup{Name: name, RetryLimit: retry, Targets: targets}
}

func defaultHealthPolicy() healthPolicy {
	return healthPolicy{
		FailureThreshold:     2,
		FailureWindowSeconds: 30,
		CooldownSeconds:      30,
		ConsecutiveFailures:  2,
	}
}

func (l *lab) createRule(name string, hp healthPolicy, groups []routeGroup) (string, error) {
	payload := map[string]any{
		"name":                    name,
		"description":             "hybrid health probing lab scenario",
		"enabled":                 true,
		"cel_expression":          "true",
		"targets":                 []any{},
		"fallbacks":               []any{},
		"scope":                   "global",
		"priority":                10,
		"grouped_routing_enabled": true,
		"health_policy":           hp,
		"route_groups":            groups,
	}
	var resp ruleCreateResponse
	if err := l.postJSON(l.bifrostURL+"/api/governance/routing-rules", payload, &resp); err != nil {
		return "", err
	}
	return resp.Rule.ID, nil
}

func (l *lab) deleteRule(id string) {
	req, _ := http.NewRequest(http.MethodDelete, l.bifrostURL+"/api/governance/routing-rules/"+id, nil)
	resp, err := l.client.Do(req)
	if err == nil && resp != nil {
		resp.Body.Close()
	}
}

func (l *lab) sendChat(user string) (requestResult, error) {
	payload := map[string]any{
		"model": modelName,
		"messages": []map[string]any{
			{"role": "user", "content": "say hello"},
		},
		"user": user,
	}

	var resp chatResponse
	start := time.Now()
	status, body, err := l.doJSON(http.MethodPost, l.bifrostURL+"/v1/chat/completions", payload, &resp)
	if err != nil {
		return requestResult{}, err
	}

	result := requestResult{
		User:       user,
		StatusCode: status,
		LatencyMs:  time.Since(start).Milliseconds(),
	}
	if status == http.StatusOK && len(resp.Choices) > 0 {
		result.Text = resp.Choices[0].Message.Content
	} else if resp.Error != nil {
		result.ErrorText = resp.Error.Message
	} else {
		result.ErrorText = string(body)
	}
	return result, nil
}

func (l *lab) getHealth(ruleID string) (healthAPIResponse, error) {
	var resp healthAPIResponse
	if _, _, err := l.doJSON(http.MethodGet, l.bifrostURL+"/api/governance/health-status", nil, &resp); err != nil {
		return healthAPIResponse{}, err
	}
	filtered := healthAPIResponse{}
	for _, rule := range resp.Rules {
		if rule.RuleID == ruleID {
			filtered.Rules = append(filtered.Rules, rule)
			break
		}
	}
	return filtered, nil
}

func (l *lab) waitForTargetState(ruleID, targetKey, wantSource, wantStatus string, timeout time.Duration) (targetHealthSnapshot, error) {
	deadline := time.Now().Add(timeout)
	var last targetHealthSnapshot
	var seen bool
	for time.Now().Before(deadline) {
		health, err := l.getHealth(ruleID)
		if err == nil {
			if snap, ok := findTarget(health, targetKey); ok {
				last = snap
				seen = true
				if (wantSource == "" || snap.LastObservationSource == wantSource) &&
					(wantStatus == "" || snap.Status == wantStatus) {
					return snap, nil
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if seen {
		return last, fmt.Errorf("timed out waiting for target %s state source=%q status=%q, last source=%q status=%q", targetKey, wantSource, wantStatus, last.LastObservationSource, last.Status)
	}
	return targetHealthSnapshot{}, fmt.Errorf("timed out waiting for target %s to appear in health status", targetKey)
}

func findTarget(resp healthAPIResponse, key string) (targetHealthSnapshot, bool) {
	if len(resp.Rules) == 0 {
		return targetHealthSnapshot{}, false
	}
	for _, target := range resp.Rules[0].Targets {
		if target.Key == key {
			return target, true
		}
	}
	return targetHealthSnapshot{}, false
}

func (l *lab) waitForProbeHit(key string, minCount int, timeout time.Duration) (mockEventsResponse, error) {
	deadline := time.Now().Add(timeout)
	var last mockEventsResponse
	for time.Now().Before(deadline) {
		resp, err := l.getMockEvents()
		if err == nil {
			last = resp
			if countProbeHits(resp, key) >= minCount {
				return resp, nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return last, fmt.Errorf("timed out waiting for %d probe hits on %s", minCount, key)
}

func countProbeHits(resp mockEventsResponse, key string) int {
	count := 0
	for _, ev := range resp.Events {
		if ev.Key == key && ev.User == "" {
			count++
		}
	}
	return count
}

func countUserHits(resp mockEventsResponse, key string) int {
	count := 0
	for _, ev := range resp.Events {
		if ev.Key == key && ev.User != "" {
			count++
		}
	}
	return count
}

func totalEventCount(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

func (l *lab) setProfiles(profiles map[string]mockProfile) error {
	return l.postJSON(l.mockAdmin+"/__admin/profiles", mockProfilesPayload{Profiles: profiles}, nil)
}

func (l *lab) resetMock() error {
	return l.postJSON(l.mockAdmin+"/__admin/reset", map[string]any{}, nil)
}

func (l *lab) getMockEvents() (mockEventsResponse, error) {
	var resp mockEventsResponse
	if _, _, err := l.doJSON(http.MethodGet, l.mockAdmin+"/__admin/events", nil, &resp); err != nil {
		return mockEventsResponse{}, err
	}
	if resp.Counts == nil {
		resp.Counts = map[string]int{}
	}
	return resp, nil
}

func (l *lab) postJSON(url string, payload any, out any) error {
	_, _, err := l.doJSON(http.MethodPost, url, payload, out)
	return err
}

func (l *lab) doJSON(method, url string, payload any, out any) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return 0, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if resp.StatusCode >= 400 && out == nil {
		return resp.StatusCode, data, nil
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, data, fmt.Errorf("unmarshal %s: %w", url, err)
		}
	}
	return resp.StatusCode, data, nil
}

func (l *lab) scenarioDir(name string) string {
	dir := filepath.Join(l.outputDir, "evidence", name)
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func writeScenarioArtifacts(dir string, files map[string]any) error {
	for name, payload := range files {
		if err := writeJSONFile(filepath.Join(dir, name+".json"), payload); err != nil {
			return err
		}
	}
	return nil
}

func writeJSONFile(path string, payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
