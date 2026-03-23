package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kunwar/cats/internal/config"
	"github.com/kunwar/cats/internal/pool"
	"github.com/kunwar/cats/internal/ui"
)

func main() {
	// Find workspace root (directory containing cats.toml).
	workspace, err := findWorkspace()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintf(os.Stderr, "Run moe from a cats workspace (directory with cats.toml)\n")
		os.Exit(1)
	}

	// Load config.
	cfg, err := config.Load(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Ensure logs directory exists.
	os.MkdirAll(filepath.Join(workspace, "logs"), 0755)

	// Create pool and TUI.
	p := pool.New(workspace, cfg)
	m := ui.New(p, workspace)

	prog := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func findWorkspace() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "cats.toml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("cats.toml not found in any parent directory")
		}
		dir = parent
	}
}
