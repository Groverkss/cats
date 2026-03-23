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
	cmd    *exec.Cmd
	cancel func()

	// Output capture.
	mu      sync.Mutex
	output  []byte
	logFile *os.File
}

func New(id, role string) *Agent {
	return &Agent{
		ID:   id,
		Role: role,
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

		a.mu.Lock()
		a.output = append(a.output, []byte(text)...)
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
		// Assistant message — extract text from content blocks.
		content, _ := msg["message"].(map[string]interface{})
		if content == nil {
			return ""
		}
		blocks, _ := content["content"].([]interface{})
		var text string
		for _, b := range blocks {
			block, _ := b.(map[string]interface{})
			if block == nil {
				continue
			}
			if block["type"] == "text" {
				if t, ok := block["text"].(string); ok {
					text += t
				}
			}
			if block["type"] == "tool_use" {
				name, _ := block["name"].(string)
				text += fmt.Sprintf("\n[tool: %s]\n", name)
			}
		}
		return text

	case "result":
		// Final result.
		if result, ok := msg["result"].(string); ok {
			return "\n" + result + "\n"
		}
		// Might be structured.
		subtype, _ := msg["subtype"].(string)
		if subtype == "success" {
			return "\n[completed]\n"
		}
		return ""

	default:
		return ""
	}
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

// Output returns the current captured output with ANSI escapes stripped.
func (a *Agent) Output() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	cleaned := ansiRegex.ReplaceAll(a.output, nil)
	out := make([]byte, len(cleaned))
	copy(out, cleaned)
	return out
}

// Kill terminates the agent process.
func (a *Agent) Kill() {
	if a.cmd != nil && a.cmd.Process != nil {
		a.cmd.Process.Kill()
	}
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
