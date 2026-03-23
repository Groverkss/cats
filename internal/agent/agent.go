package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b\[[?][0-9;]*[a-zA-Z]|\x1b\[<[a-zA-Z]`)

type State int

const (
	Idle State = iota
	Working
	Failed
)

func (s State) String() string {
	switch s {
	case Idle:
		return "idle"
	case Working:
		return "working"
	case Failed:
		return "failed"
	default:
		return "unknown"
	}
}

type Agent struct {
	ID       string
	Role     string
	State    State
	TicketID string
	Topic    string
	Branch   string

	// Process management.
	cmd *exec.Cmd

	// Output capture.
	mu      sync.Mutex
	output  []byte
	logFile *os.File
}

func New(id, role string) *Agent {
	return &Agent{
		ID:    id,
		Role:  role,
		State: Idle,
	}
}

// Start launches a sandboxed claude agent for the given ticket.
func (a *Agent) Start(workspace, ticketID, topic, branch, worktree, promptTemplate, sandboxPath, logDir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.TicketID = ticketID
	a.Topic = topic
	a.Branch = branch
	a.State = Working
	a.output = nil

	// Prepare the prompt by substituting template variables.
	prompt := promptTemplate
	prompt = strings.ReplaceAll(prompt, "{{TICKET_ID}}", ticketID)
	prompt = strings.ReplaceAll(prompt, "{{TOPIC_NAME}}", topic)
	prompt = strings.ReplaceAll(prompt, "{{BRANCH}}", branch)

	// Build the claude command.
	// Use --output-format=stream-json for real-time streaming via pipe.
	args := []string{
		sandboxPath,
		"--workdir", worktree,
		"claude",
		"--dangerously-skip-permissions",
		"-p",
		"--output-format", "stream-json",
		"--append-system-prompt", prompt,
		fmt.Sprintf("Read CLAUDE.md, then run 'br show %s' and work on the ticket.", ticketID),
	}

	a.cmd = exec.Command(args[0], args[1:]...)
	a.cmd.Env = append(os.Environ(),
		fmt.Sprintf("BR_ACTOR=%s", a.ID),
		fmt.Sprintf("CATS_WORKSPACE=%s", workspace),
	)

	stdout, err := a.cmd.StdoutPipe()
	if err != nil {
		a.State = Failed
		return fmt.Errorf("stdout pipe: %w", err)
	}

	a.cmd.Stderr = a.cmd.Stdout

	// Open log file.
	logPath := fmt.Sprintf("%s/%s-%s.log", logDir, a.ID, time.Now().Format("2006-01-02T15-04-05"))
	a.logFile, err = os.Create(logPath)
	if err != nil {
		a.State = Failed
		return fmt.Errorf("failed to create log file: %w", err)
	}

	if err := a.cmd.Start(); err != nil {
		a.logFile.Close()
		a.State = Failed
		return fmt.Errorf("failed to start agent: %w", err)
	}

	// Read streaming JSON output in background.
	go a.readStreamJSON(stdout)

	return nil
}

func (a *Agent) readStreamJSON(r interface{ Read([]byte) (int, error) }) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 256*1024), 256*1024) // 256KB line buffer

	for scanner.Scan() {
		line := scanner.Bytes()

		// Parse stream-json to extract display text.
		text := extractDisplayText(line)
		if text == "" {
			continue
		}

		cleaned := ansiRegex.ReplaceAllString(text, "")
		a.mu.Lock()
		a.output = append(a.output, []byte(cleaned)...)
		// Keep last 64KB.
		if len(a.output) > 65536 {
			a.output = a.output[len(a.output)-65536:]
		}
		if a.logFile != nil {
			a.logFile.Write(line)
			a.logFile.Write([]byte("\n"))
		}
		a.mu.Unlock()
	}
}

// extractDisplayText pulls human-readable text from claude's stream-json output.
func extractDisplayText(line []byte) string {
	var msg map[string]interface{}
	if err := json.Unmarshal(line, &msg); err != nil {
		return ""
	}

	msgType, _ := msg["type"].(string)

	switch msgType {
	case "assistant":
		message, _ := msg["message"].(map[string]interface{})
		if message == nil {
			return ""
		}
		blocks, _ := message["content"].([]interface{})
		var parts []string
		for _, b := range blocks {
			block, _ := b.(map[string]interface{})
			if block == nil {
				continue
			}
			switch block["type"] {
			case "text":
				if t, ok := block["text"].(string); ok && t != "" {
					parts = append(parts, t)
				}
			case "tool_use":
				name, _ := block["name"].(string)
				input, _ := block["input"].(map[string]interface{})
				summary := formatToolUse(name, input)
				parts = append(parts, summary)
			}
		}
		if len(parts) == 0 {
			return ""
		}
		return strings.Join(parts, "\n") + "\n"

	case "user":
		// Tool results — show what happened.
		message, _ := msg["message"].(map[string]interface{})
		if message == nil {
			return ""
		}
		content, _ := message["content"].([]interface{})
		for _, c := range content {
			item, _ := c.(map[string]interface{})
			if item == nil {
				continue
			}
			if item["type"] == "tool_result" {
				result, _ := item["content"].(string)
				if result != "" {
					// Truncate long results.
					if len(result) > 200 {
						result = result[:200] + "..."
					}
					return "  → " + result + "\n"
				}
			}
		}
		return ""

	case "result":
		subtype, _ := msg["subtype"].(string)
		result, _ := msg["result"].(string)
		durationMs, _ := msg["duration_ms"].(float64)
		cost, _ := msg["total_cost_usd"].(float64)

		status := "✓ done"
		if subtype != "success" {
			status = "✗ " + subtype
		}
		summary := fmt.Sprintf("\n── %s", status)
		if durationMs > 0 {
			summary += fmt.Sprintf(" (%.1fs", durationMs/1000)
			if cost > 0 {
				summary += fmt.Sprintf(", $%.4f", cost)
			}
			summary += ")"
		}
		summary += "\n"
		if result != "" {
			summary += result + "\n"
		}
		return summary

	default:
		return ""
	}
}

// formatToolUse returns a concise description of a tool call.
func formatToolUse(name string, input map[string]interface{}) string {
	switch name {
	case "Write":
		path, _ := input["file_path"].(string)
		return fmt.Sprintf("📝 Write %s", shortPath(path))
	case "Edit":
		path, _ := input["file_path"].(string)
		return fmt.Sprintf("✏️  Edit %s", shortPath(path))
	case "Read":
		path, _ := input["file_path"].(string)
		return fmt.Sprintf("📖 Read %s", shortPath(path))
	case "Bash":
		cmd, _ := input["command"].(string)
		if len(cmd) > 80 {
			cmd = cmd[:80] + "..."
		}
		return fmt.Sprintf("$ %s", cmd)
	case "Glob":
		pattern, _ := input["pattern"].(string)
		return fmt.Sprintf("🔍 Glob %s", pattern)
	case "Grep":
		pattern, _ := input["pattern"].(string)
		return fmt.Sprintf("🔍 Grep %s", pattern)
	case "Agent":
		desc, _ := input["description"].(string)
		return fmt.Sprintf("🤖 Agent: %s", desc)
	case "TodoWrite":
		return "📋 Update todos"
	default:
		return fmt.Sprintf("🔧 %s", name)
	}
}

// shortPath returns the last 2 components of a path.
func shortPath(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) <= 2 {
		return path
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

// Wait waits for the agent process to exit and returns the exit error.
func (a *Agent) Wait() error {
	if a.cmd == nil || a.cmd.Process == nil {
		return nil
	}
	err := a.cmd.Wait()

	a.mu.Lock()
	if a.logFile != nil {
		a.logFile.Close()
		a.logFile = nil
	}
	a.mu.Unlock()

	return err
}

// Output returns the current captured output.
func (a *Agent) Output() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]byte, len(a.output))
	copy(out, a.output)
	return out
}

// Kill terminates the agent process.
func (a *Agent) Kill() {
	if a.cmd != nil && a.cmd.Process != nil {
		a.cmd.Process.Kill()
	}
}

// StartCmd launches an agent with a pre-built command (from sandbox.Command).
func (a *Agent) StartCmd(cmd *exec.Cmd, ticketID, topic, branch, logDir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.TicketID = ticketID
	a.Topic = topic
	a.Branch = branch
	a.State = Working
	a.output = nil
	a.cmd = cmd

	stdout, err := a.cmd.StdoutPipe()
	if err != nil {
		a.State = Failed
		return fmt.Errorf("stdout pipe: %w", err)
	}
	a.cmd.Stderr = a.cmd.Stdout

	logPath := fmt.Sprintf("%s/%s-%s.log", logDir, a.ID, time.Now().Format("2006-01-02T15-04-05"))
	a.logFile, err = os.Create(logPath)
	if err != nil {
		a.State = Failed
		return fmt.Errorf("failed to create log file: %w", err)
	}

	if err := a.cmd.Start(); err != nil {
		a.logFile.Close()
		a.State = Failed
		return fmt.Errorf("failed to start agent: %w", err)
	}

	go a.readStreamJSON(stdout)
	return nil
}

// IsAlive returns true if the agent's process is still running.
func (a *Agent) IsAlive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd == nil || a.cmd.Process == nil {
		return false
	}
	// Signal 0 checks if process exists without killing it.
	return a.cmd.Process.Signal(syscall.Signal(0)) == nil
}

// Reset returns the agent to idle state.
func (a *Agent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.State = Idle
	a.TicketID = ""
	a.Topic = ""
	a.Branch = ""
	a.output = nil
	a.cmd = nil
}
