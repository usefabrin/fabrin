// Package agentcheck validates Fabrin's multi-agent dispatch and fan-in packets.
package agentcheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// ValidateSchemaInstance applies the checked-in JSON Schema before semantic
// cross-packet validation adds runtime, ownership, and stale-state rules.
func ValidateSchemaInstance(schemaFile, instanceFile string) error {
	compiler := jsonschema.NewCompiler()
	schema, err := compiler.Compile(schemaFile)
	if err != nil {
		return fmt.Errorf("compile %s: %w", schemaFile, err)
	}
	file, err := os.Open(instanceFile)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	instance, err := jsonschema.UnmarshalJSON(file)
	if err != nil {
		return fmt.Errorf("decode %s: %w", instanceFile, err)
	}
	if err := schema.Validate(instance); err != nil {
		return fmt.Errorf("%s: %w", instanceFile, err)
	}
	return nil
}

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]+$`)
	rolePattern    = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	shaPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	issuePattern   = regexp.MustCompile(`^#[0-9]+$`)
	windowsAbsPath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

type Task struct {
	Schema             string   `json:"schema"`
	TaskID             string   `json:"task_id"`
	Runtime            string   `json:"runtime"`
	Role               string   `json:"role"`
	Issue              string   `json:"issue"`
	Objective          string   `json:"objective"`
	Risk               string   `json:"risk"`
	BaseSHA            string   `json:"base_sha"`
	Worktree           string   `json:"worktree"`
	Access             Access   `json:"access"`
	Inputs             []string `json:"inputs"`
	Invariants         []string `json:"invariants"`
	AcceptanceCommands []string `json:"acceptance_commands"`
	ExclusiveResources []string `json:"exclusive_resources"`
	Ports              *Ports   `json:"ports"`
}

type Access struct {
	Mode       string   `json:"mode"`
	OwnedPaths []string `json:"owned_paths"`
}

type Ports struct {
	SmokePortBase int `json:"smoke_port_base"`
	ScaffoldPort  int `json:"scaffold_port"`
}

type Result struct {
	Schema          string    `json:"schema"`
	TaskID          string    `json:"task_id"`
	Runtime         string    `json:"runtime"`
	Role            string    `json:"role"`
	Status          string    `json:"status"`
	ObservedBaseSHA string    `json:"observed_base_sha"`
	ObservedHeadSHA string    `json:"observed_head_sha"`
	ChangedPaths    []string  `json:"changed_paths"`
	Findings        []Finding `json:"findings"`
	Commands        []Command `json:"commands"`
	Assumptions     []string  `json:"assumptions"`
	Risks           []string  `json:"risks"`
	Blockers        []string  `json:"blockers"`
	Handback        string    `json:"handback"`
}

type Finding struct {
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	Evidence string `json:"evidence"`
}

type Command struct {
	Command  string `json:"command"`
	Outcome  string `json:"outcome"`
	ExitCode *int   `json:"exit_code"`
	Summary  string `json:"summary"`
}

func ReadTask(file string) (Task, error) {
	var task Task
	return task, decode(file, &task, []string{
		"schema", "task_id", "runtime", "role", "issue", "objective", "risk",
		"base_sha", "worktree", "access", "inputs", "invariants",
		"acceptance_commands", "exclusive_resources", "ports",
	})
}

func ReadResult(file string) (Result, error) {
	var result Result
	return result, decode(file, &result, []string{
		"schema", "task_id", "runtime", "role", "status", "observed_base_sha",
		"observed_head_sha", "changed_paths", "findings", "commands", "assumptions",
		"risks", "blockers", "handback",
	})
}

func decode(file string, dst any, required []string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%s: more than one JSON value", file)
		}
		return fmt.Errorf("%s: trailing JSON: %w", file, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	for _, field := range required {
		if _, ok := fields[field]; !ok {
			return fmt.Errorf("%s: required field %q is missing", file, field)
		}
	}
	return nil
}

