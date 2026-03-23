package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kunwar/cats/internal/agent"
	"github.com/kunwar/cats/internal/peggy"
	"github.com/kunwar/cats/internal/pool"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("57")).
			Padding(0, 1)

	sidebarStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1).
			Width(30)

	outputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	activeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82")).
			Bold(true)

	idleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	failedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212")).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)

type mode int

const (
	modeNormal mode = iota
	modeSpawnRole
	modeStaleRecovery
)

type MoeModel struct {
	pool      *pool.Pool
	workspace string
	selected  int
	mode      mode
	width     int
	height    int

	// Spawn role selection.
	spawnRoles    []string
	spawnSelected int

	// Stale ticket recovery.
	staleTickets  []peggy.Ticket
	staleSelected int
}

func NewMoe(p *pool.Pool, workspace string) MoeModel {
	return MoeModel{
		pool:       p,
		workspace:  workspace,
		spawnRoles: []string{"coder", "reviewer"},
	}
}

// Messages.
type tickMsg struct{}
type refreshMsg struct{}
type assignMsg struct{}
type staleMsg struct {
	tickets []peggy.Ticket
}

func (m MoeModel) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
		refreshCmd(),
		checkStaleCmd(m.pool),
	)
}

func checkStaleCmd(p *pool.Pool) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		status := peggy.StatusInProgress
		stale, _ := p.Store().List(ctx, peggy.Filter{Status: &status})
		return staleMsg{tickets: stale}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(
		5*time.Second,
		func(_ time.Time) tea.Msg { return tickMsg{} },
	)
}

func refreshCmd() tea.Cmd {
	return tea.Tick(
		500*time.Millisecond,
		func(_ time.Time) tea.Msg { return refreshMsg{} },
	)
}

func (m MoeModel) tryAssign() tea.Msg {
	agents := m.pool.Agents()
	activeBranches := m.pool.ActiveBranches()
	ctx := context.Background()

	for _, a := range agents {
		if a.State != agent.Idle {
			continue
		}

		readyTickets, _ := m.pool.Store().Ready(ctx, a.Role)
		if len(readyTickets) == 0 {
			continue
		}

		for _, ticket := range readyTickets {
			topic, err := m.pool.Store().ResolveTopicForTicket(ctx, ticket.ID)
			if err != nil {
				continue
			}

			if activeBranches[topic.Branch] {
				continue
			}

			if err := m.pool.AssignTicket(a, ticket, topic); err != nil {
				continue
			}
			activeBranches[topic.Branch] = true
			break
		}
	}
	return assignMsg{}
}

func (m MoeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case refreshMsg:
		return m, refreshCmd()

	case tickMsg:
		// Reconcile dead agents, then assign.
		m.pool.Reconcile()
		return m, tea.Batch(
			func() tea.Msg { return m.tryAssign() },
			tickCmd(),
		)

	case assignMsg:
		return m, nil

	case staleMsg:
		if len(msg.tickets) > 0 {
			m.staleTickets = msg.tickets
			m.staleSelected = 0
			m.mode = modeStaleRecovery
		}
		return m, nil
	}
	return m, nil
}

func (m MoeModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeSpawnRole:
		return m.handleSpawnKey(msg)
	case modeStaleRecovery:
		return m.handleStaleKey(msg)
	default:
		return m.handleNormalKey(msg)
	}
}

func (m MoeModel) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "l":
		m.mode = modeSpawnRole
		m.spawnSelected = 0
		return m, nil

	case "k":
		agents := m.pool.Agents()
		if len(agents) > 0 && m.selected < len(agents) {
			a := agents[m.selected]
			m.pool.Remove(a.ID)
			if m.selected >= len(m.pool.Agents()) && m.selected > 0 {
				m.selected--
			}
		}
		return m, nil

	case "j", "down":
		agents := m.pool.Agents()
		if m.selected < len(agents)-1 {
			m.selected++
		}
		return m, nil

	case "up":
		if m.selected > 0 {
			m.selected--
		}
		return m, nil
	}
	return m, nil
}

func (m MoeModel) handleSpawnKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil

	case "j", "down":
		if m.spawnSelected < len(m.spawnRoles)-1 {
			m.spawnSelected++
		}
		return m, nil

	case "k", "up":
		if m.spawnSelected > 0 {
			m.spawnSelected--
		}
		return m, nil

	case "enter":
		role := m.spawnRoles[m.spawnSelected]
		m.pool.Spawn(role)
		m.mode = modeNormal
		m.selected = len(m.pool.Agents()) - 1
		return m, nil
	}
	return m, nil
}

func (m MoeModel) handleStaleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ctx := context.Background()
	switch msg.String() {
	case "esc", "s":
		m.mode = modeNormal
		m.staleTickets = nil
		return m, nil

	case "j", "down":
		if m.staleSelected < len(m.staleTickets)-1 {
			m.staleSelected++
		}
		return m, nil

	case "k", "up":
		if m.staleSelected > 0 {
			m.staleSelected--
		}
		return m, nil

	case "r":
		if m.staleSelected < len(m.staleTickets) {
			t := m.staleTickets[m.staleSelected]
			m.pool.Store().UpdateStatus(ctx, t.ID, peggy.StatusOpen, "moe")
			m.staleTickets = append(m.staleTickets[:m.staleSelected], m.staleTickets[m.staleSelected+1:]...)
			if m.staleSelected >= len(m.staleTickets) && m.staleSelected > 0 {
				m.staleSelected--
			}
			if len(m.staleTickets) == 0 {
				m.mode = modeNormal
			}
		}
		return m, nil

	case "a":
		for _, t := range m.staleTickets {
			m.pool.Store().UpdateStatus(ctx, t.ID, peggy.StatusOpen, "moe")
		}
		m.staleTickets = nil
		m.mode = modeNormal
		return m, nil
	}
	return m, nil
}

