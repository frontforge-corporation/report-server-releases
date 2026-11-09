package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"time"
)

// exchangesJSON is the protocol as exchanges, the same file reportd's own
// tests replay against their fake plugin; its "about" says how each is read.
//
//go:embed exchanges.json
var exchangesJSON []byte

type exchange struct {
	Name string `json:"name"`
	Send struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	} `json:"send"`
	Answer map[string]any `json:"answer"`
}

type exchanges struct {
	Protocol int    `json:"protocol"`
	Reportd  string `json:"reportd"`
	Scripts  map[string]struct {
		HostOnly  bool       `json:"hostOnly"`
		Exchanges []exchange `json:"exchanges"`
	} `json:"scripts"`
}

func readExchanges() exchanges {
	var x exchanges
	if err := json.Unmarshal(exchangesJSON, &x); err != nil {
		panic("exchanges.json: " + err.Error())
	}
	return x
}

// replayable are the scripts a plugin can be held to; the others drive a
// fake plugin's misbehaviour in reportd's own tests.
func replayable() []string {
	var names []string
	for name, s := range readExchanges().Scripts {
		if !s.HostOnly {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// replay sends each exchange of the script, in order, and checks the
// plugin's answer against the script's. Gets in a row go out together, so
// their answers may come in any order. What is the plugin's own (hello's
// name, version, feature and writable; health; error messages; the refs
// and versions a store makes) is checked for shape only.
func (r *runner) replay(name string) {
	x := readExchanges()
	script := x.Scripts[name]
	p, err := r.start()
	if err != nil {
		r.add("replay "+name, fmt.Errorf("it doesn't start: %v", err))
		return
	}
	writable := false
	refs := map[string]string{}     // the script's ref → the plugin's, from put
	versions := map[string]string{} // the plugin's ref → its version
	list := script.Exchanges
	for i := 0; i < len(list); {
		n := 1
		for list[i].Send.Method == "get" && i+n < len(list) && list[i+n].Send.Method == "get" {
			n++
		}
		group := list[i : i+n]
		i += n
		type sent struct {
			id  int64
			ch  chan answer
			err error
		}
		var out []sent
		for _, ex := range group {
			for _, v := range values(ex) {
				r.values[v] = "a value of the exchange " + ex.Name
			}
			id, ch, err := p.send(ex.Send.Method, r.replayParams(x, ex, refs))
			out = append(out, sent{id, ch, err})
		}
		for k, ex := range group {
			err := out[k].err
			var a answer
			if err == nil {
				a, err = p.wait(out[k].id, out[k].ch, ex.Send.Method, r.timeout(ex.Send.Method))
			}
			if err == nil {
				err = r.match(x, ex, a, &writable, refs, versions)
			}
			if err == nil && ex.Send.Method == "shutdown" && !p.exited(r.opt.timeouts.shutdown) {
				err = fmt.Errorf("it didn't exit within %s of shutdown", r.opt.timeouts.shutdown)
			}
			r.add("replay "+name+": "+ex.Name, err)
		}
	}
	p.stop(r.opt.timeouts)
}

// replayParams are what reportd would send for ex: its params, with the
// fields reportd always sends, and the refs a put made.
func (r *runner) replayParams(x exchanges, ex exchange, refs map[string]string) map[string]any {
	params := map[string]any{}
	switch ex.Send.Method {
	case "hello":
		params = r.helloParams(x.Protocol)
		params["reportd"] = x.Reportd
	case "init":
		config, _ := ex.Send.Params["config"].(map[string]any)
		if r.opt.config != nil {
			config = r.opt.config
		}
		params = r.initParams(config)
	}
	for k, v := range ex.Send.Params {
		if _, set := params[k]; set && (ex.Send.Method == "init" || ex.Send.Method == "hello" && k == "protocol") {
			continue
		}
		if s, ok := v.(string); ok && refs[s] != "" {
			v = refs[s]
		}
		params[k] = v
	}
	return params
}

func (r *runner) timeout(method string) time.Duration {
	switch method {
	case "hello":
		return r.opt.timeouts.hello
	case "init":
		return r.opt.timeouts.init
	case "shutdown":
		return r.opt.timeouts.shutdown
	}
	return r.opt.timeouts.call
}

// match checks a against ex's answer.
func (r *runner) match(x exchanges, ex exchange, a answer, writable *bool, refs, versions map[string]string) error {
	want := ex.Answer
	method := ex.Send.Method
	if !*writable && (method == "put" || method == "replace" || method == "delete") {
		want = map[string]any{"error": map[string]any{"code": "not-writable"}}
	}
	if e, ok := want["error"].(map[string]any); ok {
		return isError(a, e["code"].(string))
	}
	wantResult, _ := want["result"].(map[string]any)
	switch method {
	case "hello":
		h, err := checkHello(a)
		if err == nil && h.Protocol != x.Protocol {
			err = fmt.Errorf("it chose protocol %d", h.Protocol)
		}
		*writable = h.Writable
		return err
	case "health":
		return checkHealth(a)
	case "put":
		var put struct{ Ref, Version string }
		if err := decode(a, &put); err != nil {
			return err
		}
		if put.Ref == "" || put.Version == "" {
			return errors.New("its result needs a ref and a version")
		}
		if ref, _ := wantResult["ref"].(string); ref != "" {
			refs[ref] = put.Ref
		}
		versions[put.Ref] = put.Version
		return nil
	case "replace":
		var rep struct{ Version string }
		if err := decode(a, &rep); err != nil {
			return err
		}
		ref, _ := ex.Send.Params["ref"].(string)
		if old := versions[refs[ref]]; rep.Version == "" || rep.Version == old {
			return fmt.Errorf("it answered version %q: a replace needs a new version", rep.Version)
		}
		return nil
	}
	var got map[string]any
	if err := decode(a, &got); err != nil {
		return err
	}
	for _, k := range slices.Sorted(maps.Keys(wantResult)) {
		if !reflect.DeepEqual(wantResult[k], got[k]) {
			return fmt.Errorf("its result has %s %v, want %v", k, show(got[k]), show(wantResult[k]))
		}
	}
	return nil
}

func show(v any) string {
	if v == nil {
		return "none"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// values are the secret values ex sends or answers.
func values(ex exchange) []string {
	var out []string
	if v, ok := ex.Send.Params["value"].(string); ok {
		out = append(out, v)
	}
	if res, ok := ex.Answer["result"].(map[string]any); ok {
		if v, ok := res["value"].(string); ok {
			out = append(out, v)
		}
	}
	return out
}
