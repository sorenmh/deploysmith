package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	// These will be set by ldflags during build
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show forge version information",
	Long:  `Display the version, git commit, and build time of the forge binary.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("forge version %s\n", Version)
		fmt.Printf("commit: %s\n", GitCommit)
		fmt.Printf("built: %s\n", BuildTime)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
