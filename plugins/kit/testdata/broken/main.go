// Command broken is the kit's test plugin: a good writable one, or, with
// KIT_BREAK set, one that breaks a single rule of the protocol. Its config
// must name itself ({"name": "broken"}); it holds the ref "hit".
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Protocol []int          `json:"protocol"`
		Config   map[string]any `json:"config"`
		Ref      string         `json:"ref"`
		Value    string         `json:"value"`
	} `json:"params"`
}

type secret struct{ value, version string }

var (
	mode = os.Getenv("KIT_BREAK")

	out    sync.Mutex
	mu     sync.Mutex
	store  = map[string]secret{"hit": {"the-hit-value", "1"}}
	nextID = 0
	// lastID is the id of the latest request read, for "shared-id".
	lastID json.RawMessage
)

func main() {
	if mode == "banner" {
		fmt.Println("broken plugin starting")
	}
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 64<<10), 2<<20)
	var wg sync.WaitGroup
	for in.Scan() {
		var req request
		if json.Unmarshal(in.Bytes(), &req) != nil {
			continue
		}
		mu.Lock()
		lastID = req.ID
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Gets of the hit are slower, so answers come out of order.
			if req.Method == "get" && req.Params.Ref == "hit" || mode == "shared-id" {
				time.Sleep(30 * time.Millisecond)
			}
			id := req.ID
			if mode == "shared-id" {
				mu.Lock()
				id = lastID
				mu.Unlock()
			}
			result, code := handle(req)
			msg := map[string]any{"id": id, "result": result}
			if code != "" {
				msg = map[string]any{"id": id, "error": map[string]string{"code": code, "message": code}}
			}
			b, _ := json.Marshal(msg)
			out.Lock()
			if req.Method == "shutdown" && mode == "no-newline" {
				os.Stdout.Write(b)
				os.Exit(0)
			}
			os.Stdout.Write(append(b, '\n'))
			out.Unlock()
			if req.Method == "shutdown" && mode != "shutdown" {
				os.Exit(0)
			}
		}()
	}
	wg.Wait()
	if mode == "input" {
		time.Sleep(time.Hour)
	}
}

func handle(req request) (any, string) {
	p := req.Params
	writable := mode != "readonly" && mode != "readonly-put"
	switch req.Method {
	case "hello":
		choice := 1
		if mode == "protocol" {
			choice = 2
		}
		if mode == "echo-version" && len(p.Protocol) > 0 {
			choice = p.Protocol[0]
		}
		h := map[string]any{"protocol": choice, "provider": "broken", "version": "1.0.0", "writable": writable}
		if mode == "hello-shape" {
			delete(h, "provider")
		}
		return h, ""
	case "init":
		if mode == "init" || p.Config["name"] != "broken" && mode != "bad-config" {
			return nil, "invalid-config"
		}
		return map[string]any{}, ""
	case "get":
		if strings.ContainsAny(p.Ref, " \x00") {
			if mode == "bad-ref" {
				return nil, "not-found"
			}
			return nil, "invalid-ref"
		}
		mu.Lock()
		s, ok := store[p.Ref]
		mu.Unlock()
		if !ok {
			if mode == "miss" {
				return nil, "internal"
			}
			return nil, "not-found"
		}
		if mode == "stderr-value" {
			fmt.Fprintln(os.Stderr, "answering", s.value)
		}
		if mode == "hit-shape" {
			return map[string]any{"value": 42}, ""
		}
		return map[string]any{"value": s.value, "version": s.version}, ""
	case "put", "replace", "delete":
		if !writable && mode != "readonly-put" {
			return nil, "not-writable"
		}
		return write(req.Method, p.Ref, p.Value)
	case "health":
		if mode == "health" {
			return map[string]any{"state": "fine"}, ""
		}
		return map[string]any{"state": "ok"}, ""
	case "shutdown":
		return map[string]any{}, ""
	}
	if mode == "unknown-method" {
		return map[string]any{}, ""
	}
	return nil, "unsupported"
}

func write(method, ref, value string) (any, string) {
	mu.Lock()
	defer mu.Unlock()
	switch method {
	case "put":
		nextID++
		ref = "id-" + strconv.Itoa(nextID)
		store[ref] = secret{value, "1"}
		return map[string]any{"ref": ref, "version": "1"}, ""
	case "replace":
		s, ok := store[ref]
		if !ok {
			return nil, "not-found"
		}
		if mode != "replace-version" {
			n, _ := strconv.Atoi(s.version)
			s.version = strconv.Itoa(n + 1)
		}
		store[ref] = secret{value, s.version}
		return map[string]any{"version": s.version}, ""
	}
	if mode != "delete" {
		delete(store, ref)
	}
	return map[string]any{}, ""
}
