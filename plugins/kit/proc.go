package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Limits of the protocol: a line either way, and a value.
const (
	maxLine  = 1 << 20
	maxValue = 64 << 10
)

// timeouts are how long the kit waits, as reportd does.
type timeouts struct {
	hello, init, call, shutdown time.Duration
}

// protocolTimeouts are reportd's: hello 5 s, init 30 s (a sign-in), every
// other call 10 s, and 5 s to exit once asked.
var protocolTimeouts = timeouts{hello: 5 * time.Second, init: 30 * time.Second, call: 10 * time.Second, shutdown: 5 * time.Second}

// answer is one line a plugin wrote: a result or an error, never both.
type answer struct {
	ID     *int64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// proc is one run of the plugin's program, started as reportd starts it:
// in its working folder, with only the environment variables named, and
// no arguments.
type proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *buffer
	trace  func(dir string, line []byte)

	mu      sync.Mutex
	next    int64
	pending map[int64]chan answer
	late    map[int64]bool
	// junk is the first thing on stdout that isn't a protocol answer.
	junk error
	// done is closed once the program has exited and stdout is drained.
	done    chan struct{}
	exitErr error
}

// buffer is the plugin's stderr, kept whole for the stderr check.
type buffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func start(opt *options, dir string) (*proc, error) {
	cmd := exec.Command(opt.program)
	cmd.Dir = dir
	cmd.Env = []string{}
	for _, name := range opt.env {
		if v, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+v)
		}
	}
	p := &proc{cmd: cmd, stderr: &buffer{}, pending: map[int64]chan answer{}, late: map[int64]bool{}, done: make(chan struct{})}
	if opt.verbose {
		p.trace = func(dir string, line []byte) { fmt.Fprintf(opt.out, "      %s %s\n", dir, line) }
	}
	cmd.Stderr = p.stderr
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p.stdin = stdin
	go p.read(stdout)
	return p, nil
}

// read hands stdout's lines to their requests, by the rules reportd keeps:
// anything else ends the plugin.
func (p *proc) read(stdout io.Reader) {
	br := bufio.NewReaderSize(stdout, 64<<10)
	var err error
	for {
		var line []byte
		if line, err = readLine(br); err != nil {
			break
		}
		if p.trace != nil {
			p.trace("<-", line)
		}
		if err = p.dispatch(line); err != nil {
			break
		}
	}
	if !errors.Is(err, io.EOF) {
		p.mu.Lock()
		p.junk = err
		p.mu.Unlock()
		_ = p.cmd.Process.Kill()
		_, _ = io.Copy(io.Discard, stdout)
	}
	werr := p.cmd.Wait()
	p.mu.Lock()
	p.exitErr = werr
	for id, ch := range p.pending {
		close(ch)
		delete(p.pending, id)
	}
	p.mu.Unlock()
	close(p.done)
}

func readLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		frag, err := br.ReadSlice('\n')
		line = append(line, frag...)
		if len(line) > maxLine+2 {
			return nil, errors.New("a line over 1 MiB")
		}
		switch {
		case err == nil:
			line = bytes.TrimSuffix(line[:len(line)-1], []byte("\r"))
			if len(line) > maxLine {
				return nil, errors.New("a line over 1 MiB")
			}
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) > 0:
			return nil, fmt.Errorf("a last line without its newline: %s", cut(line))
		default:
			return nil, err
		}
	}
}

