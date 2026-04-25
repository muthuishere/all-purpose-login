package setup

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Emitter receives lifecycle events from the runner. Implementations:
//   - JSONEmitter: NDJSON, one event per line, for LLM/automation consumers.
//   - InteractiveEmitter: human-readable prose on stdout.
type Emitter interface {
	StepStarted(step string)
	StepDone(step string, output map[string]string)
	AwaitingHuman(step, url, message, resumeCmd string)
	AwaitingInput(step, url, message string, fields []string, resumeCmd string)
	StepFailed(step, reason string, recoverable bool)
	Completed(label string, summary map[string]string)
}

// JSONEmitter writes NDJSON events to w.
type JSONEmitter struct {
	mu sync.Mutex
	w  io.Writer
}

func NewJSONEmitter(w io.Writer) *JSONEmitter { return &JSONEmitter{w: w} }

func (j *JSONEmitter) write(ev map[string]any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	b, _ := json.Marshal(ev)
	fmt.Fprintln(j.w, string(b))
}

func (j *JSONEmitter) StepStarted(step string) {
	j.write(map[string]any{"event": "step_started", "step": step})
}
func (j *JSONEmitter) StepDone(step string, output map[string]string) {
	ev := map[string]any{"event": "step_done", "step": step}
	if len(output) > 0 {
		ev["output"] = output
	}
	j.write(ev)
}
func (j *JSONEmitter) AwaitingHuman(step, url, message, resumeCmd string) {
	j.write(map[string]any{
		"event":          "awaiting_human",
		"step":           step,
		"url":            url,
		"instructions":   message,
		"resume_command": resumeCmd,
	})
}
func (j *JSONEmitter) AwaitingInput(step, url, message string, fields []string, resumeCmd string) {
	j.write(map[string]any{
		"event":           "awaiting_input",
		"step":            step,
		"url":             url,
		"instructions":    message,
		"required_fields": fields,
		"resume_command":  resumeCmd,
	})
}
func (j *JSONEmitter) StepFailed(step, reason string, recoverable bool) {
	j.write(map[string]any{
		"event":       "step_failed",
		"step":        step,
		"reason":      reason,
		"recoverable": recoverable,
	})
}
func (j *JSONEmitter) Completed(label string, summary map[string]string) {
	ev := map[string]any{"event": "completed", "label": label}
	if len(summary) > 0 {
		ev["summary"] = summary
	}
	j.write(ev)
}

// InteractiveEmitter writes human-readable lines to w.
type InteractiveEmitter struct {
	w io.Writer
}

func NewInteractiveEmitter(w io.Writer) *InteractiveEmitter { return &InteractiveEmitter{w: w} }

func (i *InteractiveEmitter) StepStarted(step string) {
	fmt.Fprintf(i.w, "→ %s\n", step)
}
func (i *InteractiveEmitter) StepDone(step string, output map[string]string) {
	if len(output) > 0 {
		fmt.Fprintf(i.w, "✓ %s %v\n", step, output)
	} else {
		fmt.Fprintf(i.w, "✓ %s\n", step)
	}
}
func (i *InteractiveEmitter) AwaitingHuman(step, url, message, resumeCmd string) {
	fmt.Fprintf(i.w, "\n[%s] %s\n  URL: %s\n  Resume: %s\n", step, message, url, resumeCmd)
}
func (i *InteractiveEmitter) AwaitingInput(step, url, message string, fields []string, resumeCmd string) {
	fmt.Fprintf(i.w, "\n[%s] %s\n  URL: %s\n  Required: %v\n  Resume: %s\n",
		step, message, url, fields, resumeCmd)
}
func (i *InteractiveEmitter) StepFailed(step, reason string, recoverable bool) {
	rec := "fatal"
	if recoverable {
		rec = "recoverable — retry with --resume"
	}
	fmt.Fprintf(i.w, "✗ %s: %s (%s)\n", step, reason, rec)
}
func (i *InteractiveEmitter) Completed(label string, summary map[string]string) {
	fmt.Fprintf(i.w, "\n✓ Setup complete for %s\n", label)
	for k, v := range summary {
		fmt.Fprintf(i.w, "  %s: %s\n", k, v)
	}
}
