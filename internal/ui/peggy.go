package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kunwar/cats/internal/peggy"
)

// Styles specific to peggy TUI.
var (
	peggyTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("213")).
			Padding(0, 1)

	listPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	detailPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("240")).
				Padding(0, 1)

	headerBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	p0Style = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	p1Style = lipgloss.NewStyle()
)

// peggyMode tracks what view we're in.
type peggyMode int

const (
	peggyModeTickets peggyMode = iota
	peggyModeTopics
	peggyModeStatusChange
)

// peggyFocus tracks which panel has focus.
type peggyFocus int

const (
	focusList peggyFocus = iota
	focusDetail
)

// detailView tracks which detail view mode.
type detailView int

const (
	detailInfo detailView = iota
	detailChildren
	detailRaw
)

// Filter presets cycle order.
var filterCycle = []peggy.Status{
	"", // all
	peggy.StatusOpen,
	peggy.StatusInProgress,
	peggy.StatusBlocked,
	peggy.StatusCompleted,
	peggy.StatusCancelled,
}

type PeggyModel struct {
	store     peggy.TicketStore
	workspace string
	width     int
	height    int

	// Mode and focus.
	mode  peggyMode
	focus peggyFocus

	// Ticket list.
	tickets   []peggy.Ticket
	selected  int
	filterIdx int // index into filterCycle
	scrollOff int // scroll offset for list

	// Detail.
	detail     *peggy.TicketDetail
	detailView detailView

	// Topic list.
	topics        []peggy.Topic
	topicSelected int

	// Status change overlay.
	statusOpts     []peggy.Status
	statusSelected int
}

func NewPeggy(store peggy.TicketStore, workspace string) PeggyModel {
	return PeggyModel{
		store:     store,
		workspace: workspace,
	}
}

// Messages.
type peggyTicketsMsg struct {
	tickets []peggy.Ticket
	topics  []peggy.Topic
}
type peggyDetailMsg struct {
	detail *peggy.TicketDetail
}
type peggyRefreshMsg struct{}

func (m PeggyModel) Init() tea.Cmd {
	return tea.Batch(
		m.fetchTickets(),
		peggyRefreshCmd(),
	)
}

func peggyRefreshCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(_ time.Time) tea.Msg {
		return peggyRefreshMsg{}
	})
}

func (m PeggyModel) fetchTickets() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		var filter peggy.Filter
		if m.filterIdx > 0 && m.filterIdx < len(filterCycle) {
			s := filterCycle[m.filterIdx]
			filter.Status = &s
		}
		tickets, _ := m.store.List(ctx, filter)
		topics, _ := m.store.ListTopics(ctx)
		return peggyTicketsMsg{tickets: tickets, topics: topics}
	}
}

func (m PeggyModel) fetchDetail(id string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		detail, _ := m.store.Get(ctx, id)
		return peggyDetailMsg{detail: detail}
	}
}

func (m PeggyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case peggyTicketsMsg:
		m.tickets = msg.tickets
		m.topics = msg.topics
		// Clamp selection.
		if m.selected >= len(m.tickets) {
			m.selected = max(0, len(m.tickets)-1)
		}
		// Auto-fetch detail for selected ticket.
		if len(m.tickets) > 0 && m.selected < len(m.tickets) {
			return m, m.fetchDetail(m.tickets[m.selected].ID)
		}
		m.detail = nil
		return m, nil

	case peggyDetailMsg:
		m.detail = msg.detail
		return m, nil

	case peggyRefreshMsg:
		return m, tea.Batch(m.fetchTickets(), peggyRefreshCmd())
	}
	return m, nil
}

