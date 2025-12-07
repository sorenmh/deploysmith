package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	// Version is the semantic version of smithctl
	Version = "dev"
	// GitCommit is the git commit hash
	GitCommit = "unknown"
	// BuildTime is the build timestamp
	BuildTime = "unknown"
)

var cliVersionCmd = &cobra.Command{
	Use:   "cli-version",
	Short: "Show smithctl version",
	Long:  `Display the version information for smithctl.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("smithctl version %s\n", Version)
		fmt.Printf("commit: %s\n", GitCommit)
		fmt.Printf("built: %s\n", BuildTime)
	},
}

func init() {
	rootCmd.AddCommand(cliVersionCmd)
}
