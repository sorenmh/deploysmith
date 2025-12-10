package main

import (
	"fmt"
	"os"

	"github.com/sorenmh/deploysmith/internal/forge/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
