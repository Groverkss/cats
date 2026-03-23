package main

import (
	"context"
	"encoding/json"
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
	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:           "cats",
		Short:         "Multi-agent workspace coordination",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	rootCmd.AddCommand(
		kittenCmd(),
		peggyCmd(),
		moeCmd(),
		planCmd(),
		boxCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// --- workspace helpers ---

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

func requireWorkspace(cmd *cobra.Command) string {
	ws, err := findWorkspace()
	if err != nil {
		cmd.PrintErrln("Error:", err)
		cmd.PrintErrln("Run 'cats kitten' to initialize a workspace.")
		os.Exit(1)
	}
	return ws
}

func newStore(cmd *cobra.Command, workspace string) *peggy.BeadsStore {
	store, err := peggy.NewBeadsStore(workspace)
	if err != nil {
		cmd.PrintErrln("Error:", err)
		os.Exit(1)
	}
	return store
}

// --- cats kitten ---

func kittenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kitten",
		Short: "Initialize a new workspace",
		Long:  "Create a cats workspace in the current directory with default config, prompts, and directory structure.",
		RunE:  runKitten,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "update",
		Short: "Update the cats binary in the workspace",
		Long:  "Copy the current cats binary into the workspace, replacing the old one.",
		RunE:  runKittenUpdate,
	})
	return cmd
}

func runKitten(cmd *cobra.Command, args []string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	if _, err := os.Stat(filepath.Join(dir, "cats.toml")); err == nil {
		return fmt.Errorf("workspace already initialized (cats.toml exists)\nUse 'cats kitten update' to update the cats binary")
	}

	dirs := []string{".beads", ".topics", ".worktrees", "logs", "prompts", ".tmp"}
	for _, d := range dirs {
		os.MkdirAll(filepath.Join(dir, d), 0755)
	}

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
		return err
	}

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

	// Initialize the ticket store.
	store, err := peggy.NewBeadsStore(dir)
	if err != nil {
		return fmt.Errorf("failed to initialize ticket store: %w", err)
	}
	if err := store.Init(context.Background()); err != nil {
		return fmt.Errorf("failed to initialize ticket store: %w", err)
	}

	if err := copySelfTo(filepath.Join(dir, "cats")); err != nil {
		cmd.PrintErrf("Warning: could not copy cats binary: %v\n", err)
	}

	fmt.Println("Initialized cats workspace in", dir)
	fmt.Println("  cats        cats binary (for agents)")
	fmt.Println("  .beads/     ticket database")
	fmt.Println("  .topics/    topic metadata")
	fmt.Println("  .worktrees/ git worktrees")
	fmt.Println("  logs/       agent session logs")
	fmt.Println("  prompts/    agent prompt templates")
	fmt.Println()
	fmt.Println("Next: cats peggy topic create <name> --repo <path> --description \"...\"")
	return nil
}

func runKittenUpdate(cmd *cobra.Command, args []string) error {
	ws := requireWorkspace(cmd)
	dest := filepath.Join(ws, "cats")
	if err := copySelfTo(dest); err != nil {
		return err
	}
	fmt.Printf("Updated cats binary in %s\n", ws)
	return nil
}

// --- cats peggy ---

func peggyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peggy",
		Short: "Ticket and topic management",
	}
	cmd.AddCommand(
		peggyTuiCmd(),
		peggyTicketCmd(),
		peggyTopicCmd(),
	)
	return cmd
}

func peggyTuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Launch ticket management TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)
			m := ui.NewPeggy(store, ws)
			prog := tea.NewProgram(m, tea.WithAltScreen())
			_, err := prog.Run()
			return err
		},
	}
}

// --- peggy ticket ---

func peggyTicketCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ticket",
		Short: "Ticket operations",
	}
	cmd.AddCommand(
		ticketListCmd(),
		ticketShowCmd(),
		ticketCreateCmd(),
		ticketReadyCmd(),
		ticketCloseCmd(),
		ticketUpdateCmd(),
		ticketDepCmd(),
		ticketBlockedCmd(),
	)
	return cmd
}

