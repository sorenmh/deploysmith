package flux

import (
	"errors"
	"testing"
)

func status(applied, attempted, ready, message string) map[string]interface{} {
	return map[string]interface{}{
		"status": map[string]interface{}{
			"lastAppliedRevision":   applied,
			"lastAttemptedRevision": attempted,
			"conditions": []interface{}{
				map[string]interface{}{"type": "Reconciling", "status": "True"},
				map[string]interface{}{"type": "Ready", "status": ready, "message": message},
			},
		},
	}
}

func TestDecide(t *testing.T) {
	// "ours" is the deploy commit, "later" a descendant, "old" an ancestor
	contains := func(revision string) (bool, error) {
		switch revision {
		case "later":
			return true, nil
		case "broken":
			return false, errors.New("fetch failed")
		}
		return false, nil
	}

	tests := []struct {
		name   string
		obj    map[string]interface{}
		done   bool
		ready  bool
		reason string
	}{
		{"no status yet", map[string]interface{}{}, false, false, ""},
		{"previous revision still applied", status("master@sha1:old", "master@sha1:old", "True", "Applied revision: master@sha1:old"), false, false, ""},
		{"working on our commit", status("master@sha1:old", "master@sha1:ours", "Unknown", "Reconciliation in progress"), false, false, ""},
		{"our commit applied", status("master@sha1:ours", "master@sha1:ours", "True", "Applied"), true, true, ""},
		{"later commit applied", status("master@sha1:later", "master@sha1:later", "True", "Applied"), true, true, ""},
		{"older Flux revision format", status("master/ours", "master/ours", "True", "Applied"), true, true, ""},
		{"our commit failed", status("master@sha1:old", "master@sha1:ours", "False", "kustomize build failed"), true, false, "kustomize build failed"},
		{"later commit failed", status("master@sha1:old", "master@sha1:later", "False", "health check failed"), true, false, "health check failed"},
		{"unrelated failure before our commit", status("master@sha1:old", "master@sha1:old", "False", "dependency not ready"), false, false, ""},
		{"lookup error is retried", status("master@sha1:broken", "master@sha1:broken", "True", "Applied"), false, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, done := decide(parseStatus(tt.obj), revisionMatcher("ours", contains))
			if done != tt.done || result.Ready != tt.ready || (tt.reason != "" && result.Message != tt.reason) {
				t.Errorf("decide = %+v, done %v; want done %v, ready %v, message %q", result, done, tt.done, tt.ready, tt.reason)
			}
		})
	}
}
