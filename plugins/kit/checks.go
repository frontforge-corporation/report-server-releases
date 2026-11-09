package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// result is one check's outcome: ok, failed (err) or skipped (why).
type result struct {
	name    string
	err     error
	skipped string
}

// runner runs the checks against one plugin.
type runner struct {
	opt     *options
	dir     string
	results []result
	procs   []*proc
	// values are every value the test read or wrote, and what each was,
	// for the stderr check.
	values map[string]string
}

func newRunner(opt *options) (*runner, error) {
	dir, err := os.MkdirTemp("", "rf-plugin-kit-")
	if err != nil {
		return nil, err
	}
	return &runner{opt: opt, dir: dir, values: map[string]string{}}, nil
}

func (r *runner) close() { _ = os.RemoveAll(r.dir) }

func (r *runner) add(name string, err error) bool {
	r.results = append(r.results, result{name: name, err: err})
	return err == nil
}

func (r *runner) skip(name, why string) {
	r.results = append(r.results, result{name: name, skipped: why})
}

func (r *runner) start() (*proc, error) {
	p, err := start(r.opt, r.dir)
	if err == nil {
		r.procs = append(r.procs, p)
	}
	return p, err
}

// hello is what reportd sends first.
func (r *runner) helloParams(protocols ...int) map[string]any {
	return map[string]any{"protocol": protocols, "reportd": "0.0.0-kit"}
}

func (r *runner) initParams(config map[string]any) map[string]any {
	if config == nil {
		config = map[string]any{}
	}
	return map[string]any{"config": config, "dataDir": r.dir}
}

// checks runs every check of the protocol.
func (r *runner) checks() {
	t := r.opt.timeouts
	names := []string{"hello", "init", "get: a hit", "get: a miss", "get: a bad reference", "pipelining", "unknown method", "health", "writes", "shutdown", "version choice", "bad config", "input closed"}
	skipRest := func(from int, why string) {
		for _, name := range names[from:] {
			r.skip(name, why)
		}
	}
	p, err := r.start()
	if err != nil {
		r.add("hello", fmt.Errorf("it doesn't start: %v", err))
		skipRest(1, "it doesn't start")
		return
	}
	a, err := p.call("hello", r.helloParams(1), t.hello)
	var hello helloResult
	if err == nil {
		hello, err = checkHello(a)
	}
	if !r.add("hello", err) {
		p.kill(0)
		skipRest(1, "hello failed")
		return
	}
	a, err = p.call("init", r.initParams(r.opt.config), t.init)
	if err == nil {
		err = isResult(a)
	}
	if !r.add("init", err) {
		p.stop(t)
		skipRest(2, "init failed")
		return
	}
	var hit string
	if r.opt.hit == "" {
		r.skip("get: a hit", "no -hit reference given")
	} else {
		a, err = p.call("get", map[string]any{"ref": r.opt.hit}, t.call)
		if err == nil {
			hit, err = checkGet(a)
			r.values[hit] = "the value get answered for -hit"
		}
		r.add("get: a hit", err)
	}
	a, err = p.call("get", map[string]any{"ref": r.opt.miss}, t.call)
	if err == nil {
		err = isError(a, "not-found")
	}
	r.add("get: a miss", err)
	a, err = p.call("get", map[string]any{"ref": r.opt.badRef}, t.call)
	if err == nil {
		err = isError(a, "invalid-ref")
	}
	r.add("get: a bad reference", err)
	r.add("pipelining", r.pipelining(p, hit))
	a, err = p.call("kit-no-such-method", map[string]any{}, t.call)
	if err == nil {
		err = isError(a, "unsupported")
	}
	r.add("unknown method", err)
	a, err = p.call("health", map[string]any{}, t.call)
	if err == nil {
		err = checkHealth(a)
	}
	r.add("health", err)
	r.add("writes", r.writes(p, hello.Writable))
	r.add("shutdown", r.shutdown(p))

	r.versionChoice()
	r.badConfig()
	r.inputClosed()
}

// pipelining sends several gets before reading any answer: each answer
// must carry its own request's id, in any order.
func (r *runner) pipelining(p *proc, hit string) error {
	type sent struct {
		id  int64
		ch  chan answer
		hit bool
	}
	var all []sent
	for i := range 8 {
		// Misses of its own shape when -miss gives one: a plugin whose refs
		// have a fixed form calls anything else a bad reference.
		ref, isHit := fmt.Sprintf("%s-%d", r.opt.miss, i), false
		if r.opt.missGiven {
			ref = r.opt.miss
		}
		if hit != "" && i%2 == 0 {
			ref, isHit = r.opt.hit, true
		}
		id, ch, err := p.send("get", map[string]any{"ref": ref})
		if err != nil {
			return err
		}
		all = append(all, sent{id, ch, isHit})
	}
	deadline := time.Now().Add(r.opt.timeouts.call)
	for _, s := range all {
		a, err := p.wait(s.id, s.ch, "get", time.Until(deadline))
		if err != nil {
			return fmt.Errorf("with 8 requests in flight, %v", err)
		}
		if s.hit {
			v, err := checkGet(a)
			if err == nil && v != hit {
				err = errors.New("the value isn't the one it answered alone")
			}
			if err != nil {
				return fmt.Errorf("request %d, a get of -hit: %v", s.id, err)
			}
		} else if err := isError(a, "not-found"); err != nil {
			return fmt.Errorf("request %d, a get of a miss: %v", s.id, err)
		}
	}
	return nil
}

