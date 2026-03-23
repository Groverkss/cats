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
			Foreground(lipgloss.Color("245"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	// Priority styles.
	p0Style = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true) // red
	p1Style = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))            // orange
	p2Style = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))            // light gray
	p3Style = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))            // gray
	p4Style = lipgloss.NewStyle().Foreground(lipgloss.Color("236"))            // dark gray

	// Ticket type styles.
	typeTaskStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))  // blue
	typeBugStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // red
	typeReviewStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("213")) // pink
	typeEpicStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("141")) // purple

	// Assignee styles.
	assigneeCoderStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("117")) // light blue
	assigneeReviewerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("213")) // pink
	assigneeOtherStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252")) // white

	// Detail panel label style.
	labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Bold(true)

	// Topic styles.
	topicOpenStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true) // green
	topicClosedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))           // gray
	topicNameStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Bold(true) // white bold
	topicFieldStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("117"))           // light blue

	// Title in list.
	ticketTitleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
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

// Filter presets cycle order. "ready" and "blocked" are special — they use
// dedicated store methods instead of List with a status filter.
const filterReady peggy.Status = "_ready"

var filterCycle = []peggy.Status{
	"",          // all
	filterReady, // ready (open + unblocked)
	peggy.StatusOpen,
	peggy.StatusInProgress,
	peggy.StatusBlocked, // blocked (has unmet deps)
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
	tickets    []peggy.Ticket
	blockedIDs map[string]bool // ticket IDs that are blocked by deps
	selected   int
	filterIdx  int // index into filterCycle
	scrollOff  int // scroll offset for list

	// Detail.
	detail          *peggy.TicketDetail
	detailScrollOff int

	// Topic list.
	topics        []peggy.Topic
	topicSelected int
	topicTickets  []peggy.Ticket // tickets under selected topic

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
	tickets    []peggy.Ticket
	blockedIDs map[string]bool
	topics     []peggy.Topic
}
type peggyDetailMsg struct {
	detail *peggy.TicketDetail
}
type peggyTopicTicketsMsg struct {
	tickets []peggy.Ticket
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
		var tickets []peggy.Ticket

		currentFilter := peggy.Status("")
		if m.filterIdx > 0 && m.filterIdx < len(filterCycle) {
			currentFilter = filterCycle[m.filterIdx]
		}

		switch currentFilter {
		case peggy.StatusBlocked:
			tickets, _ = m.store.Blocked(ctx)
		case filterReady:
			// Combine ready tickets for all roles.
			coderReady, _ := m.store.Ready(ctx, "coder")
			reviewerReady, _ := m.store.Ready(ctx, "reviewer")
			tickets = append(coderReady, reviewerReady...)
		default:
			var filter peggy.Filter
			if currentFilter != "" {
				filter.Status = &currentFilter
			}
			tickets, _ = m.store.List(ctx, filter)
		}

		// Filter out epics — topics represent them.
		var filtered []peggy.Ticket
		for _, t := range tickets {
			if t.Type != "epic" {
				filtered = append(filtered, t)
			}
		}

		// Fetch blocked IDs so we can show correct icons.
		blockedIDs := make(map[string]bool)
		blocked, _ := m.store.Blocked(ctx)
		for _, b := range blocked {
			blockedIDs[b.ID] = true
		}

		topics, _ := m.store.ListTopics(ctx)
		return peggyTicketsMsg{tickets: filtered, blockedIDs: blockedIDs, topics: topics}
	}
}

func (m PeggyModel) fetchTopicTicketsForSelected() tea.Cmd {
	if m.topicSelected >= len(m.topics) {
		return nil
	}
	return m.fetchTopicTickets(m.topics[m.topicSelected].EpicID)
}