func ValidateTask(task Task, roles map[string]string) error {
	var errs []error
	if task.Schema != "fabrin.agent-task/v1" {
		errs = append(errs, fmt.Errorf("schema = %q", task.Schema))
	}
	if !idPattern.MatchString(task.TaskID) {
		errs = append(errs, fmt.Errorf("invalid task_id %q", task.TaskID))
	}
	if task.Runtime != "codex" && task.Runtime != "claude" && task.Runtime != "cursor" {
		errs = append(errs, fmt.Errorf("invalid runtime %q", task.Runtime))
	}
	roleAccess, knownRole := roles[task.Role]
	if !rolePattern.MatchString(task.Role) || (roles != nil && !knownRole) {
		errs = append(errs, fmt.Errorf("unknown role %q", task.Role))
	}
	if !issuePattern.MatchString(task.Issue) {
		errs = append(errs, fmt.Errorf("invalid issue %q", task.Issue))
	}
	if strings.TrimSpace(task.Objective) == "" || len(task.Invariants) == 0 {
		errs = append(errs, errors.New("objective and at least one invariant are required"))
	}
	if task.Risk != "trivial" && task.Risk != "normal" && task.Risk != "high" {
		errs = append(errs, fmt.Errorf("invalid risk %q", task.Risk))
	}
	if !shaPattern.MatchString(task.BaseSHA) {
		errs = append(errs, fmt.Errorf("invalid base_sha %q", task.BaseSHA))
	}
	if !strings.HasPrefix(task.Worktree, "/") && !windowsAbsPath.MatchString(task.Worktree) {
		errs = append(errs, fmt.Errorf("worktree %q is not an absolute POSIX or Windows path", task.Worktree))
	}
	if task.Access.Mode != "read-only" && task.Access.Mode != "owned-write" {
		errs = append(errs, fmt.Errorf("invalid access mode %q", task.Access.Mode))
	}
	if roles != nil && knownRole && roleAccess != task.Access.Mode {
		errs = append(errs, fmt.Errorf("role %q requires access %q, packet grants %q", task.Role, roleAccess, task.Access.Mode))
	}
	if task.Access.Mode == "read-only" && len(task.Access.OwnedPaths) != 0 {
		errs = append(errs, errors.New("read-only task has owned paths"))
	}
	if task.Access.Mode == "owned-write" && len(task.Access.OwnedPaths) == 0 {
		errs = append(errs, errors.New("owned-write task has no owned paths"))
	}
	if task.Access.OwnedPaths == nil || task.Inputs == nil || task.AcceptanceCommands == nil || task.ExclusiveResources == nil {
		errs = append(errs, errors.New("access paths, inputs, acceptance commands, and exclusive resources must be JSON arrays"))
	}
	for _, values := range [][]string{task.Inputs, task.Invariants, task.AcceptanceCommands} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				errs = append(errs, errors.New("packet string arrays must not contain empty values"))
			}
		}
	}
	resources := map[string]bool{}
	for _, resource := range task.ExclusiveResources {
		switch resource {
		case "git-index", "go-build-cache", "loopback-ports", "benchmark-cpu", "final-validation":
		default:
			errs = append(errs, fmt.Errorf("invalid exclusive resource %q", resource))
		}
		if resources[resource] {
			errs = append(errs, fmt.Errorf("duplicate exclusive resource %q", resource))
		}
		resources[resource] = true
	}
	if task.Ports != nil && (task.Ports.SmokePortBase < 1024 || task.Ports.SmokePortBase > 65535 || task.Ports.ScaffoldPort < 1024 || task.Ports.ScaffoldPort > 65535) {
		errs = append(errs, errors.New("packet ports must be between 1024 and 65535"))
	}
	for _, owned := range task.Access.OwnedPaths {
		if err := validRepoPath(owned); err != nil {
			errs = append(errs, fmt.Errorf("owned path %q: %w", owned, err))
		}
	}
	return errors.Join(errs...)
}

