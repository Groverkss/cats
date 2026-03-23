package pool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kunwar/cats/internal/agent"
	"github.com/kunwar/cats/internal/config"
	"github.com/kunwar/cats/internal/peggy"
	"github.com/kunwar/cats/internal/sandbox"
)

type Pool struct {
	mu        sync.Mutex
	agents    []*agent.Agent
	counters  map[string]int // role -> next ID number
	retries   map[string]int // ticket ID -> retry count
	cfg       config.Config
	workspace string
	store     peggy.TicketStore
}

func New(workspace string, cfg config.Config, store peggy.TicketStore) *Pool {
	return &Pool{
		counters:  make(map[string]int),
		retries:   make(map[string]int),
		cfg:       cfg,
		workspace: workspace,
		store:     store,
	}
}

// Store returns the ticket store for external use (e.g. TUI polling).
func (p *Pool) Store() peggy.TicketStore {
	return p.store
}

// Spawn creates a new idle agent with the given role.
func (p *Pool) Spawn(role string) *agent.Agent {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.counters[role]++
	id := fmt.Sprintf("%s-%d", role, p.counters[role])
	a := agent.New(id, role)
	p.agents = append(p.agents, a)
	return a
}

// Remove removes an agent from the pool.
func (p *Pool) Remove(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i, a := range p.agents {
		if a.ID == id {
			a.Kill()
			p.agents = append(p.agents[:i], p.agents[i+1:]...)
			return
		}
	}
}

// Agents returns a snapshot of all agents.
func (p *Pool) Agents() []*agent.Agent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*agent.Agent, len(p.agents))
	copy(out, p.agents)
	return out
}

// ActiveBranches returns the set of branches currently being worked on.
func (p *Pool) ActiveBranches() map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	branches := make(map[string]bool)
	for _, a := range p.agents {
		if a.State == agent.Working && a.Branch != "" {
			branches[a.Branch] = true
		}
	}
	return branches
}

// AssignTicket starts an idle agent on a ticket.
func (p *Pool) AssignTicket(a *agent.Agent, ticket peggy.Ticket, topic *peggy.Topic) error {
	// Reload config for hot reloading.
	cfg, err := config.Load(p.workspace)
	if err == nil {
		p.cfg = cfg
	}

	// Read prompt template.
	promptPath := filepath.Join(p.workspace, "prompts", a.Role+".md")
	promptData, err := os.ReadFile(promptPath)
	if err != nil {
		return fmt.Errorf("cannot read prompt %s: %w", promptPath, err)
	}

	logDir := filepath.Join(p.workspace, "logs")
	os.MkdirAll(logDir, 0755)

	worktree := topic.Worktree
	if !filepath.IsAbs(worktree) {
		worktree = filepath.Join(p.workspace, worktree)
	}

	// Prepare prompt.
	prompt := string(promptData)
	prompt = strings.ReplaceAll(prompt, "{{TICKET_ID}}", ticket.ID)
	prompt = strings.ReplaceAll(prompt, "{{TOPIC_NAME}}", topic.Name)
	prompt = strings.ReplaceAll(prompt, "{{BRANCH}}", topic.Branch)

	// Claim ticket via peggy.
	ctx := context.Background()
	if err := p.store.UpdateStatus(ctx, ticket.ID, peggy.StatusInProgress, a.ID); err != nil {
		return fmt.Errorf("claim ticket: %w", err)
	}

	// Build sandbox command.
	sbCfg := sandbox.Config{
		Workspace: p.workspace,
		Workdir:   worktree,
		GPU:       p.cfg.Sandbox.GPU,
		Network:   p.cfg.Sandbox.Network,
		ExtraRO:   p.cfg.Sandbox.ExtraRO,
		ExtraRW:   p.cfg.Sandbox.ExtraRW,
		Env: map[string]string{
			"BR_ACTOR": a.ID,
		},
	}

	claudeArgs := []string{
		"claude",
		"--dangerously-skip-permissions",
		"-p",
		"--output-format", "stream-json",
		"--append-system-prompt", prompt,
		fmt.Sprintf("Read CLAUDE.md, then run 'cats peggy ticket show %s' and work on the ticket.", ticket.ID),
	}

	cmd := sandbox.Command(sbCfg, claudeArgs...)

	err = a.StartCmd(cmd, ticket.ID, topic.Name, topic.Branch, logDir)
	if err != nil {
		// Release ticket on start failure.
		p.store.UpdateStatus(ctx, ticket.ID, peggy.StatusOpen, a.ID)
		return err
	}

	// Watch for completion in background.
	go p.watchAgent(a, ticket)

	return nil
}

func (p *Pool) watchAgent(a *agent.Agent, ticket peggy.Ticket) {
	err := a.Wait()
	ctx := context.Background()

	if err != nil {
		p.mu.Lock()
		p.retries[ticket.ID]++
		retryCount := p.retries[ticket.ID]
		maxRetries := p.cfg.Pool.MaxRetries
		p.mu.Unlock()

		// Release ticket back to open.
		p.store.UpdateStatus(ctx, ticket.ID, peggy.StatusOpen, a.ID)

		if retryCount >= maxRetries {
			a.State = agent.Failed
		} else {
			a.Reset()
		}
	} else {
		a.Reset()
	}
}

// Reconcile checks all working agents and releases tickets for dead processes.
func (p *Pool) Reconcile() {
	p.mu.Lock()
	var dead []*agent.Agent
	for _, a := range p.agents {
		if a.State == agent.Working && !a.IsAlive() {
			dead = append(dead, a)
		}
	}
	p.mu.Unlock()

	ctx := context.Background()
	for _, a := range dead {
		if a.TicketID != "" {
			p.store.UpdateStatus(ctx, a.TicketID, peggy.StatusOpen, a.ID)
		}
		a.Reset()
	}
}