func (m PeggyModel) fetchTopicTickets(epicID string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		tickets, _ := m.store.ListChildren(ctx, epicID)
		return peggyTopicTicketsMsg{tickets: tickets}
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
		m.blockedIDs = msg.blockedIDs
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
		m.detailScrollOff = 0
		return m, nil

	case peggyTopicTicketsMsg:
		m.topicTickets = msg.tickets
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
		if m.focus == focusDetail {
			m.detailScrollOff++
			return m, nil
		}
		if m.mode == peggyModeTopics {
			if m.topicSelected < len(m.topics)-1 {
				m.topicSelected++
				return m, m.fetchTopicTicketsForSelected()
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
		if m.focus == focusDetail {
			if m.detailScrollOff > 0 {
				m.detailScrollOff--
			}
			return m, nil
		}
		if m.mode == peggyModeTopics {
			if m.topicSelected > 0 {
				m.topicSelected--
				return m, m.fetchTopicTicketsForSelected()
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
			return m, m.fetchTopicTicketsForSelected()
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

	}

	return m, nil
}

func (m PeggyModel) handleStatusKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
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
			if newStatus == peggy.StatusCompleted {
				m.store.Close(ctx, t.ID, "Completed via TUI")
			} else {
				m.store.UpdateStatus(ctx, t.ID, newStatus, "user")
			}
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
		f := filterCycle[m.filterIdx]
		if f == filterReady {
			filterLabel = "ready"
		} else {
			filterLabel = string(f)
		}
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
		rightPanel = m.scrollContent(m.renderTopicDetail())
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
		if m.blockedIDs[t.ID] {
			icon = statusIcon(peggy.StatusBlocked)
		}
		prio := priorityStr(t.Priority)
		typ := styledType(t.Type)

		// Truncate title.
		title := t.Title
		maxTitleLen := 18
		if len(title) > maxTitleLen {
			title = title[:maxTitleLen-1] + "~"
		}

		line := fmt.Sprintf("%s%s %s %s %s", prefix, icon, prio, typ, ticketTitleStyle.Render(title))
		b.WriteString(line + "\n")
	}

	// Scroll indicator.
	if len(m.tickets) > visibleLines {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  (%d/%d)", m.selected+1, len(m.tickets))))
	}

	return b.String()
}

func (m PeggyModel) scrollContent(content string) string {
	lines := strings.Split(content, "\n")
	visible := m.panelHeight()

	scrollOff := m.detailScrollOff
	maxScroll := len(lines) - visible
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scrollOff > maxScroll {
		scrollOff = maxScroll
	}

	start := scrollOff
	end := start + visible
	if end > len(lines) {
		end = len(lines)
	}

	result := strings.Join(lines[start:end], "\n")

	if len(lines) > visible {
		result += "\n" + dimStyle.Render(fmt.Sprintf("  [%d/%d lines]", start+1, len(lines)))
	}

	return result
}

func (m PeggyModel) panelHeight() int {
	h := m.height - 8
	if h < 5 {
		h = 5
	}
	return h
}

func (m PeggyModel) renderDetailPanel() string {
	if m.detail == nil {
		return dimStyle.Render("Select a ticket to view details.")
	}
	return m.scrollContent(m.renderInfoView())
}

func (m PeggyModel) renderInfoView() string {
	d := m.detail
	var b strings.Builder

	b.WriteString(topicNameStyle.Render(d.Title) + "\n")
	b.WriteString(dimStyle.Render(strings.Repeat("-", min(len(d.Title), 40))) + "\n")
	b.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("ID:"), d.ID))
	displayStatus := d.Status
	if m.blockedIDs[d.ID] {
		displayStatus = peggy.StatusBlocked
	}
	b.WriteString(fmt.Sprintf("%s %s %s\n", labelStyle.Render("Status:"), statusIcon(displayStatus), styledStatus(displayStatus)))
	b.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Priority:"), priorityStr(d.Priority)))
	b.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Type:"), styledType(d.Type)))

	if d.Assignee != "" {
		b.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Assignee:"), styledAssignee(d.Assignee)))
	}
	if d.ParentID != "" {
		b.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Parent:"), dimStyle.Render(d.ParentID)))
	}

	if d.Description != "" {
		b.WriteString("\n" + labelStyle.Render("Description:") + "\n")
		for _, line := range strings.Split(d.Description, "\n") {
			b.WriteString("  " + line + "\n")
		}
	}

	if len(d.AcceptanceCriteria) > 0 {
		b.WriteString("\n" + labelStyle.Render("Acceptance Criteria:") + "\n")
		for _, c := range d.AcceptanceCriteria {
			b.WriteString("  [ ] " + c + "\n")
		}
	}

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

		var icon, name string
		if t.Status == "open" {
			icon = topicOpenStyle.Render("*")
			name = topicNameStyle.Render(t.Name)
		} else {
			icon = topicClosedStyle.Render("o")
			name = topicClosedStyle.Render(t.Name)
		}

		b.WriteString(fmt.Sprintf("%s%s %s\n", prefix, icon, name))
	}

	return b.String()
}

