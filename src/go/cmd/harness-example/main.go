// Package main is a runnable demo that exercises every harness subsystem:
// tools, shell, events, skills, and hooks.
//
// Usage:
//
//	harness-example [flags]
//
// Flags:
//
//	--model     LLM model (default: gpt-4o-mini)
//	--provider  openai | anthropic (default: openai)
//	--prompt    user message (default: "List the files …")
//	--verbose   print hook logs and event stream to stderr
//	--dry-run   use a scripted fake LLM; no network required (CI-safe)
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"

	"agent-harness/go/agent"
	"agent-harness/go/events"
	"agent-harness/go/hooks"
	"agent-harness/go/llm"
	"agent-harness/go/llm/anthropic"
	"agent-harness/go/llm/openai"
	"agent-harness/go/middleware"
	"agent-harness/go/shell/builtin"
	"agent-harness/go/skills"
	"agent-harness/go/tools"
)

// hookRunStart and hookToolCall are exported from the hooks package but
// re-aliased here so the test can reference them without importing hooks
// directly. We use the hooks constants directly instead.
const (
	hookRunStart  = hooks.RunStart
	hookToolCall  = hooks.ToolCall
)

// ---------------------------------------------------------------------------
// Tool
// ---------------------------------------------------------------------------

type addArgs struct {
	A int `json:"a" desc:"first number"`
	B int `json:"b" desc:"second number"`
}

func addFn(_ context.Context, args addArgs) (int, error) {
	return args.A + args.B, nil
}

// ---------------------------------------------------------------------------
// Skill
// ---------------------------------------------------------------------------

type helloSkill struct{ skills.Base }

func (helloSkill) Instructions() string {
	return "When the user greets you, respond with a friendly greeting in return."
}

// ---------------------------------------------------------------------------
// Dry-run fake LLM client
// ---------------------------------------------------------------------------

// fakeClient scripts three responses:
//  1. tool_call exec ls /data
//  2. tool_call exec cat /data/numbers.txt
//  3. plain assistant text
type fakeClient struct {
	mu   sync.Mutex
	call int
}

func (f *fakeClient) Complete(_ context.Context, _ llm.Request) (llm.Response, error) {
	f.mu.Lock()
	n := f.call
	f.call++
	f.mu.Unlock()

	switch n {
	case 0:
		return llm.Response{Message: middleware.Message{
			Role: "assistant",
			ToolCalls: []middleware.ToolCall{
				{ID: "call_ls", Name: "exec", Arguments: `{"command":"ls /data"}`},
			},
		}}, nil
	case 1:
		return llm.Response{Message: middleware.Message{
			Role: "assistant",
			ToolCalls: []middleware.ToolCall{
				{ID: "call_cat", Name: "exec", Arguments: `{"command":"cat /data/numbers.txt"}`},
			},
		}}, nil
	default:
		return llm.Response{Message: middleware.Message{
			Role:    "assistant",
			Content: "The /data directory contains README.md and numbers.txt. numbers.txt contains: 1, 2, 3, 42, 5.",
		}}, nil
	}
}

func (f *fakeClient) Stream(_ context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	// Delegate to Complete and emit as a single Done chunk.
	resp, err := f.Complete(context.Background(), req)
	if err != nil {
		return nil, err
	}
	ch := make(chan llm.Chunk, 1)
	msg := resp.Message
	// Build one chunk per tool call plus one content chunk, then Done.
	go func() {
		defer close(ch)
		if msg.Content != "" {
			ch <- llm.Chunk{Content: msg.Content}
		}
		for _, tc := range msg.ToolCalls {
			ch <- llm.Chunk{ToolCallID: tc.ID, ToolName: tc.Name, ToolArgs: tc.Arguments}
		}
		ch <- llm.Chunk{Done: true}
	}()
	return ch, nil
}

// ---------------------------------------------------------------------------
// run — the testable entry-point
// ---------------------------------------------------------------------------

