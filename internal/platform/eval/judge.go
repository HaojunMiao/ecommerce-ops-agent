package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

type JudgeResult struct {
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// Judge 负责判断期望答案与实际答案的语义一致性。
type Judge interface {
	Kind() string
	Score(ctx context.Context, expected, actual string) JudgeResult
}

// ContainsJudge 只做忽略大小写的包含判断，结果稳定且不产生模型费用。
type ContainsJudge struct{}

func (ContainsJudge) Kind() string { return "deterministic" }

func (ContainsJudge) Score(_ context.Context, expected, actual string) JudgeResult {
	if expected == "" || strings.Contains(strings.ToLower(actual), strings.ToLower(expected)) {
		return JudgeResult{Score: 1, Reason: "expected content matched"}
	}
	return JudgeResult{Score: 0, Reason: "expected content missing"}
}

// LLMJudge 使用另一个固定版本的 Agent 判断语义正确性；Tier 只用于区分轻量和完整裁判。
type LLMJudge struct {
	Tier   string
	Runner func(context.Context, string) (string, error)
}

func (j LLMJudge) Kind() string {
	if j.Tier == "light" {
		return "llm-light"
	}
	return "llm-full"
}

func (j LLMJudge) Score(ctx context.Context, expected, actual string) JudgeResult {
	if j.Runner == nil {
		return JudgeResult{Reason: "LLM judge runner is required"}
	}
	prompt := fmt.Sprintf("Evaluate semantic correctness. Return JSON only: {\"score\":0..1,\"reason\":\"...\"}.\nexpected: %s\nactual: %s", expected, actual)
	raw, err := j.Runner(ctx, prompt)
	if err != nil {
		return JudgeResult{Reason: "LLM judge error: " + err.Error()}
	}
	// 兼容模型在 JSON 前后附带少量文字，只截取最外层对象进行解析。
	start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return JudgeResult{Reason: "LLM judge returned invalid JSON"}
	}
	var result JudgeResult
	if err := json.Unmarshal([]byte(raw[start:end+1]), &result); err != nil || math.IsNaN(result.Score) || math.IsInf(result.Score, 0) {
		return JudgeResult{Reason: "LLM judge returned invalid score"}
	}
	result.Score = math.Max(0, math.Min(1, result.Score))
	return result
}
