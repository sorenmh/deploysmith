package events

import (
	"github.com/sorenmh/deploysmith/internal/shared/logging"
)

// New returns a NATSPublisher if natsURL is non-empty, otherwise a MemoryPublisher.
func New(natsURL string) Publisher {
	if natsURL != "" {
		p, err := NewNATSPublisher(natsURL)
		if err != nil {
			logging.LogWarn("Failed to connect to NATS, falling back to in-memory publisher: %v", err)
			return NewMemoryPublisher()
		}
		logging.LogInfo("Connected to NATS at %s", natsURL)
		return p
	}
	logging.LogInfo("NATS not configured, using in-memory event publisher")
	return NewMemoryPublisher()
}
