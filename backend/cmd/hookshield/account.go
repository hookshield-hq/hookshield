package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/hookshield-hq/hookshield/backend/internal/cli"
)

// Overridable in tests.
var (
	stdin      io.Reader = os.Stdin
	getenv               = os.Getenv
	isTerminal           = func(f *os.File) bool { return term.IsTerminal(int(f.Fd())) }
	readSecret           = func() (string, error) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		return string(b), err
	}
)

// parseInterspersed parses flags that may come before or after positional arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func newFlags(name, usage string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: hookshield %s\n\nFlags:\n", usage)
		fs.PrintDefaults()
	}
	return fs
}

func fail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "✗ %v\n", err)
	return 1
}

func client() (*cli.Client, cli.Config, error) {
	cfg, err := cli.LoadConfig(getenv)
	if err != nil {
		return nil, cfg, err
	}
	c, err := cli.NewClient(cfg)
	return c, cfg, err
}

func runLogin(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("login", "login [--api-url URL] [--api-key KEY]", stderr)
	apiURL := fs.String("api-url", "", "API URL: your install's URL (https://hooks.example.com) or HookShield Cloud's (default "+cli.DefaultAPIURL+")")
	apiKey := fs.String("api-key", "", "API key (hs_live_…); asked for when omitted. Create one in the dashboard → API keys")
	if _, err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	current, _ := cli.LoadConfig(func(k string) string {
		if k == cli.EnvConfig {
			return getenv(k)
		}
		return "" // the saved file only
	})
	interactive := isTerminal(os.Stdin) && stdin == io.Reader(os.Stdin)
	in := bufio.NewReader(stdin)

	u := *apiURL
	if u == "" && interactive {
		fmt.Fprintf(stdout, "API URL [%s]: ", current.APIURL)
		line, _ := in.ReadString('\n')
		u = strings.TrimSpace(line)
	}
	if u == "" {
		u = current.APIURL
	}
	base, err := cli.NormalizeAPIURL(u)
	if err != nil {
		return fail(stderr, err)
	}

	key := strings.TrimSpace(*apiKey)
	if key == "" {
		if interactive {
			fmt.Fprint(stdout, "API key (dashboard → API keys, scope \"ingest\" to also use trigger): ")
			k, err := readSecret()
			fmt.Fprintln(stdout)
			if err != nil {
				return fail(stderr, err)
			}
			key = strings.TrimSpace(k)
		} else {
			line, _ := in.ReadString('\n') // echo "$KEY" | hookshield login --api-url …
			key = strings.TrimSpace(line)
		}
	}
	switch {
	case key == "":
		return fail(stderr, errors.New("no API key given"))
	case strings.HasPrefix(key, "hs_in_"):
		return fail(stderr, errors.New("that is an endpoint token (hs_in_…), not an API key: create an API key in the dashboard → API keys"))
	case !strings.HasPrefix(key, "hs_live_"):
		return fail(stderr, errors.New("that does not look like a HookShield API key (they start with hs_live_)"))
	}

	cfg := cli.Config{APIURL: base, APIKey: key}
	c, _ := cli.NewClient(cfg)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	me, err := c.Me(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	path, err := cli.SaveConfig(getenv, cfg)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "✓ Logged in to %s as %s\n  Saved to %s\n  Next: hookshield listen <endpoint> --forward localhost:3000/webhooks\n", base, me.Email, path)
	return 0
}

func runLogout(args []string, stdout, stderr io.Writer) int {
	fs := newFlags("logout", "logout", stderr)
	if _, err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	path, err := cli.RemoveConfig(getenv)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "✓ Logged out (%s removed). The API key itself still works until you revoke it in the dashboard.\n", path)
	return 0
}

func runWhoami(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("whoami", "whoami", stderr)
	if _, err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	c, cfg, err := client()
	if err != nil {
		return fail(stderr, err)
	}
	me, err := c.Me(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	source := "saved login"
	if getenv(cli.EnvAPIKey) != "" {
		source = cli.EnvAPIKey
	}
	fmt.Fprintf(stdout, "%s (%s plan)\nAPI   %s\nKey   %s… (%s)\n", me.Email, me.PlanType, cfg.APIURL, cfg.APIKey[:min(len(cfg.APIKey), 12)], source)
	return 0
}

func runEndpoints(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("endpoints", "endpoints", stderr)
	if _, err := parseInterspersed(fs, args); err != nil {
		return 2
	}
	c, _, err := client()
	if err != nil {
		return fail(stderr, err)
	}
	eps, err := c.Endpoints(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	if len(eps) == 0 {
		fmt.Fprintln(stdout, "No endpoints yet — create one in the dashboard.")
		return 0
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSLUG\tSTATUS\tWEBHOOK URL")
	for _, e := range eps {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.Name, e.Slug, e.Status, e.IngestURL)
	}
	_ = w.Flush()
	return 0
}