// ValidateFanout proves that concurrently dispatched tasks share one native
// runtime/base and that writable prefix ownership does not overlap.
func ValidateFanout(tasks []Task, roles map[string]string) error {
	var errs []error
	if len(tasks) > 3 {
		errs = append(errs, fmt.Errorf("fanout has %d workers; maximum is 3", len(tasks)))
	}
	seenIDs := map[string]bool{}
	for _, task := range tasks {
		if err := ValidateTask(task, roles); err != nil {
			errs = append(errs, fmt.Errorf("task %q: %w", task.TaskID, err))
		}
		if seenIDs[task.TaskID] {
			errs = append(errs, fmt.Errorf("duplicate task_id %q", task.TaskID))
		}
		seenIDs[task.TaskID] = true
	}
	for i := range tasks {
		for j := i + 1; j < len(tasks); j++ {
			if tasks[i].Runtime != tasks[j].Runtime {
				errs = append(errs, fmt.Errorf("tasks %q and %q cross runtimes", tasks[i].TaskID, tasks[j].TaskID))
			}
			if tasks[i].BaseSHA != tasks[j].BaseSHA {
				errs = append(errs, fmt.Errorf("tasks %q and %q use different bases", tasks[i].TaskID, tasks[j].TaskID))
			}
			if tasks[i].Access.Mode == "owned-write" && tasks[j].Access.Mode == "owned-write" && tasks[i].Worktree == tasks[j].Worktree {
				errs = append(errs, fmt.Errorf("writable tasks %q and %q share worktree %q", tasks[i].TaskID, tasks[j].TaskID, tasks[i].Worktree))
			}
			for _, a := range tasks[i].ExclusiveResources {
				for _, b := range tasks[j].ExclusiveResources {
					if a == b {
						errs = append(errs, fmt.Errorf("tasks %q and %q both claim exclusive resource %q", tasks[i].TaskID, tasks[j].TaskID, a))
					}
				}
			}
			if tasks[i].Ports != nil && tasks[j].Ports != nil {
				left := []int{tasks[i].Ports.SmokePortBase, tasks[i].Ports.ScaffoldPort}
				right := []int{tasks[j].Ports.SmokePortBase, tasks[j].Ports.ScaffoldPort}
				for _, a := range left {
					for _, b := range right {
						if a == b {
							errs = append(errs, fmt.Errorf("tasks %q and %q both allocate port %d", tasks[i].TaskID, tasks[j].TaskID, a))
						}
					}
				}
			}
			if tasks[i].Access.Mode != "owned-write" || tasks[j].Access.Mode != "owned-write" {
				continue
			}
			for _, a := range tasks[i].Access.OwnedPaths {
				for _, b := range tasks[j].Access.OwnedPaths {
					if owns(a, b) || owns(b, a) {
						errs = append(errs, fmt.Errorf("tasks %q and %q overlap at %q and %q", tasks[i].TaskID, tasks[j].TaskID, a, b))
					}
				}
			}
		}
	}
	return errors.Join(errs...)
}

// ValidateLead binds a fanout to the current native Lead and integration base.
func ValidateLead(tasks []Task, runtime, base string) error {
	if runtime != "codex" && runtime != "claude" && runtime != "cursor" {
		return fmt.Errorf("invalid Lead runtime %q", runtime)
	}
	if !shaPattern.MatchString(base) {
		return fmt.Errorf("invalid integration base %q", base)
	}
	var errs []error
	for _, task := range tasks {
		if task.Runtime != runtime {
			errs = append(errs, fmt.Errorf("task %q runtime %q differs from Lead %q", task.TaskID, task.Runtime, runtime))
		}
		if task.BaseSHA != base {
			errs = append(errs, fmt.Errorf("task %q base differs from integration base", task.TaskID))
		}
	}
	return errors.Join(errs...)
}

