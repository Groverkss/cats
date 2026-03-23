# cats workspace

## What This Is

cats is a multi-agent workspace coordination system. Work is tracked via
beads-rust (`br`) tickets stored in `.beads/`. Agents are stateless —
everything they need is in the ticket description and this file.

## Agent Roles

- **Planner (peggy):** Creates topics, designs solutions, writes tickets.
  Does NOT write code.
- **Coder:** Implements tickets. Works on topic branches in `.worktrees/`.
- **Reviewer:** Reviews code on topic branches. Does NOT modify code.

## Conventions

- All code changes happen on topic branches (`topic/<name>`)
- Each topic has a worktree in `.worktrees/<name>/`
- Commit messages: imperative mood, reference the ticket ID
- One ticket per agent session — finish it or escalate
- If blocked, update the ticket and reassign to planner

## Beads Commands

```bash
br ready                    # Find actionable work
br show <id>                # Full ticket details
br update <id> --status=in_progress
br close <id> --reason="Done"
br sync --flush-only        # Export before session end
```

## Before Ending a Session

```bash
git add <files>
git commit -m "ticket-id: description"
br close <ticket-id> --reason="..."
br sync --flush-only
```
