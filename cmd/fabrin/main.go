// Command fabrin scaffolds and inspects Fabrin projects.
//
//	fabrin new <name>     # a runnable project
//	fabrin version
//
// # What this binary deliberately does not do
//
// There is no `fabrin routes` and no `fabrin serve`. Go compiles: this binary
// cannot introspect an application it did not build, and a tool that shells out
// to `go run` to answer would pay a compile per invocation, require the toolchain
// at runtime, and relay someone else's exit codes. Those commands belong to the
// application's own binary, which already has the modules linked in — see
// fabrin.App.Execute.
//
// So what is left here is exactly what needs no compilation: making a project,
// making a module inside one, and reporting a version.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"

	"github.com/usefabrin/fabrin/cli"
	"github.com/usefabrin/fabrin/internal/scaffold"
)

func main() {
	if err := cli.Dispatch(context.Background(), os.Stdout, commands(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func commands() []cli.Command {
	var (
		module   string
		dir      string
		skipTidy bool
	)

	return []cli.Command{
		{
			Name:  "new",
			Short: "scaffold a runnable Fabrin project",
			Flags: func(fs *flag.FlagSet) {
				fs.StringVar(&module, "module", "", "Go module path (default: the project name)")
				fs.StringVar(&dir, "dir", ".", "parent directory to create the project in")
				fs.BoolVar(&skipTidy, "skip-tidy", false, "do not run `go mod tidy` in the new project")
			},
			Run: func(ctx context.Context, out io.Writer, args []string) error {
				if len(args) != 1 {
					return fmt.Errorf("fabrin: new takes exactly one name — try `fabrin new myapp`")
				}
				name := args[0]

				p := scaffold.Project{Name: name, Module: module, Dir: filepath.Join(dir, name)}
				if err := p.Generate(); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(out, "created %s\n", p.Dir); err != nil {
					return err
				}

				// The generated go.mod names no dependencies. Resolving them is the
				// toolchain's job, not a template's: a version pinned in a template is
				// wrong the first time Fabrin is tagged, and nothing fails when it is.
				if !skipTidy {
					if err := tidy(ctx, out, p.Dir); err != nil {
						return err
					}
				}

				_, err := fmt.Fprintf(out, "\n    cd %s\n    just run\n", p.Dir)
				return err
			},
		},
		{
			Name:  "version",
			Short: "print this tool's version",
			Run: func(_ context.Context, out io.Writer, _ []string) error {
				_, err := fmt.Fprintf(out, "fabrin %s\n", version())
				return err
			},
		},
	}
}

// tidy runs `go mod tidy` in dir.
//
// Its output goes to out rather than being swallowed: resolving modules can take
// seconds and can fail for reasons only the go command knows about (no network,
// a proxy that has not seen the version yet), and hiding that turns a slow
// download into an apparently hung tool.
func tidy(ctx context.Context, out io.Writer, dir string) error {
	if _, err := fmt.Fprintln(out, "resolving dependencies (go mod tidy)…"); err != nil {
		return err
	}

	goBin := "go"
	if v := os.Getenv("GO"); v != "" {
		goBin = v
	}

	cmd := exec.CommandContext(ctx, goBin, "mod", "tidy")
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Run(); err != nil {
		// The project is on disk and correct; only resolution failed. Say so, and
		// say what to run — deleting the user's new project over a network blip
		// would be worse than leaving it.
		return fmt.Errorf("fabrin: the project was created, but `go mod tidy` failed in %s: %w\n"+
			"    Run it yourself once you are online, or pass -skip-tidy next time", dir, err)
	}
	return nil
}

func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "unknown"
	}
	return info.Main.Version
}