func (p *proc) dispatch(line []byte) error {
	var a answer
	dec := json.NewDecoder(bytes.NewReader(line))
	if err := dec.Decode(&a); err != nil || dec.More() {
		return fmt.Errorf("a line that isn't one JSON answer: %s", cut(line))
	}
	noResult := len(a.Result) == 0 || string(a.Result) == "null"
	switch {
	case a.ID == nil:
		return fmt.Errorf("an answer without an id: %s", cut(line))
	case a.Error == nil && noResult:
		return fmt.Errorf("an answer with neither a result nor an error: %s", cut(line))
	case a.Error != nil && !noResult:
		return fmt.Errorf("an answer with both a result and an error: %s", cut(line))
	case a.Error != nil && a.Error.Code == "":
		return fmt.Errorf("an error without a code: %s", cut(line))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	ch, ok := p.pending[*a.ID]
	switch {
	case ok:
		delete(p.pending, *a.ID)
		ch <- a
	case p.late[*a.ID]:
		delete(p.late, *a.ID)
	default:
		return fmt.Errorf("an answer to id %d, which wasn't sent or was answered already", *a.ID)
	}
	return nil
}

func cut(line []byte) string {
	s := string(line)
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return fmt.Sprintf("%q", s)
}

// send writes a request without waiting for its answer.
func (p *proc) send(method string, params any) (int64, chan answer, error) {
	p.mu.Lock()
	if p.ended() {
		p.mu.Unlock()
		return 0, nil, p.why()
	}
	p.next++
	id := p.next
	ch := make(chan answer, 1)
	p.pending[id] = ch
	p.mu.Unlock()
	line, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return 0, nil, err
	}
	if p.trace != nil {
		p.trace("->", line)
	}
	wrote := make(chan error, 1)
	go func() {
		_, err := p.stdin.Write(append(line, '\n'))
		wrote <- err
	}()
	select {
	case err := <-wrote:
		if err != nil {
			return 0, nil, fmt.Errorf("it isn't reading its input: %v", err)
		}
	case <-time.After(protocolTimeouts.call):
		return 0, nil, errors.New("it stopped reading its input")
	}
	return id, ch, nil
}

// wait is the answer to request id, or why there is none.
func (p *proc) wait(id int64, ch chan answer, method string, timeout time.Duration) (answer, error) {
	select {
	case a, ok := <-ch:
		if !ok {
			return answer{}, fmt.Errorf("it ended before it answered %s: %v", method, p.why())
		}
		return a, nil
	case <-time.After(timeout):
		p.mu.Lock()
		delete(p.pending, id)
		p.late[id] = true
		p.mu.Unlock()
		return answer{}, fmt.Errorf("it didn't answer %s within %s", method, timeout)
	}
}

func (p *proc) call(method string, params any, timeout time.Duration) (answer, error) {
	id, ch, err := p.send(method, params)
	if err != nil {
		return answer{}, err
	}
	return p.wait(id, ch, method, timeout)
}

// ended reports whether the program has ended; p.mu is held.
func (p *proc) ended() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// why is why the program ended; p.mu is held, or the run is done.
func (p *proc) why() error {
	switch {
	case p.junk != nil:
		return fmt.Errorf("it broke the protocol on stdout: %v", p.junk)
	case p.exitErr != nil:
		return fmt.Errorf("it exited (%v)", p.exitErr)
	}
	return errors.New("it exited")
}

// exited waits up to d for the program to end.
func (p *proc) exited(d time.Duration) bool {
	select {
	case <-p.done:
		return true
	case <-time.After(d):
		return false
	}
}

// stop ends the run the way reportd does: shutdown, close its input, and
// kill it unless it has exited in time.
func (p *proc) stop(t timeouts) {
	if !p.exited(0) {
		_, _ = p.call("shutdown", map[string]any{}, t.shutdown)
	}
	p.kill(t.shutdown)
}

// kill closes the program's input and kills it unless it exits within d.
func (p *proc) kill(d time.Duration) {
	_ = p.stdin.Close()
	if !p.exited(d) {
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

// junkSeen is the protocol break on stdout, once the run is done.
func (p *proc) junkSeen() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.junk
}

// stderrHolds names the values found in what the plugin logged.
func (p *proc) stderrHolds(values map[string]string) []string {
	log := p.stderr.String()
	var found []string
	for v, what := range values {
		if len(v) >= 4 && strings.Contains(log, v) {
			found = append(found, what)
		}
	}
	return found
}
