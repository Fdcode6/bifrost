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
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	providerName = "mock-lab"
	modelName    = "gpt-4.1"
)

var keyIDs = map[string]string{
	"site-a-ok":       "site-a-ok-id",
	"site-b-slow":     "site-b-slow-id",
	"site-c-flaky":    "site-c-flaky-id",
	"site-d-hardfail": "site-d-hardfail-id",
	"site-e-timeout":  "site-e-timeout-id",
	"site-f-recover":  "site-f-recover-id",
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
	Name             string            `json:"name"`
	Goal             string            `json:"goal"`
	Passed           bool              `json:"passed"`
	DurationMs       int64             `json:"duration_ms"`
	SuccessCount     int               `json:"success_count"`
	ErrorCount       int               `json:"error_count"`
	MockCounts       map[string]int    `json:"mock_counts,omitempty"`
	Observations     []string          `json:"observations"`
	SampleRoutingLog string            `json:"sample_routing_log,omitempty"`
	HealthSummary    map[string]string `json:"health_summary,omitempty"`
	ArtifactDir      string            `json:"artifact_dir"`
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
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

type healthAPIResponse struct {
	Rules []struct {
		RuleID   string `json:"rule_id"`
		RuleName string `json:"rule_name"`
		Targets  []struct {
			Key                 string  `json:"key"`
			Status              string  `json:"status"`
			FailureCount        int     `json:"failure_count"`
			ConsecutiveFailures int     `json:"consecutive_failures"`
			CooldownUntil       *string `json:"cooldown_until,omitempty"`
			LastFailureMsg      string  `json:"last_failure_msg,omitempty"`
		} `json:"targets"`
	} `json:"rules"`
}

type logsResponse struct {
	Logs []struct {
		ID                string  `json:"id"`
		Status            string  `json:"status"`
		Latency           float64 `json:"latency"`
		RoutingEngineLogs string  `json:"routing_engine_logs"`
	} `json:"logs"`
}

type mockEventsResponse struct {
	Events []struct {
		Timestamp  string `json:"timestamp"`
		Path       string `json:"path"`
		Key        string `json:"key"`
		Model      string `json:"model"`
		User       string `json:"user"`
		ActionType string `json:"action_type"`
		StatusCode int    `json:"status_code"`
	} `json:"events"`
	Counts map[string]int `json:"counts"`
}

type ruleCreateResponse struct {
	Rule struct {
		ID string `json:"id"`
	} `json:"rule"`
}

type lab struct {
	bifrostURL string
	mockAdmin  string
	outputDir  string
	client     *http.Client
}

type requestResult struct {
	User       string `json:"user"`
	StatusCode int    `json:"status_code"`
	Text       string `json:"text,omitempty"`
	ErrorText  string `json:"error_text,omitempty"`
	LatencyMs  int64  `json:"latency_ms"`
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
		{"baseline_primary_success", "主站点直接成功", runBaselinePrimarySuccess},
		{"same_request_no_repeat", "同请求内不重复打失败站点", runSameRequestNoRepeat},
		{"cross_group_failover", "主组失败后切备用组", runCrossGroupFailover},
		{"cooldown_skip", "触发冷却后直接跳过异常站点", runCooldownSkip},
		{"cooldown_recovery", "冷却结束后恢复尝试", runCooldownRecovery},
		{"timeout_failover", "超时后切换到下一站点", runTimeoutFailover},
		{"final_group_rescue", "前两组失败时最终兜底组产出结果", runFinalGroupRescue},
		{"pressure_mixed_failures", "中等并发压力下保持成功与不重复命中失败站点", runPressureScenario},
		{"all_groups_down_boundary", "全部分组都失败时返回错误并保留可解释日志", runAllGroupsDownBoundary},
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

func runBaselinePrimarySuccess(l *lab) (scenarioResult, error) {
	start := time.Now()
	dir := l.scenarioDir("baseline_primary_success")
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-a-ok":   {Default: mockProfileAction{Type: "success", DelayMs: 120, ResponseText: "primary success"}},
		"site-b-slow": {Default: mockProfileAction{Type: "success", DelayMs: 1200, ResponseText: "slow success"}},
	}); err != nil {
		return scenarioResult{}, err
	}
	ruleID, err := l.createRule("lab-baseline", healthPolicy{2, 30, 30, 2}, []routeGroup{
		group("primary", 0, target("site-a-ok", 1)),
		group("backup", 0, target("site-b-slow", 1)),
	})
	if err != nil {
		return scenarioResult{}, err
	}
	defer l.deleteRule(ruleID)

	reqRes, err := l.sendChat("baseline-001")
	if err != nil {
		return scenarioResult{}, err
	}
	events, err := l.getMockEvents()
	if err != nil {
		return scenarioResult{}, err
	}
	logs, err := l.getLogs(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}

	observations := []string{}
	passed := reqRes.StatusCode == http.StatusOK && events.Counts["site-a-ok"] == 1 && totalEventCount(events.Counts) == 1
	if reqRes.StatusCode == http.StatusOK {
		observations = append(observations, "请求返回成功")
	} else {
		observations = append(observations, "请求未成功返回")
	}
	observations = append(observations, fmt.Sprintf("主站点命中 %d 次", events.Counts["site-a-ok"]))

	if err := writeScenarioArtifacts(dir, map[string]any{
		"request_result": reqRes,
		"mock_events":    events,
		"logs":           logs,
	}); err != nil {
		return scenarioResult{}, err
	}

	return scenarioResult{
		Name:             "baseline_primary_success",
		Goal:             "主站点直接成功",
		Passed:           passed,
		DurationMs:       time.Since(start).Milliseconds(),
		SuccessCount:     boolToInt(reqRes.StatusCode == http.StatusOK),
		ErrorCount:       boolToInt(reqRes.StatusCode != http.StatusOK),
		MockCounts:       events.Counts,
		Observations:     observations,
		SampleRoutingLog: firstRoutingLog(logs),
		ArtifactDir:      dir,
	}, nil
}

