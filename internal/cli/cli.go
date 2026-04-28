package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/williamjaackson/git-weld/internal/weld"
)

type CLI struct {
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader
	cwd    string
}

// Run dispatches the CLI entrypoint.
func Run(args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	cli := &CLI{
		stdout: os.Stdout,
		stderr: os.Stderr,
		stdin:  os.Stdin,
		cwd:    cwd,
	}
	return cli.run(args)
}

func (c *CLI) run(args []string) error {
	if len(args) == 0 {
		printHelp(c.stdout)
		return nil
	}

	switch args[0] {
	case "help", "--help", "-h":
		printHelp(c.stdout)
		return nil
	case "version", "--version":
		_, err := fmt.Fprintln(c.stdout, "git-weld dev")
		return err
	case "new":
		return c.runNew(args[1:])
	case "init":
		return c.runInit(args[1:])
	case "stack":
		return c.runStack(args[1:])
	case "unstack":
		return c.runUnstack(args[1:])
	case "show":
		return c.runShow(args[1:])
	case "status":
		return c.runStatus(args[1:])
	case "diff":
		return c.runDiff(args[1:])
	case "sync":
		return c.runSync(args[1:])
	case "ship":
		return c.runShip(args[1:])
	case "pr":
		return c.runPR(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (c *CLI) runNew(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: git weld new <branch>")
	}
	svc, err := weld.Open(c.cwd)
	if err != nil {
		return err
	}
	c.bindReporter(svc)
	return svc.NewBranch(args[0])
}

func (c *CLI) runInit(args []string) error {
	svc, err := weld.Open(c.cwd)
	if err != nil {
		return err
	}
	mainBranch, remoteName, remoteDisabled, interactive, err := parseInitArgs(args)
	if err != nil {
		return err
	}
	if interactive {
		if err := svc.InitInteractive(c.stdin, c.stdout); err != nil {
			return err
		}
		return nil
	}
	if mainBranch == "" {
		mainBranch = svc.Settings().RootBranch
	}
	if !remoteDisabled && remoteName == "" {
		remoteName = svc.Settings().RemoteName
		if remoteName == "" {
			remoteName = weld.DefaultSettings().RemoteName
		}
	}
	c.bindReporter(svc)
	return svc.Init(mainBranch, remoteName, remoteDisabled)
}

func (c *CLI) runStack(args []string) error {
	positional, flags, err := parseBoolFlags(args, map[string]string{
		"-c":       "create",
		"--create": "create",
	})
	if err != nil {
		return err
	}

	svc, err := weld.Open(c.cwd)
	if err != nil {
		return err
	}

	if flags["create"] {
		if len(positional) < 1 || len(positional) > 2 {
			return errors.New("usage: git weld stack -c <branch> [<base>]")
		}
		base := ""
		if len(positional) == 2 {
			base = positional[1]
		}
		if base == "" {
			base, err = svc.CurrentBranch()
			if err != nil {
				return err
			}
		}
		c.bindReporter(svc)
		return svc.Stack(positional[0], base, true)
	}

	if len(positional) < 1 || len(positional) > 2 {
		return errors.New("usage: git weld stack <branch> [<base>]")
	}
	base := ""
	if len(positional) == 2 {
		base = positional[1]
	}
	if base == "" {
		base, err = svc.CurrentBranch()
		if err != nil {
			return err
		}
	}
	c.bindReporter(svc)
	return svc.Stack(positional[0], base, false)
}

func (c *CLI) runUnstack(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: git weld unstack <branch> [<base>]")
	}
	svc, err := weld.Open(c.cwd)
	if err != nil {
		return err
	}
	base := ""
	if len(args) == 2 {
		base = args[1]
	}
	c.bindReporter(svc)
	return svc.Unstack(args[0], base)
}

func (c *CLI) runShow(args []string) error {
	positional, flags, err := parseBoolFlags(args, map[string]string{
		"--tree": "tree",
	})
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return errors.New("usage: git weld show [<branch>] [--tree]")
	}

	svc, err := weld.Open(c.cwd)
	if err != nil {
		return err
	}

	branch := ""
	if len(positional) == 1 {
		branch = positional[0]
	}
	out, err := svc.Show(branch, flags["tree"])
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(c.stdout, out)
	return err
}

func (c *CLI) runStatus(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: git weld status")
	}
	svc, err := weld.Open(c.cwd)
	if err != nil {
		return err
	}
	entries, err := svc.Status()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err := fmt.Fprintf(c.stdout, "%s:\n", entry.Branch); err != nil {
			return err
		}
		for _, parent := range entry.Parents {
			if _, err := fmt.Fprintf(c.stdout, "  %s\n", parent); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *CLI) runDiff(args []string) error {
	if len(args) > 1 {
		return errors.New("usage: git weld diff [<branch>]")
	}
	svc, err := weld.Open(c.cwd)
	if err != nil {
		return err
	}
	branch := ""
	if len(args) == 1 {
		branch = args[0]
	}
	out, err := svc.Diff(branch)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(c.stdout, out)
	return err
}

func (c *CLI) runSync(args []string) error {
	positional, flags, err := parseBoolFlags(args, map[string]string{
		"--tree":   "tree",
		"--local":  "local",
		"--remote": "remote",
	})
	if err != nil {
		return err
	}
	if flags["local"] && flags["remote"] {
		return errors.New("usage: git weld sync [<branch>] [--tree] [--local|--remote]")
	}
	if len(positional) > 1 {
		return errors.New("usage: git weld sync [<branch>] [--tree] [--local|--remote]")
	}

	svc, err := weld.Open(c.cwd)
	if err != nil {
		return err
	}
	branch := ""
	if len(positional) == 1 {
		branch = positional[0]
	}
	c.bindReporter(svc)
	return svc.Sync(branch, flags["tree"], syncModeFromFlags(flags))
}

