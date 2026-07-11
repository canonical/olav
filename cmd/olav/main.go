package main

import (
	"fmt"
	"os"

	"github.com/canonical/olav/internal/oci"
	"github.com/canonical/olav/internal/tui"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: olav <oci-layout-dir-or-tarball>\n")
		os.Exit(2)
	}

	layout, err := oci.Load(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "olav: %v\n", err)
		os.Exit(1)
	}

	program := tea.NewProgram(tui.New(layout), tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "olav: %v\n", err)
		os.Exit(1)
	}
}
