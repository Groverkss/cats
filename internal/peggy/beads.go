package peggy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// BeadsStore implements TicketStore by shelling out to the br CLI.
type BeadsStore struct {
	workspace string
	brPath    string
}

func NewBeadsStore(workspace string) (*BeadsStore, error) {
	brPath, err := exec.LookPath("br")
	if err != nil {
		return nil, fmt.Errorf("br not found in PATH: %w", err)
	}
	return &BeadsStore{workspace: workspace, brPath: brPath}, nil
}

func (s *BeadsStore) Init(ctx context.Context) error {
	_, err := s.br(ctx, "init", "--prefix=ws")
	return err
}

// br runs a br command with context timeout and returns stdout.
func (s *BeadsStore) br(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	args = append(args, "--no-auto-import", "--lock-timeout=500")
	cmd := exec.CommandContext(ctx, s.brPath, args...)
	cmd.Dir = s.workspace
	cmd.Stdin = nil
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("br %s: %s: %w", strings.Join(args, " "), stderr.String(), err)
	}
	return stdout.Bytes(), nil
}

// brJSON runs a br command with --format=json and parses the result.
func (s *BeadsStore) brJSON(ctx context.Context, result interface{}, args ...string) error {
	args = append(args, "--format=json")
	// Note: --no-auto-import is already added by br()
	out, err := s.br(ctx, args...)
	if err != nil {
		return err
	}
	if len(out) == 0 {
		return nil
	}
	return json.Unmarshal(out, result)
}

// brTicket is the raw shape br returns for tickets.
type brTicket struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Status    string  `json:"status"`
	Assignee  *string `json:"assignee"`
	IssueType string  `json:"issue_type"`
	Priority  int     `json:"priority"`
	Parent    *string `json:"parent"`
}

func toBrTicket(t brTicket) Ticket {
	assignee := ""
	if t.Assignee != nil {
		assignee = *t.Assignee
	}
	parentID := ""
	if t.Parent != nil {
		parentID = *t.Parent
	}
	return Ticket{
		ID:       t.ID,
		Title:    t.Title,
		Status:   Status(t.Status),
		Assignee: assignee,
		Type:     t.IssueType,
		Priority: t.Priority,
		ParentID: parentID,
	}
}

func (s *BeadsStore) List(ctx context.Context, filter Filter) ([]Ticket, error) {
	args := []string{"list"}
	if filter.Status != nil {
		args = append(args, "--status="+string(*filter.Status))
	}
	if filter.Assignee != nil {
		args = append(args, "--assignee="+*filter.Assignee)
	}

	var raw []brTicket
	if err := s.brJSON(ctx, &raw, args...); err != nil {
		return nil, err
	}

	tickets := make([]Ticket, len(raw))
	for i, t := range raw {
		tickets[i] = toBrTicket(t)
	}
	return tickets, nil
}

func (s *BeadsStore) Get(ctx context.Context, id string) (*TicketDetail, error) {
	out, err := s.br(ctx, "show", id, "--format=json")
	if err != nil {
		return nil, err
	}

	// br show returns an array even for single IDs.
	var raw []brTicket
	if err := json.Unmarshal(out, &raw); err != nil {
		var single brTicket
		if err2 := json.Unmarshal(out, &single); err2 != nil {
			return nil, fmt.Errorf("parse br show output: %w", err)
		}
		raw = []brTicket{single}
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("ticket %s not found", id)
	}

	t := raw[0]
	detail := &TicketDetail{
		Ticket: toBrTicket(t),
	}
	return detail, nil
}

func (s *BeadsStore) Ready(ctx context.Context, role string) ([]Ticket, error) {
	args := []string{"ready", "--assignee=" + role}
	var raw []brTicket
	if err := s.brJSON(ctx, &raw, args...); err != nil {
		return nil, err
	}

	tickets := make([]Ticket, len(raw))
	for i, t := range raw {
		tickets[i] = toBrTicket(t)
	}
	return tickets, nil
}

func (s *BeadsStore) Create(ctx context.Context, opts CreateOpts) (string, error) {
	args := []string{"create", "--title=" + opts.Title, "--silent"}
	if opts.Type != "" {
		args = append(args, "--type="+opts.Type)
	}
	if opts.Topic != "" {
		topic, err := s.GetTopic(ctx, opts.Topic)
		if err != nil {
			return "", fmt.Errorf("topic %q: %w", opts.Topic, err)
		}
		args = append(args, "--parent="+topic.EpicID)
	}
	if opts.Assignee != "" {
		args = append(args, "--assignee="+opts.Assignee)
	}
	if opts.Priority > 0 {
		args = append(args, fmt.Sprintf("--priority=%d", opts.Priority))
	}
	if opts.Description != "" {
		args = append(args, "--description="+opts.Description)
	}

	out, err := s.br(ctx, args...)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(out))

	// Add dependencies if specified.
	for _, dep := range opts.DependsOn {
		if err := s.AddDep(ctx, id, dep); err != nil {
			return id, fmt.Errorf("created %s but failed to add dep on %s: %w", id, dep, err)
		}
	}

	return id, nil
}

func (s *BeadsStore) UpdateStatus(ctx context.Context, id string, status Status, actor string) error {
	args := []string{"update", id, "--status=" + string(status)}
	if actor != "" {
		args = append(args, "--actor="+actor)
	}
	_, err := s.br(ctx, args...)
	return err
}

