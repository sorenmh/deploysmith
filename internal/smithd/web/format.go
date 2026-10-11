package web

import (
	"fmt"
	"time"
)

// ago formats a time relative to now, e.g. "5 min ago"
func ago(t any) string {
	var ts time.Time
	switch v := t.(type) {
	case time.Time:
		ts = v
	case *time.Time:
		if v == nil {
			return ""
		}
		ts = *v
	default:
		return ""
	}
	if ts.IsZero() {
		return ""
	}

	d := time.Since(ts)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "yesterday"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return ts.Format("2 Jan 2006")
	}
}

// shortSHA abbreviates a git commit SHA
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
