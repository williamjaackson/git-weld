package main

import (
	"fmt"
	"os"

	"github.com/williamjaackson/git-weld/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "git-weld:", err)
		os.Exit(1)
	}
}