// run is the testable entry-point. main() calls run(ctx, os.Stdout, os.Stderr, os.Args[1:]).
func run(ctx context.Context, stdout, stderr io.Writer, args []string) error {
	fs := flag.NewFlagSet("harness-example", flag.ContinueOnError)
	fs.SetOutput(stderr)

	model := fs.String("model", "gpt-4o-mini", "LLM model identifier")
	provider := fs.String("provider", "openai", "LLM provider: openai | anthropic")
	prompt := fs.String("prompt",
		"List the files in the /data directory, then tell me what numbers.txt contains.",
		"User prompt")
	verbose := fs.Bool("verbose", false, "print hook logs and event stream to stderr")
	dryRun := fs.Bool("dry-run", false, "use scripted fake LLM (no network)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// --- LLM client ---
	var client llm.Client
	if *dryRun {
		client = &fakeClient{}
	} else {
		switch *provider {
		case "openai":
			key := os.Getenv("OPENAI_API_KEY")
			if key == "" {
				return fmt.Errorf("OPENAI_API_KEY is not set (use --dry-run to skip)")
			}
			client = openai.New(openai.WithAPIKey(key))
		case "anthropic":
			key := os.Getenv("ANTHROPIC_API_KEY")
			if key == "" {
				return fmt.Errorf("ANTHROPIC_API_KEY is not set (use --dry-run to skip)")
			}
			client = anthropic.New(anthropic.WithAPIKey(key))
		default:
			return fmt.Errorf("unknown provider %q; use openai or anthropic", *provider)
		}
	}

	// --- Tool ---
	addTool := tools.Tool(addFn, tools.Description("Add two integers."))

	// --- Event type ---
	progressEvent := events.EventType{
		Name:        "progress",
		Description: "Report incremental progress on a long task.",
		Schema:      map[string]any{"step": "string", "percent": "integer"},
		Instructions: "Emit one progress event per major step.",
	}

	// --- Hook logger ---
	var hookLog []string
	var hookMu sync.Mutex
	logHook := func(_ context.Context, args ...any) {
		// args[0] is the event payload (name, bytes, etc.) – not used for logging here.
		_ = args
	}
	// We build per-event hooks below that capture the event name.
	makeLogHook := func(e hooks.Event) hooks.Handler {
		return func(_ context.Context, hargs ...any) {
			if *verbose {
				hookMu.Lock()
				hookLog = append(hookLog, string(e))
				hookMu.Unlock()
				fmt.Fprintf(stderr, "[hook] %s\n", e)
			}
			logHook(nil, hargs...)
		}
	}
	_ = logHook

	// --- Build the builtin shell driver and pre-populate VFS ---
	driver := builtin.NewBuiltinShellDriver()
	vfsDriver := driver.FS()
	_ = vfsDriver.WriteString("/data/README.md",
		"# Data directory\nThis directory contains sample data files for the harness demo.\n")
	_ = vfsDriver.WriteString("/data/numbers.txt", "1\n2\n3\n42\n5\n")

	// --- Build the agent ---
	a, err := agent.NewBuilder(*model).
		System("You are a helpful assistant. Use the exec shell to inspect files. Emit progress events as you work.").
		Client(client).
		Streaming(false). // Use Complete path so fake client is simple.
		Tool(addTool).
		Shell(driver).
		Event(progressEvent).
		Skill(helloSkill{}, nil).
		On(hooks.RunStart, makeLogHook(hooks.RunStart)).
		On(hooks.ToolCall, makeLogHook(hooks.ToolCall)).
		On(hooks.RunEnd, makeLogHook(hooks.RunEnd)).
		Build(ctx)
	if err != nil {
		return fmt.Errorf("build agent: %w", err)
	}

	// --- Subscribe to progress events on the message bus ---
	_ = a.EventBus().Subscribe("progress", func(_ context.Context, ev events.ParsedEvent, _ *events.MessageBus) error {
		if *verbose {
			fmt.Fprintf(stderr, "[event:progress] %v\n", ev.Data)
		}
		return nil
	})

	// --- Run ---
	result, err := a.Run(ctx, []middleware.Message{
		{Role: "user", Content: *prompt},
	})
	if err != nil {
		return fmt.Errorf("agent run: %w", err)
	}

	fmt.Fprintln(stdout, result)

	if *verbose {
		hookMu.Lock()
		defer hookMu.Unlock()
		fmt.Fprintf(stderr, "[hooks fired: %v]\n", hookLog)
	}

	return nil
}

func main() {
	if err := run(context.Background(), os.Stdout, os.Stderr, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
