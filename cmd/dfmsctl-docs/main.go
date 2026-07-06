// Command dfmsctl-docs generates shell completion scripts and man pages for
// dfmsctl. It is a build-time helper used by GoReleaser and the Makefile, not
// distributed to end users.
//
// Usage:
//
//	dfmsctl-docs completions <dir>   # writes bash/zsh/fish/powershell scripts
//	dfmsctl-docs man <dir>           # writes man pages for every subcommand
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/AnirudhSinghRajora/DFMS/internal/cli"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: %s <completions|man> <dir>\n", os.Args[0])
		os.Exit(2)
	}
	mode, dir := os.Args[1], os.Args[2]

	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatalf("creating output directory: %v", err)
	}

	switch mode {
	case "completions":
		genCompletions(dir)
	case "man":
		genManPages(dir)
	default:
		fatalf("unknown mode %q (want completions or man)", mode)
	}
}

func genCompletions(dir string) {
	root := cli.NewRootCommand(cli.BuildInfo{Version: "dev"})

	generators := map[string]func(string) error{
		"dfmsctl.bash": func(path string) error {
			f, err := os.Create(path)
			if err != nil {
				return err
			}
			defer f.Close()
			return root.GenBashCompletionV2(f, true)
		},
		"dfmsctl.zsh": func(path string) error {
			f, err := os.Create(path)
			if err != nil {
				return err
			}
			defer f.Close()
			return root.GenZshCompletion(f)
		},
		"dfmsctl.fish": func(path string) error {
			f, err := os.Create(path)
			if err != nil {
				return err
			}
			defer f.Close()
			return root.GenFishCompletion(f, true)
		},
		"dfmsctl.ps1": func(path string) error {
			f, err := os.Create(path)
			if err != nil {
				return err
			}
			defer f.Close()
			return root.GenPowerShellCompletionWithDesc(f)
		},
	}

	for name, gen := range generators {
		path := filepath.Join(dir, name)
		if err := gen(path); err != nil {
			fatalf("generating %s: %v", name, err)
		}
		fmt.Printf("  → %s\n", path)
	}
}

func genManPages(dir string) {
	if err := cli.GenManPages(dir); err != nil {
		fatalf("generating man pages: %v", err)
	}
	fmt.Printf("  → man pages written to %s/\n", dir)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "dfmsctl-docs: "+format+"\n", args...)
	os.Exit(1)
}
