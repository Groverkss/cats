package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kunwar/cats/internal/config"
	"github.com/kunwar/cats/internal/peggy"
	"github.com/kunwar/cats/internal/pool"
	"github.com/kunwar/cats/internal/sandbox"
	"github.com/kunwar/cats/internal/ui"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "kitten":
		cmdKitten(os.Args[2:])
	case "peggy":
		cmdPeggy(os.Args[2:])
	case "moe":
		cmdMoe(os.Args[2:])
	case "plan":
		cmdPlan(os.Args[2:])
	case "box":
		cmdBox(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: cats <command> [args...]

Commands:
  kitten    Initialize a new workspace
  peggy     Ticket + topic management (TUI and CLI)
  moe       Agent pool manager (TUI and CLI)
  plan      Launch the planner agent (interactive claude session)
  box       Sandboxed shell in a worktree
`)
}

// findWorkspace walks up from cwd looking for cats.toml.
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

func requireWorkspace() string {
	ws, err := findWorkspace()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\nRun 'cats kitten' to initialize a workspace.\n", err)
		os.Exit(1)
	}
	return ws
}

func newStore(workspace string) *peggy.BeadsStore {
	store, err := peggy.NewBeadsStore(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return store
}

// --- cats kitten ---

func cmdKitten(args []string) {
	if len(args) > 0 && args[0] == "update" {
		cmdKittenUpdate()
		return
	}

	dir, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	// Check if already initialized.
	if _, err := os.Stat(filepath.Join(dir, "cats.toml")); err == nil {
		fmt.Fprintf(os.Stderr, "Error: workspace already initialized (cats.toml exists)\n")
		fmt.Fprintf(os.Stderr, "Use 'cats kitten update' to update the cats binary.\n")
		os.Exit(1)
	}

	// Create directory structure.
	dirs := []string{".beads", ".topics", ".worktrees", "logs", "prompts", ".tmp"}
	for _, d := range dirs {
		os.MkdirAll(filepath.Join(dir, d), 0755)
	}

	// Write default cats.toml.
	tomlContent := `[pool]
poll_interval = "5s"
max_retries = 2

[sandbox]
gpu = false
network = true
extra_ro = []
extra_rw = []
`
	if err := os.WriteFile(filepath.Join(dir, "cats.toml"), []byte(tomlContent), 0644); err != nil {
		fatal(err)
	}

	// Copy default prompts if they don't exist.
	defaultPrompts := map[string]string{
		"planner.md":  defaultPlannerPrompt,
		"coder.md":    defaultCoderPrompt,
		"reviewer.md": defaultReviewerPrompt,
	}
	for name, content := range defaultPrompts {
		path := filepath.Join(dir, "prompts", name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			os.WriteFile(path, []byte(content), 0644)
		}
	}

	// Copy cats binary into workspace.
	if err := copySelfTo(filepath.Join(dir, "cats")); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not copy cats binary: %v\n", err)
	}

	fmt.Println("Initialized cats workspace in", dir)
	fmt.Println("  cats        cats binary (for agents)")
	fmt.Println("  .beads/     ticket database")
	fmt.Println("  .topics/    topic metadata")
	fmt.Println("  .worktrees/ git worktrees")
	fmt.Println("  logs/       agent session logs")
	fmt.Println("  prompts/    agent prompt templates")
	fmt.Println("")
	fmt.Println("Next: cats peggy topic create <name> --repo <path> \"description\"")
}

func cmdKittenUpdate() {
	ws := requireWorkspace()
	dest := filepath.Join(ws, "cats")
	if err := copySelfTo(dest); err != nil {
		fatal(err)
	}
	fmt.Printf("Updated cats binary in %s\n", ws)
}

// copySelfTo copies the currently running binary to dest.
// Uses write-to-temp + rename to avoid "text file busy" when overwriting self.
func copySelfTo(dest string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find own executable: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("cannot resolve executable path: %w", err)
	}

	// Refuse to overwrite self.
	destAbs, _ := filepath.Abs(dest)
	if self == destAbs {
		return fmt.Errorf("source and destination are the same file")
	}

	src, err := os.Open(self)
	if err != nil {
		return err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return err
	}

	// Write to temp file in same directory, then atomic rename.
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".cats-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	tmp.Close()

	if err := os.Chmod(tmpPath, info.Mode()); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, dest); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// --- cats peggy ---

func cmdPeggy(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: cats peggy <tui|ticket|topic>\n")
		fmt.Fprintf(os.Stderr, "  tui        Launch ticket management TUI\n")
		fmt.Fprintf(os.Stderr, "  ticket     Ticket operations\n")
		fmt.Fprintf(os.Stderr, "  topic      Topic operations\n")
		os.Exit(1)
	}

	switch args[0] {
	case "tui":
		cmdPeggyTUI()
	case "ticket":
		cmdPeggyTicket(args[1:])
	case "topic":
		cmdPeggyTopic(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Usage: cats peggy <tui|ticket|topic>\n")
		os.Exit(1)
	}
}

func cmdPeggyTUI() {
	ws := requireWorkspace()
	store := newStore(ws)

	m := ui.NewPeggy(store, ws)
	prog := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		fatal(err)
	}
}

func cmdPeggyTicket(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: cats peggy ticket <list|show|create|ready|close|update|dep|blocked>\n")
		os.Exit(1)
	}

	ws := requireWorkspace()
	store := newStore(ws)
	ctx := context.Background()

	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("ticket list", flag.ExitOnError)
		status := fs.String("status", "", "Filter by status")
		assignee := fs.String("assignee", "", "Filter by assignee")
		typ := fs.String("type", "", "Filter by type")
		format := fs.String("format", "text", "Output format (text|json)")
		fs.Parse(args[1:])

		var filter peggy.Filter
		if *status != "" {
			s := peggy.Status(*status)
			filter.Status = &s
		}
		if *assignee != "" {
			filter.Assignee = assignee
		}
		if *typ != "" {
			filter.Type = typ
		}

		tickets, err := store.List(ctx, filter)
		if err != nil {
			fatal(err)
		}
		printTickets(tickets, *format)

	case "show":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: cats peggy ticket show <id>\n")
			os.Exit(1)
		}
		detail, err := store.Get(ctx, args[1])
		if err != nil {
			fatal(err)
		}
		data, _ := json.MarshalIndent(detail, "", "  ")
		fmt.Println(string(data))

	case "create":
		fs := flag.NewFlagSet("ticket create", flag.ExitOnError)
		title := fs.String("title", "", "Ticket title (required)")
		topicName := fs.String("topic", "", "Topic name (required for task/bug/review)")
		assignee := fs.String("assignee", "", "Assignee role")
		priority := fs.Int("priority", 1, "Priority (0-4)")
		typ := fs.String("type", "task", "Ticket type (task|bug|review)")
		desc := fs.String("description", "", "Description")
		dependsOn := fs.String("depends-on", "", "Comma-separated ticket IDs this depends on")
		fs.Parse(args[1:])

		if *title == "" {
			fmt.Fprintf(os.Stderr, "Error: --title is required\n")
			os.Exit(1)
		}
		if *topicName == "" {
			fmt.Fprintf(os.Stderr, "Error: --topic is required\n")
			os.Exit(1)
		}

		var deps []string
		if *dependsOn != "" {
			for _, d := range strings.Split(*dependsOn, ",") {
				d = strings.TrimSpace(d)
				if d != "" {
					deps = append(deps, d)
				}
			}
		}

		id, err := store.Create(ctx, peggy.CreateOpts{
			Title:       *title,
			Description: *desc,
			Topic:       *topicName,
			Assignee:    *assignee,
			Priority:    *priority,
			Type:        *typ,
			DependsOn:   deps,
		})
		if err != nil {
			fatal(err)
		}
		fmt.Println(id)

	case "ready":
		fs := flag.NewFlagSet("ticket ready", flag.ExitOnError)
		role := fs.String("role", "", "Role to check (required)")
		format := fs.String("format", "text", "Output format (text|json)")
		fs.Parse(args[1:])

		if *role == "" {
			fmt.Fprintf(os.Stderr, "Error: --role is required\n")
			os.Exit(1)
		}

		tickets, err := store.Ready(ctx, *role)
		if err != nil {
			fatal(err)
		}
		printTickets(tickets, *format)

	case "close":
		fs := flag.NewFlagSet("ticket close", flag.ExitOnError)
		reason := fs.String("reason", "", "Closing reason")
		fs.Parse(args[1:])

		remaining := fs.Args()
		if len(remaining) == 0 {
			fmt.Fprintf(os.Stderr, "Usage: cats peggy ticket close <id> [--reason=...]\n")
			os.Exit(1)
		}
		if err := store.Close(ctx, remaining[0], *reason); err != nil {
			fatal(err)
		}
		fmt.Printf("Closed %s\n", remaining[0])

	case "update":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: cats peggy ticket update <id> --status=... [--assignee=...]\n")
			os.Exit(1)
		}
		ticketID := args[1]
		fs := flag.NewFlagSet("ticket update", flag.ExitOnError)
		status := fs.String("status", "", "New status")
		assignee := fs.String("assignee", "", "Actor/assignee")
		fs.Parse(args[2:])

		if *status == "" {
			fmt.Fprintf(os.Stderr, "Error: --status is required\n")
			os.Exit(1)
		}
		if err := store.UpdateStatus(ctx, ticketID, peggy.Status(*status), *assignee); err != nil {
			fatal(err)
		}
		fmt.Printf("Updated %s -> %s\n", ticketID, *status)

	case "dep":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: cats peggy ticket dep <add|remove|list> <ticket-id> [depends-on-id]\n")
			os.Exit(1)
		}
		switch args[1] {
		case "add":
			if len(args) < 4 {
				fmt.Fprintf(os.Stderr, "Usage: cats peggy ticket dep add <ticket-id> <depends-on-id>\n")
				os.Exit(1)
			}
			if err := store.AddDep(ctx, args[2], args[3]); err != nil {
				fatal(err)
			}
			fmt.Printf("%s now depends on %s\n", args[2], args[3])

		case "remove":
			if len(args) < 4 {
				fmt.Fprintf(os.Stderr, "Usage: cats peggy ticket dep remove <ticket-id> <depends-on-id>\n")
				os.Exit(1)
			}
			if err := store.RemoveDep(ctx, args[2], args[3]); err != nil {
				fatal(err)
			}
			fmt.Printf("Removed dependency: %s no longer depends on %s\n", args[2], args[3])

		case "list":
			if len(args) < 3 {
				fmt.Fprintf(os.Stderr, "Usage: cats peggy ticket dep list <ticket-id>\n")
				os.Exit(1)
			}
			deps, err := store.ListDeps(ctx, args[2])
			if err != nil {
				fatal(err)
			}
			if len(deps) == 0 {
				fmt.Println("No dependencies.")
			} else {
				for _, d := range deps {
					fmt.Println(" ", d)
				}
			}

		default:
			fmt.Fprintf(os.Stderr, "Usage: cats peggy ticket dep <add|remove|list>\n")
			os.Exit(1)
		}

	case "blocked":
		fs := flag.NewFlagSet("ticket blocked", flag.ExitOnError)
		format := fs.String("format", "text", "Output format (text|json)")
		fs.Parse(args[1:])

		tickets, err := store.Blocked(ctx)
		if err != nil {
			fatal(err)
		}
		printTickets(tickets, *format)

	default:
		fmt.Fprintf(os.Stderr, "Unknown ticket command: %s\n", args[0])
		os.Exit(1)
	}
}

func cmdPeggyTopic(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: cats peggy topic <create|list|status|close>\n")
		os.Exit(1)
	}

	ws := requireWorkspace()
	store := newStore(ws)
	ctx := context.Background()

	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("topic create", flag.ExitOnError)
		repo := fs.String("repo", "", "Path to git repo (required)")
		fs.Parse(args[1:])

		remaining := fs.Args()
		if len(remaining) < 2 || *repo == "" {
			fmt.Fprintf(os.Stderr, "Usage: cats peggy topic create <name> --repo <path> \"description\"\n")
			os.Exit(1)
		}

		// Resolve repo to absolute path.
		repoPath, err := filepath.Abs(*repo)
		if err != nil {
			fatal(err)
		}

		topic, err := store.CreateTopic(ctx, peggy.TopicOpts{
			Name:        remaining[0],
			Repo:        repoPath,
			Description: strings.Join(remaining[1:], " "),
		})
		if err != nil {
			fatal(err)
		}

		fmt.Printf("Topic '%s' created:\n", topic.Name)
		fmt.Printf("  Branch:   %s\n", topic.Branch)
		fmt.Printf("  Worktree: %s\n", topic.Worktree)
		fmt.Printf("\nNext: cats peggy ticket create --title='...' --topic=%s --assignee=coder\n", topic.Name)

	case "list":
		topics, err := store.ListTopics(ctx)
		if err != nil {
			fatal(err)
		}
		if len(topics) == 0 {
			fmt.Println("No topics.")
			return
		}
		for _, t := range topics {
			fmt.Printf("  %s (%s)\n", t.Name, t.Status)
			fmt.Printf("    branch: %s | repo: %s\n", t.Branch, t.Repo)
		}

	case "status":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: cats peggy topic status <name>\n")
			os.Exit(1)
		}
		topic, err := store.GetTopic(ctx, args[1])
		if err != nil {
			fatal(err)
		}
		data, _ := json.MarshalIndent(topic, "", "  ")
		fmt.Println(string(data))

	case "close":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: cats peggy topic close <name>\n")
			os.Exit(1)
		}
		if err := store.CloseTopic(ctx, args[1]); err != nil {
			fatal(err)
		}
		fmt.Printf("Topic '%s' closed.\n", args[1])
		fmt.Println("Next steps:")
		fmt.Println("  1. Squash merge: git merge --squash topic/" + args[1])
		fmt.Println("  2. Remove worktree: git worktree remove .worktrees/" + args[1])

	default:
		fmt.Fprintf(os.Stderr, "Unknown topic command: %s\n", args[0])
		os.Exit(1)
	}
}

// --- cats moe ---

func cmdMoe(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: cats moe <tui>\n")
		fmt.Fprintf(os.Stderr, "  tui        Launch agent pool manager TUI\n")
		os.Exit(1)
	}

	switch args[0] {
	case "tui":
		cmdMoeTUI()
	default:
		fmt.Fprintf(os.Stderr, "Usage: cats moe <tui>\n")
		os.Exit(1)
	}
}

func cmdMoeTUI() {
	ws := requireWorkspace()
	cfg, err := config.Load(ws)
	if err != nil {
		fatal(err)
	}

	os.MkdirAll(filepath.Join(ws, "logs"), 0755)

	store := newStore(ws)
	p := pool.New(ws, cfg, store)
	m := ui.NewMoe(p, ws)

	prog := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := prog.Run(); err != nil {
		fatal(err)
	}
}

// --- cats plan ---

func cmdPlan(args []string) {
	ws := requireWorkspace()

	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	topic := fs.String("topic", "", "Scope to a specific topic")
	fs.Parse(args)

	promptPath := filepath.Join(ws, "prompts", "planner.md")
	promptData, err := os.ReadFile(promptPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot read planner prompt: %v\n", err)
		os.Exit(1)
	}

	prompt := string(promptData)
	if *topic != "" {
		prompt += fmt.Sprintf("\n\nYou are scoped to topic: %s\n", *topic)
	}

	claudePath, err := findClaude()
	if err != nil {
		fatal(err)
	}

	// Planner can read code and manage tickets/topics, but cannot write code.
	claudeArgs := []string{
		"--append-system-prompt", prompt,
		"--allowedTools",
		"Read",
		"Glob",
		"Grep",
		"Bash(cats peggy *)",
		"Bash(git log *)",
		"Bash(git diff *)",
		"Bash(git show *)",
		"Bash(ls *)",
	}

	err = execClaude(claudePath, claudeArgs...)
	if err != nil {
		fatal(err)
	}
}

// --- cats box ---

func cmdBox(args []string) {
	ws := requireWorkspace()
	cfg, err := config.Load(ws)
	if err != nil {
		fatal(err)
	}

	fs := flag.NewFlagSet("box", flag.ExitOnError)
	topic := fs.String("topic", "", "Topic name (uses its worktree)")
	fs.Parse(args)

	workdir := ws
	if *topic != "" {
		store := newStore(ws)
		t, err := store.GetTopic(context.Background(), *topic)
		if err != nil {
			fatal(err)
		}
		workdir = t.Worktree
		if !filepath.IsAbs(workdir) {
			workdir = filepath.Join(ws, workdir)
		}
	}

	sbCfg := sandbox.Config{
		Workspace: ws,
		Workdir:   workdir,
		GPU:       cfg.Sandbox.GPU,
		Network:   cfg.Sandbox.Network,
		ExtraRO:   cfg.Sandbox.ExtraRO,
		ExtraRW:   cfg.Sandbox.ExtraRW,
	}

	cmd := fs.Args()
	if err := sandbox.Exec(sbCfg, cmd...); err != nil {
		fatal(err)
	}
}

// --- helpers ---

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

func printTickets(tickets []peggy.Ticket, format string) {
	if format == "json" {
		data, _ := json.MarshalIndent(tickets, "", "  ")
		fmt.Println(string(data))
		return
	}
	if len(tickets) == 0 {
		fmt.Println("No tickets.")
		return
	}
	for _, t := range tickets {
		fmt.Printf("  %-14s P%d %-12s %-10s %s\n", t.ID, t.Priority, t.Status, t.Assignee, t.Title)
	}
}

func findClaude() (string, error) {
	// Check common locations.
	candidates := []string{
		"claude",
	}
	for _, c := range candidates {
		if p, err := findInPath(c); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("claude not found in PATH")
}

func findInPath(name string) (string, error) {
	// exec.LookPath but we need the full path for syscall.Exec.
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found", name)
}

func execClaude(claudePath string, args ...string) error {
	argv := append([]string{"claude"}, args...)
	return execve(claudePath, argv, os.Environ())
}

// Default prompts for cats kitten.
const defaultPlannerPrompt = `You are Peggy, the planner for this project. You work interactively with the human to scope work, design solutions, and task them out for coding agents.

## What You Do

- Read the codebase and assess what's needed
- Create design docs when the work warrants it (in docs/design/)
- Create topics: cats peggy topic create <name> --repo <path> "<description>"
- Create tickets under topics: cats peggy ticket create --title="..." --topic=<name> --type=task --assignee=coder
- Track progress: cats peggy ticket list, cats peggy ticket show <id>, cats peggy ticket ready --role=coder

## What You Do NOT Do

- You do NOT write code, run builds, or make commits
- You do NOT modify source files
- You do NOT run tests
- You coordinate — the coding agents implement

## How to Create Good Tickets

Coding agents are stateless. They have no memory of this conversation. Every ticket must be self-contained:

1. What to do — clear, specific, actionable
2. Files to read first — what context the coder needs
3. Files to modify/create — be explicit
4. Constraints — what NOT to do
5. Test expectations — how to verify the work
6. Design doc reference — if applicable
`

const defaultCoderPrompt = `You are a coding agent. You have been assigned one ticket to work on.

TICKET: {{TICKET_ID}}
TOPIC: {{TOPIC_NAME}}
BRANCH: {{BRANCH}}

## Workflow

1. Run ` + "`cats peggy ticket show {{TICKET_ID}}`" + ` to read the full ticket description
2. Read CLAUDE.md for project conventions
3. You are already on the correct branch in the topic's worktree
4. Implement what the ticket asks for
5. Test your changes
6. Commit with a descriptive message
7. Close the ticket: ` + "`cats peggy ticket close {{TICKET_ID}} --reason=\"Done\"`" + `

## Rules

- Only work on this one ticket
- Stay on the assigned branch
- Do not modify files outside the scope of the ticket
- If you're blocked or unsure, update the ticket with your findings and reassign to planner:
  ` + "`cats peggy ticket update {{TICKET_ID}} --status=open --assignee=planner`" + `
`

const defaultReviewerPrompt = `You are a code reviewer. You have been assigned a review ticket.

TICKET: {{TICKET_ID}}

## Workflow

1. Run ` + "`cats peggy ticket show {{TICKET_ID}}`" + ` to read the review request
2. Read CLAUDE.md for project conventions
3. Review the diff: ` + "`git diff main...HEAD`" + `
4. Check for: correctness, code quality, test coverage, design alignment
5. If approved: close the ticket with your summary as the reason
6. If changes needed: create specific tickets assigned to coder, then close the review ticket

## Rules

- Do NOT modify code yourself
- Be specific in feedback: file, line number, what's wrong, what to do instead
- Reference CLAUDE.md or design docs when citing conventions
- One ticket per issue found — don't bundle unrelated feedback
`
