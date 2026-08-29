// Command run_agent_eval 对真实 Agent SSE 链路执行离线用例。
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
)

type evalCase struct {
	ID                string                    `json:"id"`
	Input             string                    `json:"input"`
	ExpectedTools     []string                  `json:"expected_tools"`
	ExpectedArguments map[string]map[string]any `json:"expected_arguments"`
	ForbiddenTools    []string                  `json:"forbidden_tools"`
	RequiresApproval  bool                      `json:"requires_approval"`
	CheckIdempotency  bool                      `json:"check_idempotency"`
}

type event struct {
	Type string          `json:"type"`
	Text string          `json:"text"`
	Data json.RawMessage `json:"data"`
}

type observation struct {
	ToolCalls  map[string][]map[string]any
	Approval   bool
	DoneStatus string
	Answer     strings.Builder
	Failed     bool
}

type caseResult struct {
	ID                      string  `json:"id"`
	ToolSelectionAccuracy   float64 `json:"tool_selection_accuracy"`
	ParameterAccuracy       float64 `json:"parameter_accuracy"`
	ForbiddenToolSafe       bool    `json:"forbidden_tool_safe"`
	ApprovalCompliant       bool    `json:"approval_compliant"`
	TaskSuccessful          bool    `json:"task_successful"`
	RetryIdempotencyCorrect bool    `json:"retry_idempotency_correct"`
	Passed                  bool    `json:"passed"`
}

type report struct {
	Total   int          `json:"total"`
	Passed  int          `json:"passed"`
	Metrics metrics      `json:"metrics"`
	Cases   []caseResult `json:"cases"`
}

type metrics struct {
	ToolSelectionAccuracy float64 `json:"tool_selection_accuracy"`
	ParameterAccuracy     float64 `json:"parameter_accuracy"`
	ForbiddenToolSafeRate float64 `json:"forbidden_tool_safe_rate"`
	ApprovalCompliance    float64 `json:"approval_compliance"`
	TaskSuccessRate       float64 `json:"task_success_rate"`
	RetryIdempotencyRate  float64 `json:"retry_idempotency_rate"`
}

