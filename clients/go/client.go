package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// APIError is an answer that wasn't 2xx. The server answers errors as JSON:
// {"error": <kind>, "message": ...}, and a render the engine refused adds
// "diagnostics", each with code, severity, message and where in the
// template it is.
type APIError struct {
	Status      int
	Kind        string       `json:"error"`
	Message     string       `json:"message"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	RequestID   string
	RetryAfter  time.Duration
}

type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Node     string `json:"node"`
	Path     string `json:"path"`
}

func (e *APIError) Error() string { return fmt.Sprintf("%d %s: %s", e.Status, e.Kind, e.Message) }

func newAPIError(res *http.Response) *APIError {
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	e := &APIError{Status: res.StatusCode, RequestID: res.Header.Get("X-Request-Id")}
	if json.Unmarshal(body, e) != nil {
		e.Message = strings.TrimSpace(string(body))
	}
	if e.Kind == "" {
		e.Kind = fmt.Sprintf("http-%d", res.StatusCode)
	}
	if e.Message == "" {
		e.Message = http.StatusText(res.StatusCode)
	}
	if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil {
		e.RetryAfter = time.Duration(s) * time.Second
	}
	return e
}

// Client makes the few calls an application needs, over net/http.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// Template is an entry of GET /v1/templates. Published maps each
// environment the template is published in to its newest version there.
type Template struct {
	ID        string                     `json:"id"`
	Name      string                     `json:"name"`
	Folder    string                     `json:"folder"`
	Published map[string]json.RawMessage `json:"published"`
}

// Run is one render of a job: its datasets by name, and its params.
type Run struct {
	Datasets map[string]any `json:"datasets,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
}

// Job is the background render's state (GET /v1/jobs/{id}).
type Job struct {
	ID        string   `json:"id"`
	Status    string   `json:"status"` // queued, running, done, error or cancelled
	Error     string   `json:"error"`
	ReportIDs []string `json:"reportIds"`
	Progress  struct {
		Done  int `json:"done"`
		Total int `json:"total"`
	} `json:"progress"`
}

func (j *Job) Finished() bool {
	return j.Status == "done" || j.Status == "error" || j.Status == "cancelled"
}

