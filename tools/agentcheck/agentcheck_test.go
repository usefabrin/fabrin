package agentcheck

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

func TestPacketSchemas_AreStrictJSONWithRequiredProtocolFields(t *testing.T) {
	t.Parallel()

	tests := map[string][]string{
		"task-packet.schema.json": {
			"schema", "task_id", "runtime", "role", "base_sha", "worktree",
			"access", "invariants", "acceptance_commands",
		},
		"result-packet.schema.json": {
			"schema", "task_id", "runtime", "role", "status",
			"observed_base_sha", "observed_head_sha", "changed_paths",
			"commands", "risks", "blockers", "handback",
		},
	}

	for name, required := range tests {
		name, required := name, required
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(filepath.Join("..", "..", "docs", "agents", "schemas", name))
			if err != nil {
				t.Fatal(err)
			}
			var schema map[string]any
			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatalf("schema is not JSON: %v", err)
			}
			if strict, ok := schema["additionalProperties"].(bool); !ok || strict {
				t.Fatal("top-level protocol schema must set additionalProperties: false")
			}
			got := map[string]bool{}
			for _, value := range schema["required"].([]any) {
				got[value.(string)] = true
			}
			for _, field := range required {
				if !got[field] {
					t.Errorf("required protocol field %q is missing", field)
				}
			}
			if _, err := jsonschema.NewCompiler().Compile(filepath.Join("..", "..", "docs", "agents", "schemas", name)); err != nil {
				t.Fatalf("schema does not compile as JSON Schema 2020-12: %v", err)
			}
		})
	}
}

func TestNativeAdapters_ParseInTheirDeclaredFormats(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..")
	for _, platform := range []string{".claude", ".cursor"} {
		allowed := map[string]bool{"name": true, "description": true, "model": true}
		if platform == ".claude" {
			allowed["tools"], allowed["permissionMode"], allowed["isolation"] = true, true, true
		} else {
			allowed["readonly"] = true
		}
		files, err := filepath.Glob(filepath.Join(root, platform, "agents", "*.md"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			parts := strings.SplitN(string(data), "---", 3)
			if len(parts) != 3 {
				t.Errorf("%s has no YAML frontmatter", file)
				continue
			}
			var metadata map[string]any
			if err := yaml.Unmarshal([]byte(parts[1]), &metadata); err != nil {
				t.Errorf("%s frontmatter: %v", file, err)
			}
			if metadata["name"] == nil || metadata["description"] == nil {
				t.Errorf("%s lacks required native metadata", file)
			}
			for key := range metadata {
				if !allowed[key] {
					t.Errorf("%s declares unsupported %s metadata", file, key)
				}
			}
		}
	}

	tomlFiles, err := filepath.Glob(filepath.Join(root, ".codex", "agents", "*.toml"))
	if err != nil {
		t.Fatal(err)
	}
	tomlFiles = append(tomlFiles, filepath.Join(root, ".codex", "config.toml"))
	for _, file := range tomlFiles {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := toml.Unmarshal(data, &document); err != nil {
			t.Errorf("%s: %v", file, err)
		}
		if strings.Contains(file, string(filepath.Separator)+"agents"+string(filepath.Separator)) {
			for _, key := range []string{"name", "description", "sandbox_mode", "developer_instructions"} {
				if document[key] == nil {
					t.Errorf("%s lacks required Codex metadata %s", file, key)
				}
			}
			for key := range document {
				if key != "name" && key != "description" && key != "sandbox_mode" && key != "developer_instructions" {
					t.Errorf("%s declares unsupported Codex metadata %s", file, key)
				}
			}
		}
	}
}

func TestRoleCatalog_HasOneNativeAdapterPerPlatform(t *testing.T) {
	t.Parallel()

	f, err := os.Open(filepath.Join("..", "..", "docs", "agents", "catalog.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var roles []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		roles = append(roles, strings.Split(line, "\t")[0])
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(roles) == 0 {
		t.Fatal("catalog declares no roles")
	}

	for _, role := range roles {
		for _, path := range []string{
			filepath.Join("..", "..", ".claude", "agents", role+".md"),
			filepath.Join("..", "..", ".codex", "agents", role+".toml"),
			filepath.Join("..", "..", ".cursor", "agents", role+".md"),
			filepath.Join("..", "..", "docs", "agents", "roles", role+".md"),
		} {
			if _, err := os.Stat(path); err != nil {
				t.Errorf("role %s: %v", role, err)
			}
		}
	}
}
