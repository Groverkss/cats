package tickets

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Ticket matches br's ReadyIssue / Issue JSON schema.
type Ticket struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Status    string  `json:"status"`
	Assignee  *string `json:"assignee"`
	IssueType string  `json:"issue_type"`
	Priority  int     `json:"priority"`
}

// IssueDetails matches br show --json output.
type IssueDetails struct {
	Ticket
	Parent *string `json:"parent"`
}

type TopicMeta struct {
	Name     string `json:"name"`
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
	EpicID   string `json:"epic_id"`
	Status   string `json:"status"`
}

// queryTickets runs a br command and parses the JSON output into tickets.
func queryTickets(args ...string) ([]Ticket, error) {
	args = append(args, "--format=json", "--no-auto-import")
	out, err := exec.Command("br", args...).Output()
	if err != nil {
		return nil, nil
	}
	var tickets []Ticket
	if err := json.Unmarshal(out, &tickets); err != nil {
		return nil, nil
	}
	return tickets, nil
}

// Ready returns tickets that are ready for the given role.
func Ready(role string) ([]Ticket, error) {
	return queryTickets("ready", "--assignee="+role)
}

// ListByStatus returns tickets with the given status.
func ListByStatus(status string) ([]Ticket, error) {
	return queryTickets("list", "--status="+status)
}

// Show returns full details for a ticket.
func Show(id string) (*IssueDetails, error) {
	out, err := exec.Command("br", "show", id, "--format=json", "--no-auto-import").Output()
	if err != nil {
		return nil, fmt.Errorf("br show %s failed: %w", id, err)
	}

	// br show returns an array even for single IDs.
	var details []IssueDetails
	if err := json.Unmarshal(out, &details); err != nil {
		// Try single object.
		var detail IssueDetails
		if err2 := json.Unmarshal(out, &detail); err2 != nil {
			return nil, fmt.Errorf("parse br show output: %w", err)
		}
		return &detail, nil
	}
	if len(details) == 0 {
		return nil, fmt.Errorf("ticket %s not found", id)
	}
	return &details[0], nil
}

// UpdateStatus changes a ticket's status.
func UpdateStatus(id, status, actor string) error {
	args := []string{"update", id, "--status=" + status}
	if actor != "" {
		args = append(args, "--actor="+actor)
	}
	return exec.Command("br", args...).Run()
}

// findTopicByEpicID searches loaded topic metadata for a matching epic.
func findTopicByEpicID(topics []*TopicMeta, epicID string) *TopicMeta {
	for _, meta := range topics {
		if meta.EpicID == epicID && meta.Status == "open" {
			return meta
		}
	}
	return nil
}

// LoadAllTopics loads all topic metadata files from .topics/.
func LoadAllTopics(workspace string) ([]*TopicMeta, error) {
	topicsDir := filepath.Join(workspace, ".topics")
	entries, err := os.ReadDir(topicsDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read .topics/: %w", err)
	}

	var topics []*TopicMeta
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		meta, err := loadTopicMeta(filepath.Join(topicsDir, e.Name()))
		if err != nil {
			continue
		}
		topics = append(topics, meta)
	}
	return topics, nil
}

// ResolveTopicForTicket finds the topic metadata for a ticket by looking
// at its parent epic and matching against loaded topics.
func ResolveTopicForTicket(topics []*TopicMeta, ticket Ticket) (*TopicMeta, error) {
	// Get the parent from br show.
	details, err := Show(ticket.ID)
	if err != nil {
		return nil, err
	}

	if details.Parent == nil || *details.Parent == "" {
		return nil, fmt.Errorf("ticket %s has no parent epic", ticket.ID)
	}

	// Match parent against topic epic IDs.
	if meta := findTopicByEpicID(topics, *details.Parent); meta != nil {
		return meta, nil
	}

	// The parent might itself be a child — walk up one level.
	parentDetails, err := Show(*details.Parent)
	if err == nil && parentDetails.Parent != nil && *parentDetails.Parent != "" {
		if meta := findTopicByEpicID(topics, *parentDetails.Parent); meta != nil {
			return meta, nil
		}
	}

	return nil, fmt.Errorf("no topic found for ticket %s (parent: %s)", ticket.ID, *details.Parent)
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
