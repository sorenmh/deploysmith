package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion script",
	Long: `Generate shell completion script for smithctl.

To load completions:

Bash:
  $ source <(smithctl completion bash)

  # To load completions for each session, execute once:
  # Linux:
  $ smithctl completion bash > /etc/bash_completion.d/smithctl
  # macOS:
  $ smithctl completion bash > /usr/local/etc/bash_completion.d/smithctl

Zsh:
  # If shell completion is not already enabled in your environment,
  # you will need to enable it.  You can execute the following once:
  $ echo "autoload -U compinit; compinit" >> ~/.zshrc

  # To load completions for each session, execute once:
  $ smithctl completion zsh > "${fpath[1]}/_smithctl"

  # You will need to start a new shell for this setup to take effect.

Fish:
  $ smithctl completion fish | source

  # To load completions for each session, execute once:
  $ smithctl completion fish > ~/.config/fish/completions/smithctl.fish

PowerShell:
  PS> smithctl completion powershell | Out-String | Invoke-Expression

  # To load completions for every new session, run:
  PS> smithctl completion powershell > smithctl.ps1
  # and source this file from your PowerShell profile.
`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.ExactValidArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		switch args[0] {
		case "bash":
			cmd.Root().GenBashCompletion(os.Stdout)
		case "zsh":
			cmd.Root().GenZshCompletion(os.Stdout)
		case "fish":
			cmd.Root().GenFishCompletion(os.Stdout, true)
		case "powershell":
			cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
		}
	},
}

func init() {
	rootCmd.AddCommand(completionCmd)
}
