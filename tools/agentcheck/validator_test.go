package agentcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

func TestValidateSchemaInstance_RejectsAMissingRequiredField(t *testing.T) {
	t.Parallel()
	packet := filepath.Join(t.TempDir(), "task.json")
	if err := os.WriteFile(packet, []byte(`{"schema":"fabrin.agent-task/v1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	schema := filepath.Join("..", "..", "docs", "agents", "schemas", "task-packet.schema.json")
	if err := ValidateSchemaInstance(schema, packet); err == nil {
		t.Fatal("packet missing required fields unexpectedly passed its JSON Schema")
	}
}

func validTask(id, mode string, owned ...string) Task {
	role := "test-first"
	if mode == "read-only" {
		role = "api-guardian"
	}
	return Task{
		Schema: "fabrin.agent-task/v1", TaskID: id, Runtime: "codex",
		Role: role, Issue: "#75", Objective: "write the expected-red test",
		Risk: "normal", BaseSHA: testSHA, Worktree: "/tmp/fabrin-agent-" + id,
		Access: Access{Mode: mode, OwnedPaths: append([]string{}, owned...)},
		Inputs: []string{"issue #75"}, Invariants: []string{"do not mutate Git"},
		AcceptanceCommands: []string{"go test ./..."}, ExclusiveResources: []string{},
	}
}

func validResult(task Task) Result {
	return Result{
		Schema: "fabrin.agent-result/v1", TaskID: task.TaskID, Runtime: task.Runtime,
		Role: task.Role, Status: "completed", ObservedBaseSHA: task.BaseSHA,
		ObservedHeadSHA: task.BaseSHA, ChangedPaths: []string{}, Findings: []Finding{},
		Commands: []Command{}, Assumptions: []string{}, Risks: []string{},
		Blockers: []string{}, Handback: "expected-red test is ready",
	}
}

func TestValidateFanout_AcceptsDisjointNativeTasks(t *testing.T) {
	t.Parallel()
	tasks := []Task{
		validTask("task-a", "owned-write", "health"),
		validTask("task-b", "owned-write", "logging"),
		validTask("task-c", "read-only"),
	}
	if err := ValidateFanout(tasks, map[string]string{"test-first": "owned-write", "api-guardian": "read-only"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateFanout_RejectsCrossRuntimeAndPrefixOverlap(t *testing.T) {
	t.Parallel()
	a := validTask("task-a", "owned-write", "docs")
	b := validTask("task-b", "owned-write", "docs/agents")
	b.Runtime = "claude"
	err := ValidateFanout([]Task{a, b}, map[string]string{"test-first": "owned-write"})
	if err == nil || !strings.Contains(err.Error(), "cross runtimes") || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("ValidateFanout() = %v, want runtime and ownership failures", err)
	}
}

func TestValidateLead_RejectsAPlatformOrBaseMismatch(t *testing.T) {
	t.Parallel()
	task := validTask("task-a", "read-only")
	err := ValidateLead([]Task{task}, "claude", strings.Repeat("f", 40))
	if err == nil || !strings.Contains(err.Error(), "differs from Lead") || !strings.Contains(err.Error(), "integration base") {
		t.Fatalf("ValidateLead() = %v, want runtime and base failures", err)
	}
}

func TestValidateTask_RejectsUnsafeOwnership(t *testing.T) {
	t.Parallel()
	for _, owned := range []string{".", ".git/index", "../outside", "docs/*", `docs\agents`} {
		task := validTask("unsafe-task", "owned-write", owned)
		if err := ValidateTask(task, map[string]string{"test-first": "owned-write"}); err == nil {
			t.Errorf("owned path %q unexpectedly passed", owned)
		}
	}
	win := validTask("windows-task", "read-only")
	win.Worktree = `C:\work\fabrin`
	if err := ValidateTask(win, map[string]string{"api-guardian": "read-only"}); err != nil {
		t.Errorf("portable Windows worktree rejected: %v", err)
	}
}

func TestValidateResult_RejectsStaleReadOnlyAndOutOfScopeChanges(t *testing.T) {
	t.Parallel()
	readTask := validTask("read-task", "read-only")
	readResult := validResult(readTask)
	readResult.ChangedPaths = []string{"README.md"}
	if err := ValidateResult(readTask, readResult, map[string]string{"api-guardian": "read-only"}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only change = %v", err)
	}

	writeTask := validTask("write-task", "owned-write", "docs/agents")
	writeResult := validResult(writeTask)
	writeResult.ObservedHeadSHA = strings.Repeat("f", 40)
	writeResult.ChangedPaths = []string{"docs/TODO.md"}
	err := ValidateResult(writeTask, writeResult, map[string]string{"test-first": "owned-write"})
	if err == nil || !strings.Contains(err.Error(), "observed SHAs") || !strings.Contains(err.Error(), "outside task ownership") {
		t.Fatalf("stale/out-of-scope result = %v", err)
	}
}

func TestValidateResult_AcceptsOwnedUncommittedChanges(t *testing.T) {
	t.Parallel()
	task := validTask("write-task", "owned-write", "docs/agents")
	result := validResult(task)
	result.ChangedPaths = []string{"docs/agents/ORCHESTRATION.md"}
	if err := ValidateResult(task, result, map[string]string{"test-first": "owned-write"}); err != nil {
		t.Fatal(err)
	}
}