func (m PeggyModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == peggyModeStatusChange {
		return m.handleStatusKey(msg)
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "j", "down":
		if m.mode == peggyModeTopics {
			if m.topicSelected < len(m.topics)-1 {
				m.topicSelected++
			}
		} else {
			if m.selected < len(m.tickets)-1 {
				m.selected++
				m.clampScroll()
				return m, m.fetchDetail(m.tickets[m.selected].ID)
			}
		}
		return m, nil

	case "k", "up":
		if m.mode == peggyModeTopics {
			if m.topicSelected > 0 {
				m.topicSelected--
			}
		} else {
			if m.selected > 0 {
				m.selected--
				m.clampScroll()
				return m, m.fetchDetail(m.tickets[m.selected].ID)
			}
		}
		return m, nil

	case "tab":
		if m.focus == focusList {
			m.focus = focusDetail
		} else {
			m.focus = focusList
		}
		return m, nil

	case "f":
		m.filterIdx = (m.filterIdx + 1) % len(filterCycle)
		m.selected = 0
		m.scrollOff = 0
		return m, m.fetchTickets()

	case "t":
		if m.mode == peggyModeTopics {
			m.mode = peggyModeTickets
		} else {
			m.mode = peggyModeTopics
		}
		return m, nil

	case "s":
		if m.mode == peggyModeTickets && len(m.tickets) > 0 {
			m.mode = peggyModeStatusChange
			m.statusOpts = []peggy.Status{
				peggy.StatusOpen,
				peggy.StatusInProgress,
				peggy.StatusBlocked,
				peggy.StatusCompleted,
				peggy.StatusCancelled,
			}
			m.statusSelected = 0
		}
		return m, nil

	case "r":
		// Reopen: set closed/cancelled ticket to open.
		if len(m.tickets) > 0 && m.selected < len(m.tickets) {
			t := m.tickets[m.selected]
			if t.Status == peggy.StatusCompleted || t.Status == peggy.StatusCancelled {
				ctx := context.Background()
				m.store.UpdateStatus(ctx, t.ID, peggy.StatusOpen, "user")
				return m, m.fetchTickets()
			}
		}
		return m, nil

	case "1":
		m.detailView = detailInfo
		return m, nil
	case "2":
		m.detailView = detailChildren
		return m, nil
	case "3":
		m.detailView = detailRaw
		return m, nil
	}

	return m, nil
}

func (m PeggyModel) handleStatusKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "escape":
		m.mode = peggyModeTickets
		return m, nil

	case "j", "down":
		if m.statusSelected < len(m.statusOpts)-1 {
			m.statusSelected++
		}
		return m, nil

	case "k", "up":
		if m.statusSelected > 0 {
			m.statusSelected--
		}
		return m, nil

	case "enter":
		if m.selected < len(m.tickets) {
			t := m.tickets[m.selected]
			newStatus := m.statusOpts[m.statusSelected]
			ctx := context.Background()
			m.store.UpdateStatus(ctx, t.ID, newStatus, "user")
			m.mode = peggyModeTickets
			return m, m.fetchTickets()
		}
		return m, nil
	}
	return m, nil
}

func (m *PeggyModel) clampScroll() {
	visibleLines := m.listHeight()
	if visibleLines <= 0 {
		visibleLines = 10
	}
	if m.selected < m.scrollOff {
		m.scrollOff = m.selected
	}
	if m.selected >= m.scrollOff+visibleLines {
		m.scrollOff = m.selected - visibleLines + 1
	}
}

func (m PeggyModel) listHeight() int {
	h := m.height - 6
	if h < 5 {
		h = 5
	}
	return h
}

func (m PeggyModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	title := peggyTitleStyle.Render(" peggy ")

	// Header bar with active filter.
	filterLabel := "all"
	if m.filterIdx > 0 && m.filterIdx < len(filterCycle) {
		filterLabel = string(filterCycle[m.filterIdx])
	}
	header := headerBarStyle.Render(fmt.Sprintf(" filter: [%s]  tickets: %d  topics: %d",
		filterLabel, len(m.tickets), len(m.topics)))

	listWidth := 38
	detailWidth := m.width - listWidth - 4
	if detailWidth < 20 {
		detailWidth = 20
	}
	panelHeight := m.height - 6
	if panelHeight < 5 {
		panelHeight = 5
	}

	var leftPanel, rightPanel string

	if m.mode == peggyModeTopics {
		leftPanel = m.renderTopicList()
		rightPanel = m.renderTopicDetail()
	} else {
		leftPanel = m.renderTicketList()
		rightPanel = m.renderDetailPanel()
	}

	// Status change overlay replaces detail panel.
	if m.mode == peggyModeStatusChange {
		rightPanel = m.renderStatusOverlay()
	}

	listBorderColor := lipgloss.Color("240")
	detailBorderColor := lipgloss.Color("240")
	if m.focus == focusList {
		listBorderColor = lipgloss.Color("212")
	} else {
		detailBorderColor = lipgloss.Color("212")
	}

	leftRendered := listPanelStyle.
		Copy().
		BorderForeground(listBorderColor).
		Width(listWidth).
		Height(panelHeight).
		Render(leftPanel)

	rightRendered := detailPanelStyle.
		Copy().
		BorderForeground(detailBorderColor).
		Width(detailWidth).
		Height(panelHeight).
		Render(rightPanel)

	main := lipgloss.JoinHorizontal(lipgloss.Top, leftRendered, rightRendered)
	help := m.renderHelp()

	return lipgloss.JoinVertical(lipgloss.Left,
		title,
		header,
		main,
		help,
	)
}

