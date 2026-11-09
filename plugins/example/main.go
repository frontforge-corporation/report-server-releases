// Command reportd-secret-example is a small, read-only secret plugin for
// Frontforge Report Server. It answers references from a JSON file:
//
//	{
//	  "billing#password": {"value": "hunter2", "version": "3"},
//	  "token": {"value": "t0k3n", "expiresAt": "2030-01-01T00:00:00Z"},
//	  "forbidden": {"error": "denied"}
//	}
//
// An entry with "error" answers that error code instead of a value, so you
// can see what reportd does with each one. Its config names the file:
// {"file": "/plugins/secrets.json"}. Copy it and change lookup to read
// from wherever your secrets are.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
)

const version = "1.0.0"

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type entry struct {
	Value     string `json:"value"`
	Version   string `json:"version,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	Error     string `json:"error,omitempty"`
}

// protoError is an answer with an error code of the protocol.
type protoError struct{ Code, Message string }

func (e *protoError) Error() string { return e.Code + ": " + e.Message }

func fail(code, format string, a ...any) error {
	return &protoError{code, fmt.Sprintf(format, a...)}
}

// file is the secrets file init named.
var file string

func main() {
	log := func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 64<<10), 1<<20+2)
	out := json.NewEncoder(os.Stdout) // one line per answer
	for in.Scan() {
		var req request
		if err := json.Unmarshal(in.Bytes(), &req); err != nil {
			log("a request that isn't JSON: %v", err)
			continue
		}
		result, err := handle(req)
		answer := map[string]any{"id": req.ID, "result": result}
		var pe *protoError
		switch {
		case errors.As(err, &pe):
			answer = map[string]any{"id": req.ID, "error": map[string]string{"code": pe.Code, "message": pe.Message}}
		case err != nil:
			answer = map[string]any{"id": req.ID, "error": map[string]string{"code": "internal", "message": err.Error()}}
		}
		if err := out.Encode(answer); err != nil {
			log("writing an answer: %v", err)
			os.Exit(1)
		}
		if req.Method == "shutdown" {
			return
		}
	}
	// reportd closed our input: it is gone, so are we.
}

func handle(req request) (any, error) {
	var p struct {
		Config struct {
			File string `json:"file"`
		} `json:"config"`
		Ref string `json:"ref"`
	}
	if len(req.Params) > 0 && json.Unmarshal(req.Params, &p) != nil {
		return nil, fail("internal", "params of the wrong shape")
	}
	switch req.Method {
	case "hello":
		// This plugin speaks version 1 only. If reportd doesn't offer it,
		// reportd refuses the plugin and says why in its log.
		return map[string]any{"protocol": 1, "provider": "example", "version": version, "writable": false}, nil
	case "init":
		if p.Config.File == "" {
			return nil, fail("invalid-config", "config.file is required")
		}
		file = p.Config.File
		if _, err := read(); err != nil {
			return nil, fail("invalid-config", "%v", err)
		}
		fmt.Fprintf(os.Stderr, "example %s reading %s\n", version, file)
		return struct{}{}, nil
	case "get":
		return lookup(p.Ref)
	case "health":
		if _, err := read(); err != nil {
			return map[string]string{"state": "failing", "message": err.Error()}, nil
		}
		return map[string]string{"state": "ok"}, nil
	case "put", "replace", "delete":
		return nil, fail("not-writable", "the example plugin is read-only")
	case "shutdown":
		return struct{}{}, nil
	}
	return nil, fail("unsupported", "no method %s", req.Method)
}

// lookup answers get. Never log a value.
func lookup(ref string) (any, error) {
	if ref == "" || strings.IndexFunc(ref, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return nil, fail("invalid-ref", "a reference has no spaces or control characters")
	}
	secrets, err := read()
	if err != nil {
		return nil, fail("unavailable", "%v", err)
	}
	e, ok := secrets[ref]
	switch {
	case !ok:
		return nil, fail("not-found", "no secret %s", ref)
	case e.Error != "":
		return nil, fail(e.Error, "the file says %s for %s", e.Error, ref)
	}
	return e, nil
}

// read reads the file again each time, so reportd's next read sees an
// edit. (A relative path is from the working folder reportd gives it.)
func read() (map[string]entry, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("the secrets file: %w", err)
	}
	var secrets map[string]entry
	if err := json.Unmarshal(b, &secrets); err != nil {
		return nil, fmt.Errorf("the secrets file %s isn't JSON: %w", file, err)
	}
	return secrets, nil
}
