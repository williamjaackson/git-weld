package cli

import (
	"fmt"
	"io"
	"os"
)

// Run dispatches the CLI entrypoint.
func Run(args []string) error {
	return run(os.Stdout, args)
}

func run(out io.Writer, args []string) error {
	if len(args) == 0 {
		printHelp(out)
		return nil
	}

	switch args[0] {
	case "help", "--help", "-h":
		printHelp(out)
		return nil
	case "version", "--version":
		_, err := fmt.Fprintln(out, "git-weld dev")
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, "git-weld")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  git weld <command> [options]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Current scaffold commands:")
	fmt.Fprintln(out, "  help        Show this help output")
	fmt.Fprintln(out, "  version     Show the current version")
}
