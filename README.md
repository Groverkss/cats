# cats

Multi-agent workspace coordination system. A single `cats` binary provides
agent orchestration, ticket management, and sandboxed execution through
subcommands.

## Commands

```
cats kitten              Initialize a new workspace
cats peggy tui           Ticket + topic management TUI
cats peggy ticket ...    Ticket CLI (list, show, create, ready, close, update, dep, blocked)
cats peggy topic ...     Topic CLI (create, list, status, close)
cats moe tui             Agent pool manager TUI
cats plan                Launch the planner agent (interactive claude session)
cats box                 Sandboxed shell in a worktree
```

## Agent Roles

- **Planner (`cats plan`):** Creates topics, designs solutions, writes tickets. Does not write code.
- **Coder:** Implements tickets. Works on topic branches in worktrees.
- **Reviewer:** Reviews code on topic branches. Does not modify code.

## Building

```
go build -o cats ./cmd/cats
```

## Workflow

All code changes happen on topic branches (`topic/<name>`), each with a
worktree in `.worktrees/<name>/`. One ticket per agent session — finish it or
escalate. If blocked, update the ticket and reassign to planner.
