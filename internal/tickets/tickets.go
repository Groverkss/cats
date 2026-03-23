package tickets

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Ticket struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Assignee string `json:"assignee"`
	Type     string `json:"type"`
	Priority int    `json:"priority"`
	ParentID string `json:"parent_id"`
}

type TopicMeta struct {
	Name     string `json:"name"`
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
	EpicID   string `json:"epic_id"`
	Status   string `json:"status"`
}

// Ready returns tickets that are ready for the given role.
func Ready(role string) ([]Ticket, error) {
	out, err := exec.Command("br", "ready", "--assignee="+role, "--format=json").Output()
	if err != nil {
		// br ready with no results exits non-zero sometimes.
		// Try plain text fallback.
		return readyFallback(role)
	}

	var tickets []Ticket
	if err := json.Unmarshal(out, &tickets); err != nil {
		return readyFallback(role)
	}
	return tickets, nil
}

// readyFallback parses br ready text output when JSON isn't available.
func readyFallback(role string) ([]Ticket, error) {
	out, err := exec.Command("br", "ready", "--assignee="+role).CombinedOutput()
	if err != nil {
		return nil, nil // No ready tickets.
	}

	var tickets []Ticket
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !isTicketLine(line) {
			continue
		}
		// Parse minimal info from text output: "ID  Title  [status]"
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			tickets = append(tickets, Ticket{
				ID:       parts[0],
				Title:    strings.Join(parts[1:], " "),
				Assignee: role,
			})
		}
	}
	return tickets, nil
}

func isTicketLine(line string) bool {
	// Ticket IDs from br typically start with the project prefix.
	return len(line) > 0 && !strings.HasPrefix(line, "=") && !strings.HasPrefix(line, "-")
}

// ListByStatus returns tickets with the given status.
func ListByStatus(status string) ([]Ticket, error) {
	out, err := exec.Command("br", "list", "--status="+status, "--format=json").CombinedOutput()
	if err != nil {
		return nil, nil
	}
	var tickets []Ticket
	json.Unmarshal(out, &tickets)
	return tickets, nil
}

// UpdateStatus changes a ticket's status.
func UpdateStatus(id, status, actor string) error {
	args := []string{"update", id, "--status=" + status}
	if actor != "" {
		args = append(args, "--actor="+actor)
	}
	return exec.Command("br", args...).Run()
}

// ResolveTopicForTicket finds the topic metadata for a ticket by looking
// at its parent epic and matching against .topics/ files.
func ResolveTopicForTicket(workspace string, ticket Ticket) (*TopicMeta, error) {
	topicsDir := filepath.Join(workspace, ".topics")

	entries, err := os.ReadDir(topicsDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read .topics/: %w", err)
	}

	// If ticket has a parent, match by epic ID.
	if ticket.ParentID != "" {
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			meta, err := loadTopicMeta(filepath.Join(topicsDir, e.Name()))
			if err != nil {
				continue
			}
			if meta.EpicID == ticket.ParentID && meta.Status == "open" {
				return meta, nil
			}
		}
	}

	// Fallback: try to get parent from br show.
	parentID, err := getTicketParent(ticket.ID)
	if err == nil && parentID != "" {
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			meta, err := loadTopicMeta(filepath.Join(topicsDir, e.Name()))
			if err != nil {
				continue
			}
			if meta.EpicID == parentID && meta.Status == "open" {
				return meta, nil
			}
		}
	}

	return nil, fmt.Errorf("no topic found for ticket %s", ticket.ID)
}

func getTicketParent(id string) (string, error) {
	out, err := exec.Command("br", "show", id, "--format=json").Output()
	if err != nil {
		return "", err
	}
	var data map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		return "", err
	}
	if parent, ok := data["parent_id"].(string); ok {
		return parent, nil
	}
	return "", nil
}

func loadTopicMeta(path string) (*TopicMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var meta TopicMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// StaleTickets returns in_progress tickets (for startup recovery).
func StaleTickets() ([]Ticket, error) {
	return ListByStatus("in_progress")
}
