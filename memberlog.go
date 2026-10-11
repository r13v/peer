package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// logKind is what a member log entry shows.
type logKind int

const (
	logRaw    logKind = iota // a line as written: plain output or stderr
	logText                  // the member's own words, shown as markdown
	logTool                  // a command or tool call
	logOutput                // what a command or tool returned
)

// logEntry is one thing a member did, at the time its line was written;
// At is zero in logs written before lines were stamped.
type logEntry struct {
	At     time.Time `json:"at"`
	Kind   logKind   `json:"kind"`
	Text   string    `json:"text"`
	Failed bool      `json:"failed,omitempty"`
}

func (k logKind) MarshalJSON() ([]byte, error) {
	return json.Marshal([]string{"raw", "text", "tool", "result"}[k])
}

// stamped splits the time stamp writes before a log line from the line.
func stamped(line []byte) (time.Time, []byte) {
	if i := bytes.IndexByte(line, '\t'); i > 0 && i < 40 {
		if at, err := time.Parse(time.RFC3339Nano, string(line[:i])); err == nil {
			return at, line[i+1:]
		}
	}
	return time.Time{}, line
}

// memberLogLine turns one log line into entries. The apps write JSON
// events: codex exec --json, claude stream-json and pi --mode json.
// Anything else, such as stderr, older plain codex logs or an event it
// does not know, is shown as it is.
func memberLogLine(line []byte) []logEntry {
	var ev struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"` // claude's or pi's message, or codex's error text
		Subtype string          `json:"subtype"`
		Result  json.RawMessage `json:"result"` // claude's text, or a pi tool's result
		Errors  []string        `json:"errors"`
		IsError bool            `json:"is_error"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
		Item struct {
			Type             string `json:"type"`
			Text             string `json:"text"`
			Message          string `json:"message"`
			Command          string `json:"command"`
			AggregatedOutput string `json:"aggregated_output"`
			ExitCode         *int   `json:"exit_code"`
			Status           string `json:"status"`
			Server           string `json:"server"`
			Tool             string `json:"tool"`
			Query            string `json:"query"`
			Changes          []struct {
				Path string `json:"path"`
			} `json:"changes"`
		} `json:"item"`
	}
	raw := strings.TrimRight(string(line), "\r\n")
	if json.Unmarshal(line, &ev) != nil || ev.Type == "" {
		if raw == "" {
			return nil
		}
		return []logEntry{{Kind: logRaw, Text: raw}}
	}
	switch ev.Type {
	case "item.completed": // codex; item.started would repeat each command
		it := ev.Item
		switch it.Type {
		case "agent_message":
			return []logEntry{{Kind: logText, Text: it.Text}}
		case "command_execution":
			failed := it.Status == "failed" || it.ExitCode != nil && *it.ExitCode != 0
			out := []logEntry{{Kind: logTool, Text: it.Command, Failed: failed}}
			if o := strings.TrimRight(it.AggregatedOutput, "\n"); o != "" {
				out = append(out, logEntry{Kind: logOutput, Text: o})
			}
			return out
		case "file_change":
			var paths []string
			for _, c := range it.Changes {
				paths = append(paths, c.Path)
			}
			return []logEntry{{Kind: logTool, Text: "edit " + strings.Join(paths, " "), Failed: it.Status == "failed"}}
		case "mcp_tool_call":
			return []logEntry{{Kind: logTool, Text: it.Server + "." + it.Tool, Failed: it.Status == "failed"}}
		case "web_search":
			return []logEntry{{Kind: logTool, Text: "search " + it.Query}}
		case "error":
			return []logEntry{{Kind: logRaw, Text: it.Message, Failed: true}}
		case "reasoning", "todo_list":
			return nil
		}
		return []logEntry{{Kind: logRaw, Text: raw}}
	case "error": // codex
		var text string
		if json.Unmarshal(ev.Message, &text) != nil {
			text = raw
		}
		return []logEntry{{Kind: logRaw, Text: text, Failed: true}}
	case "turn.failed": // codex
		return []logEntry{{Kind: logRaw, Text: ev.Error.Message, Failed: true}}
	case "assistant", "user": // claude
		var msg struct {
			Content []struct {
				Type    string          `json:"type"`
				Text    string          `json:"text"`
				Name    string          `json:"name"`
				Input   json.RawMessage `json:"input"`
				Content json.RawMessage `json:"content"`
				IsError bool            `json:"is_error"`
			} `json:"content"`
		}
		if json.Unmarshal(ev.Message, &msg) != nil {
			return nil // a user prompt given as a string
		}
		var out []logEntry
		for _, c := range msg.Content {
			switch c.Type {
			case "text":
				out = append(out, logEntry{Kind: logText, Text: c.Text})
			case "tool_use":
				var in struct {
					Command string `json:"command"`
				}
				arg := string(c.Input)
				if json.Unmarshal(c.Input, &in) == nil && in.Command != "" {
					arg = in.Command
				}
				out = append(out, logEntry{Kind: logTool, Text: c.Name + " " + ansi.Truncate(arg, 200, "…")})
			case "tool_result":
				if text := strings.TrimRight(toolResult(c.Content), "\n"); text != "" || c.IsError {
					out = append(out, logEntry{Kind: logOutput, Text: text, Failed: c.IsError})
				}
			}
		}
		return out
	case "result": // claude; its text repeats the last message
		if ev.IsError {
			var result string
			_ = json.Unmarshal(ev.Result, &result)
			why := cmp.Or(result, strings.Join(ev.Errors, "; "), ev.Subtype)
			return []logEntry{{Kind: logRaw, Text: "result: " + why, Failed: true}}
		}
		return nil
	case "message_end": // pi; the message's other events repeat it
		return piMessage(ev.Message)
	case "system", // claude
		"thread.started", "turn.started", "turn.completed", "item.started", "item.updated", // codex
		"session", "agent_start", "agent_end", "agent_settled", "turn_start", "turn_end", "message_start", "message_update", // pi
		"tool_execution_start", "tool_execution_update", "tool_execution_end", "queue_update":
		return nil
	}
	return []logEntry{{Kind: logRaw, Text: raw}}
}

// piMessage turns a finished pi message into entries: the assistant's
// words, tool calls and error, and what each tool returned.
func piMessage(raw json.RawMessage) []logEntry {
	var msg struct {
		Role    string `json:"role"`
		Content []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"content"`
		IsError      bool   `json:"isError"`
		StopReason   string `json:"stopReason"`
		ErrorMessage string `json:"errorMessage"`
	}
	if json.Unmarshal(raw, &msg) != nil {
		return nil
	}
	var out []logEntry
	switch msg.Role {
	case "assistant":
		for _, c := range msg.Content {
			switch c.Type {
			case "text":
				out = append(out, logEntry{Kind: logText, Text: c.Text})
			case "toolCall":
				var in struct {
					Command string `json:"command"`
				}
				arg := string(c.Arguments)
				if json.Unmarshal(c.Arguments, &in) == nil && in.Command != "" {
					arg = in.Command
				}
				out = append(out, logEntry{Kind: logTool, Text: c.Name + " " + ansi.Truncate(arg, 200, "…")})
			}
		}
		// Pi exits with status 0 after a model error in json mode.
		if msg.StopReason == "error" || msg.StopReason == "aborted" {
			out = append(out, logEntry{Kind: logRaw, Text: cmp.Or(msg.ErrorMessage, msg.StopReason), Failed: true})
		}
	case "toolResult":
		var parts []string
		for _, c := range msg.Content {
			if c.Type == "text" {
				parts = append(parts, c.Text)
			}
		}
		if text := strings.TrimRight(strings.Join(parts, "\n"), "\n"); text != "" || msg.IsError {
			out = append(out, logEntry{Kind: logOutput, Text: text, Failed: msg.IsError})
		}
	}
	return out
}

