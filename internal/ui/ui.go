package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kunwar/cats/internal/agent"
	"github.com/kunwar/cats/internal/pool"
	"github.com/kunwar/cats/internal/tickets"
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

// Modes for the TUI.
type mode int

const (
	modeNormal mode = iota
	modeSpawnRole
	modeStaleRecovery
)

type Model struct {
	pool      *pool.Pool
	workspace string
	selected  int
	mode      mode
	width     int
	height    int

	// Ticket stats (updated on tick).
	readyCoder    int
	readyReviewer int
	inProgress    int

	// Spawn role selection.
	spawnRoles    []string
	spawnSelected int

	// Stale ticket recovery.
	staleTickets  []tickets.Ticket
	staleSelected int
}

func New(p *pool.Pool, workspace string) Model {
	return Model{
		pool:       p,
		workspace:  workspace,
		spawnRoles: []string{"coder", "reviewer"},
	}
}

// Messages.
type tickMsg struct{}
type pollMsg struct {
	readyCoder    int
	readyReviewer int
	inProgress    int
}
type assignMsg struct{}
type staleMsg struct {
	tickets []tickets.Ticket
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
		checkStaleCmd,
	)
}

func checkStaleCmd() tea.Msg {
	stale, _ := tickets.StaleTickets()
	return staleMsg{tickets: stale}
}

func tickCmd() tea.Cmd {
	return tea.Tick(
		5*time.Second,
		func(_ time.Time) tea.Msg { return tickMsg{} },
	)
}

func (m Model) pollTickets() tea.Msg {
	coderTickets, _ := tickets.Ready("coder")
	reviewerTickets, _ := tickets.Ready("reviewer")
	inProgress, _ := tickets.ListByStatus("in_progress")

	return pollMsg{
		readyCoder:    len(coderTickets),
		readyReviewer: len(reviewerTickets),
		inProgress:    len(inProgress),
	}
}

func (m Model) tryAssign() tea.Msg {
	agents := m.pool.Agents()
	activeBranches := m.pool.ActiveBranches()

	for _, a := range agents {
		if a.State != agent.Idle {
			continue
		}

		readyTickets, err := tickets.Ready(a.Role)
		if err != nil || len(readyTickets) == 0 {
			continue
		}

		for _, ticket := range readyTickets {
			topic, err := tickets.ResolveTopicForTicket(m.workspace, ticket)
			if err != nil {
				continue
			}

			// One agent per branch.
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

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		return m, tea.Batch(
			func() tea.Msg { return m.pollTickets() },
			func() tea.Msg { return m.tryAssign() },
			tickCmd(),
		)

	case pollMsg:
		m.readyCoder = msg.readyCoder
		m.readyReviewer = msg.readyReviewer
		m.inProgress = msg.inProgress
		return m, nil

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

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeSpawnRole:
		return m.handleSpawnKey(msg)
	case modeStaleRecovery:
		return m.handleStaleKey(msg)
	default:
		return m.handleNormalKey(msg)
	}
}

func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

func (m Model) handleSpawnKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "escape":
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

func (m Model) handleStaleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "escape", "s":
		// Skip — leave all as-is.
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
		// Reset selected ticket to open.
		if m.staleSelected < len(m.staleTickets) {
			t := m.staleTickets[m.staleSelected]
			tickets.UpdateStatus(t.ID, "open", "moe")
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
		// Reset all to open.
		for _, t := range m.staleTickets {
			tickets.UpdateStatus(t.ID, "open", "moe")
		}
		m.staleTickets = nil
		m.mode = modeNormal
		return m, nil
	}
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	title := titleStyle.Render(" 🐈‍⬛ moe ")

	// Stale recovery mode shows a full-screen overlay.
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

	// Layout.
	outputWidth := m.width - 32 - 2 // sidebar width + gaps
	if outputWidth < 20 {
		outputWidth = 20
	}
	outputHeight := m.height - 6 // title + status + help + borders
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

func (m Model) renderStaleRecovery() string {
	var b strings.Builder
	b.WriteString("\n  Stale tickets found (in_progress with no active agent):\n\n")

	for i, t := range m.staleTickets {
		prefix := "  "
		if i == m.staleSelected {
			prefix = selectedStyle.Render("▸ ")
		}
		assignee := "(unassigned)"
		if t.Assignee != nil {
			assignee = *t.Assignee
		}
		b.WriteString(fmt.Sprintf("%s%s  %s  [%s]\n", prefix, t.ID, t.Title, assignee))
	}

	return b.String()
}

func (m Model) renderSidebar(agents []*agent.Agent) string {
	var b strings.Builder

	b.WriteString("Workers\n")
	b.WriteString("───────\n")

	if len(agents) == 0 {
		b.WriteString(idleStyle.Render("(none)"))
		b.WriteString("\n\nPress [l] to launch")
	}

	for i, a := range agents {
		prefix := "  "
		if i == m.selected {
			prefix = "▸ "
		}

		var stateStr string
		switch a.State {
		case agent.Working:
			stateStr = activeStyle.Render("● " + a.ID)
		case agent.Failed:
			stateStr = failedStyle.Render("✗ " + a.ID)
		default:
			stateStr = idleStyle.Render("○ " + a.ID)
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

	b.WriteString("\n───────\n")
	b.WriteString("Tickets\n")
	b.WriteString(fmt.Sprintf("  coder:    %d ready\n", m.readyCoder))
	b.WriteString(fmt.Sprintf("  reviewer: %d ready\n", m.readyReviewer))
	b.WriteString(fmt.Sprintf("  in progress: %d\n", m.inProgress))

	// Spawn overlay.
	if m.mode == modeSpawnRole {
		b.WriteString("\n───────\n")
		b.WriteString("Spawn role:\n")
		for i, role := range m.spawnRoles {
			if i == m.spawnSelected {
				b.WriteString(selectedStyle.Render("▸ " + role))
			} else {
				b.WriteString("  " + role)
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (m Model) renderOutput(agents []*agent.Agent) string {
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

	// Trim to fit screen.
	lines := strings.Split(output, "\n")
	maxLines := m.height - 10
	if maxLines < 5 {
		maxLines = 5
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}

	return header + "\n" + strings.Repeat("─", 40) + "\n" + strings.Join(lines, "\n")
}

func (m Model) renderStatusBar() string {
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
		fmt.Sprintf(" agents: %d (%d working, %d idle)  |  tickets: %d ready, %d in progress",
			len(agents), working, idle,
			m.readyCoder+m.readyReviewer, m.inProgress))
}

func (m Model) renderHelp() string {
	if m.mode == modeSpawnRole {
		return helpStyle.Render(" [enter] select  [esc] cancel")
	}
	return helpStyle.Render(" [l]aunch  [k]ill  [j/↓] next  [↑] prev  [q]uit")
}