func (m MoeModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	title := titleStyle.Render(" moe ")

	if m.mode == modeStaleRecovery {
		return lipgloss.JoinVertical(lipgloss.Left,
			title,
			m.renderStaleRecovery(),
			helpStyle.Render(" [r]eset selected  [a]ll reset  [s]kip all  [j/k] navigate"),
		)
	}

	agents := m.pool.Agents()
	sidebar := m.renderSidebar(agents)
	output := m.renderOutput(agents)
	statusBar := m.renderStatusBar()
	help := m.renderHelp()

	outputWidth := m.width - 32 - 2
	if outputWidth < 20 {
		outputWidth = 20
	}
	outputHeight := m.height - 6
	if outputHeight < 5 {
		outputHeight = 5
	}

	sidebarRendered := sidebarStyle.Height(outputHeight).Render(sidebar)
	outputRendered := outputStyle.Width(outputWidth).Height(outputHeight).Render(output)

	main := lipgloss.JoinHorizontal(lipgloss.Top, sidebarRendered, outputRendered)

	return lipgloss.JoinVertical(lipgloss.Left,
		title,
		main,
		statusBar,
		help,
	)
}

func (m MoeModel) renderStaleRecovery() string {
	var b strings.Builder
	b.WriteString("\n  Stale tickets found (in_progress with no active agent):\n\n")

	for i, t := range m.staleTickets {
		prefix := "  "
		if i == m.staleSelected {
			prefix = selectedStyle.Render("| ")
		}
		assignee := "(unassigned)"
		if t.Assignee != "" {
			assignee = t.Assignee
		}
		b.WriteString(fmt.Sprintf("%s%s  %s  [%s]\n", prefix, t.ID, t.Title, assignee))
	}

	return b.String()
}

func (m MoeModel) renderSidebar(agents []*agent.Agent) string {
	var b strings.Builder

	b.WriteString("Workers\n")
	b.WriteString("-------\n")

	if len(agents) == 0 {
		b.WriteString(idleStyle.Render("(none)"))
		b.WriteString("\n\nPress [l] to launch")
	}

	for i, a := range agents {
		prefix := "  "
		if i == m.selected {
			prefix = "| "
		}

		var stateStr string
		switch a.State {
		case agent.Working:
			stateStr = activeStyle.Render("* " + a.ID)
		case agent.Failed:
			stateStr = failedStyle.Render("x " + a.ID)
		default:
			stateStr = idleStyle.Render("o " + a.ID)
		}

		if i == m.selected {
			stateStr = selectedStyle.Render(prefix) + stateStr
		} else {
			b.WriteString(prefix)
		}

		b.WriteString(stateStr)
		b.WriteString("\n")

		if a.State == agent.Working {
			b.WriteString(fmt.Sprintf("    %s\n", a.TicketID))
		}
	}

	// Spawn overlay.
	if m.mode == modeSpawnRole {
		b.WriteString("\n-------\n")
		b.WriteString("Spawn role:\n")
		for i, role := range m.spawnRoles {
			if i == m.spawnSelected {
				b.WriteString(selectedStyle.Render("| " + role))
			} else {
				b.WriteString("  " + role)
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (m MoeModel) renderOutput(agents []*agent.Agent) string {
	if len(agents) == 0 {
		return "No agents running.\n\nPress [l] to launch a new agent."
	}
	if m.selected >= len(agents) {
		return ""
	}

	a := agents[m.selected]
	header := fmt.Sprintf("Output: %s", a.ID)
	if a.State == agent.Working {
		header += fmt.Sprintf("  [%s] %s", a.TicketID, a.Topic)
	}

	output := string(a.Output())
	if output == "" {
		if a.State == agent.Idle {
			output = "(idle — waiting for ticket)"
		} else {
			output = "(starting...)"
		}
	}

	lines := strings.Split(output, "\n")
	maxLines := m.height - 10
	if maxLines < 5 {
		maxLines = 5
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	return header + "\n" + strings.Repeat("-", 40) + "\n" + strings.Join(lines, "\n")
}

func (m MoeModel) renderStatusBar() string {
	agents := m.pool.Agents()
	working := 0
	idle := 0
	for _, a := range agents {
		switch a.State {
		case agent.Working:
			working++
		case agent.Idle:
			idle++
		}
	}
	return statusBarStyle.Render(
		fmt.Sprintf(" agents: %d (%d working, %d idle)",
			len(agents), working, idle))
}

func (m MoeModel) renderHelp() string {
	if m.mode == modeSpawnRole {
		return helpStyle.Render(" [enter] select  [esc] cancel")
	}
	return helpStyle.Render(" [l]aunch  [k]ill  [j/k] navigate  [q]uit")
}
