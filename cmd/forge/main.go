package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/sorenmh/deploysmith/internal/forge/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		// Some commands, such as forge plan, have exit codes of their own
		var exit *cmd.ExitCodeError
		if errors.As(err, &exit) {
			os.Exit(exit.Code)
		}
		os.Exit(1)
	}
}