func ValidateResult(task Task, result Result, roles map[string]string) error {
	var errs []error
	if err := ValidateTask(task, roles); err != nil {
		errs = append(errs, fmt.Errorf("assigned task: %w", err))
	}
	if result.Schema != "fabrin.agent-result/v1" {
		errs = append(errs, fmt.Errorf("result schema = %q", result.Schema))
	}
	if result.TaskID != task.TaskID || result.Runtime != task.Runtime || result.Role != task.Role {
		errs = append(errs, errors.New("result identity does not match assigned task"))
	}
	if result.ObservedBaseSHA != task.BaseSHA || result.ObservedHeadSHA != task.BaseSHA {
		errs = append(errs, errors.New("stale result or worker-created commit: observed SHAs must equal assigned base"))
	}
	if result.Status != "completed" && result.Status != "blocked" && result.Status != "failed" {
		errs = append(errs, fmt.Errorf("invalid result status %q", result.Status))
	}
	if result.Status == "completed" && len(result.Blockers) != 0 {
		errs = append(errs, errors.New("completed result has blockers"))
	}
	if strings.TrimSpace(result.Handback) == "" {
		errs = append(errs, errors.New("result handback is empty"))
	}
	if result.ChangedPaths == nil || result.Findings == nil || result.Commands == nil || result.Assumptions == nil || result.Risks == nil || result.Blockers == nil {
		errs = append(errs, errors.New("result collection fields must be JSON arrays"))
	}
	for _, finding := range result.Findings {
		switch finding.Severity {
		case "critical", "high", "medium", "low", "note":
		default:
			errs = append(errs, fmt.Errorf("invalid finding severity %q", finding.Severity))
		}
		if strings.TrimSpace(finding.Summary) == "" || strings.TrimSpace(finding.Evidence) == "" {
			errs = append(errs, errors.New("finding summary and evidence are required"))
		}
	}
	for _, command := range result.Commands {
		switch command.Outcome {
		case "passed", "failed-expected", "failed-unexpected", "not-run":
		default:
			errs = append(errs, fmt.Errorf("invalid command outcome %q", command.Outcome))
		}
		if strings.TrimSpace(command.Command) == "" || strings.TrimSpace(command.Summary) == "" {
			errs = append(errs, errors.New("command and summary are required"))
		}
	}
	for _, values := range [][]string{result.Assumptions, result.Risks, result.Blockers} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				errs = append(errs, errors.New("result string arrays must not contain empty values"))
			}
		}
	}
	for _, changed := range result.ChangedPaths {
		if err := validRepoPath(changed); err != nil {
			errs = append(errs, fmt.Errorf("changed path %q: %w", changed, err))
			continue
		}
		if task.Access.Mode == "read-only" {
			errs = append(errs, fmt.Errorf("read-only task changed %q", changed))
			continue
		}
		owned := false
		for _, prefix := range task.Access.OwnedPaths {
			owned = owned || owns(prefix, changed)
		}
		if !owned {
			errs = append(errs, fmt.Errorf("changed path %q is outside task ownership", changed))
		}
	}
	return errors.Join(errs...)
}

// Owned paths are slash-separated repository-relative prefixes: "docs" owns
// docs itself and every descendant, while "app.go" owns that exact file.
func owns(prefix, candidate string) bool {
	return candidate == prefix || strings.HasPrefix(candidate, prefix+"/")
}

func validRepoPath(value string) error {
	if value == "" || value == "." || value != path.Clean(value) || strings.HasPrefix(value, "/") || windowsAbsPath.MatchString(value) {
		return errors.New("must be a normalized repository-relative path")
	}
	if strings.Contains(value, "\\") || strings.ContainsAny(value, "*?[") {
		return errors.New("must use literal slash-separated prefix semantics, not backslashes or globs")
	}
	if value == ".git" || strings.HasPrefix(value, ".git/") || value == ".." || strings.HasPrefix(value, "../") {
		return errors.New("must not own repository control or parent paths")
	}
	return nil
}