func runSameRequestNoRepeat(l *lab) (scenarioResult, error) {
	start := time.Now()
	dir := l.scenarioDir("same_request_no_repeat")
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-d-hardfail": {Default: mockProfileAction{Type: "error", Status: 502, Message: "primary exploded"}},
		"site-a-ok":       {Default: mockProfileAction{Type: "success", DelayMs: 80, ResponseText: "fallback in same group"}},
	}); err != nil {
		return scenarioResult{}, err
	}
	ruleID, err := l.createRule("lab-no-repeat", healthPolicy{2, 30, 30, 2}, []routeGroup{
		group("primary", 1, target("site-d-hardfail", 1), target("site-a-ok", 0)),
	})
	if err != nil {
		return scenarioResult{}, err
	}
	defer l.deleteRule(ruleID)

	reqRes, err := l.sendChat("no-repeat-001")
	if err != nil {
		return scenarioResult{}, err
	}
	events, err := l.getMockEvents()
	if err != nil {
		return scenarioResult{}, err
	}
	logs, err := l.getLogs(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}

	duplicateViolation := repeatedKeyPerUser(events, "no-repeat-001", "site-d-hardfail") > 1
	passed := reqRes.StatusCode == http.StatusOK &&
		events.Counts["site-d-hardfail"] == 1 &&
		events.Counts["site-a-ok"] == 1 &&
		!duplicateViolation

	if err := writeScenarioArtifacts(dir, map[string]any{
		"request_result":      reqRes,
		"mock_events":         events,
		"logs":                logs,
		"duplicate_violation": duplicateViolation,
	}); err != nil {
		return scenarioResult{}, err
	}

	return scenarioResult{
		Name:         "same_request_no_repeat",
		Goal:         "同请求内不重复打失败站点",
		Passed:       passed,
		DurationMs:   time.Since(start).Milliseconds(),
		SuccessCount: boolToInt(reqRes.StatusCode == http.StatusOK),
		ErrorCount:   boolToInt(reqRes.StatusCode != http.StatusOK),
		MockCounts:   events.Counts,
		Observations: []string{
			fmt.Sprintf("失败站点命中 %d 次", events.Counts["site-d-hardfail"]),
			fmt.Sprintf("同组备用站点命中 %d 次", events.Counts["site-a-ok"]),
		},
		SampleRoutingLog: firstRoutingLog(logs),
		ArtifactDir:      dir,
	}, nil
}

