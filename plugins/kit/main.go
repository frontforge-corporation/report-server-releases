// Command kit is the conformance kit for Frontforge Report Server's secret
// plugins. It starts a plugin as reportd does, in any language, and holds
// it to protocol version 1:
//
//	go run github.com/frontforge-corporation/report-server-releases/plugins/kit@v1 -- ./my-plugin --config cfg.json --hit kv/app#password
//
// It exits 0 when every check passes, 1 when one fails, and 2 when it was
// run wrongly. -replay instead holds the plugin to one of the scripts in
// exchanges.json, which reportd's own tests replay too.
package main

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

//go:embed VERSION
var version string

// options are what one run of the kit checks.
type options struct {
	program   string
	config    map[string]any
	badConfig map[string]any
	hit       string
	miss      string
	// missGiven is set when -miss was: the plugin's own refs may have a
	// shape the kit's random misses don't.
	missGiven bool
	badRef    string
	env       []string
	replay    string
	verbose   bool
	timeouts  timeouts
	out       io.Writer
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	opt, err := parse(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "kit:", err)
		return 2
	}
	opt.out = stdout
	results, err := check(opt)
	if err != nil {
		fmt.Fprintln(stderr, "kit:", err)
		return 2
	}
	fmt.Fprintf(stdout, "Secret plugin kit %s, protocol 1: %s\n", strings.TrimSpace(version), opt.program)
	failed, skipped := 0, 0
	for _, r := range results {
		switch {
		case r.err != nil:
			failed++
			fmt.Fprintf(stdout, "  FAIL  %s: %v\n", r.name, r.err)
		case r.skipped != "":
			skipped++
			fmt.Fprintf(stdout, "  skip  %s: %s\n", r.name, r.skipped)
		default:
			fmt.Fprintf(stdout, "  ok    %s\n", r.name)
		}
	}
	fmt.Fprintf(stdout, "%d passed, %d failed, %d skipped\n", len(results)-failed-skipped, failed, skipped)
	if failed > 0 {
		return 1
	}
	return 0
}

// check runs the checks, or the replay, against opt.program.
func check(opt *options) ([]result, error) {
	r, err := newRunner(opt)
	if err != nil {
		return nil, err
	}
	defer r.close()
	if opt.replay != "" {
		r.replay(opt.replay)
	} else {
		r.checks()
	}
	r.streams()
	return r.results, nil
}

func parse(args []string, stderr io.Writer) (*options, error) {
	opt := &options{timeouts: protocolTimeouts}
	var config, badConfig string
	fs := flag.NewFlagSet("kit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: kit [flags] -- program [flags]\n\nIt starts program as reportd does: with no arguments, in a working folder\nof its own, and with only the environment variables -env names.\n\n")
		fs.PrintDefaults()
	}
	fs.StringVar(&config, "config", "", "a JSON `file` holding the config sent in init, as reportd sends the plugin's config: {} if none")
	fs.StringVar(&badConfig, "bad-config", "", "a JSON `file` holding a config init must refuse with invalid-config")
	fs.StringVar(&opt.hit, "hit", "", "a `ref` the plugin holds, as in secret:<name>:<ref>")
	fs.StringVar(&opt.miss, "miss", "", "a `ref` it doesn't hold, of the shape its refs take (default: a random one)")
	fs.StringVar(&opt.badRef, "bad-ref", "bad\x00ref", "a `ref` it can't read: invalid-ref")
	fs.Func("env", "pass the environment variable `NAME` through, as a plugin's env does (repeatable)", func(s string) error {
		opt.env = append(opt.env, s)
		return nil
	})
	fs.StringVar(&opt.replay, "replay", "", "replay one of the `script`s of exchanges.json ("+strings.Join(replayable(), ", ")+") instead; the plugin must hold its data")
	fs.BoolVar(&opt.verbose, "v", false, "print each line sent and answered")
	showVersion := fs.Bool("version", false, "print the kit's version")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if *showVersion {
		fmt.Fprintln(stderr, strings.TrimSpace(version))
		return nil, flag.ErrHelp
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return nil, errors.New("name the plugin's program")
	}
	// Flags may follow the program too: reportd gives a plugin no
	// arguments, so neither does the kit.
	program := fs.Arg(0)
	if err := fs.Parse(fs.Args()[1:]); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("a plugin gets no arguments, so %q is neither the program nor a flag", fs.Arg(0))
	}
	path, err := exec.LookPath(program)
	if err == nil {
		opt.program, err = filepath.Abs(path)
	}
	if err != nil {
		return nil, fmt.Errorf("the program: %v", err)
	}
	if opt.config, err = readConfig(config); err != nil {
		return nil, err
	}
	if opt.badConfig, err = readConfig(badConfig); err != nil {
		return nil, err
	}
	opt.missGiven = opt.miss != ""
	if opt.miss == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		opt.miss = "kit-missing-" + hex.EncodeToString(b)
	}
	if opt.replay != "" && !slices.Contains(replayable(), opt.replay) {
		return nil, fmt.Errorf("-replay %s: the scripts are %s", opt.replay, strings.Join(replayable(), ", "))
	}
	return opt, nil
}

// readConfig reads a JSON object from file; "" is none.
func readConfig(file string) (map[string]any, error) {
	if file == "" {
		return nil, nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var config map[string]any
	if err := json.Unmarshal(b, &config); err != nil || config == nil {
		return nil, fmt.Errorf("%s: a config is a JSON object", file)
	}
	return config, nil
}
