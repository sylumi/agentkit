// Package full combines console and web launchers with the bundled UI.
package full

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sylumi/agentkit/cmd/launcher"
	"github.com/sylumi/agentkit/cmd/launcher/console"
	weblauncher "github.com/sylumi/agentkit/cmd/launcher/web"
	"github.com/sylumi/agentkit/web"
)

// Run starts a console with no arguments, or the web UI with ["web"].
// Web accepts -addr (default 127.0.0.1:8080). Both modes use Config's services
// and defaults. Use the console or web package directly for a narrower build.
// Interrupts and context cancellation stop the launcher normally. Model
// settings and credentials belong to the caller's Agent configuration.
func Run(ctx context.Context, cfg launcher.Config, args []string) error {
	mode, addr, err := parseArgs(args, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if ctx == nil {
		return fmt.Errorf("launcher: context is required")
	}
	if mode == "web" {
		return weblauncher.Run(ctx, cfg, addr, web.Handler())
	}
	return console.Run(ctx, cfg)
}

func parseArgs(args []string, output io.Writer) (mode, addr string, err error) {
	if len(args) == 0 {
		return "console", "", nil
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprintln(output, "Usage: <program> [web [-addr 127.0.0.1:8080]]\n\nNo arguments: interactive chat.\nweb: serve the built-in UI and API together.")
		return "", "", flag.ErrHelp
	case "web":
		flags := flag.NewFlagSet("web", flag.ContinueOnError)
		flags.SetOutput(output)
		flags.StringVar(&addr, "addr", "127.0.0.1:8080", "HTTP listen address")
		if err := flags.Parse(args[1:]); err != nil {
			return "", "", err
		}
		if flags.NArg() != 0 || strings.TrimSpace(addr) == "" {
			return "", "", fmt.Errorf("launcher: expected web [-addr host:port]")
		}
		return "web", addr, nil
	default:
		return "", "", fmt.Errorf("launcher: unknown command %q; use web or run without arguments for chat", args[0])
	}
}
