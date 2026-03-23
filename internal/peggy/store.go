package peggy

import "context"

type Status string

const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked"
	StatusCompleted  Status = "completed"
	StatusCancelled  Status = "cancelled"
)

type Ticket struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   Status `json:"status"`
	Assignee string `json:"assignee,omitempty"`
	Type     string `json:"issue_type"`
	Priority int    `json:"priority"`
	ParentID string `json:"parent,omitempty"`
}

type TicketDetail struct {
	Ticket
	Description        string   `json:"description,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	Children           []string `json:"children,omitempty"`
}

type Topic struct {
	Name     string `json:"name"`
	Repo     string `json:"repo"`
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
	EpicID   string `json:"epic_id"`
	Status   string `json:"status"`
	Created  string `json:"created,omitempty"`
}

type Filter struct {
	Status   *Status
	Assignee *string
	Type     *string
	TopicID  *string
}

type CreateOpts struct {
	Title       string
	Description string
	Topic       string   // topic name — peggy resolves to epic ID internally
	Assignee    string
	Priority    int      // 0-4
	Type        string   // task, bug, epic, review
	DependsOn   []string // ticket IDs this ticket depends on
}

type TopicOpts struct {
	Name        string
	Repo        string
	Description string
}

// TicketStore abstracts the ticket backend.
type TicketStore interface {
	// Queries
	List(ctx context.Context, filter Filter) ([]Ticket, error)
	Get(ctx context.Context, id string) (*TicketDetail, error)
	Ready(ctx context.Context, role string) ([]Ticket, error)

	// Mutations
	Create(ctx context.Context, opts CreateOpts) (string, error)
	UpdateStatus(ctx context.Context, id string, status Status, actor string) error
	Close(ctx context.Context, id string, reason string) error

	// Dependencies
	AddDep(ctx context.Context, id string, dependsOn string) error
	RemoveDep(ctx context.Context, id string, dependsOn string) error
	ListDeps(ctx context.Context, id string) ([]string, error)
	Blocked(ctx context.Context) ([]Ticket, error)

	// Topics
	ListTopics(ctx context.Context) ([]Topic, error)
	GetTopic(ctx context.Context, name string) (*Topic, error)
	CreateTopic(ctx context.Context, opts TopicOpts) (*Topic, error)
	CloseTopic(ctx context.Context, name string) error

	// Resolution
	ResolveTopicForTicket(ctx context.Context, id string) (*Topic, error)
}
