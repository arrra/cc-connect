package hexmem

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestHexmem_Save_FireAndForgetWithCorrectArgs verifies the exact CLI args
// passed to memory_save.py, independent of the non-blocking timing assertion.
func TestHexmem_Save_FireAndForgetWithCorrectArgs(t *testing.T) {
	var mu sync.Mutex
	var capturedArgs []string
	done := make(chan struct{})

	mock := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		mu.Lock()
		all := make([]string, 0, 1+len(args))
		all = append(all, name)
		all = append(all, args...)
		capturedArgs = all
		mu.Unlock()
		close(done)
		return nil, nil
	}

	c := &Client{
		cfg:      Config{HexRoot: "/fake", Enabled: true},
		savePath: "/fake/.hex/skills/memory/scripts/memory_save.py",
		srchPath: "/fake/.hex/skills/memory/scripts/memory_search.py",
		enabled:  true,
		exec:     mock,
	}

	item := MemoryItem{
		Content:    "behavioral test content",
		Tags:       "test-tag",
		Source:     "test-source",
		Type:       "insight",
		ScopePath:  "chief-of-staff/C999",
		Provenance: "user_message",
	}

	c.Save(context.Background(), item)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("execFn was never called")
	}

	mu.Lock()
	args := capturedArgs
	mu.Unlock()

	expectedTags := EncodeTags(item)
	want := []string{
		"python3",
		"/fake/.hex/skills/memory/scripts/memory_save.py",
		item.Content,
		"--tags", expectedTags,
		"--source", item.Source,
	}

	if len(args) != len(want) {
		t.Fatalf("arg count: want %d, got %d\n  want: %v\n  got:  %v", len(want), len(args), want, args)
	}
	for i, w := range want {
		if args[i] != w {
			t.Errorf("arg[%d]: want %q, got %q", i, w, args[i])
		}
	}
}

// TestHexmem_Save_DoesNotBlockCaller verifies the fire-and-forget contract:
// Save returns to the caller immediately even when the script is slow.
func TestHexmem_Save_DoesNotBlockCaller(t *testing.T) {
	mock := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		time.Sleep(5 * time.Second)
		return nil, nil
	}

	c := &Client{
		cfg:      Config{HexRoot: "/fake", Enabled: true},
		savePath: "/fake/memory_save.py",
		srchPath: "/fake/memory_search.py",
		enabled:  true,
		exec:     mock,
	}

	start := time.Now()
	c.Save(context.Background(), MemoryItem{Content: "slow save", Source: "src"})
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Fatalf("Save blocked caller for %v, want < 100ms", elapsed)
	}
}

// TestHexmem_Search_ParsesCompactOutput verifies compact-format stdout is
// correctly parsed into SearchResult structs using the committed golden sample.
func TestHexmem_Search_ParsesCompactOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/compact_sample.txt")
	if err != nil {
		t.Fatalf("failed to read golden sample: %v", err)
	}

	mock := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return data, nil
	}

	c := &Client{
		cfg:      Config{HexRoot: "/fake", Enabled: true},
		savePath: "/fake/memory_save.py",
		srchPath: "/fake/memory_search.py",
		enabled:  true,
		exec:     mock,
	}

	results, err := c.Search(context.Background(), "chief of staff", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(results), results)
	}

	want := []SearchResult{
		{
			Source:  "projects/chief-of-staff/context.md",
			Tags:    "Chief of Staff Role",
			Content: "Coordinates executive workflows, manages priorities, and triages incoming requests.",
		},
		{
			Source:  "projects/chief-of-staff/rituals.md",
			Tags:    "Weekly Rituals",
			Content: "Monday alignment meeting, Friday retrospective, async updates via Slack.",
		},
	}
	for i, w := range want {
		if results[i].Source != w.Source {
			t.Errorf("results[%d].Source: want %q, got %q", i, w.Source, results[i].Source)
		}
		if results[i].Tags != w.Tags {
			t.Errorf("results[%d].Tags: want %q, got %q", i, w.Tags, results[i].Tags)
		}
		if results[i].Content != w.Content {
			t.Errorf("results[%d].Content: want %q, got %q", i, w.Content, results[i].Content)
		}
	}
}

// TestHexmem_Search_FailOpen_OnScriptError verifies that a non-zero exit from
// memory_search.py returns (nil, nil) — fail-open per q-087's fix.
func TestHexmem_Search_FailOpen_OnScriptError(t *testing.T) {
	mock := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, errors.New("exit status 2")
	}

	c := &Client{
		cfg:      Config{HexRoot: "/fake", Enabled: true},
		savePath: "/fake/memory_save.py",
		srchPath: "/fake/memory_search.py",
		enabled:  true,
		exec:     mock,
	}

	results, err := c.Search(context.Background(), "query", 5)
	if err != nil {
		t.Fatalf("expected nil error (fail-open), got: %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil results on script error, got: %v", results)
	}
}

// TestHexmem_Disabled_NoShellOut verifies that a disabled client never invokes
// execFn — Save and Search must be pure no-ops.
func TestHexmem_Disabled_NoShellOut(t *testing.T) {
	var callCount atomic.Int64

	mock := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		callCount.Add(1)
		return nil, nil
	}

	// Disabled client: enabled=false, exec set to catch any stray calls.
	c := &Client{
		cfg:     Config{HexRoot: "", Enabled: false},
		enabled: false,
		exec:    mock,
	}

	c.Save(context.Background(), MemoryItem{Content: "should not exec", Source: "s"})
	// Give the goroutine a moment if Save fires one anyway.
	time.Sleep(50 * time.Millisecond)

	results, err := c.Search(context.Background(), "query", 3)
	if err != nil {
		t.Fatalf("Search on disabled client returned error: %v", err)
	}
	if results != nil {
		t.Fatalf("Search on disabled client returned non-nil results: %v", results)
	}

	if n := callCount.Load(); n != 0 {
		t.Fatalf("execFn was called %d time(s) on a disabled client", n)
	}
}