func runCrossGroupFailover(l *lab) (scenarioResult, error) {
	start := time.Now()
	dir := l.scenarioDir("cross_group_failover")
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-d-hardfail": {Default: mockProfileAction{Type: "error", Status: 502, Message: "primary group failed"}},
		"site-f-recover":  {Default: mockProfileAction{Type: "success", DelayMs: 90, ResponseText: "backup group success"}},
	}); err != nil {
		return scenarioResult{}, err
	}
	ruleID, err := l.createRule("lab-cross-group", healthPolicy{2, 30, 30, 2}, []routeGroup{
		group("primary", 0, target("site-d-hardfail", 1)),
		group("backup", 0, target("site-f-recover", 1)),
	})
	if err != nil {
		return scenarioResult{}, err
	}
	defer l.deleteRule(ruleID)

	reqRes, err := l.sendChat("cross-group-001")
	if err != nil {
		return scenarioResult{}, err
	}
	events, err := l.getMockEvents()
	if err != nil {
		return scenarioResult{}, err
	}
	logs, err := l.getLogs(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}

	passed := reqRes.StatusCode == http.StatusOK &&
		events.Counts["site-d-hardfail"] == 1 &&
		events.Counts["site-f-recover"] == 1

	if err := writeScenarioArtifacts(dir, map[string]any{
		"request_result": reqRes,
		"mock_events":    events,
		"logs":           logs,
	}); err != nil {
		return scenarioResult{}, err
	}

	return scenarioResult{
		Name:         "cross_group_failover",
		Goal:         "主组失败后切备用组",
		Passed:       passed,
		DurationMs:   time.Since(start).Milliseconds(),
		SuccessCount: boolToInt(reqRes.StatusCode == http.StatusOK),
		ErrorCount:   boolToInt(reqRes.StatusCode != http.StatusOK),
		MockCounts:   events.Counts,
		Observations: []string{
			fmt.Sprintf("主组命中 %d 次，备用组命中 %d 次", events.Counts["site-d-hardfail"], events.Counts["site-f-recover"]),
		},
		SampleRoutingLog: firstRoutingLog(logs),
		ArtifactDir:      dir,
	}, nil
}

func runCooldownSkip(l *lab) (scenarioResult, error) {
	start := time.Now()
	dir := l.scenarioDir("cooldown_skip")
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-d-hardfail": {Default: mockProfileAction{Type: "error", Status: 502, Message: "trip cooldown"}},
		"site-a-ok":       {Default: mockProfileAction{Type: "success", DelayMs: 70, ResponseText: "backup after cooldown"}},
	}); err != nil {
		return scenarioResult{}, err
	}
	ruleID, err := l.createRule("lab-cooldown-skip", healthPolicy{2, 30, 30, 2}, []routeGroup{
		group("primary", 0, target("site-d-hardfail", 1)),
		group("backup", 0, target("site-a-ok", 1)),
	})
	if err != nil {
		return scenarioResult{}, err
	}
	defer l.deleteRule(ruleID)

	firstReq, err := l.sendChat("cooldown-001")
	if err != nil {
		return scenarioResult{}, err
	}
	secondReq, err := l.sendChat("cooldown-002")
	if err != nil {
		return scenarioResult{}, err
	}
	healthBefore, err := l.getHealth(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	thirdReq, err := l.sendChat("cooldown-003")
	if err != nil {
		return scenarioResult{}, err
	}
	eventsAfterSkip, err := l.getMockEvents()
	if err != nil {
		return scenarioResult{}, err
	}
	logs, err := l.getLogs(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}

	healthSummary := map[string]string{}
	for _, target := range firstHealthTargets(healthBefore) {
		healthSummary[target.Key] = target.Status
	}

	passed := firstReq.StatusCode == http.StatusOK &&
		secondReq.StatusCode == http.StatusOK &&
		thirdReq.StatusCode == http.StatusOK &&
		healthSummary[fmt.Sprintf("%s:%s:%s", providerName, modelName, keyIDs["site-d-hardfail"])] == "cooldown" &&
		eventsAfterSkip.Counts["site-d-hardfail"] == 0 &&
		eventsAfterSkip.Counts["site-a-ok"] == 1

	if err := writeScenarioArtifacts(dir, map[string]any{
		"first_request":   firstReq,
		"second_request":  secondReq,
		"third_request":   thirdReq,
		"health_before":   healthBefore,
		"mock_after_skip": eventsAfterSkip,
		"logs":            logs,
	}); err != nil {
		return scenarioResult{}, err
	}

	return scenarioResult{
		Name:         "cooldown_skip",
		Goal:         "触发冷却后直接跳过异常站点",
		Passed:       passed,
		DurationMs:   time.Since(start).Milliseconds(),
		SuccessCount: 3,
		ErrorCount:   0,
		MockCounts:   eventsAfterSkip.Counts,
		Observations: []string{
			"前两次请求用于触发冷却",
			fmt.Sprintf("冷却后第三次请求对异常站点命中 %d 次", eventsAfterSkip.Counts["site-d-hardfail"]),
		},
		SampleRoutingLog: firstRoutingLog(logs),
		HealthSummary:    healthSummary,
		ArtifactDir:      dir,
	}, nil
}

