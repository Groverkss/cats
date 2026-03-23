package agent

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
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
	ptmx   *os.File
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

	// Use a PTY so claude sees a terminal and streams output unbuffered.
	// We don't connect this PTY to bubbletea — we just read from it.
	ptmx, err := pty.Start(a.cmd)
	if err != nil {
		a.State = Failed
		return fmt.Errorf("failed to start agent: %w", err)
	}
	a.ptmx = ptmx

	// Open log file.
	logPath := fmt.Sprintf("%s/%s-%s.log", logDir, a.ID, time.Now().Format("2006-01-02T15-04-05"))
	a.logFile, err = os.Create(logPath)
	if err != nil {
		a.ptmx.Close()
		a.cmd.Process.Kill()
		a.State = Failed
		return fmt.Errorf("failed to create log file: %w", err)
	}

	// Read PTY output in background.
	go a.readOutput()

	return nil
}

func (a *Agent) readOutput() {
	buf := make([]byte, 4096)
	for {
		n, err := a.ptmx.Read(buf)
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
	if a.ptmx != nil {
		a.ptmx.Close()
		a.ptmx = nil
	}
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
	a.ptmx = nil
}
