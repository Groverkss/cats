package agent

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

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
	args := []string{
		sandboxPath,
		"--workdir", worktree,
	}
	args = append(args,
		"claude",
		"--dangerously-skip-permissions",
		"-p",
		"--append-system-prompt", prompt,
		fmt.Sprintf("Read CLAUDE.md, then run 'br show %s' and work on the ticket.", ticketID),
	)

	a.cmd = exec.Command(args[0], args[1:]...)
	a.cmd.Env = append(os.Environ(),
		fmt.Sprintf("BR_ACTOR=%s", a.ID),
		fmt.Sprintf("CATS_WORKSPACE=%s", workspace),
	)

	// Capture stdout and stderr via pipes (not PTY — avoids conflict with bubbletea).
	stdout, err := a.cmd.StdoutPipe()
	if err != nil {
		a.State = Failed
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := a.cmd.StderrPipe()
	if err != nil {
		a.State = Failed
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := a.cmd.Start(); err != nil {
		a.State = Failed
		return fmt.Errorf("failed to start agent: %w", err)
	}

	// Open log file.
	logPath := fmt.Sprintf("%s/%s-%s.log", logDir, a.ID, time.Now().Format("2006-01-02T15-04-05"))
	a.logFile, err = os.Create(logPath)
	if err != nil {
		a.cmd.Process.Kill()
		a.State = Failed
		return fmt.Errorf("failed to create log file: %w", err)
	}

	// Read stdout and stderr in background.
	go a.readPipe(stdout)
	go a.readPipe(stderr)

	return nil
}

func (a *Agent) readPipe(r io.ReadCloser) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			a.mu.Lock()
			a.output = append(a.output, buf[:n]...)
			// Keep last 64KB of output.
			if len(a.output) > 65536 {
				a.output = a.output[len(a.output)-65536:]
			}
			if a.logFile != nil {
				a.logFile.Write(buf[:n])
			}
			a.mu.Unlock()
		}
		if err != nil {
			return
		}
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
