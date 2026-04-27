package hexmem

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	defaultHexRoot = ""
	saveTimeout    = 10 * time.Second
	scriptSubpath  = ".hex/skills/memory/scripts"
)

// Config holds hexmem client configuration.
type Config struct {
	HexRoot string
	Enabled bool
}

// MemoryItem represents a memory to persist via hex.
// Type, ScopePath, and Provenance are encoded into Tags (Option A lossy encoding).
type MemoryItem struct {
	Content    string
	Tags       string
	Source     string
	Type       string
	ScopePath  string
	Provenance string
}

// SearchResult is one result from memory_search.py --compact.
type SearchResult struct {
	Content   string
	Source    string
	Tags      string
	Timestamp string
}

// execFn is the type used for shelling out — injectable for tests.
type execFn func(ctx context.Context, name string, args ...string) ([]byte, error)

// Client shells out to hex CLI scripts. A disabled client is always safe to call.
type Client struct {
	cfg      Config
	savePath string
	srchPath string
	enabled  bool
	exec     execFn
}

func defaultExec(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// NewClient constructs a Client from cfg. If HexRoot is empty, CC_HEX_ROOT env or
// the default path is used. Returns a disabled client when Enabled=false or scripts
// are absent — all calls on a disabled client are safe no-ops.
func NewClient(cfg Config) *Client {
	if cfg.HexRoot == "" {
		if root := os.Getenv("CC_HEX_ROOT"); root != "" {
			cfg.HexRoot = root
		}
	}

	c := &Client{cfg: cfg, exec: defaultExec}

	if !cfg.Enabled {
		return c
	}

	if cfg.HexRoot == "" {
		slog.Info("hexmem: hex disabled: no HexRoot configured")
		return c
	}

	savePath := filepath.Join(cfg.HexRoot, scriptSubpath, "memory_save.py")
	srchPath := filepath.Join(cfg.HexRoot, scriptSubpath, "memory_search.py")

	if _, err := os.Stat(savePath); err != nil {
		slog.Warn("hexmem: memory_save.py not found, disabling", "path", savePath)
		return c
	}
	if _, err := os.Stat(srchPath); err != nil {
		slog.Warn("hexmem: memory_search.py not found, disabling", "path", srchPath)
		return c
	}

	c.savePath = savePath
	c.srchPath = srchPath
	c.enabled = true
	return c
}

// Enabled reports whether the client is active and scripts are present.
func (c *Client) Enabled() bool {
	return c.enabled
}

// NewClientForTest constructs an enabled Client with an injected exec function.
// Use in core-package tests that call SetHexClient on the engine but cannot
// import unexported hexmem fields directly.
func NewClientForTest(hexRoot, savePath, srchPath string, fn func(ctx context.Context, name string, args ...string) ([]byte, error)) *Client {
	return &Client{
		cfg:      Config{HexRoot: hexRoot, Enabled: true},
		savePath: savePath,
		srchPath: srchPath,
		enabled:  true,
		exec:     fn,
	}
}

// Save persists item to hex asynchronously. It returns immediately; errors are
// logged via slog.Warn. Safe to call when disabled.
func (c *Client) Save(ctx context.Context, item MemoryItem) {
	if !c.enabled {
		return
	}
	tags := EncodeTags(item)
	go func() {
		saveCtx, cancel := context.WithTimeout(ctx, saveTimeout)
		defer cancel()
		if _, err := c.exec(saveCtx, "python3", c.savePath, item.Content, "--tags", tags, "--source", item.Source); err != nil {
			slog.Warn("hexmem: save failed", "err", err, "source", item.Source)
		}
	}()
}

// Search runs memory_search.py --compact and returns up to top results.
// Returns nil, nil on script error (fail-open) — hex is a best-effort enhancement.
func (c *Client) Search(ctx context.Context, query string, top int) ([]SearchResult, error) {
	if !c.enabled {
		return nil, nil
	}
	out, err := c.exec(ctx, "python3", c.srchPath, "--compact", "--top", fmt.Sprintf("%d", top), query)
	if err != nil {
		slog.Warn("hexmem: search failed", "err", err)
		return nil, nil
	}
	return parseCompact(string(out)), nil
}

// EncodeTags composes the final --tags string for memory_save.py.
// Structured fields (Type, ScopePath, Provenance) are prepended as prefixed tokens;
// the caller's base Tags are appended, deduplicating empties.
func EncodeTags(item MemoryItem) string {
	var parts []string
	if item.Type != "" {
		parts = append(parts, "type:"+item.Type)
	}
	if item.ScopePath != "" {
		parts = append(parts, "scope:"+item.ScopePath)
	}
	if item.Provenance != "" {
		parts = append(parts, "prov:"+item.Provenance)
	}
	if item.Tags != "" {
		for _, t := range strings.Split(item.Tags, ",") {
			if t = strings.TrimSpace(t); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, ",")
}

// ScopePathFromSessionKey converts a session key to a hex scope path.
//
//	slack:C123ABC              → chief-of-staff/C123ABC
//	slack:C123ABC:1714220000   → chief-of-staff/C123ABC/1714220000
//	""                         → chief-of-staff
func ScopePathFromSessionKey(key string) string {
	if key == "" {
		return "chief-of-staff"
	}
	// strip "slack:" prefix
	rest := strings.TrimPrefix(key, "slack:")
	parts := strings.SplitN(rest, ":", 2)
	channel := parts[0]
	if len(parts) == 2 && parts[1] != "" {
		return "chief-of-staff/" + channel + "/" + parts[1]
	}
	return "chief-of-staff/" + channel
}

// ChannelID extracts the Slack channel ID from a session key.
//
//	slack:C123ABC:T456 → C123ABC
//	slack:C123ABC      → C123ABC
func ChannelID(key string) string {
	rest := strings.TrimPrefix(key, "slack:")
	return strings.SplitN(rest, ":", 2)[0]
}

// parseCompact parses memory_search.py --compact stdout into SearchResult slice.
// Each result block:
//
//	  [N] source_path > heading  (score: 1.23)
//	      content snippet
//
// Heading is stored in Tags; timestamp is not present in compact format.
var compactResultLine = regexp.MustCompile(`^\s+\[\d+\]\s+(.+?)\s+>\s+(.+?)\s+\(score:`)

func parseCompact(output string) []SearchResult {
	var results []SearchResult
	scanner := bufio.NewScanner(strings.NewReader(output))

	var pending *SearchResult
	for scanner.Scan() {
		line := scanner.Text()

		if m := compactResultLine.FindStringSubmatch(line); m != nil {
			// Start a new result
			pending = &SearchResult{
				Source: strings.TrimSpace(m[1]),
				Tags:   strings.TrimSpace(m[2]),
			}
			continue
		}

		if pending != nil {
			// Next non-empty line after the header line is the snippet
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				if pending.Content != "" {
					results = append(results, *pending)
					pending = nil
				}
				continue
			}
			if pending.Content == "" {
				pending.Content = trimmed
			}
		}
	}

	// Flush last pending result if output doesn't end with a blank line
	if pending != nil && pending.Content != "" {
		results = append(results, *pending)
	}

	return results
}
