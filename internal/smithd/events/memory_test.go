package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryPublisher_Publish_StoresEvents(t *testing.T) {
	p := NewMemoryPublisher()

	event := Event{
		Type:      EventVersionPublished,
		AppName:   "my-app",
		AppID:     "app-1",
		VersionID: "v1.0.0",
		Timestamp: time.Now(),
	}

	err := p.Publish(event)
	require.NoError(t, err)

	events := p.Events()
	assert.Len(t, events, 1)
	assert.Equal(t, event.Type, events[0].Type)
	assert.Equal(t, event.AppName, events[0].AppName)
	assert.Equal(t, event.VersionID, events[0].VersionID)
}

func TestMemoryPublisher_Events_ReturnsCopy(t *testing.T) {
	p := NewMemoryPublisher()

	err := p.Publish(Event{
		Type:      EventDeploymentStarted,
		AppName:   "my-app",
		AppID:     "app-1",
		Timestamp: time.Now(),
	})
	require.NoError(t, err)

	// Get a snapshot and mutate it
	snapshot := p.Events()
	snapshot[0].AppName = "mutated"

	// Internal state should not be affected
	fresh := p.Events()
	assert.Equal(t, "my-app", fresh[0].AppName)
}

func TestMemoryPublisher_MultipleEvents_StoredInOrder(t *testing.T) {
	p := NewMemoryPublisher()

	eventTypes := []EventType{
		EventDeploymentStarted,
		EventDeploymentSucceeded,
		EventVersionPublished,
		EventDeploymentFailed,
	}

	for _, et := range eventTypes {
		err := p.Publish(Event{
			Type:      et,
			AppName:   "my-app",
			AppID:     "app-1",
			Timestamp: time.Now(),
		})
		require.NoError(t, err)
	}

	events := p.Events()
	assert.Len(t, events, len(eventTypes))
	for i, et := range eventTypes {
		assert.Equal(t, et, events[i].Type, "event at index %d should have type %s", i, et)
	}
}