// toolResult is the text of a claude tool result, which is a string or
// a list of content blocks.
func toolResult(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(content, &blocks) // anything else has no text
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// exitReasonTail bounds how much of a member's log exitReason reads, and
// exitReasonLen how long a reason it returns.
const (
	exitReasonTail = 64 * 1024
	exitReasonLen  = 200
)

// exitLinePrefix starts the line startMember's wrapper writes when a
// member's process exits.
const exitLinePrefix = "[peer] exited with status "

// exitReason is the last error of the member's app in its latest run in
// the log at path, such as a model at capacity, or "" when there is none.
// A role invited again appends to the same log, and each run ends with the
// wrapper's exit line. Failed commands are left out: the member saw them
// and went on.
func exitReason(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > exitReasonTail {
		_, _ = f.Seek(-exitReasonTail, io.SeekEnd)
	}
	tail, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	// The app's own error explains more than peer's, which comes after it,
	// such as peer drive's failed turn.
	app, own, latest := "", "", ""
	for line := range bytes.Lines(tail) {
		_, line = stamped(line)
		// The exit line can follow output that did not end its line.
		if bytes.Contains(line, []byte(exitLinePrefix)) {
			latest, app, own = cmp.Or(app, own), "", ""
			continue
		}
		if bytes.HasPrefix(line, []byte("peer: ")) {
			own = strings.TrimSpace(string(line[len("peer: "):]))
			continue
		}
		for _, e := range memberLogLine(line) {
			if e.Kind == logRaw && e.Failed {
				app = strings.TrimSpace(e.Text)
			}
		}
	}

	return ansi.Truncate(strings.Join(strings.Fields(latest), " "), exitReasonLen, "…")
}
