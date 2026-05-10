package events

import "time"

type EventType string

const (
	EventDeploymentStarted   EventType = "deployment.started"
	EventDeploymentSucceeded EventType = "deployment.succeeded"
	EventDeploymentFailed    EventType = "deployment.failed"
	EventVersionPublished    EventType = "version.published"
)

type Event struct {
	Type         EventType `json:"type"`
	AppName      string    `json:"app_name"`
	AppID        string    `json:"app_id"`
	VersionID    string    `json:"version_id,omitempty"`
	Environment  string    `json:"environment,omitempty"`
	PolicyName   string    `json:"policy_name,omitempty"`
	DeploymentID string    `json:"deployment_id,omitempty"`
	CommitSHA    string    `json:"commit_sha,omitempty"`
	Error        string    `json:"error,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

// Publisher publishes events to a backend (NATS or in-memory).
type Publisher interface {
	Publish(event Event) error
	Close() error
}
