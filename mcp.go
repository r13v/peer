package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpWaitMax keeps peer_wait under Codex's default 60-second tool timeout.
const mcpWaitMax = 55 * time.Second

// mcpRoom holds the fields every room tool takes. Repo is the checkout;
// tools default to the directory the host started peer mcp in, since a
// stateless server cannot remember a room between calls.
type mcpRoom struct {
	Repo string `json:"repo,omitempty" jsonschema:"path inside the shared Git checkout; defaults to the server's working directory"`
	Room string `json:"room" jsonschema:"room ID that peer_start printed"`
}

type mcpStartIn struct {
	Repo  string `json:"repo,omitempty" jsonschema:"path inside the shared Git checkout; defaults to the server's working directory"`
	Name  string `json:"name" jsonschema:"room name: 1-40 characters a-z, 0-9 or -, starting with a letter"`
	Agent string `json:"agent,omitempty" jsonschema:"your app, such as claude or codex"`
}

type mcpJoinIn struct {
	mcpRoom
	Role  string `json:"role" jsonschema:"your role in the room, such as reader"`
	Agent string `json:"agent,omitempty" jsonschema:"your app, such as claude or codex"`
}

type mcpInviteIn struct {
	mcpRoom
	As    string `json:"as" jsonschema:"your role; only the writer can invite"`
	Role  string `json:"role" jsonschema:"role for the new member, such as reader"`
	Agent string `json:"agent" jsonschema:"codex or claude"`
	Brief string `json:"brief,omitempty" jsonschema:"what the member should focus on"`
}

type mcpSendIn struct {
	mcpRoom
	As   string `json:"as" jsonschema:"your role"`
	To   string `json:"to,omitempty" jsonschema:"recipient role, or * for every member, the default"`
	Text string `json:"text" jsonschema:"message text, at most 64 KiB"`
}

type mcpWaitIn struct {
	mcpRoom
	As      string `json:"as" jsonschema:"your role"`
	Timeout int    `json:"timeout_seconds,omitempty" jsonschema:"how long to wait, 1-55 seconds; default 45"`
}

type mcpAsIn struct {
	mcpRoom
	As string `json:"as" jsonschema:"your role"`
}

type mcpStatusIn struct {
	Repo string `json:"repo,omitempty" jsonschema:"path inside the shared Git checkout; defaults to the server's working directory"`
	Room string `json:"room,omitempty" jsonschema:"room ID; every active room of the checkout when omitted"`
}

type mcpSkillsIn struct {
	Name string `json:"name" jsonschema:"flow, writer or member"`
}

