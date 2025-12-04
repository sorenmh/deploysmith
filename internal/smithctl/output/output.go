package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"gopkg.in/yaml.v3"
)

// Format represents an output format
type Format string

const (
	// FormatTable is the table output format
	FormatTable Format = "table"
	// FormatJSON is the JSON output format
	FormatJSON Format = "json"
	// FormatYAML is the YAML output format
	FormatYAML Format = "yaml"
)

// PrintTable prints data in table format
func PrintTable(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	// Print headers
	fmt.Fprintln(w, strings.Join(headers, "\t"))

	// Print rows
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}

	w.Flush()
}

// PrintJSON prints data in JSON format
func PrintJSON(data interface{}) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// PrintYAML prints data in YAML format
func PrintYAML(data interface{}) error {
	encoder := yaml.NewEncoder(os.Stdout)
	encoder.SetIndent(2)
	return encoder.Encode(data)
}

// Print prints data in the specified format
func Print(format Format, data interface{}, tableFunc func()) error {
	switch format {
	case FormatJSON:
		return PrintJSON(data)
	case FormatYAML:
		return PrintYAML(data)
	case FormatTable:
		tableFunc()
		return nil
	default:
		return fmt.Errorf("unsupported output format: %s", format)
	}
}

// FormatTime formats a time for display
func FormatTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

// FormatTimeAgo formats a time as "X ago"
func FormatTimeAgo(t time.Time) string {
	duration := time.Since(t)

	if duration < time.Minute {
		return "just now"
	} else if duration < time.Hour {
		minutes := int(duration.Minutes())
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	} else if duration < 24*time.Hour {
		hours := int(duration.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	} else {
		days := int(duration.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// Success prints a success message
func Success(message string) {
	fmt.Printf("✓ %s\n", message)
}

// Error prints an error message
func Error(message string) {
	fmt.Fprintf(os.Stderr, "Error: %s\n", message)
}

// Info prints an info message
func Info(message string) {
	fmt.Println(message)
}

// Warn prints a warning message
func Warn(message string) {
	fmt.Printf("Warning: %s\n", message)
}
