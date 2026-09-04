// Command tx-agent is the terminalX controlled-side agent.
//
//	tx-agent pair --relay URL --code XXXX-XXXX [--name NAME] [--config PATH]
//	tx-agent run [--config PATH] [--log-level LEVEL]          (default)
//	tx-agent doctor [--config PATH]
//	tx-agent install | uninstall [--config PATH]
//	tx-agent notify --sid N --port P --token T '<json>'       (used by Codex)
//	tx-agent hook --sid N --port P --token T --event NAME     (used by Claude)
//	tx-agent version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/wfighter1/terminalX/internal/agent"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tx-agent:", err)
		os.Exit(1)
	}
}

func usage() string {
	return strings.TrimSpace(`
usage: tx-agent <command> [flags]

commands:
  pair       pair this machine with a relay:  --relay URL --code XXXX-XXXX [--name NAME] [--config PATH]
  run        run the agent (default):         [--config PATH] [--log-level debug|info|warn|error]
  doctor     print a diagnostic summary:      [--config PATH]
  install    register autostart at logon (Windows: Task Scheduler ONLOGON)
  uninstall  remove the autostart registration
  notify     forward a Codex notify payload:  --sid N --port P --token T '<json>'
  hook       forward a Claude hook payload from stdin: --sid N --port P --token T --event NAME
  version    print the version
`)
}

func configFlag(fs *flag.FlagSet) *string {
	def, _ := agent.DefaultPath()
	return fs.String("config", def, "path to agent.json")
}

func run(args []string) error {
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "pair":
		return cmdPair(args)
	case "run":
		return cmdRun(args)
	case "doctor":
		return cmdDoctor(args)
	case "install":
		return cmdInstall(args, true)
	case "uninstall":
		return cmdInstall(args, false)
	case "notify":
		return cmdNotify(args)
	case "hook":
		return cmdHook(args)
	case "version", "--version", "-v":
		fmt.Printf("tx-agent %s (%s/%s)\n", agent.Version, runtime.GOOS, runtime.GOARCH)
		return nil
	case "help", "--help", "-h":
		fmt.Println(usage())
		return nil
	}
	return fmt.Errorf("unknown command %q\n%s", cmd, usage())
}

func newLogger(level string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("bad --log-level %q: %w", level, err)
	}
	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
	slog.SetDefault(l)
	return l, nil
}

func loadConfig(path string) (*agent.Config, error) {
	cfg, err := agent.Load(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config %s not found; run `tx-agent pair --relay URL --code CODE` first", path)
		}
		return nil, err
	}
	return cfg, nil
}

func cmdPair(args []string) error {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	relay := fs.String("relay", "", "relay base URL, e.g. https://tx.example.com")
	code := fs.String("code", "", "8-character pairing code shown in the console (XXXX-XXXX)")
	name := fs.String("name", "", "device name shown in the console (default: hostname)")
	cfgPath := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *relay == "" || *code == "" {
		return errors.New("pair: --relay and --code are required")
	}
	cfg, err := agent.Load(*cfgPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if *name == "" && cfg.Name == "" {
		h, _ := os.Hostname()
		*name = h
	}
	res, err := agent.Pair(context.Background(), cfg, *relay, *code, *name)
	if err != nil {
		return err
	}
	if err := cfg.Save(*cfgPath); err != nil {
		return err
	}
	fmt.Printf("paired with %s\n", cfg.RelayURL)
	fmt.Printf("device_id:   %s\n", res.DeviceID)
	fmt.Printf("name:        %s\n", res.Name)
	fmt.Printf("config:      %s\n", *cfgPath)
	fmt.Printf("fingerprint: %s  (compare with the console; local pubkey fingerprint %s)\n", res.Fingerprint, res.LocalFP)
	if res.Fingerprint != res.LocalFP {
		fmt.Println("warning: the relay's fingerprint differs from the local public-key fingerprint; verify it in the console before trusting this pairing")
	}
	return nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	cfgPath := configFlag(fs)
	level := fs.String("log-level", envOr("TX_LOG_LEVEL", "info"), "debug | info | warn | error")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log, err := newLogger(*level)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	a, err := agent.New(cfg, *cfgPath, log)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("tx-agent starting", "version", agent.Version, "os", runtime.GOOS, "relay", cfg.RelayURL, "device_id", cfg.DeviceID, "name", cfg.Name)
	return a.Run(ctx)
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	cfgPath := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := agent.Load(*cfgPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err != nil {
		fmt.Printf("config %s not found (not paired yet)\n\n", *cfgPath)
	}
	return agent.Doctor(context.Background(), cfg, *cfgPath, os.Stdout)
}

func cmdInstall(args []string, install bool) error {
	name := "uninstall"
	if install {
		name = "install"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	cfgPath := configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	var (
		out string
		err error
	)
	if install {
		if _, lerr := loadConfig(*cfgPath); lerr != nil {
			return lerr
		}
		out, err = agent.Install(*cfgPath)
	} else {
		out, err = agent.Uninstall()
	}
	fmt.Print(out)
	return err
}

func cmdNotify(args []string) error {
	fs := flag.NewFlagSet("notify", flag.ContinueOnError)
	sid := fs.Uint("sid", 0, "session id")
	port := fs.Int("port", 0, "hooks port")
	token := fs.String("token", "", "hook token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return errors.New("notify: missing JSON payload argument")
	}
	return agent.Notify(context.Background(), *port, uint32(*sid), *token, rest[len(rest)-1])
}

// cmdHook forwards one Claude hook payload from stdin to the local hooks
// endpoint and writes the endpoint's answer to stdout, which is what Claude
// reads back from a command hook.
func cmdHook(args []string) error {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	sid := fs.Uint("sid", 0, "session id")
	port := fs.Int("port", 0, "hooks port")
	token := fs.String("token", "", "hook token")
	event := fs.String("event", "", "hook event name, or \"statusline\"")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return agent.ForwardHook(context.Background(), *port, uint32(*sid), *token, *event, os.Stdin, os.Stdout)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