func (c *CLI) runShip(args []string) error {
	positional, flags, err := parseBoolFlags(args, map[string]string{
		"--tree":   "tree",
		"--local":  "local",
		"--remote": "remote",
	})
	if err != nil {
		return err
	}
	if flags["local"] && flags["remote"] {
		return errors.New("usage: git weld ship [<branch>] [--tree] [--local|--remote]")
	}
	if len(positional) > 1 {
		return errors.New("usage: git weld ship [<branch>] [--tree] [--local|--remote]")
	}

	svc, err := weld.Open(c.cwd)
	if err != nil {
		return err
	}
	branch := ""
	if len(positional) == 1 {
		branch = positional[0]
	}
	c.bindReporter(svc)
	_, err = svc.Ship(branch, flags["tree"], syncModeFromFlags(flags))
	return err
}

func syncModeFromFlags(flags map[string]bool) weld.SyncMode {
	if flags["local"] {
		return weld.SyncModeLocal
	}
	if flags["remote"] {
		return weld.SyncModeRemote
	}
	return weld.SyncModeDefault
}

func parseInitArgs(args []string) (string, string, bool, bool, error) {
	mainBranch := ""
	remoteName := ""
	remoteDisabled := false
	interactive := len(args) == 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--main":
			i++
			if i >= len(args) {
				return "", "", false, false, errors.New("usage: git weld init [--main <branch>] [--remote <remote>] [--no-remote]")
			}
			mainBranch = args[i]
			interactive = false
		case "--remote":
			i++
			if i >= len(args) {
				return "", "", false, false, errors.New("usage: git weld init [--main <branch>] [--remote <remote>] [--no-remote]")
			}
			remoteName = args[i]
			interactive = false
		case "--no-remote":
			remoteDisabled = true
			interactive = false
		default:
			if strings.HasPrefix(arg, "--main=") {
				mainBranch = strings.TrimPrefix(arg, "--main=")
				interactive = false
				continue
			}
			if strings.HasPrefix(arg, "--remote=") {
				remoteName = strings.TrimPrefix(arg, "--remote=")
				interactive = false
				continue
			}
			return "", "", false, false, fmt.Errorf("unknown flag %q", arg)
		}
	}
	if remoteDisabled && remoteName != "" {
		return "", "", false, false, errors.New("usage: git weld init [--main <branch>] [--remote <remote>] [--no-remote]")
	}
	return mainBranch, remoteName, remoteDisabled, interactive, nil
}

func (c *CLI) runPR(args []string) error {
	branch, title, body, draft, web, err := parsePRArgs(args)
	if err != nil {
		return err
	}
	svc, err := weld.Open(c.cwd)
	if err != nil {
		return err
	}
	c.bindReporter(svc)
	result, err := svc.PR(branch, title, body, draft, web)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(c.stdout, "pr ready: #%d %s\n", result.Number, result.URL)
	return err
}

func parseBoolFlags(args []string, known map[string]string) ([]string, map[string]bool, error) {
	flags := make(map[string]bool)
	for _, canonical := range known {
		flags[canonical] = false
	}

	positional := make([]string, 0, len(args))
	for _, arg := range args {
		if alias, ok := known[arg]; ok {
			flags[alias] = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return nil, nil, fmt.Errorf("unknown flag %q", arg)
		}
		positional = append(positional, arg)
	}
	return positional, flags, nil
}

func parsePRArgs(args []string) (string, string, string, bool, bool, error) {
	branch := ""
	title := ""
	body := ""
	draft := false
	web := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--title":
			i++
			if i >= len(args) {
				return "", "", "", false, false, errors.New("usage: git weld pr [<branch>] [--title <title>] [--body <body>] [--draft] [--web]")
			}
			title = args[i]
		case "--body":
			i++
			if i >= len(args) {
				return "", "", "", false, false, errors.New("usage: git weld pr [<branch>] [--title <title>] [--body <body>] [--draft] [--web]")
			}
			body = args[i]
		case "--draft":
			draft = true
		case "--web":
			web = true
		default:
			if strings.HasPrefix(arg, "--title=") {
				title = strings.TrimPrefix(arg, "--title=")
				continue
			}
			if strings.HasPrefix(arg, "--body=") {
				body = strings.TrimPrefix(arg, "--body=")
				continue
			}
			if strings.HasPrefix(arg, "-") {
				return "", "", "", false, false, fmt.Errorf("unknown flag %q", arg)
			}
			if branch != "" {
				return "", "", "", false, false, errors.New("usage: git weld pr [<branch>] [--title <title>] [--body <body>] [--draft] [--web]")
			}
			branch = arg
		}
	}
	return branch, title, body, draft, web, nil
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, "git-weld")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  git weld <command> [options]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  new <branch>")
	fmt.Fprintln(out, "  init [--main <branch>] [--remote <remote>] [--no-remote]")
	fmt.Fprintln(out, "  stack <branch> [<base>] [-c|--create]")
	fmt.Fprintln(out, "  unstack <branch> [<base>]")
	fmt.Fprintln(out, "  show [<branch>] [--tree]")
	fmt.Fprintln(out, "  status")
	fmt.Fprintln(out, "  diff [<branch>]")
	fmt.Fprintln(out, "  sync [<branch>] [--tree] [--local|--remote]")
	fmt.Fprintln(out, "  ship [<branch>] [--tree] [--local|--remote]")
	fmt.Fprintln(out, "  pr [<branch>] [--title <title>] [--body <body>] [--draft] [--web]")
}

func (c *CLI) bindReporter(svc *weld.Service) {
	svc.SetReporter(func(message string) {
		_, _ = fmt.Fprintf(c.stdout, "==> %s\n", message)
	})
}
