package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The programs the tests start, built once by TestMain: the broken test
// plugin, the example next to the kit, and reportd's fake plugin when the
// kit is inside reportd's repository ("" when it isn't).
var brokenProgram, exampleProgram, fakeProgram string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rf-kit-test-")
	if err != nil {
		panic(err)
	}
	build := func(name, moduleDir, pkg string) string {
		out := filepath.Join(dir, name)
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", out, pkg)
		cmd.Dir, cmd.Stdout, cmd.Stderr = moduleDir, os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			panic("building " + name + ": " + err.Error())
		}
		return out
	}
	brokenProgram = build("broken", ".", "./testdata/broken")
	exampleProgram = build("example", filepath.Join("..", "example"), ".")
	if _, err := os.Stat(filepath.Join("..", "..", "internal", "secrets", "plugin", "testdata", "fakeplugin")); err == nil {
		fakeProgram = build("fakeplugin", filepath.Join("..", ".."), "./internal/secrets/plugin/testdata/fakeplugin")
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// writeJSON writes v to a file in a test folder.
func writeJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// kit runs the kit as people do, with shorter waits.
func kit(t *testing.T, args ...string) []result {
	t.Helper()
	var stderr bytes.Buffer
	opt, err := parse(args, &stderr)
	if err != nil {
		t.Fatalf("kit %v: %v\n%s", args, err, stderr.String())
	}
	opt.out = &bytes.Buffer{}
	opt.timeouts = timeouts{hello: 5 * time.Second, init: 5 * time.Second, call: 3 * time.Second, shutdown: time.Second}
	results, err := check(opt)
	if err != nil {
		t.Fatal(err)
	}
	return results
}

func failed(results []result) map[string]error {
	out := map[string]error{}
	for _, r := range results {
		if r.err != nil {
			out[r.name] = r.err
		}
	}
	return out
}

func requirePass(t *testing.T, results []result, skips int) {
	t.Helper()
	for name, err := range failed(results) {
		t.Errorf("%s: %v", name, err)
	}
	n := 0
	for _, r := range results {
		if r.skipped != "" {
			n++
		}
	}
	if n != skips {
		t.Errorf("%d checks skipped, want %d", n, skips)
	}
}

func exampleConfig(t *testing.T) string {
	file, err := filepath.Abs(filepath.Join("..", "example", "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	return writeJSON(t, map[string]any{"file": file})
}

// The example passes every check, and every script: it holds the scripts'
// data in its secrets.json.
func TestTheExamplePasses(t *testing.T) {
	config, bad := exampleConfig(t), writeJSON(t, map[string]any{})
	t.Run("checks", func(t *testing.T) {
		requirePass(t, kit(t, "--", exampleProgram, "--config", config, "--bad-config", bad, "--hit", "billing#password"), 0)
	})
	for _, script := range replayable() {
		t.Run("replay "+script, func(t *testing.T) {
			c := config
			if script == "bad-config" {
				c = bad
			}
			requirePass(t, kit(t, "-replay", script, "-config", c, exampleProgram), 0)
		})
	}
}

// reportd's fake plugin, which answers from reportd's own copy of the
// exchanges, passes the kit's replay of each script: the kit and reportd's
// tests read the protocol the same way.
func TestReportdsFakePluginPassesTheReplay(t *testing.T) {
	if fakeProgram == "" {
		t.Skip("reportd's fake plugin isn't here: the kit is on its own")
	}
	scripts, err := filepath.Abs(filepath.Join("..", "..", "internal", "secrets", "plugin", "testdata", "exchanges.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_SCRIPTS", scripts)
	for _, script := range replayable() {
		t.Run(script, func(t *testing.T) {
			t.Setenv("FAKE_SCRIPT", script)
			requirePass(t, kit(t, "-replay", script, "-env", "FAKE_SCRIPTS", "-env", "FAKE_SCRIPT", fakeProgram), 0)
		})
	}
}

// The replay holds every script that isn't reportd's alone.
func TestReplayableScripts(t *testing.T) {
	if got := strings.Join(replayable(), " "); got != "bad-config store vault" {
		t.Fatalf("replayable scripts: %s", got)
	}
	var stderr bytes.Buffer
	if _, err := parse([]string{"-replay", "misbehaving", exampleProgram}, &stderr); err == nil {
		t.Fatal("a hostOnly script was taken")
	}
}

// Each broken plugin fails the check of the rule it breaks; the good ones
// pass every check.
func TestBrokenPluginsFail(t *testing.T) {
	config, bad := writeJSON(t, map[string]any{"name": "broken"}), writeJSON(t, map[string]any{})
	cases := []struct{ mode, fails string }{
		{"", ""},
		{"readonly", ""},
		{"protocol", "hello"},
		{"hello-shape", "hello"},
		{"echo-version", "version choice"},
		{"init", "init"},
		{"bad-config", "bad config"},
		{"hit-shape", "get: a hit"},
		{"miss", "get: a miss"},
		{"bad-ref", "get: a bad reference"},
		{"shared-id", "pipelining"},
		{"unknown-method", "unknown method"},
		{"health", "health"},
		{"replace-version", "writes"},
		{"delete", "writes"},
		{"readonly-put", "writes"},
		{"shutdown", "shutdown"},
		{"input", "input closed"},
		{"banner", "stdout"},
		{"no-newline", "stdout"},
		{"stderr-value", "stderr"},
	}
	for _, c := range cases {
		t.Run("mode "+c.mode, func(t *testing.T) {
			t.Setenv("KIT_BREAK", c.mode)
			results := kit(t, "-env", "KIT_BREAK", "--", brokenProgram, "--config", config, "--bad-config", bad, "--hit", "hit")
			if c.fails == "" {
				requirePass(t, results, 0)
				return
			}
			if _, ok := failed(results)[c.fails]; !ok {
				t.Errorf("%q passed; failed: %v", c.fails, failed(results))
			}
		})
	}
}

// Flags may come after the program, as in the guide; anything else there is
// refused, since a plugin gets no arguments.
func TestArguments(t *testing.T) {
	var stderr bytes.Buffer
	opt, err := parse([]string{"--", exampleProgram, "--hit", "x", "-env", "A", "-env", "B"}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if opt.hit != "x" || strings.Join(opt.env, ",") != "A,B" || !filepath.IsAbs(opt.program) {
		t.Fatalf("parsed %+v", opt)
	}
	if _, err := parse([]string{exampleProgram, "serve"}, &stderr); err == nil {
		t.Fatal("an argument for the plugin was taken")
	}
	if _, err := parse([]string{}, &stderr); err == nil {
		t.Fatal("no program was taken")
	}
}

// The exit code: 0 when all pass, 1 when one fails.
func TestExitCode(t *testing.T) {
	var out, errs bytes.Buffer
	if code := run([]string{"-config", exampleConfig(t), exampleProgram}, &out, &errs); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errs.String())
	}
	if !strings.Contains(out.String(), "skip  get: a hit") {
		t.Errorf("no skip for the missing -hit:\n%s", out.String())
	}
	t.Setenv("KIT_BREAK", "miss")
	out.Reset()
	if code := run([]string{"-env", "KIT_BREAK", "-config", writeJSON(t, map[string]any{"name": "broken"}), brokenProgram}, &out, &errs); code != 1 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}