// writes runs put, replace and delete: the whole cycle for a writable
// plugin, which makes its own refs and versions; not-writable for the rest.
func (r *runner) writes(p *proc, writable bool) error {
	t := r.opt.timeouts
	v1, v2 := r.newValue("the value put wrote"), r.newValue("the value replace wrote")
	owner := map[string]any{"connection": "kit", "environment": "test", "field": "password"}
	if !writable {
		ref := r.opt.hit
		if ref == "" {
			ref = r.opt.miss
		}
		for _, w := range []struct {
			method string
			params map[string]any
		}{
			{"put", map[string]any{"value": v1, "owner": owner}},
			{"replace", map[string]any{"ref": ref, "value": v2}},
			{"delete", map[string]any{"ref": ref}},
		} {
			a, err := p.call(w.method, w.params, t.call)
			if err == nil {
				err = isError(a, "not-writable")
			}
			if err != nil {
				return fmt.Errorf("its hello says writable: false, so %s: %v", w.method, err)
			}
		}
		return nil
	}
	a, err := p.call("put", map[string]any{"value": v1, "owner": owner}, t.call)
	var put struct{ Ref, Version string }
	if err == nil {
		err = decode(a, &put)
	}
	if err == nil && (put.Ref == "" || put.Version == "") {
		err = errors.New("its result needs a ref and a version")
	}
	if err != nil {
		return fmt.Errorf("put: %v", err)
	}
	get := func(step, want, version string) error {
		a, err := p.call("get", map[string]any{"ref": put.Ref}, t.call)
		var got struct{ Value, Version string }
		if err == nil {
			err = decode(a, &got)
		}
		switch {
		case err != nil:
		case got.Value != want:
			err = errors.New("it answered another value")
		case got.Version != version:
			err = fmt.Errorf("it answered version %q, and %s answered %q", got.Version, step, version)
		}
		if err != nil {
			return fmt.Errorf("get after %s: %v", step, err)
		}
		return nil
	}
	if err := get("put", v1, put.Version); err != nil {
		return err
	}
	a, err = p.call("replace", map[string]any{"ref": put.Ref, "value": v2}, t.call)
	var replaced struct{ Version string }
	if err == nil {
		err = decode(a, &replaced)
	}
	if err == nil && (replaced.Version == "" || replaced.Version == put.Version) {
		err = fmt.Errorf("it answered version %q: a replace needs a new version, so reportd opens the connection again", replaced.Version)
	}
	if err != nil {
		return fmt.Errorf("replace: %v", err)
	}
	if err := get("replace", v2, replaced.Version); err != nil {
		return err
	}
	a, err = p.call("delete", map[string]any{"ref": put.Ref}, t.call)
	if err == nil {
		err = isResult(a)
	}
	if err != nil {
		return fmt.Errorf("delete: %v", err)
	}
	a, err = p.call("get", map[string]any{"ref": put.Ref}, t.call)
	if err == nil {
		err = isError(a, "not-found")
	}
	if err != nil {
		return fmt.Errorf("get after delete: %v", err)
	}
	return nil
}

// shutdown asks the plugin to end: it answers, then exits by itself.
func (r *runner) shutdown(p *proc) error {
	t := r.opt.timeouts
	sentAt := time.Now()
	a, err := p.call("shutdown", map[string]any{}, t.shutdown)
	if err == nil {
		err = isResult(a)
	}
	if err == nil && !p.exited(t.shutdown-time.Since(sentAt)) {
		err = fmt.Errorf("it didn't exit within %s of shutdown", t.shutdown)
	}
	p.kill(0)
	return err
}

// versionChoice offers only a version that doesn't exist: the plugin
// mustn't choose it. reportd then refuses it, and says so in its log.
func (r *runner) versionChoice() {
	p, err := r.start()
	if err != nil {
		r.add("version choice", err)
		return
	}
	defer p.kill(0)
	a, err := p.call("hello", r.helloParams(99), r.opt.timeouts.hello)
	if err == nil && a.Error == nil {
		var hello struct{ Protocol int }
		if decode(a, &hello) == nil && hello.Protocol == 99 {
			err = errors.New("offered only protocol 99, it chose 99: choose a version from the list, or answer the one you speak")
		}
	}
	r.add("version choice", err)
}

