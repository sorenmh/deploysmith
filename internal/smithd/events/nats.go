package events

import (
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const subjectPrefix = "deploysmith"

// NATSPublisher publishes events to NATS JetStream.
type NATSPublisher struct {
	nc *nats.Conn
	js jetstream.JetStream
}

func NewNATSPublisher(url string) (*NATSPublisher, error) {
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}
	return &NATSPublisher{nc: nc, js: js}, nil
}

func (p *NATSPublisher) Publish(event Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}
	subject := fmt.Sprintf("%s.%s", subjectPrefix, event.Type)
	// PublishAsync for fire-and-forget; errors surface via the error handler
	if _, err := p.js.PublishAsync(subject, data); err != nil {
		return fmt.Errorf("failed to publish event to NATS: %w", err)
	}
	return nil
}

func (p *NATSPublisher) Close() error {
	p.nc.Close()
	return nil
}
