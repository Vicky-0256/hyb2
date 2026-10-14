// Package core is the desktop shell's link to the Python runtime: the two
// persistent bridge sidecars, the task scheduler, preferences and the
// operation allowlists. It mirrors desktop/src-tauri (contract C1 and the
// R6 scheduler) so the Python side is unchanged.
package core

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const stderrTail = 4000

// SidecarSpec describes one persistent Python bridge process.
type SidecarSpec struct {
	Name    string
	Program string
	Args    []string
	Dir     string
	Env     []string // appended to os.Environ()
	Timeout time.Duration
}

type proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan string
	done   chan struct{} // closed when stdout reaches EOF
	mu     sync.Mutex
	stderr []byte
}

func (p *proc) tail() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.TrimSpace(string(p.stderr))
}

func (p *proc) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *proc) kill() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.cmd.Wait()
}

// shutdown closes stdin (the bridge exits on EOF) and kills it after 500 ms.
func (p *proc) shutdown() {
	_ = p.stdin.Close()
	select {
	case <-p.done:
	case <-time.After(500 * time.Millisecond):
	}
	p.kill()
}

// Sidecar speaks the newline JSON protocol of `bridge --serve`:
// stdin `{"id":N,"request":{...}}`, stdout `{"id":N,"response":{...}}`.
// Requests are serialized; the process restarts after a crash or EOF and is
// killed and restarted when a response exceeds the timeout.
type Sidecar struct {
	spec   SidecarSpec
	mu     sync.Mutex
	proc   *proc
	nextID uint64
	closed bool
}

func NewSidecar(spec SidecarSpec) *Sidecar {
	return &Sidecar{spec: spec, nextID: 1}
}

func (s *Sidecar) spawn() (*proc, error) {
	cmd := exec.Command(s.spec.Program, s.spec.Args...)
	cmd.Dir = s.spec.Dir
	cmd.Env = append(os.Environ(), s.spec.Env...)
	hideWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("could not start desktop Python %s sidecar (%s): %w", s.spec.Name, s.spec.Program, err)
	}
	p := &proc{cmd: cmd, stdin: stdin, lines: make(chan string, 16), done: make(chan struct{})}
	go func() {
		reader := bufio.NewReaderSize(stdout, 1<<20)
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				p.lines <- strings.TrimRight(line, "\r\n")
			}
			if err != nil {
				close(p.done)
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				p.mu.Lock()
				p.stderr = append(p.stderr, buf[:n]...)
				if len(p.stderr) > stderrTail {
					p.stderr = append([]byte(nil), p.stderr[len(p.stderr)-stderrTail:]...)
				}
				p.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return p, nil
}

// Request sends one request and returns the raw `response` object.
func (s *Sidecar) Request(request any) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("desktop runtime is shutting down")
	}
	if s.proc != nil && s.proc.exited() {
		s.proc.kill()
		s.proc = nil
	}
	id := s.nextID
	s.nextID++
	line, err := json.Marshal(map[string]any{"id": id, "request": request})
	if err != nil {
		return nil, fmt.Errorf("could not encode bridge request: %w", err)
	}
	line = append(line, '\n')
	// A write failure means the request never reached Python, so one
	// restart-and-retry is safe even for non-idempotent operations.
	for attempt := 0; ; attempt++ {
		if s.proc == nil {
			p, err := s.spawn()
			if err != nil {
				return nil, err
			}
			s.proc = p
		}
		if _, err := s.proc.stdin.Write(line); err == nil {
			break
		} else if attempt > 0 {
			s.proc.kill()
			s.proc = nil
			return nil, fmt.Errorf("could not send request to %s sidecar: %w", s.spec.Name, err)
		}
		s.proc.kill()
		s.proc = nil
	}
	timer := time.NewTimer(s.spec.Timeout)
	defer timer.Stop()
	for {
		select {
		case text := <-s.proc.lines:
			if response, ok, err := match(text, id); ok {
				return response, err
			}
		case <-s.proc.done:
			// stdout is closed; the reader has queued everything it read.
			for {
				select {
				case text := <-s.proc.lines:
					if response, ok, err := match(text, id); ok {
						return response, err
					}
					continue
				default:
				}
				break
			}
			p := s.proc
			s.proc = nil
			time.Sleep(50 * time.Millisecond) // let the stderr reader collect the traceback
			detail := p.tail()
			p.kill()
			if detail == "" {
				detail = "no diagnostic output"
			}
			return nil, fmt.Errorf("%s sidecar exited unexpectedly: %s", s.spec.Name, detail)
		case <-timer.C:
			s.proc.kill()
			s.proc = nil
			return nil, fmt.Errorf("%s sidecar timed out after %d s and was restarted", s.spec.Name, int(s.spec.Timeout.Seconds()))
		}
	}
}

// match decodes one stdout line; ok is false for non-protocol output and
// stale responses from earlier requests.
func match(text string, id uint64) (json.RawMessage, bool, error) {
	var message struct {
		ID       *uint64         `json:"id"`
		Response json.RawMessage `json:"response"`
	}
	if json.Unmarshal([]byte(text), &message) != nil || message.ID == nil || *message.ID != id {
		return nil, false, nil
	}
	if message.Response == nil {
		return nil, true, errors.New("bridge response is missing `response`")
	}
	return message.Response, true, nil
}

// Shutdown stops the process and refuses later requests (application exit).
func (s *Sidecar) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.proc != nil {
		s.proc.shutdown()
		s.proc = nil
	}
}

// pid is for tests.
func (s *Sidecar) pid() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc == nil || s.proc.cmd.Process == nil {
		return 0
	}
	return s.proc.cmd.Process.Pid
}