func (m PeggyModel) renderTopicDetail() string {
	if len(m.topics) == 0 || m.topicSelected >= len(m.topics) {
		return dimStyle.Render("Select a topic to view details.")
	}

	t := m.topics[m.topicSelected]
	var b strings.Builder

	b.WriteString(topicNameStyle.Render("Topic: "+t.Name) + "\n")
	b.WriteString(dimStyle.Render(strings.Repeat("-", 30)) + "\n")

	statusStyle := topicOpenStyle
	if t.Status != "open" {
		statusStyle = topicClosedStyle
	}
	b.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Status:"), statusStyle.Render(t.Status)))
	b.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Repo:"), topicFieldStyle.Render(t.Repo)))
	b.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render("Branch:"), topicFieldStyle.Render(t.Branch)))

	// Show tickets under this topic.
	if len(m.topicTickets) > 0 {
		b.WriteString("\n" + labelStyle.Render("Tickets:") + "\n")
		for _, tk := range m.topicTickets {
			icon := statusIcon(tk.Status)
			if m.blockedIDs[tk.ID] {
				icon = statusIcon(peggy.StatusBlocked)
			}
			b.WriteString(fmt.Sprintf("  %s %s %s %s\n", icon, priorityStr(tk.Priority), styledType(tk.Type), ticketTitleStyle.Render(tk.Title)))
		}
	} else {
		b.WriteString("\n" + dimStyle.Render("No tickets.") + "\n")
	}

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
	return helpStyle.Render(" [f]ilter  [s]tatus  [t]opics  [tab] focus  [j/k] navigate  [q]uit")
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
	switch p {
	case 0:
		return p0Style.Render(label)
	case 1:
		return p1Style.Render(label)
	case 2:
		return p2Style.Render(label)
	case 3:
		return p3Style.Render(label)
	default:
		return p4Style.Render(label)
	}
}

func styledType(t string) string {
	padded := fmt.Sprintf("%-6s", t)
	switch t {
	case "task":
		return typeTaskStyle.Render(padded)
	case "bug":
		return typeBugStyle.Render(padded)
	case "review":
		return typeReviewStyle.Render(padded)
	case "epic":
		return typeEpicStyle.Render(padded)
	default:
		return padded
	}
}

func styledAssignee(a string) string {
	switch {
	case strings.HasPrefix(a, "coder"):
		return assigneeCoderStyle.Render(a)
	case strings.HasPrefix(a, "reviewer"):
		return assigneeReviewerStyle.Render(a)
	default:
		return assigneeOtherStyle.Render(a)
	}
}

func styledStatus(s peggy.Status) string {
	switch s {
	case peggy.StatusOpen:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render(string(s))
	case peggy.StatusInProgress:
		return activeStyle.Render(string(s))
	case peggy.StatusBlocked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Render(string(s))
	case peggy.StatusCompleted:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render(string(s))
	case peggy.StatusCancelled:
		return dimStyle.Render(string(s))
	default:
		return string(s)
	}
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
