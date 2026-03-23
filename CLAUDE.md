# cats workspace

## What This Is

cats is a multi-agent workspace coordination system. A single `cats` binary
provides all functionality through subcommands.

## Commands

```
cats kitten              Initialize a new workspace
cats peggy               Ticket + topic management TUI
cats peggy ticket ...    Ticket CLI (list, show, create, ready, close, update)
cats peggy topic ...     Topic CLI (create, list, status, close)
cats moe                 Agent pool manager TUI
cats plan                Launch the planner agent (interactive claude session)
cats box                 Sandboxed shell in a worktree
```

## Agent Roles

- **Planner (cats plan):** Creates topics, designs solutions, writes tickets.
  Does NOT write code. Uses `cats peggy` CLI to manage tickets and topics.
- **Coder:** Implements tickets. Works on topic branches in worktrees.
- **Reviewer:** Reviews code on topic branches. Does NOT modify code.

## Conventions

- All code changes happen on topic branches (`topic/<name>`)
- Each topic has a worktree in `.worktrees/<name>/`
- Commit messages: imperative mood, reference the ticket ID
- One ticket per agent session — finish it or escalate
- If blocked, update the ticket and reassign to planner

## Ticket Commands (for agents)

```bash
cats peggy ticket show <id>                    # Full ticket details
cats peggy ticket update <id> --status=in_progress
cats peggy ticket close <id> --reason="Done"
```

## Before Ending a Session

```bash
git add <files>
git commit -m "ticket-id: description"
cats peggy ticket close <ticket-id> --reason="..."
```