func (m PeggyModel) renderTicketList() string {
	var b strings.Builder

	modeLabel := "Tickets"
	b.WriteString(modeLabel + "\n")
	b.WriteString(strings.Repeat("-", 34) + "\n")

	if len(m.tickets) == 0 {
		b.WriteString(dimStyle.Render("  (no tickets)"))
		return b.String()
	}

	visibleLines := m.listHeight() - 2 // header lines
	endIdx := m.scrollOff + visibleLines
	if endIdx > len(m.tickets) {
		endIdx = len(m.tickets)
	}

	for i := m.scrollOff; i < endIdx; i++ {
		t := m.tickets[i]
		prefix := "  "
		if i == m.selected {
			prefix = selectedStyle.Render("| ")
		}

		icon := statusIcon(t.Status)
		prio := priorityStr(t.Priority)

		// Truncate title.
		title := t.Title
		maxTitleLen := 18
		if len(title) > maxTitleLen {
			title = title[:maxTitleLen-1] + "~"
		}

		line := fmt.Sprintf("%s%s %s %-5s %s", prefix, icon, prio, t.Type, title)
		b.WriteString(line + "\n")
	}

	// Scroll indicator.
	if len(m.tickets) > visibleLines {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  (%d/%d)", m.selected+1, len(m.tickets))))
	}

	return b.String()
}

func (m PeggyModel) renderDetailPanel() string {
	if m.detail == nil {
		return dimStyle.Render("Select a ticket to view details.")
	}

	switch m.detailView {
	case detailChildren:
		return m.renderChildrenView()
	case detailRaw:
		return m.renderRawView()
	default:
		return m.renderInfoView()
	}
}

func (m PeggyModel) renderInfoView() string {
	d := m.detail
	var b strings.Builder

	b.WriteString(fmt.Sprintf("%s\n", d.Title))
	b.WriteString(strings.Repeat("-", min(len(d.Title), 40)) + "\n")
	b.WriteString(fmt.Sprintf("ID:       %s\n", d.ID))
	b.WriteString(fmt.Sprintf("Status:   %s %s\n", statusIcon(d.Status), d.Status))
	b.WriteString(fmt.Sprintf("Priority: %s\n", priorityStr(d.Priority)))
	b.WriteString(fmt.Sprintf("Type:     %s\n", d.Type))

	if d.Assignee != "" {
		b.WriteString(fmt.Sprintf("Assignee: %s\n", d.Assignee))
	}
	if d.ParentID != "" {
		b.WriteString(fmt.Sprintf("Parent:   %s\n", d.ParentID))
	}

	if d.Description != "" {
		b.WriteString("\nDescription:\n")
		// Wrap description lines.
		for _, line := range strings.Split(d.Description, "\n") {
			b.WriteString("  " + line + "\n")
		}
	}

	if len(d.AcceptanceCriteria) > 0 {
		b.WriteString("\nAcceptance Criteria:\n")
		for _, c := range d.AcceptanceCriteria {
			b.WriteString("  [ ] " + c + "\n")
		}
	}

	b.WriteString(dimStyle.Render("\n[1]info [2]children [3]raw"))

	return b.String()
}

func (m PeggyModel) renderChildrenView() string {
	d := m.detail
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Children of %s\n", d.ID))
	b.WriteString(strings.Repeat("-", 30) + "\n")

	if len(d.Children) == 0 {
		b.WriteString(dimStyle.Render("  (no children)"))
	} else {
		for _, childID := range d.Children {
			b.WriteString(fmt.Sprintf("  %s\n", childID))
		}
	}

	b.WriteString(dimStyle.Render("\n[1]info [2]children [3]raw"))
	return b.String()
}

