package pool

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/kunwar/cats/internal/agent"
	"github.com/kunwar/cats/internal/config"
	"github.com/kunwar/cats/internal/tickets"
)

type Pool struct {
	mu       sync.Mutex
	agents   []*agent.Agent
	counters map[string]int // role -> next ID number
	cfg      config.Config
	workspace string
}

func New(workspace string, cfg config.Config) *Pool {
	return &Pool{
		counters:  make(map[string]int),
		cfg:       cfg,
		workspace: workspace,
	}
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

// IdleAgents returns agents in idle state for a given role.
func (p *Pool) IdleAgents(role string) []*agent.Agent {
	p.mu.Lock()
	defer p.mu.Unlock()

	var idle []*agent.Agent
	for _, a := range p.agents {
		if a.Role == role && a.State == agent.Idle {
			idle = append(idle, a)
		}
	}
	return idle
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
func (p *Pool) AssignTicket(a *agent.Agent, ticket tickets.Ticket, topic *tickets.TopicMeta) error {
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

	sandboxPath := filepath.Join(p.workspace, "tools", "sandbox.sh")
	logDir := filepath.Join(p.workspace, "logs")
	os.MkdirAll(logDir, 0755)

	// Worktree path is absolute in topic metadata.
	worktree := topic.Worktree
	if !filepath.IsAbs(worktree) {
		worktree = filepath.Join(p.workspace, worktree)
	}

	err = a.Start(
		p.workspace,
		ticket.ID,
		topic.Name,
		topic.Branch,
		worktree,
		string(promptData),
		sandboxPath,
		logDir,
	)
	if err != nil {
		return err
	}

	// Watch for completion in background.
	go p.watchAgent(a, ticket)

	return nil
}

func (p *Pool) watchAgent(a *agent.Agent, ticket tickets.Ticket) {
	err := a.Wait()

	if err != nil {
		// Agent failed. Retry logic.
		a.State = agent.Failed
		// Mark ticket back to ready for retry.
		tickets.UpdateStatus(ticket.ID, "open", a.ID)
	} else {
		// Agent completed successfully.
		a.Reset()
	}
}
