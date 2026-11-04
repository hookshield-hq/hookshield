package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hookshield-hq/hookshield/backend/internal/cli"
)

func runListen(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("listen", "listen [endpoint] --forward URL", stderr)
	endpoint := fs.String("endpoint", "", "endpoint slug (ep_…), ID or name; also the first argument. Omit to forward every endpoint")
	forward := fs.String("forward", "", "where to send the webhooks: 3000, localhost:3000/webhooks or a full URL (required)")
	fs.StringVar(forward, "f", "", "shorthand for --forward")
	original := fs.Bool("original", false, "forward the request as received, before the endpoint's transform rules")
	printBody := fs.Bool("print-body", false, "print the response body of your app")
	skipVerify := fs.Bool("skip-verify", false, "accept a self-signed HTTPS certificate of your app")
	noColor := fs.Bool("no-color", false, "plain output (also NO_COLOR=1)")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if *endpoint == "" && len(positional) > 0 {
		*endpoint = positional[0]
	}
	target, err := cli.ParseForward(*forward)
	if err != nil {
		fmt.Fprintf(stderr, "✗ %v\n", err)
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
	l := &cli.Listener{
		Client: c, Forward: target, Original: *original, PrintBody: *printBody, SkipVerify: *skipVerify,
		Out: stdout, Color: !*noColor && getenv("NO_COLOR") == "" && stdout == io.Writer(os.Stdout) && isTerminal(os.Stdout),
	}
	if *endpoint != "" {
		ep, err := cli.FindEndpoint(eps, *endpoint)
		if err != nil {
			return fail(stderr, err)
		}
		l.Endpoint = &ep
		fmt.Fprintf(stdout, "Ready! Forwarding webhooks of %s (%s) → %s\n", ep.Name, ep.Slug, target)
		fmt.Fprintf(stdout, "  Webhook URL  %s\n", ep.IngestURL)
		if ep.Status != "active" {
			fmt.Fprintf(stdout, "  Note: the endpoint is %s.\n", ep.Status)
		}
	} else {
		if len(eps) == 0 {
			return fail(stderr, errors.New("no endpoints yet — create one in the dashboard"))
		}
		slugs := make([]string, len(eps))
		for i, e := range eps {
			slugs[i] = e.Slug
		}
		fmt.Fprintf(stdout, "Ready! Forwarding webhooks of every endpoint (%s) → %s\n", strings.Join(slugs, ", "), target)
	}
	fmt.Fprintln(stdout, "  HookShield still delivers them to each endpoint's destination too. Ctrl+C to stop.")
	fmt.Fprintln(stdout)

	if err := l.Run(ctx); err != nil {
		return fail(stderr, err)
	}
	return 0
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func runTrigger(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("trigger", "trigger <endpoint> [--event TYPE] [--data JSON|@file|-] [-H 'Name: value']", stderr)
	endpoint := fs.String("endpoint", "", "endpoint slug (ep_…), ID or name; also the first argument")
	event := fs.String("event", "", "event type: sends {\"type\": TYPE, …} when --data is not given")
	data := fs.String("data", "", "body: JSON, @file.json or - for stdin (default: a sample event)")
	fs.StringVar(data, "d", "", "shorthand for --data")
	method := fs.String("method", "POST", "POST, PUT or PATCH")
	var headers multiFlag
	fs.Var(&headers, "header", "extra header \"Name: value\" (repeatable; replaces the default headers)")
	fs.Var(&headers, "H", "shorthand for --header")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return 2
	}
	if *endpoint == "" && len(positional) > 0 {
		*endpoint = positional[0]
	}
	if *endpoint == "" {
		fmt.Fprintln(stderr, "✗ which endpoint? hookshield trigger <slug> (see hookshield endpoints)")
		return 2
	}
	req, err := cli.BuildTrigger(*method, *event, *data, headers, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "✗ %v\n", err)
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
	ep, err := cli.FindEndpoint(eps, *endpoint)
	if err != nil {
		return fail(stderr, err)
	}
	id, err := c.Trigger(ctx, ep.ID, req)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "✓ Test event queued for %s (%s): %s\n", ep.Name, ep.Slug, id)
	return 0
}