func (m PeggyModel) renderRawView() string {
	if m.detail == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("ID:       %s\n", m.detail.ID))
	b.WriteString(fmt.Sprintf("Title:    %s\n", m.detail.Title))
	b.WriteString(fmt.Sprintf("Status:   %s\n", m.detail.Status))
	b.WriteString(fmt.Sprintf("Type:     %s\n", m.detail.Type))
	b.WriteString(fmt.Sprintf("Priority: %d\n", m.detail.Priority))
	b.WriteString(fmt.Sprintf("Assignee: %s\n", m.detail.Assignee))
	b.WriteString(fmt.Sprintf("Parent:   %s\n", m.detail.ParentID))
	if m.detail.Description != "" {
		b.WriteString(fmt.Sprintf("Desc:     %s\n", m.detail.Description))
	}
	b.WriteString(dimStyle.Render("\n[1]info [2]children [3]raw"))
	return b.String()
}

func (m PeggyModel) renderTopicList() string {
	var b strings.Builder

	b.WriteString("Topics\n")
	b.WriteString(strings.Repeat("-", 34) + "\n")

	if len(m.topics) == 0 {
		b.WriteString(dimStyle.Render("  (no topics)"))
		return b.String()
	}

	for i, t := range m.topics {
		prefix := "  "
		if i == m.topicSelected {
			prefix = selectedStyle.Render("| ")
		}

		statusIcon := "o"
		style := dimStyle
		if t.Status == "open" {
			statusIcon = "*"
			style = activeStyle
		}

		b.WriteString(fmt.Sprintf("%s%s %s\n", prefix, style.Render(statusIcon), t.Name))
	}

	return b.String()
}

func (m PeggyModel) renderTopicDetail() string {
	if len(m.topics) == 0 || m.topicSelected >= len(m.topics) {
		return dimStyle.Render("Select a topic to view details.")
	}

	t := m.topics[m.topicSelected]
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Topic: %s\n", t.Name))
	b.WriteString(strings.Repeat("-", 30) + "\n")
	b.WriteString(fmt.Sprintf("Status:   %s\n", t.Status))
	b.WriteString(fmt.Sprintf("Repo:     %s\n", t.Repo))
	b.WriteString(fmt.Sprintf("Branch:   %s\n", t.Branch))
	b.WriteString(fmt.Sprintf("Worktree: %s\n", t.Worktree))
	b.WriteString(fmt.Sprintf("Epic:     %s\n", t.EpicID))

	return b.String()
}

func (m PeggyModel) renderStatusOverlay() string {
	var b strings.Builder

	b.WriteString("Change status:\n")
	b.WriteString(strings.Repeat("-", 20) + "\n")

	for i, s := range m.statusOpts {
		prefix := "  "
		if i == m.statusSelected {
			prefix = selectedStyle.Render("| ")
		}
		b.WriteString(fmt.Sprintf("%s%s %s\n", prefix, statusIcon(s), s))
	}

	b.WriteString(dimStyle.Render("\n[enter] select  [esc] cancel"))
	return b.String()
}

func (m PeggyModel) renderHelp() string {
	if m.mode == peggyModeStatusChange {
		return helpStyle.Render(" [enter] select  [esc] cancel  [j/k] navigate")
	}
	if m.mode == peggyModeTopics {
		return helpStyle.Render(" [t]ickets  [j/k] navigate  [q]uit")
	}
	return helpStyle.Render(" [f]ilter  [s]tatus  [r]eopen  [t]opics  [tab] focus  [1/2/3] detail view  [j/k] navigate  [q]uit")
}

// --- helpers ---

func statusIcon(s peggy.Status) string {
	switch s {
	case peggy.StatusOpen:
		return "o"
	case peggy.StatusInProgress:
		return activeStyle.Render("*")
	case peggy.StatusBlocked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Render("!")
	case peggy.StatusCompleted:
		return dimStyle.Render("v")
	case peggy.StatusCancelled:
		return dimStyle.Render("x")
	default:
		return "?"
	}
}

func priorityStr(p int) string {
	label := fmt.Sprintf("P%d", p)
	if p == 0 {
		return p0Style.Render(label)
	}
	if p <= 1 {
		return p1Style.Render(label)
	}
	return dimStyle.Render(label)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
