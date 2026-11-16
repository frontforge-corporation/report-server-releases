// A sample client for the Frontforge Report Server HTTP API.
//
// It authenticates with an API key, lists the templates, renders one PDF
// straight away (POST /v1/render), renders a batch in the background
// (POST /v1/jobs), waits for that job and downloads what it made, and shows
// what an error looks like. `go run . events` listens to GET /v1/events
// instead.
//
// Standard library only. It is configured by environment variables: see
// README.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func env(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func main() {
	key := env("REPORT_SERVER_API_KEY", "")
	if key == "" {
		fmt.Fprintln(os.Stderr, "Set REPORT_SERVER_API_KEY to an API key (Admin > API keys).")
		os.Exit(2)
	}
	c := &Client{BaseURL: env("REPORT_SERVER_URL", "http://localhost:43123"), APIKey: key, HTTP: &http.Client{}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	switch args := os.Args[1:]; {
	case len(args) == 0:
		err = run(ctx, c)
	case len(args) == 1 && args[0] == "events":
		err = listen(ctx, c)
	default:
		fmt.Fprintf(os.Stderr, "usage: %s [events]\n", os.Args[0])
		os.Exit(2)
	}
	var apiErr *APIError
	switch {
	case err == nil, errors.Is(err, context.Canceled):
	case errors.As(err, &apiErr):
		printError(apiErr)
		os.Exit(1)
	default:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// exampleName is the gallery example letters.json is for. A report copied
// from it keeps this name, but not always one id: New report in the studio
// makes the id from the title (letter-to-customers), while an empty
// catalog's "Start from an example" keeps the example's own
// (customer-letter), and either ends up with another when that one is
// taken. GET /v1/templates doesn't say which example a template came from,
// so without REPORT_TEMPLATE the samples look for this name.
const exampleName = "Letter to customers"

// pickTemplate returns the template to render, REPORT_TEMPLATE's or else the
// one published as exampleName, and a line to print; or "" and why not.
func pickTemplate(templates []Template, wanted, environment string) (string, string) {
	where := ""
	if environment != "" {
		where = " in " + environment
	}
	published := func(t Template) bool {
		if environment != "" {
			_, ok := t.Published[environment]
			return ok
		}
		return len(t.Published) > 0
	}
	if wanted != "" {
		for _, t := range templates {
			if t.ID != wanted {
				continue
			}
			if !published(t) {
				return "", fmt.Sprintf("%s isn't published%s. Publish it in the studio.", wanted, where)
			}
			return wanted, fmt.Sprintf("Using %s, from REPORT_TEMPLATE.", wanted)
		}
		return "", fmt.Sprintf("There's no template %s. Set REPORT_TEMPLATE to one of the ids above.", wanted)
	}
	var named, live []Template
	for _, t := range templates {
		if strings.EqualFold(strings.TrimSpace(t.Name), exampleName) {
			named = append(named, t)
			if published(t) {
				live = append(live, t)
			}
		}
	}
	switch {
	case len(named) == 0:
		return "", fmt.Sprintf("No template is called %[1]s. In the studio, choose New report, then From a template, "+
			"pick %[1]s and publish it; or set REPORT_TEMPLATE to another template's id.", exampleName)
	case len(live) == 0:
		ids := make([]string, len(named))
		for i, t := range named {
			ids[i] = t.ID
		}
		return "", fmt.Sprintf("No template called %s is published%s (%s). Publish it in the studio, or set REPORT_TEMPLATE.",
			exampleName, where, strings.Join(ids, ", "))
	case len(live) > 1:
		return live[0].ID, fmt.Sprintf("Using %s, the first of %d published templates called %s. Set REPORT_TEMPLATE to choose another.",
			live[0].ID, len(live), exampleName)
	}
	return live[0].ID, fmt.Sprintf("Using %s, the published template called %s.", live[0].ID, exampleName)
}

func run(ctx context.Context, c *Client) error {
	environment := env("REPORT_ENVIRONMENT", "")
	out := env("REPORT_OUT", "out")
	// One element per run: each is that run's datasets, by dataset name.
	var runs []map[string]any
	raw, err := os.ReadFile(env("REPORT_DATA", "letters.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &runs); err != nil || len(runs) == 0 {
		return fmt.Errorf("the data file must be a JSON array of runs' datasets: %v", err)
	}

	// Each request gets its own deadline; the stream in listen has none.
	c.HTTP.Timeout = 2 * time.Minute

	me, err := c.Me(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Authenticated as %s %s: %s\n", me.Kind, me.Name, strings.Join(me.Permissions, ", "))

	fmt.Println("\nTemplates:")
	templates, err := c.Templates(ctx)
	if err != nil {
		return err
	}
	for _, t := range templates {
		where := make([]string, 0, len(t.Published))
		for e := range t.Published {
			where = append(where, e)
		}
		sort.Strings(where)
		if len(where) == 0 {
			where = []string{"not published"}
		}
		fmt.Printf("  %-24s %s (%s)\n", t.ID, t.Name, strings.Join(where, ", "))
	}
	templateID, line := pickTemplate(templates, env("REPORT_TEMPLATE", ""), environment)
	if templateID == "" {
		return errors.New("\n" + line)
	}
	fmt.Println("\n" + line)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	fmt.Printf("\n1. Rendering %s now (POST /v1/render)\n", templateID)
	r, err := c.Render(ctx, RenderRequest{TemplateID: templateID, Environment: environment, Format: "pdf", Datasets: runs[0]})
	if err != nil {
		return err
	}
	path := filepath.Join(out, templateID+".pdf")
	if err := os.WriteFile(path, r.Body, 0o644); err != nil {
		return err
	}
	fmt.Printf("  wrote %s: %d bytes, %s page(s), report %s, template commit %.8s\n", path, len(r.Body), r.Pages, r.ReportID, r.Commit)
	// A render that succeeds can still have warnings, such as data that
	// doesn't match the template's schema.
	for _, d := range r.Diagnostics {
		fmt.Printf("  %s %s: %s\n", d.Severity, d.Code, d.Message)
	}

	fmt.Printf("\n2. Rendering %d in the background (POST /v1/jobs)\n", len(runs))
	jobRuns := make([]Run, len(runs))
	for i, d := range runs {
		jobRuns[i] = Run{Datasets: d}
	}
	job, err := c.SubmitJob(ctx, templateID, environment, jobRuns, requestID())
	if err != nil {
		return err
	}
	fmt.Printf("  job %s is %s\n", job.ID, job.Status)
	seen := ""
	job, err = c.WaitForJob(ctx, job.ID, func(j *Job) {
		if s := fmt.Sprintf("%s: %d of %d rendered", j.Status, j.Progress.Done, j.Progress.Total); s != seen {
			fmt.Println("  " + s)
			seen = s
		}
	})
	if err != nil {
		return err
	}
	if job.Status != "done" {
		return fmt.Errorf("the job ended %s: %s", job.Status, job.Error)
	}
	for n, id := range job.ReportIDs {
		pdf, err := c.Download(ctx, id)
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden {
			return errors.New("the key may render but not download stored reports: give it report.read (see README.md)")
		} else if err != nil {
			return err
		}
		path := filepath.Join(out, fmt.Sprintf("%s-%d.pdf", templateID, n+1))
		if err := os.WriteFile(path, pdf, 0o644); err != nil {
			return err
		}
		fmt.Printf("  wrote %s: %d bytes\n", path, len(pdf))
	}

	// By default, data that breaks the template's schema is only a warning,
	// and what can't be bound renders empty; strict makes it an error.
	fmt.Println("\n3. Rendering empty data in strict schema mode, to show an error")
	empty := map[string]any{}
	for name := range runs[0] {
		empty[name] = map[string]any{}
	}
	_, err = c.Render(ctx, RenderRequest{TemplateID: templateID, Environment: environment, Datasets: empty,
		Options: map[string]any{"schemaMode": "strict"}})
	var apiErr *APIError
	switch {
	case errors.As(err, &apiErr):
		printError(apiErr)
	case err != nil:
		return err
	default:
		fmt.Println("  it rendered anyway")
	}
	return nil
}

// listen prints the server's events until the sign-in ends or Ctrl+C. When
// the server restarts, the stream ends after a `restarting` event: connect
// again, as a browser's EventSource does by itself.
func listen(ctx context.Context, c *Client) error {
	fmt.Println("Listening to GET /v1/events; Ctrl+C stops.")
	for {
		signedOut := false
		err := c.Events(ctx, func(e Event) bool {
			fmt.Printf("  %s: %s\n", e.Name, e.Data)
			signedOut = e.Name == "signed-out"
			return !signedOut
		})
		var apiErr *APIError
		switch {
		case signedOut || errors.As(err, &apiErr) || ctx.Err() != nil:
			return err
		case err != nil:
			fmt.Printf("  the stream broke (%v); connecting again\n", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

func printError(e *APIError) {
	fmt.Printf("  %d %s: %s\n", e.Status, e.Kind, e.Message)
	if e.RequestID != "" {
		fmt.Printf("  request id %s\n", e.RequestID)
	}
	shown := 0
	for _, d := range e.Diagnostics {
		if d.Severity != "error" || shown == 5 {
			continue
		}
		where := d.Node
		if where == "" {
			where = d.Path
		}
		fmt.Printf("  - %s at %s: %s\n", d.Code, where, d.Message)
		shown++
	}
	if rest := len(e.Diagnostics) - shown; rest > 0 {
		fmt.Printf("  ... and %d more diagnostics (warnings included)\n", rest)
	}
}
