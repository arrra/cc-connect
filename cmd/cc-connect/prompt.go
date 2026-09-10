package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

func runPrompt(args []string) {
	req, dataDir, err := parsePromptArgs(args)
	if err != nil {
		if errors.Is(err, errPromptUsage) {
			printPromptUsage()
			return
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		printPromptUsage()
		os.Exit(1)
	}

	sockPath := resolveSocketPath(dataDir)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: cc-connect is not running (socket not found: %s)\n", sockPath)
		os.Exit(1)
	}

	payload, err := json.Marshal(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to encode prompt payload: %v\n", err)
		os.Exit(1)
	}

	resp, err := apiPost(sockPath, "/prompt", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(body)))
		os.Exit(1)
	}
}

var errPromptUsage = errors.New("show prompt usage")

func parsePromptArgs(args []string) (core.PromptRequest, string, error) {
	var req core.PromptRequest
	var dataDir string
	var useStdin bool
	var positional []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project", "-p":
			if i+1 >= len(args) {
				return req, "", fmt.Errorf("--project requires a value")
			}
			i++
			req.Project = args[i]
		case "--session", "-s":
			if i+1 >= len(args) {
				return req, "", fmt.Errorf("--session requires a value")
			}
			i++
			req.SessionKey = args[i]
		case "--message", "-m":
			if i+1 >= len(args) {
				return req, "", fmt.Errorf("--message requires a value")
			}
			i++
			req.Message = args[i]
		case "--from":
			if i+1 >= len(args) {
				return req, "", fmt.Errorf("--from requires a value")
			}
			i++
			req.From = args[i]
		case "--stdin":
			useStdin = true
		case "--data-dir":
			if i+1 >= len(args) {
				return req, "", fmt.Errorf("--data-dir requires a value")
			}
			i++
			dataDir = args[i]
		case "--help", "-h":
			return req, "", errPromptUsage
		default:
			positional = append(positional, args[i])
		}
	}

	if useStdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return req, "", fmt.Errorf("reading stdin: %w", err)
		}
		req.Message = strings.TrimSpace(string(data))
	}
	if req.Project == "" {
		req.Project = strings.TrimSpace(os.Getenv("CC_PROJECT"))
	}
	if req.SessionKey == "" {
		req.SessionKey = strings.TrimSpace(os.Getenv("CC_SESSION_KEY"))
	}
	if req.Message == "" {
		req.Message = strings.Join(positional, " ")
	}
	if req.From == "" {
		req.From = "hex-events"
	}

	if req.SessionKey == "" {
		return req, "", fmt.Errorf("session key is required (-s/--session or CC_SESSION_KEY)")
	}
	if req.Message == "" {
		return req, "", fmt.Errorf("message is required (-m/--message, positional args, or --stdin)")
	}

	return req, dataDir, nil
}

func printPromptUsage() {
	fmt.Println(`Usage: cc-connect prompt [options] <message>
       cc-connect prompt [options] -m <message>
       cc-connect prompt [options] --stdin < file

Inject an agent prompt into a project session via internal API.

Options:
  -m, --message <text>     Prompt text to inject (preferred over positional args)
      --stdin               Read prompt from stdin (best for long/special-char messages)
  -p, --project <name>      Target project (optional if only one project)
  -s, --session <key>       Target session key (required)
      --from <name>         Attribution for the injected prompt (default: hex-events)
      --data-dir <path>     Data directory (default: ~/.cc-connect)
  -h, --help                Show this help

Examples:
  cc-connect prompt -s slack:C123 "escalation: task T1 blocked"
  cc-connect prompt -s slack:C123 -m "Build completed successfully"
  cc-connect prompt -s slack:C123 --stdin <<'EOF'
    Long message with "special" chars, $variables, and newlines
  EOF`)
}