// RenderRequest is the body of POST /v1/render.
type RenderRequest struct {
	TemplateID  string         `json:"templateId"`
	Environment string         `json:"environment,omitempty"`
	Format      string         `json:"format,omitempty"`
	Datasets    map[string]any `json:"datasets,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
	Options     map[string]any `json:"options,omitempty"`
}

// Rendered is a render's bytes and what the server says about them.
type Rendered struct {
	Body     []byte
	ReportID string // X-Report-Id: a PDF render is stored, and can be downloaded again
	Pages    string // X-Rfml-Pages
	Commit   string // X-Template-Commit: the published version that was used
	// Diagnostics are the render's warnings, such as data that doesn't
	// match the template's schema: X-Rfml-Diagnostics, JSON in unpadded
	// base64url.
	Diagnostics []Diagnostic
}

func requestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// do sends a request, waits out a rate limit (429) up to three times, and
// returns an *APIError for any other answer that isn't 2xx. The caller
// closes the body.
func (c *Client) do(ctx context.Context, method, path string, body any, header http.Header) (*http.Response, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		for k, v := range header {
			req.Header[k] = v
		}
		// An API key goes in the Authorization header as a bearer token.
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		// Our own request id, which the server logs and sends back: quote
		// it to whoever runs the server when something goes wrong.
		req.Header.Set("X-Request-Id", requestID())
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			return res, nil
		}
		apiErr := newAPIError(res)
		res.Body.Close()
		if res.StatusCode != http.StatusTooManyRequests || attempt == 3 {
			return nil, apiErr
		}
		wait := min(max(apiErr.RetryAfter, time.Second), 30*time.Second)
		fmt.Printf("  rate limited; trying again in %s\n", wait)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	return c.sendJSON(ctx, http.MethodGet, path, nil, nil, out)
}

func (c *Client) sendJSON(ctx context.Context, method, path string, body any, header http.Header, out any) error {
	res, err := c.do(ctx, method, path, body, header)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(out)
}

// Me is GET /v1/me: who the key is and what it may do.
type Me struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

func (c *Client) Me(ctx context.Context) (*Me, error) {
	var me Me
	return &me, c.getJSON(ctx, "/v1/me", &me)
}

func (c *Client) Templates(ctx context.Context) ([]Template, error) {
	var page struct {
		Templates []Template `json:"templates"`
	}
	return page.Templates, c.getJSON(ctx, "/v1/templates", &page)
}

// Render renders a published template straight away and returns its bytes.
func (c *Client) Render(ctx context.Context, r RenderRequest) (*Rendered, error) {
	res, err := c.do(ctx, http.MethodPost, "/v1/render", r, nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	out := &Rendered{
		Body:     b,
		ReportID: res.Header.Get("X-Report-Id"),
		Pages:    res.Header.Get("X-Rfml-Pages"),
		Commit:   res.Header.Get("X-Template-Commit"),
	}
	if h := res.Header.Get("X-Rfml-Diagnostics"); h != "" {
		raw, err := base64.RawURLEncoding.DecodeString(h)
		if err == nil {
			err = json.Unmarshal(raw, &out.Diagnostics)
		}
		if err != nil {
			return nil, fmt.Errorf("X-Rfml-Diagnostics: %w", err)
		}
	}
	return out, nil
}

// SubmitJob queues a background render, one render per run. The same
// idempotency key with the same request gets the same job back, so a
// submission that timed out can be sent again safely.
func (c *Client) SubmitJob(ctx context.Context, templateID, environment string, runs []Run, idempotencyKey string) (*Job, error) {
	body := map[string]any{"templateId": templateID, "format": "pdf", "runs": runs}
	if environment != "" {
		body["environment"] = environment
	}
	var job Job
	err := c.sendJSON(ctx, http.MethodPost, "/v1/jobs", body, http.Header{"Idempotency-Key": {idempotencyKey}}, &job)
	return &job, err
}

func (c *Client) Job(ctx context.Context, id string) (*Job, error) {
	var job Job
	return &job, c.getJSON(ctx, "/v1/jobs/"+id, &job)
}

// WaitForJob polls the job once a second until it has finished. A job can
// instead call your application back when it finishes: see callbackUrl in
// the API reference.
func (c *Client) WaitForJob(ctx context.Context, id string, progress func(*Job)) (*Job, error) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		job, err := c.Job(ctx, id)
		if err != nil {
			return nil, err
		}
		progress(job)
		if job.Finished() {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}

// Download reads a stored report.
func (c *Client) Download(ctx context.Context, reportID string) ([]byte, error) {
	res, err := c.do(ctx, http.MethodGet, "/v1/reports/"+reportID, nil, nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return io.ReadAll(res.Body)
}

// Event is one server-sent event of GET /v1/events.
type Event struct {
	Name string
	Data json.RawMessage
}

// Events calls fn with each event of GET /v1/events until the stream ends
// or ctx is done. An event is "event:" and "data:" lines ended by a blank
// line; lines starting with ":" are keep-alive comments (every 25 s). Use a
// client without a timeout: the stream stays open.
func (c *Client) Events(ctx context.Context, fn func(Event) bool) error {
	res, err := c.do(ctx, http.MethodGet, "/v1/events", nil, http.Header{"Accept": {"text/event-stream"}})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	sc := bufio.NewScanner(res.Body)
	name, data := "message", []string{}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if len(data) > 0 && !fn(Event{name, json.RawMessage(strings.Join(data, "\n"))}) {
				return nil
			}
			name, data = "message", data[:0]
		case strings.HasPrefix(line, ":"):
		default:
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			switch field {
			case "event":
				name = value
			case "data":
				data = append(data, value)
			}
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
