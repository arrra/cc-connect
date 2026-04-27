package hexmem

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNewClient_Disabled(t *testing.T) {
	c := NewClient(Config{HexRoot: "/nonexistent", Enabled: false})
	if c.Enabled() {
		t.Fatal("expected disabled client")
	}

	// Save must return immediately without blocking or panicking
	start := time.Now()
	c.Save(context.Background(), MemoryItem{Content: "test"})
	if elapsed := time.Since(start); elapsed > 5*time.Millisecond {
		t.Fatalf("Save on disabled client blocked for %v", elapsed)
	}

	results, err := c.Search(context.Background(), "query", 3)
	if err != nil {
		t.Fatalf("Search on disabled client returned error: %v", err)
	}
	if results != nil {
		t.Fatalf("Search on disabled client returned non-nil results: %v", results)
	}
}

func TestNewClient_ScriptsMissing(t *testing.T) {
	c := NewClient(Config{HexRoot: "/nonexistent/path/that/does/not/exist", Enabled: true})
	if c.Enabled() {
		t.Fatal("expected disabled client when scripts are missing")
	}
}

func TestSave_FireAndForget(t *testing.T) {
	var mu sync.Mutex
	var capturedArgs []string
	called := make(chan struct{})

	mock := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		mu.Lock()
		all := make([]string, 0, 1+len(args))
		all = append(all, name)
		all = append(all, args...)
		capturedArgs = all
		mu.Unlock()
		close(called)
		return nil, nil
	}

	c := &Client{
		cfg:      Config{HexRoot: "/fake", Enabled: true},
		savePath: "/fake/memory_save.py",
		srchPath: "/fake/memory_search.py",
		enabled:  true,
		exec:     mock,
	}

	item := MemoryItem{
		Content:    "test content",
		Tags:       "cc-connect",
		Source:     "cc-connect_session_abc",
		Type:       "fact",
		ScopePath:  "chief-of-staff/C123",
		Provenance: "tool_output",
	}

	start := time.Now()
	c.Save(context.Background(), item)
	elapsed := time.Since(start)

	if elapsed > time.Millisecond {
		t.Fatalf("Save blocked caller for %v, expected < 1ms", elapsed)
	}

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine never fired")
	}

	mu.Lock()
	args := capturedArgs
	mu.Unlock()

	expectedTags := EncodeTags(item)
	want := []string{"python3", "/fake/memory_save.py", item.Content, "--tags", expectedTags, "--source", item.Source}
	if len(args) != len(want) {
		t.Fatalf("expected %d args, got %d: %v", len(want), len(args), args)
	}
	for i, w := range want {
		if args[i] != w {
			t.Errorf("arg[%d]: want %q, got %q", i, w, args[i])
		}
	}
}

func TestSave_Error(t *testing.T) {
	done := make(chan struct{})

	mock := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		close(done)
		return nil, errors.New("exit status 1")
	}

	c := &Client{
		cfg:      Config{HexRoot: "/fake", Enabled: true},
		savePath: "/fake/memory_save.py",
		srchPath: "/fake/memory_search.py",
		enabled:  true,
		exec:     mock,
	}

	// Should return immediately — error must not propagate to caller
	c.Save(context.Background(), MemoryItem{Content: "test", Source: "s"})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine never fired")
	}
}

func TestSearch_ParsesCompactOutput(t *testing.T) {
	mockOutput := `============================================================
 Memory Search: "test query" — 2 results
============================================================

  [1] projects/foo/bar.md > Some Heading  (score: 1.23)
      First content snippet here

  [2] projects/baz/qux.md > Another Heading  (score: 0.98)
      Second content snippet here

(Showing top 2. Use --top N to see more.)
`

	mock := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(mockOutput), nil
	}

	c := &Client{
		cfg:      Config{HexRoot: "/fake", Enabled: true},
		savePath: "/fake/memory_save.py",
		srchPath: "/fake/memory_search.py",
		enabled:  true,
		exec:     mock,
	}

	results, err := c.Search(context.Background(), "test query", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(results), results)
	}

	if results[0].Source != "projects/foo/bar.md" {
		t.Errorf("results[0].Source: want %q, got %q", "projects/foo/bar.md", results[0].Source)
	}
	if results[0].Tags != "Some Heading" {
		t.Errorf("results[0].Tags: want %q, got %q", "Some Heading", results[0].Tags)
	}
	if results[0].Content != "First content snippet here" {
		t.Errorf("results[0].Content: want %q, got %q", "First content snippet here", results[0].Content)
	}

	if results[1].Source != "projects/baz/qux.md" {
		t.Errorf("results[1].Source: want %q, got %q", "projects/baz/qux.md", results[1].Source)
	}
	if results[1].Tags != "Another Heading" {
		t.Errorf("results[1].Tags: want %q, got %q", "Another Heading", results[1].Tags)
	}
	if results[1].Content != "Second content snippet here" {
		t.Errorf("results[1].Content: want %q, got %q", "Second content snippet here", results[1].Content)
	}
}

func TestSearch_EmptyOutput(t *testing.T) {
	mock := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(""), nil
	}

	c := &Client{
		cfg:      Config{HexRoot: "/fake", Enabled: true},
		savePath: "/fake/memory_save.py",
		srchPath: "/fake/memory_search.py",
		enabled:  true,
		exec:     mock,
	}

	results, err := c.Search(context.Background(), "nothing", 3)
	if err != nil {
		t.Fatalf("unexpected error on empty output: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %v", results)
	}
}

func TestSearch_ScriptError(t *testing.T) {
	mock := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, errors.New("exit status 1")
	}

	c := &Client{
		cfg:      Config{HexRoot: "/fake", Enabled: true},
		savePath: "/fake/memory_save.py",
		srchPath: "/fake/memory_search.py",
		enabled:  true,
		exec:     mock,
	}

	results, err := c.Search(context.Background(), "query", 3)
	if err == nil {
		t.Fatal("expected error from Search when script fails")
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results on error, got %v", results)
	}
}

func TestScopePathFromSessionKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"slack:C123ABC", "chief-of-staff/C123ABC"},
		{"slack:C123ABC:1714220000.000200", "chief-of-staff/C123ABC/1714220000.000200"},
		{"", "chief-of-staff"},
	}
	for _, tt := range tests {
		got := ScopePathFromSessionKey(tt.key)
		if got != tt.want {
			t.Errorf("ScopePathFromSessionKey(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestChannelID(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"slack:C123ABC:T456", "C123ABC"},
		{"slack:C123", "C123"},
	}
	for _, tt := range tests {
		got := ChannelID(tt.key)
		if got != tt.want {
			t.Errorf("ChannelID(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}