// badConfig sends -bad-config: init must refuse it.
func (r *runner) badConfig() {
	if r.opt.badConfig == nil {
		r.skip("bad config", "no -bad-config file given")
		return
	}
	p, err := r.start()
	if err == nil {
		defer p.stop(r.opt.timeouts)
		var a answer
		if a, err = p.call("hello", r.helloParams(1), r.opt.timeouts.hello); err == nil {
			a, err = p.call("init", r.initParams(r.opt.badConfig), r.opt.timeouts.init)
		}
		if err == nil {
			err = isError(a, "invalid-config")
		}
	}
	r.add("bad config", err)
}

// inputClosed starts the plugin and closes its input: it must exit, as it
// must when reportd ends without a word.
func (r *runner) inputClosed() {
	p, err := r.start()
	if err == nil {
		if _, err = p.call("hello", r.helloParams(1), r.opt.timeouts.hello); err == nil {
			_, err = p.call("init", r.initParams(r.opt.config), r.opt.timeouts.init)
		}
		_ = p.stdin.Close()
		if err == nil && !p.exited(r.opt.timeouts.shutdown) {
			err = fmt.Errorf("it didn't exit within %s of its input closing", r.opt.timeouts.shutdown)
		}
		p.kill(0)
	}
	r.add("input closed", err)
}

// streams checks what every run wrote: nothing but protocol lines on
// stdout, and no value on stderr.
func (r *runner) streams() {
	var junk error
	var found []string
	for _, p := range r.procs {
		p.kill(0)
		if j := p.junkSeen(); j != nil && junk == nil {
			junk = j
		}
		found = append(found, p.stderrHolds(r.values)...)
	}
	r.add("stdout", junk)
	var err error
	if len(found) > 0 {
		err = fmt.Errorf("its log holds %s; a value must never reach stderr", found[0])
	}
	r.add("stderr", err)
}

func (r *runner) newValue(what string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	v := "kit-value-" + hex.EncodeToString(b)
	r.values[v] = what
	return v
}

type helloResult struct {
	Protocol int
	Provider string
	Version  string
	Feature  string
	Writable bool
}

func checkHello(a answer) (helloResult, error) {
	var h helloResult
	if err := decode(a, &h); err != nil {
		return h, err
	}
	switch {
	case h.Protocol != 1:
		return h, fmt.Errorf("offered protocol [1], it chose %d", h.Protocol)
	case h.Provider == "":
		return h, errors.New("its result needs provider, the plugin's name")
	case h.Version == "":
		return h, errors.New("its result needs version, the plugin's own")
	}
	return h, nil
}

func checkGet(a answer) (string, error) {
	var g struct {
		Value     *string
		Version   string
		ExpiresAt string
	}
	if err := decode(a, &g); err != nil {
		return "", err
	}
	switch {
	case g.Value == nil || *g.Value == "":
		return "", errors.New("its result needs a value; answer not-found when there is none")
	case len(*g.Value) > maxValue:
		return "", errors.New("the value is over 64 KiB")
	}
	if err := isTime("expiresAt", g.ExpiresAt); err != nil {
		return "", err
	}
	return *g.Value, nil
}

func checkHealth(a answer) error {
	var h struct {
		State     string
		Message   string
		ExpiresAt string
	}
	if err := decode(a, &h); err != nil {
		return err
	}
	switch h.State {
	case "ok", "degraded", "failing":
	default:
		return fmt.Errorf("state %q is none of ok, degraded and failing", h.State)
	}
	return isTime("expiresAt", h.ExpiresAt)
}

func isTime(field, v string) error {
	if v == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		return fmt.Errorf("%s %q is not an RFC 3339 time", field, v)
	}
	return nil
}

// decode reads a's result into out, strictly as to types.
func decode(a answer, out any) error {
	if err := isResult(a); err != nil {
		return err
	}
	if err := json.Unmarshal(a.Result, out); err != nil {
		return fmt.Errorf("a result of the wrong shape: %v", err)
	}
	return nil
}

func isResult(a answer) error {
	if a.Error != nil {
		return fmt.Errorf("it answered %s (%s), want a result", a.Error.Code, a.Error.Message)
	}
	if a.Result[0] != '{' {
		return errors.New("its result isn't an object")
	}
	return nil
}

func isError(a answer, code string) error {
	switch {
	case a.Error == nil:
		return fmt.Errorf("it answered a result, want %s", code)
	case a.Error.Code != code:
		return fmt.Errorf("it answered %s, want %s", a.Error.Code, code)
	}
	return nil
}
