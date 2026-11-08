// mudbot is a manual, opt-in websocket load generator. It never runs against a
// remote host unless -allow-remote is explicitly supplied.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mud_server/src/internal/mudbot"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "seed" {
		return seed(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "storm" {
		return storm(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "world" {
		return world(args[1:], stdout, stderr)
	}
	flags := flag.NewFlagSet("mudbot", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var c mudbot.Config
	flags.StringVar(&c.URL, "url", "ws://127.0.0.1:8080/ws", "target websocket URL")
	flags.IntVar(&c.Count, "count", 200, "number of concurrent bots")
	flags.DurationVar(&c.Duration, "duration", 30*time.Second, "gameplay duration after all admissions finish")
	flags.DurationVar(&c.Interval, "interval", time.Second, "per-bot pause between actions (each also sends a whisper fence)")
	flags.DurationVar(&c.Timeout, "timeout", 5*time.Second, "per dial/login/action timeout (maximum 1m)")
	flags.DurationVar(&c.JoinTimeout, "join-timeout", 2*time.Minute, "total admission time budget")
	flags.IntVar(&c.LoginWorkers, "login-workers", 8, "concurrent logins")
	flags.StringVar(&c.Prefix, "prefix", "loadbot", "account prefix; names append a five-digit zero-based index")
	flags.BoolVar(&c.AllowRemote, "allow-remote", false, "explicitly allow a non-loopback target; requires operator authorization")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	c.Password = os.Getenv("MUD_BOT_PASSWORD")
	report, err := mudbot.Run(ctx, c)
	if encodeErr := json.NewEncoder(stdout).Encode(report); encodeErr != nil {
		return encodeErr
	}
	return err
}

func seed(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("mudbot seed", flag.ContinueOnError)
	flags.SetOutput(stderr)
	count := flags.Int("count", 200, "number of fixture accounts")
	prefix := flags.String("prefix", "loadbot", "account prefix")
	directory := flags.String("sql-dir", "src/sql", "schema/seed directory (run from repository root)")
	reset := flags.Bool("reset", false, "required: replace schema in the dedicated local mud_load database")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || !*reset {
		return fmt.Errorf("seed requires -reset and no positional arguments; only local mud_load is allowed")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := mudbot.ResetLocal(ctx, os.Getenv("MUD_BOT_DATABASE_URL"), *directory, *count, *prefix, os.Getenv("MUD_BOT_PASSWORD")); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "Seeded %d fixture accounts in local mud_load (bcrypt cost 4; not a production login benchmark).\n", *count)
	return err
}

func storm(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("mudbot storm", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var c mudbot.StormConfig
	flags.StringVar(&c.URL, "url", "ws://127.0.0.1:8080/ws", "target websocket URL")
	flags.IntVar(&c.Count, "count", 200, "number of guests")
	flags.IntVar(&c.JoinWorkers, "join-workers", 8, "concurrent joins")
	flags.StringVar(&c.Prefix, "prefix", "stormbot", "name prefix (names append five digits; at most 11 characters)")
	flags.DurationVar(&c.Interval, "interval", 4*time.Second, "per-bot pause between light actions")
	flags.DurationVar(&c.Before, "before", 10*time.Second, "play this long before the first storm")
	flags.IntVar(&c.Cycles, "cycles", 1, "storms to run; 0 only plays for -between (use it to restart the server from outside)")
	flags.DurationVar(&c.Between, "between", 15*time.Second, "play this long after each storm")
	flags.Float64Var(&c.Fraction, "fraction", 1, "share of bots whose connection each storm cuts, 0..1")
	flags.DurationVar(&c.Jitter, "jitter", 5*time.Second, "reconnect delay is uniform in [0, jitter), growing after each failed attempt")
	flags.DurationVar(&c.GiveUp, "give-up", time.Minute, "a bot stops trying to come back after this long")
	flags.DurationVar(&c.Timeout, "timeout", 10*time.Second, "per dial and handshake")
	flags.DurationVar(&c.JoinTimeout, "join-timeout", 5*time.Minute, "total admission time budget")
	flags.BoolVar(&c.AllowRemote, "allow-remote", false, "explicitly allow a non-loopback target; requires operator authorization")
	flags.StringVar(&c.Mix, "mix", "light", `what bots do: "light" (SAY and a move), "verbs" (look, examine, talk, emote, socials, WHO, SAY, a walk) or "cast" (learn the load world's spells and cast them)`)
	flags.BoolVar(&c.Stay, "stay", false, "with -mix verbs: walk east once and stay, so a crowd forms in that room (the feast)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	report, err := mudbot.RunStorm(ctx, c)
	if report != nil {
		if encodeErr := json.NewEncoder(stdout).Encode(report); encodeErr != nil {
			return encodeErr
		}
	}
	return err
}

func world(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("mudbot world", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("dir", "", "directory to write world.yaml into (created if missing)")
	feastIn := flags.Duration("feast-in", 2*time.Minute, "how long from now the Feast event starts (minute precision)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *dir == "" {
		return fmt.Errorf("world requires -dir and no positional arguments")
	}
	if err := mudbot.WriteLoadWorld(*dir, time.Now().Add(*feastIn)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "Wrote %s/world.yaml (Feast at %s).\n", *dir, time.Now().Add(*feastIn).UTC().Format("15:04 UTC"))
	return err
}
