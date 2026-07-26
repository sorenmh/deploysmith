package cmd

import (
	"fmt"
	"os"

	"github.com/sorenmh/deploysmith/internal/smithctl/client"
	"github.com/sorenmh/deploysmith/internal/smithctl/output"
	"github.com/spf13/cobra"
)

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Export or import smithd's database",
	Long:  `Export or import smithd's database, e.g. to migrate from SQLite to PostgreSQL.`,
}

var exportDBCmd = &cobra.Command{
	Use:   "export [file]",
	Short: "Export smithd's database to a file or stdout",
	Long:  `Export all data from smithd's database as JSON. Writes to the given file, or to stdout if no file is given.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		data, err := c.ExportDB()
		if err != nil {
			return err
		}

		if len(args) == 1 {
			if err := os.WriteFile(args[0], data, 0644); err != nil {
				return fmt.Errorf("failed to write file: %w", err)
			}
			output.Success(fmt.Sprintf("Database exported to %s", args[0]))
			return nil
		}

		if _, err := os.Stdout.Write(data); err != nil {
			return fmt.Errorf("failed to write to stdout: %w", err)
		}

		return nil
	},
}

var importDBCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Import a database dump into smithd",
	Long:  `Import a database dump previously produced by 'db export'. The target database must be empty.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Validate configuration
		if err := ValidateConfig(); err != nil {
			return err
		}

		data, err := os.ReadFile(args[0])
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}

		// Create API client
		c := client.NewClient(GetSmithdURL(), GetSmithdAPIKey())

		if err := c.ImportDB(data); err != nil {
			return err
		}

		output.Success("Database imported")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(dbCmd)

	dbCmd.AddCommand(exportDBCmd)
	dbCmd.AddCommand(importDBCmd)
}