// serveMCP runs peer's tools over stdio. Each tool performs the checks
// and store operation of the matching command and returns its JSON.
func serveMCP(cwd string, in io.Reader, out io.Writer) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "peer", Version: version}, nil)
	open := func(repo string) (*store, error) {
		if repo == "" {
			repo = cwd
		}
		return openStore(repo)
	}
	mcp.AddTool(server, &mcp.Tool{Name: "peer_start", Description: "Start a room for a task as its writer. Returns the room; its id is the room for later calls."},
		func(_ context.Context, _ *mcp.CallToolRequest, a mcpStartIn) (*mcp.CallToolResult, any, error) {
			if !roomName.MatchString(a.Name) {
				return nil, nil, errors.New("the room name is 1-40 characters: a-z, 0-9 or -, starting with a letter")
			}
			if err := checkAgent(a.Agent); err != nil {
				return nil, nil, err
			}
			return mcpRun(a.Repo, open, func(s *store, w io.Writer) error { return s.start(a.Name, a.Agent, w) })
		})
	mcp.AddTool(server, &mcp.Tool{Name: "peer_join", Description: "Join an active room under a role."},
		func(_ context.Context, _ *mcp.CallToolRequest, a mcpJoinIn) (*mcp.CallToolResult, any, error) {
			if err := checkRole(a.Role); err != nil {
				return nil, nil, err
			}
			if err := checkAgent(a.Agent); err != nil {
				return nil, nil, err
			}
			return mcpRun(a.Repo, open, func(s *store, w io.Writer) error { return s.join(a.Room, member{Role: a.Role, Agent: a.Agent}, w) })
		})
	mcp.AddTool(server, &mcp.Tool{Name: "peer_invite", Description: "As the writer, add a member and launch codex or claude headless in the checkout. Returns once it is started."},
		func(_ context.Context, _ *mcp.CallToolRequest, a mcpInviteIn) (*mcp.CallToolResult, any, error) {
			if err := checkRole(a.Role); err != nil {
				return nil, nil, err
			}
			if appBundles[a.Agent] == "" {
				return nil, nil, errors.New("invite launches codex or claude; to add another agent, give it a join prompt")
			}
			return mcpRun(a.Repo, open, func(s *store, w io.Writer) error {
				return s.invite(a.Room, a.As, member{Role: a.Role, Agent: a.Agent}, a.Brief, false, w)
			})
		})
	mcp.AddTool(server, &mcp.Tool{Name: "peer_send", Description: "Send a message to every member of a room, or to one role."},
		func(_ context.Context, _ *mcp.CallToolRequest, a mcpSendIn) (*mcp.CallToolResult, any, error) {
			to := a.To
			if to == "" {
				to = everyone
			}
			if to != everyone {
				if err := checkName(to); err != nil {
					return nil, nil, err
				}
			}
			if err := checkName(a.As); err != nil {
				return nil, nil, err
			}
			text, err := messageText(a.Text)
			if err != nil {
				return nil, nil, err
			}
			return mcpRun(a.Repo, open, func(s *store, w io.Writer) error { return s.send(a.Room, a.As, to, text, w) })
		})
	mcp.AddTool(server, &mcp.Tool{Name: "peer_wait", Description: "Wait for the next message to your role. Returns status message with the message, or status timeout; call again while you need a reply."},
		func(ctx context.Context, _ *mcp.CallToolRequest, a mcpWaitIn) (*mcp.CallToolResult, any, error) {
			if err := checkName(a.As); err != nil {
				return nil, nil, err
			}
			timeout := 45 * time.Second
			if a.Timeout > 0 {
				timeout = min(time.Duration(a.Timeout)*time.Second, mcpWaitMax)
			}
			return mcpRun(a.Repo, open, func(s *store, w io.Writer) error { return s.waitContext(ctx, a.Room, a.As, timeout, w) })
		})
	mcp.AddTool(server, &mcp.Tool{Name: "peer_status", Description: "Show a room, or every active room of the checkout."},
		func(_ context.Context, _ *mcp.CallToolRequest, a mcpStatusIn) (*mcp.CallToolResult, any, error) {
			return mcpRun(a.Repo, open, func(s *store, w io.Writer) error { return s.status(a.Room, w) })
		})
	mcp.AddTool(server, &mcp.Tool{Name: "peer_end", Description: "As the writer, end the room once review has closed."},
		func(_ context.Context, _ *mcp.CallToolRequest, a mcpAsIn) (*mcp.CallToolResult, any, error) {
			if err := checkName(a.As); err != nil {
				return nil, nil, err
			}
			return mcpRun(a.Repo, open, func(s *store, w io.Writer) error { return s.end(a.Room, a.As, w) })
		})
	mcp.AddTool(server, &mcp.Tool{Name: "peer_skills", Description: "Read peer's instructions: flow first, then writer or member as it says."},
		func(_ context.Context, _ *mcp.CallToolRequest, a mcpSkillsIn) (*mcp.CallToolResult, any, error) {
			docs := map[string][]byte{"flow": flowInstructions, "writer": writerInstructions, "member": memberInstructions}
			if docs[a.Name] == nil {
				return nil, nil, errors.New("name is flow, writer or member")
			}
			return mcpText(string(docs[a.Name])), nil, nil
		})
	return server.Run(context.Background(), &mcp.IOTransport{Reader: io.NopCloser(in), Writer: nopWriteCloser{out}})
}

// mcpRun opens repo's store and returns what fn writes as the result.
func mcpRun(repo string, open func(string) (*store, error), fn func(*store, io.Writer) error) (*mcp.CallToolResult, any, error) {
	s, err := open(repo)
	if err != nil {
		return nil, nil, err
	}
	var buf bytes.Buffer
	if err := fn(s, &buf); err != nil {
		return nil, nil, err
	}
	return mcpText(string(bytes.TrimSpace(buf.Bytes()))), nil, nil
}

func mcpText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