func runCooldownRecovery(l *lab) (scenarioResult, error) {
	start := time.Now()
	dir := l.scenarioDir("cooldown_recovery")
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-d-hardfail": {Default: mockProfileAction{Type: "error", Status: 502, Message: "cooldown recovery prep"}},
		"site-a-ok":       {Default: mockProfileAction{Type: "success", DelayMs: 60, ResponseText: "temporary backup"}},
	}); err != nil {
		return scenarioResult{}, err
	}
	ruleID, err := l.createRule("lab-cooldown-recovery", healthPolicy{2, 30, 30, 2}, []routeGroup{
		group("primary", 0, target("site-d-hardfail", 1)),
		group("backup", 0, target("site-a-ok", 1)),
	})
	if err != nil {
		return scenarioResult{}, err
	}
	defer l.deleteRule(ruleID)

	if _, err := l.sendChat("recover-001"); err != nil {
		return scenarioResult{}, err
	}
	if _, err := l.sendChat("recover-002"); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-d-hardfail": {Default: mockProfileAction{Type: "success", DelayMs: 90, ResponseText: "recovered primary"}},
		"site-a-ok":       {Default: mockProfileAction{Type: "success", DelayMs: 60, ResponseText: "backup still healthy"}},
	}); err != nil {
		return scenarioResult{}, err
	}
	time.Sleep(31 * time.Second)
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	reqRes, err := l.sendChat("recover-003")
	if err != nil {
		return scenarioResult{}, err
	}
	events, err := l.getMockEvents()
	if err != nil {
		return scenarioResult{}, err
	}
	healthAfter, err := l.getHealth(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}
	logs, err := l.getLogs(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}

	healthSummary := map[string]string{}
	for _, target := range firstHealthTargets(healthAfter) {
		healthSummary[target.Key] = target.Status
	}

	passed := reqRes.StatusCode == http.StatusOK &&
		events.Counts["site-d-hardfail"] == 1 &&
		events.Counts["site-a-ok"] == 0

	if err := writeScenarioArtifacts(dir, map[string]any{
		"request_result": reqRes,
		"mock_events":    events,
		"health_after":   healthAfter,
		"logs":           logs,
	}); err != nil {
		return scenarioResult{}, err
	}

	return scenarioResult{
		Name:         "cooldown_recovery",
		Goal:         "冷却结束后恢复尝试",
		Passed:       passed,
		DurationMs:   time.Since(start).Milliseconds(),
		SuccessCount: boolToInt(reqRes.StatusCode == http.StatusOK),
		ErrorCount:   boolToInt(reqRes.StatusCode != http.StatusOK),
		MockCounts:   events.Counts,
		Observations: []string{
			"等待 31 秒后重新发起请求",
			fmt.Sprintf("恢复后主站点命中 %d 次", events.Counts["site-d-hardfail"]),
		},
		SampleRoutingLog: firstRoutingLog(logs),
		HealthSummary:    healthSummary,
		ArtifactDir:      dir,
	}, nil
}

