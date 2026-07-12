package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/canonical/olav/internal/oci"
	"github.com/canonical/olav/internal/source"
	"github.com/canonical/olav/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	flags := flag.NewFlagSet("olav", flag.ExitOnError)
	platform := flags.String("platform", "", "platform for docker:// sources: os/arch, os/arch/variant, or all")
	resolveOnly := flags.Bool("resolve-only", false, "resolve, cache, and validate the input without starting the TUI")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "usage: olav [--resolve-only] [--platform os/arch|all] <oci-layout-dir|oci-layout-tar|image-source>\n")
		fmt.Fprintf(flags.Output(), "\nimage sources require explicit prefixes, for example docker://ubuntu:24.04 or docker-daemon:ubuntu:24.04\n")
	}
	if err := flags.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if flags.NArg() != 1 {
		flags.Usage()
		os.Exit(2)
	}

	resolved, err := source.Resolve(context.Background(), source.Options{Input: flags.Arg(0), Platform: *platform, Progress: os.Stderr})
	if err != nil {
		fmt.Fprintf(os.Stderr, "olav: %v\n", err)
		os.Exit(1)
	}
	layout, err := oci.Load(resolved.LocalPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "olav: %v\n", err)
		os.Exit(1)
	}
	layout.InputPath = resolved.DisplayName
	if *resolveOnly {
		fmt.Fprintf(os.Stdout, "loaded %s from %s\n", resolved.DisplayName, resolved.LocalPath)
		return
	}

	program := tea.NewProgram(tui.New(layout), tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "olav: %v\n", err)
		os.Exit(1)
	}
}
