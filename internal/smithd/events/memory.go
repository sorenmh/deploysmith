package events

import "sync"

// MemoryPublisher holds published events in a slice (process-local, no replay).
type MemoryPublisher struct {
	mu     sync.Mutex
	events []Event
}

func NewMemoryPublisher() *MemoryPublisher {
	return &MemoryPublisher{}
}

func (p *MemoryPublisher) Publish(event Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, event)
	return nil
}

// Events returns a snapshot of all published events (for testing).
func (p *MemoryPublisher) Events() []Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Event, len(p.events))
	copy(out, p.events)
	return out
}

func (p *MemoryPublisher) Close() error { return nil }