func runTimeoutFailover(l *lab) (scenarioResult, error) {
	start := time.Now()
	dir := l.scenarioDir("timeout_failover")
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-e-timeout": {Default: mockProfileAction{Type: "timeout", DelayMs: 4000, Message: "simulate timeout"}},
		"site-f-recover": {Default: mockProfileAction{Type: "success", DelayMs: 80, ResponseText: "after timeout"}},
	}); err != nil {
		return scenarioResult{}, err
	}
	ruleID, err := l.createRule("lab-timeout", healthPolicy{2, 30, 30, 2}, []routeGroup{
		group("primary", 0, target("site-e-timeout", 1)),
		group("backup", 0, target("site-f-recover", 1)),
	})
	if err != nil {
		return scenarioResult{}, err
	}
	defer l.deleteRule(ruleID)

	reqRes, err := l.sendChat("timeout-001")
	if err != nil {
		return scenarioResult{}, err
	}
	events, err := l.getMockEvents()
	if err != nil {
		return scenarioResult{}, err
	}
	logs, err := l.getLogs(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}

	passed := reqRes.StatusCode == http.StatusOK &&
		events.Counts["site-e-timeout"] == 1 &&
		events.Counts["site-f-recover"] == 1

	if err := writeScenarioArtifacts(dir, map[string]any{
		"request_result": reqRes,
		"mock_events":    events,
		"logs":           logs,
	}); err != nil {
		return scenarioResult{}, err
	}

	return scenarioResult{
		Name:             "timeout_failover",
		Goal:             "超时后切换到下一站点",
		Passed:           passed,
		DurationMs:       time.Since(start).Milliseconds(),
		SuccessCount:     boolToInt(reqRes.StatusCode == http.StatusOK),
		ErrorCount:       boolToInt(reqRes.StatusCode != http.StatusOK),
		MockCounts:       events.Counts,
		Observations:     []string{fmt.Sprintf("超时站点命中 %d 次，备用站点命中 %d 次", events.Counts["site-e-timeout"], events.Counts["site-f-recover"])},
		SampleRoutingLog: firstRoutingLog(logs),
		ArtifactDir:      dir,
	}, nil
}

func runFinalGroupRescue(l *lab) (scenarioResult, error) {
	start := time.Now()
	dir := l.scenarioDir("final_group_rescue")
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-d-hardfail": {Default: mockProfileAction{Type: "error", Status: 502, Message: "primary down"}},
		"site-f-recover":  {Default: mockProfileAction{Type: "error", Status: 503, Message: "backup down"}},
		"site-b-slow":     {Default: mockProfileAction{Type: "success", DelayMs: 1500, ResponseText: "final rescue success"}},
	}); err != nil {
		return scenarioResult{}, err
	}
	ruleID, err := l.createRule("lab-final-rescue", healthPolicy{2, 30, 30, 2}, []routeGroup{
		group("primary", 0, target("site-d-hardfail", 1)),
		group("backup", 0, target("site-f-recover", 1)),
		group("final", 0, target("site-b-slow", 1)),
	})
	if err != nil {
		return scenarioResult{}, err
	}
	defer l.deleteRule(ruleID)

	reqRes, err := l.sendChat("final-001")
	if err != nil {
		return scenarioResult{}, err
	}
	events, err := l.getMockEvents()
	if err != nil {
		return scenarioResult{}, err
	}
	logs, err := l.getLogs(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}

	passed := reqRes.StatusCode == http.StatusOK &&
		events.Counts["site-d-hardfail"] == 1 &&
		events.Counts["site-f-recover"] == 1 &&
		events.Counts["site-b-slow"] == 1

	if err := writeScenarioArtifacts(dir, map[string]any{
		"request_result": reqRes,
		"mock_events":    events,
		"logs":           logs,
	}); err != nil {
		return scenarioResult{}, err
	}

	return scenarioResult{
		Name:             "final_group_rescue",
		Goal:             "前两组失败时最终兜底组产出结果",
		Passed:           passed,
		DurationMs:       time.Since(start).Milliseconds(),
		SuccessCount:     boolToInt(reqRes.StatusCode == http.StatusOK),
		ErrorCount:       boolToInt(reqRes.StatusCode != http.StatusOK),
		MockCounts:       events.Counts,
		Observations:     []string{"最终兜底组提供成功结果"},
		SampleRoutingLog: firstRoutingLog(logs),
		ArtifactDir:      dir,
	}, nil
}

