// tf-runner-auth is the entrypoint of DeploySmith's tofu-controller runner
// image (Dockerfile.tf-runner):
//
//	tf-runner-auth exec <command> [args...]   log in to OpenBao, then exec the command
//	tf-runner-auth aws-credentials            AWS credential_process helper
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/sorenmh/deploysmith/internal/tfrunnerauth"
)

const usage = `usage:
  tf-runner-auth exec <command> [args...]
  tf-runner-auth aws-credentials`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "tf-runner-auth: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no command\n%s", usage)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "exec":
		if len(args) < 2 {
			return fmt.Errorf("exec needs a command\n%s", usage)
		}
		loginCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		env, err := tfrunnerauth.RunnerEnv(loginCtx, os.Environ(), os.ReadFile, tfrunnerauth.KubernetesLogin)
		if err != nil {
			return err
		}
		path, err := exec.LookPath(args[1])
		if err != nil {
			return err
		}
		// Replaces this process, so tini's child is the runner itself
		return syscall.Exec(path, args[1:], env)

	case "aws-credentials":
		credsCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		return tfrunnerauth.CredentialProcess(credsCtx, os.Environ(), os.Stdout)
	}
	return fmt.Errorf("unknown command %q\n%s", args[0], usage)
}