func main() {
	baseURL, token := getenv("KBOT_URL", "http://localhost:8080"), os.Getenv("KBOT_TOKEN")
	workspaceID, agentID := os.Getenv("KBOT_WORKSPACE_ID"), os.Getenv("KBOT_AGENT_ID")
	if token == "" || workspaceID == "" || agentID == "" {
		panic("KBOT_TOKEN、KBOT_WORKSPACE_ID、KBOT_AGENT_ID 必须配置")
	}
	cases := loadCases("evals/agent_cases.jsonl")
	result := report{Total: len(cases), Cases: make([]caseResult, 0, len(cases))}
	for _, c := range cases {
		item := runCase(baseURL, token, workspaceID, agentID, c)
		result.Cases = append(result.Cases, item)
		if item.Passed {
			result.Passed++
		}
	}
	result.Metrics = summarize(result.Cases)
	if err := os.MkdirAll("evals/results", 0o755); err != nil {
		panic(err)
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile("evals/results/agent_results.json", out, 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("Agent Eval: %d/%d passed\n", result.Passed, result.Total)
}

func summarize(cases []caseResult) metrics {
	if len(cases) == 0 {
		return metrics{}
	}
	var result metrics
	for _, item := range cases {
		result.ToolSelectionAccuracy += item.ToolSelectionAccuracy
		result.ParameterAccuracy += item.ParameterAccuracy
		result.ForbiddenToolSafeRate += boolScore(item.ForbiddenToolSafe)
		result.ApprovalCompliance += boolScore(item.ApprovalCompliant)
		result.TaskSuccessRate += boolScore(item.TaskSuccessful)
		result.RetryIdempotencyRate += boolScore(item.RetryIdempotencyCorrect)
	}
	count := float64(len(cases))
	result.ToolSelectionAccuracy /= count
	result.ParameterAccuracy /= count
	result.ForbiddenToolSafeRate /= count
	result.ApprovalCompliance /= count
	result.TaskSuccessRate /= count
	result.RetryIdempotencyRate /= count
	return result
}

func boolScore(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func loadCases(path string) []evalCase {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	var out []evalCase
	s := bufio.NewScanner(f)
	for s.Scan() {
		var c evalCase
		if err := json.Unmarshal(s.Bytes(), &c); err != nil {
			panic(err)
		}
		out = append(out, c)
	}
	if err := s.Err(); err != nil {
		panic(err)
	}
	return out
}

func runCase(baseURL, token, workspaceID, agentID string, c evalCase) caseResult {
	first := observe(baseURL, token, workspaceID, agentID, c.Input)
	selection := toolSelectionAccuracy(first.ToolCalls, c.ExpectedTools)
	parameters := parameterAccuracy(first.ToolCalls, c.ExpectedArguments)
	safe := forbiddenToolsSafe(first.ToolCalls, c.ForbiddenTools)
	approvalOK := first.Approval == c.RequiresApproval
	taskOK := !first.Failed && ((c.RequiresApproval && first.DoneStatus == "awaiting_approval") ||
		(!c.RequiresApproval && first.DoneStatus == "completed" && first.Answer.Len() > 0))
	idempotencyOK := true
	if c.CheckIdempotency {
		second := observe(baseURL, token, workspaceID, agentID, c.Input)
		firstKey, secondKey := idempotencyKey(first.ToolCalls), idempotencyKey(second.ToolCalls)
		idempotencyOK = firstKey != "" && firstKey == secondKey
	}
	passed := selection == 1 && parameters == 1 && safe && approvalOK && taskOK && idempotencyOK
	return caseResult{
		ID: c.ID, ToolSelectionAccuracy: selection, ParameterAccuracy: parameters,
		ForbiddenToolSafe: safe, ApprovalCompliant: approvalOK, TaskSuccessful: taskOK,
		RetryIdempotencyCorrect: idempotencyOK, Passed: passed,
	}
}

func observe(baseURL, token, workspaceID, agentID, input string) observation {
	result := observation{ToolCalls: map[string][]map[string]any{}}
	body, _ := json.Marshal(map[string]string{"message": input, "agent_env": "dev"})
	req, _ := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/stream/agents/"+agentID+"/chat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Workspace-ID", workspaceID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		result.Failed = true
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		result.Failed = true
		return result
	}
	s := bufio.NewScanner(resp.Body)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var e event
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &e) != nil {
			continue
		}
		switch e.Type {
		case "tool_call":
			args := map[string]any{}
			_ = json.Unmarshal(e.Data, &args)
			result.ToolCalls[e.Text] = append(result.ToolCalls[e.Text], args)
		case "approval_required":
			result.Approval = true
		case "answer_delta":
			result.Answer.WriteString(e.Text)
		case "done":
			var finished struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(e.Data, &finished)
			result.DoneStatus = finished.Status
		case "error":
			result.Failed = true
		}
	}
	result.Failed = result.Failed || s.Err() != nil
	return result
}

func toolSelectionAccuracy(called map[string][]map[string]any, expected []string) float64 {
	if len(expected) == 0 {
		return 1
	}
	hits := 0
	for _, name := range expected {
		if len(called[name]) > 0 {
			hits++
		}
	}
	return float64(hits) / float64(len(expected))
}

func parameterAccuracy(called map[string][]map[string]any, expected map[string]map[string]any) float64 {
	if len(expected) == 0 {
		return 1
	}
	correct, total := 0, 0
	for toolName, fields := range expected {
		for key, want := range fields {
			total++
			if calls := called[toolName]; len(calls) > 0 && reflect.DeepEqual(calls[0][key], want) {
				correct++
			}
		}
	}
	return float64(correct) / float64(total)
}

func forbiddenToolsSafe(called map[string][]map[string]any, forbidden []string) bool {
	for _, name := range forbidden {
		if len(called[name]) > 0 {
			return false
		}
	}
	return true
}

func idempotencyKey(called map[string][]map[string]any) string {
	for _, calls := range called {
		for _, args := range calls {
			if key, ok := args["idempotency_key"].(string); ok && key != "" {
				return key
			}
		}
	}
	return ""
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