func runPressureScenario(l *lab) (scenarioResult, error) {
	start := time.Now()
	dir := l.scenarioDir("pressure_mixed_failures")
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-d-hardfail": {Default: mockProfileAction{Type: "error", Status: 502, Message: "load fail"}},
		"site-a-ok":       {Default: mockProfileAction{Type: "success", DelayMs: 80, ResponseText: "load success"}},
	}); err != nil {
		return scenarioResult{}, err
	}
	ruleID, err := l.createRule("lab-pressure", healthPolicy{2, 30, 30, 2}, []routeGroup{
		group("primary", 1, target("site-d-hardfail", 1), target("site-a-ok", 0)),
	})
	if err != nil {
		return scenarioResult{}, err
	}
	defer l.deleteRule(ruleID)

	const total = 30
	const concurrency = 10
	results := make([]requestResult, total)
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res, err := l.sendChat(fmt.Sprintf("load-%03d", idx+1))
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			results[idx] = res
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		return scenarioResult{}, firstErr
	}

	events, err := l.getMockEvents()
	if err != nil {
		return scenarioResult{}, err
	}
	logs, err := l.getLogs(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}
	healthState, err := l.getHealth(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}

	successCount := 0
	for _, res := range results {
		if res.StatusCode == http.StatusOK {
			successCount++
		}
	}
	duplicateViolations := duplicateUsers(events)
	healthSummary := map[string]string{}
	for _, target := range firstHealthTargets(healthState) {
		healthSummary[target.Key] = target.Status
	}

	passed := successCount == total && len(duplicateViolations) == 0

	if err := writeScenarioArtifacts(dir, map[string]any{
		"requests":             results,
		"mock_events":          events,
		"logs":                 logs,
		"health":               healthState,
		"duplicate_violations": duplicateViolations,
	}); err != nil {
		return scenarioResult{}, err
	}

	obs := []string{
		fmt.Sprintf("并发 %d，总请求 %d，成功 %d", concurrency, total, successCount),
		fmt.Sprintf("失败站点总命中 %d 次", events.Counts["site-d-hardfail"]),
	}
	if len(duplicateViolations) > 0 {
		obs = append(obs, "发现同一请求重复命中失败站点")
	} else {
		obs = append(obs, "未发现同一请求重复命中失败站点")
	}

	return scenarioResult{
		Name:             "pressure_mixed_failures",
		Goal:             "中等并发压力下保持成功与不重复命中失败站点",
		Passed:           passed,
		DurationMs:       time.Since(start).Milliseconds(),
		SuccessCount:     successCount,
		ErrorCount:       total - successCount,
		MockCounts:       events.Counts,
		Observations:     obs,
		SampleRoutingLog: firstRoutingLog(logs),
		HealthSummary:    healthSummary,
		ArtifactDir:      dir,
	}, nil
}

func runAllGroupsDownBoundary(l *lab) (scenarioResult, error) {
	start := time.Now()
	dir := l.scenarioDir("all_groups_down_boundary")
	if err := l.resetMock(); err != nil {
		return scenarioResult{}, err
	}
	if err := l.setProfiles(map[string]mockProfile{
		"site-d-hardfail": {Default: mockProfileAction{Type: "error", Status: 502, Message: "primary dead"}},
		"site-e-timeout":  {Default: mockProfileAction{Type: "timeout", DelayMs: 4000, Message: "backup timeout"}},
		"site-f-recover":  {Default: mockProfileAction{Type: "error", Status: 503, Message: "final dead"}},
	}); err != nil {
		return scenarioResult{}, err
	}
	ruleID, err := l.createRule("lab-boundary", healthPolicy{2, 30, 30, 2}, []routeGroup{
		group("primary", 0, target("site-d-hardfail", 1)),
		group("backup", 0, target("site-e-timeout", 1)),
		group("final", 0, target("site-f-recover", 1)),
	})
	if err != nil {
		return scenarioResult{}, err
	}
	defer l.deleteRule(ruleID)

	reqRes, err := l.sendChat("boundary-001")
	if err != nil {
		return scenarioResult{}, err
	}
	events, err := l.getMockEvents()
	if err != nil {
		return scenarioResult{}, err
	}
	logs, err := l.getLogs(ruleID)
	if err != nil {
		return scenarioResult{}, err
	}

	passed := reqRes.StatusCode >= 500 &&
		events.Counts["site-d-hardfail"] == 1 &&
		events.Counts["site-e-timeout"] == 1 &&
		events.Counts["site-f-recover"] == 1

	if err := writeScenarioArtifacts(dir, map[string]any{
		"request_result": reqRes,
		"mock_events":    events,
		"logs":           logs,
	}); err != nil {
		return scenarioResult{}, err
	}

	return scenarioResult{
		Name:             "all_groups_down_boundary",
		Goal:             "全部分组都失败时返回错误并保留可解释日志",
		Passed:           passed,
		DurationMs:       time.Since(start).Milliseconds(),
		SuccessCount:     0,
		ErrorCount:       1,
		MockCounts:       events.Counts,
		Observations:     []string{"该场景用于确认系统边界，而不是追求成功返回"},
		SampleRoutingLog: firstRoutingLog(logs),
		ArtifactDir:      dir,
	}, nil
}

