// zcodecli entry point: global option parsing (--json), the command dispatch
// table, and the shared fail/exit path used by every command.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type globalOptions struct {
	port int
	json bool
}

func main() {
	opts := globalOptions{port: defaultDebugPort}
	args := make([]string, 0, len(os.Args[1:]))

	for i := 0; i < len(os.Args[1:]); i++ {
		arg := os.Args[1:][i]
		switch {
		case arg == "--json":
			opts.json = true
		case arg == "--port":
			if i+1 >= len(os.Args[1:]) {
				fail(opts, errors.New("--port requires a value"))
			}
			i++
			port, err := strconv.Atoi(os.Args[1:][i])
			if err != nil || port <= 0 || port > 65535 {
				fail(opts, fmt.Errorf("invalid --port value %q", os.Args[1:][i]))
			}
			opts.port = port
		case strings.HasPrefix(arg, "--port="):
			port, err := strconv.Atoi(strings.TrimPrefix(arg, "--port="))
			if err != nil || port <= 0 || port > 65535 {
				fail(opts, fmt.Errorf("invalid --port value %q", strings.TrimPrefix(arg, "--port=")))
			}
			opts.port = port
		case arg == "-h" || arg == "--help" || arg == "help":
			fmt.Fprint(os.Stdout, usageText)
			return
		default:
			args = append(args, arg)
		}
	}

	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}

	if err := dispatch(args, opts); err != nil {
		fail(opts, err)
	}
}

func dispatch(args []string, opts globalOptions) error {
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "status":
		return statusCommand(opts)
	case "start":
		return startCommand(opts)
	case "stop":
		return stopCommand(opts)
	case "skill":
		fmt.Fprint(os.Stdout, skillGuide)
		return nil
	case "explore":
		if len(rest) == 0 {
			return errors.New("usage: zcodecli explore methods")
		}
		switch rest[0] {
		case "methods":
			return exploreMethodsCommand(opts)
		default:
			return fmt.Errorf("unknown explore action %q", rest[0])
		}
	case "system":
		if len(rest) == 0 {
			return errors.New("usage: zcodecli system locale|device-id")
		}
		return systemCommand(rest[0], opts)
	case "editors":
		if len(rest) == 0 || rest[0] != "list" {
			return errors.New("usage: zcodecli editors list")
		}
		return editorsListCommand(opts)
	case "docker":
		if len(rest) == 0 {
			return errors.New("usage: zcodecli docker status|containers")
		}
		return dockerCommand(rest[0], opts)
	case "wsl":
		if len(rest) == 0 || rest[0] != "list" {
			return errors.New("usage: zcodecli wsl list")
		}
		return wslListCommand(opts)
	case "updates":
		if len(rest) == 0 || rest[0] != "status" {
			return errors.New("usage: zcodecli updates status")
		}
		return updatesStatusCommand(opts)
	case "window":
		if len(rest) == 0 || rest[0] != "state" {
			return errors.New("usage: zcodecli window state")
		}
		return windowStateCommand(opts)
	case "remote":
		if len(rest) == 0 || rest[0] != "status" {
			return errors.New("usage: zcodecli remote status")
		}
		return remoteStatusCommand(opts)
	case "desktop":
		if len(rest) == 0 || rest[0] != "activity" {
			return errors.New("usage: zcodecli desktop activity")
		}
		return desktopActivityCommand(opts)
	case "community":
		if len(rest) == 0 || rest[0] != "status" {
			return errors.New("usage: zcodecli community status")
		}
		return communityStatusCommand(opts)
	case "chat":
		return chatCommand(rest, opts)
	case "ssh":
		if len(rest) == 0 || rest[0] != "list" {
			return errors.New("usage: zcodecli ssh list")
		}
		return sshListCommand(opts)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usageText)
	}
}

func fail(opts globalOptions, err error) {
	if opts.json {
		fmt.Fprintf(os.Stderr, "{\"error\":%s}\n", escapeJSONString(err.Error()))
	} else {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	os.Exit(1)
}

func escapeJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

const usageText = `zcodecli controls the local ZCode Electron app through its CDP bridge.

Usage:
  zcodecli status [--json] [--port 9227]
  zcodecli start [--json] [--port 9227]
  zcodecli stop [--json]
  zcodecli chat list [--json] [--port 9227]
  zcodecli chat show <sessionId> [--all] [--json] [--port 9227]
  zcodecli chat new "<prompt>" [--json] [--port 9227]
  zcodecli chat send <sessionId> "<prompt>" [--json] [--port 9227]
  zcodecli ssh list [--json] [--port 9227]
  zcodecli system locale|device-id [--json] [--port 9227]
  zcodecli editors list [--json] [--port 9227]
  zcodecli docker status|containers [--json] [--port 9227]
  zcodecli wsl list [--json] [--port 9227]
  zcodecli updates status [--json] [--port 9227]
  zcodecli window state [--json] [--port 9227]
  zcodecli remote status [--json] [--port 9227]
  zcodecli desktop activity [--json] [--port 9227]
  zcodecli community status [--json] [--port 9227]
  zcodecli explore methods [--json] [--port 9227]
  zcodecli skill

Business commands require: zcodecli start
`
