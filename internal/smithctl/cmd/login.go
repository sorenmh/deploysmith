package cmd

import (
	"fmt"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/config"
	"github.com/sorenmh/deploysmith/internal/smithctl/client"
	"github.com/sorenmh/deploysmith/internal/smithctl/output"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to smithd with your GitHub account",
	Long: `Log in to smithd with your GitHub account, so smithd knows who you are.

smithctl shows a code to enter on GitHub; once you have, smithd gives
smithctl a session, stored in ~/.deploysmith/sessions.json and used instead
of the API key until it expires. smithd must have login with GitHub
configured.

Example:
  smithctl login --url https://smithd.example.com`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.ValidateURL(); err != nil {
			return err
		}

		// Logging in needs no API key or session
		c := client.NewClient(GetSmithdURL(), "")

		login, err := c.StartLogin()
		if err != nil {
			return fmt.Errorf("failed to start login: %w", err)
		}

		fmt.Printf("Open %s and enter the code: %s\n", login.VerificationURI, login.UserCode)
		fmt.Println("Waiting for you to confirm on GitHub...")

		session, err := c.WaitForLogin(login, time.Sleep)
		if err != nil {
			return fmt.Errorf("login failed: %w", err)
		}

		if err := config.SaveSession(GetSmithdURL(), config.Session{
			Token:     session.Token,
			Login:     session.Login,
			ExpiresAt: session.ExpiresAt,
		}); err != nil {
			return err
		}

		output.Success(fmt.Sprintf("Logged in as %s (until %s)", session.Login, output.FormatTime(session.ExpiresAt)))
		return nil
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out of smithd",
	Long:  `End your smithd session and remove it from ~/.deploysmith/sessions.json.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.ValidateURL(); err != nil {
			return err
		}

		session, err := config.LoadSession(GetSmithdURL())
		if err != nil {
			return err
		}
		if session == nil {
			output.Info("Not logged in")
			return nil
		}

		// Forget the session even if smithd can't be reached; it expires
		if !session.Expired() {
			if err := client.NewClient(GetSmithdURL(), "").WithSessionToken(session.Token).Logout(); err != nil {
				output.Warn(fmt.Sprintf("Could not end the session on smithd: %v", err))
			}
		}
		if err := config.DeleteSession(GetSmithdURL()); err != nil {
			return err
		}

		output.Success(fmt.Sprintf("Logged out %s", session.Login))
		return nil
	},
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show who smithd sees you as",
	Long:  `Show who smithd sees you as: your GitHub login after smithctl login, or the API key.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := ValidateConfig(); err != nil {
			return err
		}

		id, err := newClient().WhoAmI()
		if err != nil {
			return err
		}

		return output.Print(output.Format(GetOutputFormat()), id, func() {
			if id.Kind != "user" {
				fmt.Println("Authenticated with an API key (run `smithctl login` to use your GitHub account)")
				return
			}
			fmt.Printf("Logged in as %s", id.Login)
			if id.ExpiresAt != nil {
				fmt.Printf(" (until %s)", output.FormatTime(*id.ExpiresAt))
			}
			fmt.Println()
		})
	},
}

func init() {
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(whoamiCmd)
}
