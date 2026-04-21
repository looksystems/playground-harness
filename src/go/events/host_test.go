package events

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHost_NewHost_ZeroState(t *testing.T) {
	h := NewHost()
	require.NotNil(t, h)
	assert.Empty(t, h.Registry(), "fresh host has no registered types")
	assert.Empty(t, h.Defaults(), "fresh host has no defaults")
	assert.NotNil(t, h.Bus(), "bus is always available")
	assert.NotNil(t, h.Parser(), "parser is always available")
}

func TestHost_Register_AddsToRegistryAndParser(t *testing.T) {
	h := NewHost()
	et := EventType{
		Name:        "user_response",
		Description: "Reply to the user",
		Schema:      map[string]any{"message": "string"},
	}

	returned := h.Register(et)
	assert.Same(t, h, returned, "Register returns the host for chaining")

	snapshot := h.Registry()
	require.Len(t, snapshot, 1)
	assert.Equal(t, et, snapshot["user_response"])

	// Mutating the returned snapshot must not affect the host.
	delete(snapshot, "user_response")
	require.Len(t, h.Registry(), 1, "Registry returns a snapshot")

	// Parser must recognise the type too — fabricate a simple event block
	// and feed it through.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tokens := make(chan string, 1)
	tokens <- "---event\ntype: user_response\nmessage: hi\n---\n"
	close(tokens)

	cleanText, events := h.Parser().Wrap(ctx, tokens)

	var gotEvent ParsedEvent
	var gotCount int
	doneText := make(chan struct{})
	go func() {
		for range cleanText {
		}
		close(doneText)
	}()
	for ev := range events {
		gotEvent = ev
		gotCount++
	}
	<-doneText

	assert.Equal(t, 1, gotCount, "parser emits the registered event")
	assert.Equal(t, "user_response", gotEvent.Type)
}

func TestHost_Register_Overwrites(t *testing.T) {
	h := NewHost()
	h.Register(EventType{Name: "x", Description: "first"})
	h.Register(EventType{Name: "x", Description: "second"})

	reg := h.Registry()
	require.Len(t, reg, 1)
	assert.Equal(t, "second", reg["x"].Description)
}

func TestHost_SetDefaults_ResolveActiveUsesThem(t *testing.T) {
	h := NewHost()
	a := EventType{Name: "a"}
	b := EventType{Name: "b"}
	c := EventType{Name: "c"}
	h.Register(a).Register(b).Register(c)

	h.SetDefaults("a", "c", "missing")

	active := h.ResolveActive(nil)
	names := namesOf(active)
	assert.Equal(t, []string{"a", "c"}, names, "non-registered defaults are silently skipped")
}

func TestHost_ResolveActive_ExplicitMixed(t *testing.T) {
	h := NewHost()
	a := EventType{Name: "a"}
	b := EventType{Name: "b"}
	h.Register(a).Register(b)

	adHoc := EventType{Name: "adhoc"}
	active := h.ResolveActive([]any{"a", adHoc, "missing", 42})
	names := namesOf(active)
	// "a" resolved, adHoc used directly, "missing" and the int skipped.
	assert.Equal(t, []string{"a", "adhoc"}, names)
}

func TestHost_ResolveActive_EmptySliceUsesDefaults(t *testing.T) {
	h := NewHost()
	h.Register(EventType{Name: "a"})
	h.SetDefaults("a")

	// Nil and empty slice should both fall back to defaults.
	assert.Equal(t, []string{"a"}, namesOf(h.ResolveActive(nil)))
	assert.Equal(t, []string{"a"}, namesOf(h.ResolveActive([]any{})))
}

func TestHost_Bus_IsSameBetweenCalls(t *testing.T) {
	h := NewHost()
	assert.Same(t, h.Bus(), h.Bus(), "Bus returns a stable reference")

	// Exercise publish/subscribe via Bus to verify it is wired.
	var got string
	h.Bus().Subscribe("t", func(_ context.Context, ev ParsedEvent, _ *MessageBus) error {
		got = ev.Type
		return nil
	})
	require.NoError(t, h.Bus().Publish(context.Background(), ParsedEvent{Type: "t"}))
	assert.Equal(t, "t", got)
}

func TestHost_Parser_StableReferenceAfterRegister(t *testing.T) {
	h := NewHost()
	p1 := h.Parser()
	h.Register(EventType{Name: "a"})
	p2 := h.Parser()
	assert.Same(t, p1, p2, "Parser is a stable reference; Register mutates in-place")
}

func TestHost_ConcurrentRegisterAndResolve_NoRace(t *testing.T) {
	h := NewHost()

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n * 2)

	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			h.Register(EventType{Name: nameFor(i)})
		}()
		go func() {
			defer wg.Done()
			_ = h.ResolveActive([]any{nameFor(i)})
		}()
	}

	wg.Wait()
	assert.NotEmpty(t, h.Registry())
}

// --- helpers ---

func namesOf(ets []EventType) []string {
	out := make([]string, 0, len(ets))
	for _, et := range ets {
		out = append(out, et.Name)
	}
	return out
}

func nameFor(i int) string {
	// Short helper to avoid fmt imports in concurrent test.
	return "evt-" + string(rune('a'+(i%26)))
}