func (s *BeadsStore) Close(ctx context.Context, id string, reason string) error {
	args := []string{"close", id}
	if reason != "" {
		args = append(args, "--reason="+reason)
	}
	_, err := s.br(ctx, args...)
	return err
}

// --- Dependencies ---

func (s *BeadsStore) AddDep(ctx context.Context, id string, dependsOn string) error {
	_, err := s.br(ctx, "dep", "add", id, dependsOn)
	return err
}

func (s *BeadsStore) RemoveDep(ctx context.Context, id string, dependsOn string) error {
	_, err := s.br(ctx, "dep", "remove", id, dependsOn)
	return err
}

func (s *BeadsStore) ListDeps(ctx context.Context, id string) ([]string, error) {
	type brDep struct {
		DependsOnID string `json:"depends_on_id"`
		Type        string `json:"type"`
	}
	var deps []brDep
	if err := s.brJSON(ctx, &deps, "dep", "list", id); err != nil {
		return nil, err
	}
	var ids []string
	for _, d := range deps {
		// Only return explicit block dependencies, not parent-child.
		if d.Type == "blocks" {
			ids = append(ids, d.DependsOnID)
		}
	}
	return ids, nil
}

func (s *BeadsStore) Blocked(ctx context.Context) ([]Ticket, error) {
	var raw []brTicket
	if err := s.brJSON(ctx, &raw, "blocked"); err != nil {
		return nil, err
	}
	tickets := make([]Ticket, len(raw))
	for i, t := range raw {
		tickets[i] = toBrTicket(t)
	}
	return tickets, nil
}

// --- Topics ---

func (s *BeadsStore) topicsDir() string {
	return filepath.Join(s.workspace, ".topics")
}

func (s *BeadsStore) ListTopics(ctx context.Context) ([]Topic, error) {
	entries, err := os.ReadDir(s.topicsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read .topics/: %w", err)
	}

	var topics []Topic
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		t, err := s.loadTopic(filepath.Join(s.topicsDir(), e.Name()))
		if err != nil {
			continue
		}
		topics = append(topics, *t)
	}
	return topics, nil
}

func (s *BeadsStore) GetTopic(ctx context.Context, name string) (*Topic, error) {
	path := filepath.Join(s.topicsDir(), name+".json")
	return s.loadTopic(path)
}

func (s *BeadsStore) CreateTopic(ctx context.Context, opts TopicOpts) (*Topic, error) {
	topicFile := filepath.Join(s.topicsDir(), opts.Name+".json")
	if _, err := os.Stat(topicFile); err == nil {
		return nil, fmt.Errorf("topic %q already exists", opts.Name)
	}

	// Create beads epic.
	epicID, err := s.Create(ctx, CreateOpts{
		Title:       "Topic: " + opts.Name,
		Description: opts.Description,
		Type:        "epic",
		Priority:    1,
	})
	if err != nil {
		return nil, fmt.Errorf("create epic: %w", err)
	}

	branch := "topic/" + opts.Name
	worktree := filepath.Join(s.workspace, ".worktrees", opts.Name)

	// Create branch (ignore error if exists).
	exec.CommandContext(ctx, "git", "-C", opts.Repo, "branch", branch).Run()

	// Create worktree.
	cmd := exec.CommandContext(ctx, "git", "-C", opts.Repo, "worktree", "add", worktree, branch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git worktree add: %s: %w", stderr.String(), err)
	}

	topic := &Topic{
		Name:     opts.Name,
		Repo:     opts.Repo,
		Branch:   branch,
		Worktree: worktree,
		EpicID:   epicID,
		Status:   "open",
		Created:  time.Now().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(topic, "", "  ")
	if err != nil {
		return nil, err
	}

	os.MkdirAll(s.topicsDir(), 0755)
	if err := os.WriteFile(topicFile, data, 0644); err != nil {
		return nil, err
	}

	return topic, nil
}

func (s *BeadsStore) CloseTopic(ctx context.Context, name string) error {
	topic, err := s.GetTopic(ctx, name)
	if err != nil {
		return err
	}

	// Close the epic.
	s.Close(ctx, topic.EpicID, "Topic closed")

	// Update topic metadata.
	topic.Status = "closed"
	topicFile := filepath.Join(s.topicsDir(), name+".json")
	data, err := json.MarshalIndent(topic, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(topicFile, data, 0644)
}

func (s *BeadsStore) ResolveTopicForTicket(ctx context.Context, id string) (*Topic, error) {
	detail, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if detail.ParentID == "" {
		return nil, fmt.Errorf("ticket %s has no parent epic", id)
	}

	topics, err := s.ListTopics(ctx)
	if err != nil {
		return nil, err
	}

	// Match parent against topic epic IDs.
	if t := findTopicByEpicID(topics, detail.ParentID); t != nil {
		return t, nil
	}

	// Walk up one level — parent might itself be a child.
	parentDetail, err := s.Get(ctx, detail.ParentID)
	if err == nil && parentDetail.ParentID != "" {
		if t := findTopicByEpicID(topics, parentDetail.ParentID); t != nil {
			return t, nil
		}
	}

	return nil, fmt.Errorf("no topic found for ticket %s (parent: %s)", id, detail.ParentID)
}

func findTopicByEpicID(topics []Topic, epicID string) *Topic {
	for i := range topics {
		if topics[i].EpicID == epicID && topics[i].Status == "open" {
			return &topics[i]
		}
	}
	return nil
}

func (s *BeadsStore) loadTopic(path string) (*Topic, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var topic Topic
	if err := json.Unmarshal(data, &topic); err != nil {
		return nil, err
	}
	return &topic, nil
}