func (l *lab) setupProvider() error {
	payload := map[string]any{
		"provider": providerName,
		"keys": []map[string]any{
			keyPayload("site-a-ok", keyIDs["site-a-ok"]),
			keyPayload("site-b-slow", keyIDs["site-b-slow"]),
			keyPayload("site-c-flaky", keyIDs["site-c-flaky"]),
			keyPayload("site-d-hardfail", keyIDs["site-d-hardfail"]),
			keyPayload("site-e-timeout", keyIDs["site-e-timeout"]),
			keyPayload("site-f-recover", keyIDs["site-f-recover"]),
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

func group(name string, retry int, targets ...routeTarget) routeGroup {
	return routeGroup{Name: name, RetryLimit: retry, Targets: targets}
}

func (l *lab) createRule(name string, hp healthPolicy, groups []routeGroup) (string, error) {
	payload := map[string]any{
		"name":                    name,
		"description":             "grouped routing lab scenario",
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
	} else {
		if resp.Error != nil {
			result.ErrorText = resp.Error.Message
		} else {
			result.ErrorText = string(body)
		}
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

func (l *lab) getLogs(ruleID string) (logsResponse, error) {
	url := fmt.Sprintf("%s/api/logs?routing_rule_ids=%s&limit=20&sort_by=timestamp&order=desc", l.bifrostURL, ruleID)
	var lastResp logsResponse
	var lastErr error
	for attempt := 0; attempt < 15; attempt++ {
		var resp logsResponse
		if _, _, err := l.doJSON(http.MethodGet, url, nil, &resp); err != nil {
			lastErr = err
		} else {
			lastResp = resp
			for _, log := range resp.Logs {
				if strings.TrimSpace(log.RoutingEngineLogs) != "" {
					return resp, nil
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if lastErr != nil {
		return logsResponse{}, lastErr
	}
	return lastResp, nil
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

func firstRoutingLog(resp logsResponse) string {
	for _, logItem := range resp.Logs {
		if strings.TrimSpace(logItem.RoutingEngineLogs) != "" {
			return logItem.RoutingEngineLogs
		}
	}
	return ""
}

func firstHealthTargets(resp healthAPIResponse) []struct {
	Key                 string  `json:"key"`
	Status              string  `json:"status"`
	FailureCount        int     `json:"failure_count"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	CooldownUntil       *string `json:"cooldown_until,omitempty"`
	LastFailureMsg      string  `json:"last_failure_msg,omitempty"`
} {
	if len(resp.Rules) == 0 {
		return nil
	}
	return resp.Rules[0].Targets
}

func repeatedKeyPerUser(events mockEventsResponse, user, key string) int {
	count := 0
	for _, ev := range events.Events {
		if ev.User == user && ev.Key == key {
			count++
		}
	}
	return count
}

func duplicateUsers(events mockEventsResponse) []string {
	perUserKey := make(map[string]map[string]int)
	for _, ev := range events.Events {
		if ev.User == "" || ev.Key == "" {
			continue
		}
		if _, ok := perUserKey[ev.User]; !ok {
			perUserKey[ev.User] = make(map[string]int)
		}
		perUserKey[ev.User][ev.Key]++
	}
	var violations []string
	for user, counts := range perUserKey {
		for key, count := range counts {
			if count > 1 {
				violations = append(violations, fmt.Sprintf("%s:%s=%d", user, key, count))
			}
		}
	}
	sort.Strings(violations)
	return violations
}

func totalEventCount(counts map[string]int) int {
	total := 0
	for _, v := range counts {
		total += v
	}
	return total
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
