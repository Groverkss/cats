package peggy

import "context"

// Peggy wraps a TicketStore with convenience methods.
type Peggy struct {
	Store TicketStore
}

func New(store TicketStore) *Peggy {
	return &Peggy{Store: store}
}

// ReadyForRole returns tickets assignable to the given role.
func (p *Peggy) ReadyForRole(ctx context.Context, role string) ([]Ticket, error) {
	return p.Store.Ready(ctx, role)
}

// StaleTickets returns in_progress tickets (for startup recovery).
func (p *Peggy) StaleTickets(ctx context.Context) ([]Ticket, error) {
	status := StatusInProgress
	return p.Store.List(ctx, Filter{Status: &status})
}
