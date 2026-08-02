package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/usefabrin/fabrin/tools/agentcheck"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: agentcheck task|result [arguments]")
	}
	root := ".."
	switch os.Args[1] {
	case "task":
		fs := flag.NewFlagSet("task", flag.ExitOnError)
		var runtime, base string
		fs.StringVar(&root, "repo", root, "repository root")
		fs.StringVar(&runtime, "runtime", "", "native Lead runtime")
		fs.StringVar(&base, "base", "", "integration base SHA")
		_ = fs.Parse(os.Args[2:])
		if fs.NArg() == 0 {
			fail("agentcheck task: provide one or more task packet files")
		}
		roles := readRoles(root)
		var tasks []agentcheck.Task
		for _, file := range fs.Args() {
			if err := agentcheck.ValidateSchemaInstance(filepath.Join(root, "docs", "agents", "schemas", "task-packet.schema.json"), file); err != nil {
				fail(err.Error())
			}
			task, err := agentcheck.ReadTask(file)
			if err != nil {
				fail(err.Error())
			}
			tasks = append(tasks, task)
		}
		if err := agentcheck.ValidateFanout(tasks, roles); err != nil {
			fail(err.Error())
		}
		if err := agentcheck.ValidateLead(tasks, runtime, base); err != nil {
			fail(err.Error())
		}
		fmt.Printf("✓ agentcheck: %d dispatch packet(s) are valid and non-overlapping.\n", len(tasks))
	case "result":
		fs := flag.NewFlagSet("result", flag.ExitOnError)
		var runtime, base string
		fs.StringVar(&root, "repo", root, "repository root")
		fs.StringVar(&runtime, "runtime", "", "native Lead runtime")
		fs.StringVar(&base, "base", "", "integration base SHA")
		_ = fs.Parse(os.Args[2:])
		if fs.NArg() != 2 {
			fail("agentcheck result: provide TASK.json RESULT.json")
		}
		if err := agentcheck.ValidateSchemaInstance(filepath.Join(root, "docs", "agents", "schemas", "task-packet.schema.json"), fs.Arg(0)); err != nil {
			fail(err.Error())
		}
		if err := agentcheck.ValidateSchemaInstance(filepath.Join(root, "docs", "agents", "schemas", "result-packet.schema.json"), fs.Arg(1)); err != nil {
			fail(err.Error())
		}
		task, err := agentcheck.ReadTask(fs.Arg(0))
		if err != nil {
			fail(err.Error())
		}
		result, err := agentcheck.ReadResult(fs.Arg(1))
		if err != nil {
			fail(err.Error())
		}
		if err := agentcheck.ValidateResult(task, result, readRoles(root)); err != nil {
			fail(err.Error())
		}
		if err := agentcheck.ValidateLead([]agentcheck.Task{task}, runtime, base); err != nil {
			fail(err.Error())
		}
		fmt.Println("✓ agentcheck: result matches its assignment, base, access, and ownership.")
	default:
		fail("unknown command " + os.Args[1])
	}
}

func readRoles(root string) map[string]string {
	file, err := os.Open(filepath.Join(root, "docs", "agents", "catalog.tsv"))
	if err != nil {
		fail(err.Error())
	}
	defer func() { _ = file.Close() }()
	roles := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			fail("invalid role catalog row: " + line)
		}
		roles[fields[0]] = fields[1]
	}
	if err := scanner.Err(); err != nil {
		fail(err.Error())
	}
	return roles
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "agentcheck:", message)
	os.Exit(1)
}
