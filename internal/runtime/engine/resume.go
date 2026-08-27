package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/approval"
	platformskill "github.com/HaojunMiao/ecommerce-ops-agent/internal/platform/skill"
	"github.com/HaojunMiao/ecommerce-ops-agent/internal/runtime/skillrunner"
)

func decodeFrameworkCheckpoint(payload []byte) ([]byte, string, string, error) {
	var stored persistedFrameworkCheckpoint
	if err := json.Unmarshal(payload, &stored); err != nil || stored.Version != frameworkCheckpointVersion ||
		len(stored.Data) == 0 || stored.InterruptID == "" {
		return nil, "", "", fmt.Errorf("approval checkpoint is incompatible with Eino ADK runtime v%d", frameworkCheckpointVersion)
	}
	return stored.Data, stored.InterruptID, stored.ActiveSkillName, nil
}

// ResumeApproved 不会重新发起整轮对话，而是通过 Eino ResumeWithParams 将批准结果
// 精确送回原来的中断 ID；恢复前还会重新核对工作空间、版本快照和工具绑定。
func (e *Engine) ResumeApproved(ctx context.Context, request *approval.Request, checkpoint []byte, emit Emitter) error {
	if request == nil || request.RunID == "" || request.ToolCallID == "" || request.ToolVersionID == "" {
		return fmt.Errorf("complete approval binding is required")
	}
	if e.tools == nil || e.approvals == nil {
		return fmt.Errorf("tool runtime and approval gate are required")
	}
	snapshot, err := e.ResolveSnapshot(ctx, request.RunID)
	if err != nil {
		return err
	}
	if snapshot.WorkspaceID != request.WorkspaceID {
		return fmt.Errorf("approval and conversation workspaces do not match")
	}

	// 仍使用会话创建时固定的工具版本，不能因当前平台配置已经更新而偷换工具。
	bindings, err := e.tools.Bind(ctx, request.WorkspaceID, snapshot.ToolVersionIDs)
	if err != nil {
		return fmt.Errorf("bind pinned tools: %w", err)
	}
	if !containsToolVersion(bindings, request.ToolVersionID) {
		return fmt.Errorf("approved tool is outside the pinned agent snapshot")
	}
	plan, err := e.executionPlan(ctx, snapshot)
	if err != nil {
		return err
	}
	packages, err := e.resolveSkills(ctx, snapshot)
	if err != nil {
		return err
	}
	frameworkData, interruptID, activeSkillName, err := decodeFrameworkCheckpoint(checkpoint)
	if err != nil {
		return err
	}

	// Skill 的激活状态也属于运行现场；丢失它会让续跑前后的工具权限不一致。
	userInput := ""
	if activeSkillName != "" {
		userInput = "/skill " + activeSkillName
	}
	skillRuntime, err := skillrunner.NewRuntime(ctx, packages, bindings, userInput, func(pkg platformskill.Package) error {
		return emitContext(ctx, emit, Event{Type: "skill_trigger", Data: map[string]string{"name": pkg.Name, "resumed": "true"}})
	})
	if err != nil {
		return err
	}
	var authorize func(string, string) error
	runner := NewADKRunner(plan.Model, e.tools, request.WorkspaceID).
		WithModelPolicy(plan.Retry, plan.Failover).
		WithApprovals(e.approvals, request.RunID)
	if skillRuntime != nil {
		if err := skillRuntime.Restore(activeSkillName); err != nil {
			return err
		}
		authorize = skillRuntime.Authorize
		runner.WithHandlers(skillRuntime.Handlers...).
			WithSkillState(skillRuntime.ActiveName, skillRuntime.Restore)
	}
	runner.WithToolAuthorization(authorize)
	maxSteps := snapshot.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 4
	}
	answer, err := runner.Resume(ctx, request.RunID, frameworkData, interruptID, bindings, maxSteps, emit)
	if err != nil {
		var awaiting *AwaitingApprovalError
		if errors.As(err, &awaiting) {
			if emitErr := emitContext(ctx, emit, Event{Type: "approval_requested", Data: map[string]string{
				"approval_id": awaiting.ApprovalID, "tool_name": awaiting.ToolName,
				"tool_call_id": awaiting.ToolCallID, "tool_version_id": awaiting.ToolVersionID,
			}}); emitErr != nil {
				return emitErr
			}
			return emitContext(ctx, emit, Event{Type: "run_finished", Data: map[string]string{"status": "awaiting_approval"}})
		}
		return fmt.Errorf("resume Eino approval checkpoint: %w", err)
	}
	if history, ok := e.platform.(ConversationMessageStore); ok {
		if err := history.AppendMessage(ctx, request.WorkspaceID, request.RunID, "assistant", answer.Content); err != nil {
			return fmt.Errorf("persist resumed assistant message: %w", err)
		}
	}
	for _, delta := range answerDeltas(answer.Content, 8) {
		if err := emitContext(ctx, emit, Event{Type: "answer_delta", Text: delta}); err != nil {
			return err
		}
	}
	if err := emitContext(ctx, emit, Event{Type: "answer_done", Text: answer.Content}); err != nil {
		return err
	}
	return emitContext(ctx, emit, Event{Type: "run_finished", Data: map[string]string{"status": "completed"}})
}

func containsToolVersion(bindings []ToolBinding, versionID string) bool {
	for _, binding := range bindings {
		if binding.VersionID == versionID {
			return true
		}
	}
	return false
}