func ticketListCmd() *cobra.Command {
	var status, assignee, typ, format string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tickets",
		Example: `  cats peggy ticket list
  cats peggy ticket list --status=open
  cats peggy ticket list --assignee=coder --format=json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)
			ctx := context.Background()

			var filter peggy.Filter
			if status != "" {
				s := peggy.Status(status)
				filter.Status = &s
			}
			if assignee != "" {
				filter.Assignee = &assignee
			}
			if typ != "" {
				filter.Type = &typ
			}

			tickets, err := store.List(ctx, filter)
			if err != nil {
				return err
			}
			printTickets(tickets, format)
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "Filter by status (open, in_progress, blocked, completed, cancelled)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "Filter by assignee")
	cmd.Flags().StringVar(&typ, "type", "", "Filter by ticket type")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text|json)")
	return cmd
}

func ticketShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <ticket-id>",
		Short: "Show ticket details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)
			detail, err := store.Get(context.Background(), args[0])
			if err != nil {
				return err
			}
			data, _ := json.MarshalIndent(detail, "", "  ")
			fmt.Println(string(data))
			return nil
		},
	}
}

func ticketCreateCmd() *cobra.Command {
	var title, topic, assignee, typ, desc, dependsOn string
	var priority int
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a ticket under a topic",
		Example: `  cats peggy ticket create --title="Add JWT middleware" --topic=auth-flow --assignee=coder
  cats peggy ticket create --title="Add tests" --topic=auth-flow --assignee=coder --depends-on=ws-b1c2
  cats peggy ticket create --title="Review" --topic=auth-flow --type=review --assignee=reviewer`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)

			var deps []string
			if dependsOn != "" {
				for _, d := range strings.Split(dependsOn, ",") {
					d = strings.TrimSpace(d)
					if d != "" {
						deps = append(deps, d)
					}
				}
			}

			id, err := store.Create(context.Background(), peggy.CreateOpts{
				Title:       title,
				Description: desc,
				Topic:       topic,
				Assignee:    assignee,
				Priority:    priority,
				Type:        typ,
				DependsOn:   deps,
			})
			if err != nil {
				return err
			}
			fmt.Println(id)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "Ticket title")
	cmd.Flags().StringVar(&topic, "topic", "", "Topic name")
	cmd.Flags().StringVar(&assignee, "assignee", "", "Assignee role (coder, reviewer)")
	cmd.Flags().IntVar(&priority, "priority", 1, "Priority 0-4 (0=critical, 4=backlog)")
	cmd.Flags().StringVar(&typ, "type", "task", "Ticket type (task, bug, review)")
	cmd.Flags().StringVar(&desc, "description", "", "Ticket description")
	cmd.Flags().StringVar(&dependsOn, "depends-on", "", "Comma-separated ticket IDs this depends on")
	cmd.MarkFlagRequired("title")
	cmd.MarkFlagRequired("topic")
	return cmd
}

func ticketReadyCmd() *cobra.Command {
	var role, format string
	cmd := &cobra.Command{
		Use:   "ready",
		Short: "List tickets ready for a role (open + unblocked)",
		Example: `  cats peggy ticket ready --role=coder
  cats peggy ticket ready --role=reviewer --format=json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)
			tickets, err := store.Ready(context.Background(), role)
			if err != nil {
				return err
			}
			printTickets(tickets, format)
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "Role to check (coder, reviewer)")
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text|json)")
	cmd.MarkFlagRequired("role")
	return cmd
}

func ticketCloseCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:     "close <ticket-id>",
		Short:   "Close a ticket",
		Args:    cobra.ExactArgs(1),
		Example: `  cats peggy ticket close ws-b1c2 --reason="Implemented and tested"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)
			if err := store.Close(context.Background(), args[0], reason); err != nil {
				return err
			}
			fmt.Printf("Closed %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Closing reason")
	return cmd
}

func ticketUpdateCmd() *cobra.Command {
	var status, assignee string
	cmd := &cobra.Command{
		Use:   "update <ticket-id>",
		Short: "Update a ticket's status",
		Args:  cobra.ExactArgs(1),
		Example: `  cats peggy ticket update ws-b1c2 --status=in_progress
  cats peggy ticket update ws-b1c2 --status=open --assignee=planner`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)
			if err := store.UpdateStatus(context.Background(), args[0], peggy.Status(status), assignee); err != nil {
				return err
			}
			fmt.Printf("Updated %s -> %s\n", args[0], status)
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "New status (open, in_progress, blocked, completed, cancelled)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "Actor/assignee")
	cmd.MarkFlagRequired("status")
	return cmd
}

func ticketBlockedCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:     "blocked",
		Short:   "List blocked tickets (have unmet dependencies)",
		Example: `  cats peggy ticket blocked`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)
			tickets, err := store.Blocked(context.Background())
			if err != nil {
				return err
			}
			printTickets(tickets, format)
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "Output format (text|json)")
	return cmd
}

// --- peggy ticket dep ---

func ticketDepCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dep",
		Short: "Manage ticket dependencies",
	}
	cmd.AddCommand(
		ticketDepAddCmd(),
		ticketDepRemoveCmd(),
		ticketDepListCmd(),
	)
	return cmd
}

func ticketDepAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "add <ticket-id> <depends-on-id>",
		Short:   "Add a dependency",
		Args:    cobra.ExactArgs(2),
		Example: `  cats peggy ticket dep add ws-c3d4 ws-b1c2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)
			if err := store.AddDep(context.Background(), args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("%s now depends on %s\n", args[0], args[1])
			return nil
		},
	}
}

func ticketDepRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <ticket-id> <depends-on-id>",
		Short:   "Remove a dependency",
		Args:    cobra.ExactArgs(2),
		Example: `  cats peggy ticket dep remove ws-c3d4 ws-b1c2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)
			if err := store.RemoveDep(context.Background(), args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf("Removed dependency: %s no longer depends on %s\n", args[0], args[1])
			return nil
		},
	}
}

func ticketDepListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list <ticket-id>",
		Short:   "List dependencies of a ticket",
		Args:    cobra.ExactArgs(1),
		Example: `  cats peggy ticket dep list ws-c3d4`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)
			deps, err := store.ListDeps(context.Background(), args[0])
			if err != nil {
				return err
			}
			if len(deps) == 0 {
				fmt.Println("No dependencies.")
			} else {
				for _, d := range deps {
					fmt.Println(" ", d)
				}
			}
			return nil
		},
	}
}

// --- peggy topic ---

func peggyTopicCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "topic",
		Short: "Topic operations",
	}
	cmd.AddCommand(
		topicCreateCmd(),
		topicListCmd(),
		topicStatusCmd(),
		topicCloseCmd(),
	)
	return cmd
}

func topicCreateCmd() *cobra.Command {
	var repo, description string
	cmd := &cobra.Command{
		Use:     "create <name>",
		Short:   "Create a new topic (branch + worktree + ticket group)",
		Args:    cobra.ExactArgs(1),
		Example: `  cats peggy topic create auth-flow --repo /home/user/projects/myapp --description "Add JWT authentication"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)

			repoPath, err := filepath.Abs(repo)
			if err != nil {
				return err
			}

			topic, err := store.CreateTopic(context.Background(), peggy.TopicOpts{
				Name:        args[0],
				Repo:        repoPath,
				Description: description,
			})
			if err != nil {
				return err
			}

			fmt.Printf("Topic '%s' created:\n", topic.Name)
			fmt.Printf("  Branch:   %s\n", topic.Branch)
			fmt.Printf("  Worktree: %s\n", topic.Worktree)
			fmt.Printf("\nNext: cats peggy ticket create --title='...' --topic=%s --assignee=coder\n", topic.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "Path to git repository")
	cmd.Flags().StringVar(&description, "description", "", "Topic description")
	cmd.MarkFlagRequired("repo")
	return cmd
}

func topicListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all topics",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)
			topics, err := store.ListTopics(context.Background())
			if err != nil {
				return err
			}
			if len(topics) == 0 {
				fmt.Println("No topics.")
				return nil
			}
			for _, t := range topics {
				fmt.Printf("  %s (%s)\n", t.Name, t.Status)
				fmt.Printf("    branch: %s | repo: %s\n", t.Branch, t.Repo)
			}
			return nil
		},
	}
}

func topicStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "status <name>",
		Short:   "Show topic details",
		Args:    cobra.ExactArgs(1),
		Example: `  cats peggy topic status auth-flow`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)
			topic, err := store.GetTopic(context.Background(), args[0])
			if err != nil {
				return err
			}
			data, _ := json.MarshalIndent(topic, "", "  ")
			fmt.Println(string(data))
			return nil
		},
	}
}

func topicCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "close <name>",
		Short:   "Close a topic",
		Args:    cobra.ExactArgs(1),
		Example: `  cats peggy topic close auth-flow`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			store := newStore(cmd, ws)
			if err := store.CloseTopic(context.Background(), args[0]); err != nil {
				return err
			}
			fmt.Printf("Topic '%s' closed.\n", args[0])
			fmt.Println("Next steps:")
			fmt.Println("  1. Squash merge: git merge --squash topic/" + args[0])
			fmt.Println("  2. Remove worktree: git worktree remove .worktrees/" + args[0])
			return nil
		},
	}
}

// --- cats moe ---

func moeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "moe",
		Short: "Agent pool manager",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "tui",
		Short: "Launch agent pool manager TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			cfg, err := config.Load(ws)
			if err != nil {
				return err
			}
			os.MkdirAll(filepath.Join(ws, "logs"), 0755)
			store := newStore(cmd, ws)
			p := pool.New(ws, cfg, store)
			m := ui.NewMoe(p, ws)
			prog := tea.NewProgram(m, tea.WithAltScreen())
			_, err = prog.Run()
			return err
		},
	})
	return cmd
}

// --- cats plan ---

func planCmd() *cobra.Command {
	var topic string
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Launch the planner agent (interactive claude session)",
		Long:  "Start an interactive Claude session with the planner prompt. The planner can read code and manage tickets/topics, but cannot write code.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)

			promptPath := filepath.Join(ws, "prompts", "planner.md")
			promptData, err := os.ReadFile(promptPath)
			if err != nil {
				return fmt.Errorf("cannot read planner prompt: %w", err)
			}

			prompt := string(promptData)
			if topic != "" {
				prompt += fmt.Sprintf("\n\nYou are scoped to topic: %s\n", topic)
			}

			claudePath, err := findClaude()
			if err != nil {
				return err
			}

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

			return execClaude(claudePath, claudeArgs...)
		},
	}
	cmd.Flags().StringVar(&topic, "topic", "", "Scope to a specific topic")
	return cmd
}

// --- cats box ---

func boxCmd() *cobra.Command {
	var topic string
	cmd := &cobra.Command{
		Use:   "box [command...]",
		Short: "Sandboxed shell in a worktree",
		Long:  "Drop into a sandboxed shell using the same bwrap environment that agents run in. Useful for debugging.",
		Example: `  cats box                        # shell in workspace
  cats box --topic auth-flow      # shell in topic's worktree
  cats box --topic auth-flow ls   # run a command in the sandbox`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ws := requireWorkspace(cmd)
			cfg, err := config.Load(ws)
			if err != nil {
				return err
			}

			workdir := ws
			if topic != "" {
				store := newStore(cmd, ws)
				t, err := store.GetTopic(context.Background(), topic)
				if err != nil {
					return err
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

			return sandbox.Exec(sbCfg, args...)
		},
		DisableFlagParsing: false,
	}
	cmd.Flags().StringVar(&topic, "topic", "", "Topic name (uses its worktree)")
	return cmd
}

// --- helpers ---

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

func copySelfTo(dest string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find own executable: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("cannot resolve executable path: %w", err)
	}

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

func findClaude() (string, error) {
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		p := filepath.Join(dir, "claude")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("claude not found in PATH")
}

func execClaude(claudePath string, args ...string) error {
	argv := append([]string{"claude"}, args...)
	return execve(claudePath, argv, os.Environ())
}

// Default prompts for cats kitten.
const defaultPlannerPrompt = `You are Peggy, the planner for this project. You work interactively with the human to scope work, design solutions, and task them out for coding agents.

## What You Do

- Read the codebase and assess what's needed
- Create topics: cats peggy topic create <name> --repo <path> --description "..."
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
