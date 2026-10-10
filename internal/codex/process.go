// Package codex supervises a worker's host-local Codex app-server and durable
// execution ledger. It never reads or copies desktop credential files.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProcessOptions pins the executable, arguments, and reported version.
type ProcessOptions struct {
	Executable      string   `json:"executable"`
	ExpectedVersion string   `json:"expected_version"`
	Arguments       []string `json:"arguments"`
	testSkipVersion bool
}

// RPCMessage is local app-server protocol data, confined to this adapter.
type RPCMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

// RPCError contains a protocol failure; its raw message never crosses the worker boundary.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Process owns one hidden app-server child and one reader for its stdio stream.
type Process struct {
	command   *exec.Cmd
	input     io.WriteCloser
	writeMu   sync.Mutex
	mu        sync.Mutex
	pending   map[string]chan RPCMessage
	nextID    uint64
	events    chan RPCMessage
	done      chan struct{}
	waitDone  chan struct{}
	closeOnce sync.Once
	stopOnce  sync.Once
	contain   func() error
}

// StartProcess performs the documented initialization handshake. Tests pass a
// disposable fake executable; normal workers use their locally signed-in CLI.
func StartProcess(ctx context.Context, options ProcessOptions) (*Process, error) {
	if options.Executable == "" || options.ExpectedVersion == "" {
		return nil, errors.New("codex executable and exact version are required")
	}
	if !options.testSkipVersion {
		versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		// #nosec G204 -- only an owner-configured executable is invoked, with an argument array and exact version check.
		versionCommand := exec.CommandContext(versionCtx, options.Executable, "--version")
		hideProcess(versionCommand)
		version, err := versionCommand.Output()
		cancel()
		if err != nil || strings.TrimSpace(string(version)) != "codex-cli "+options.ExpectedVersion {
			return nil, errors.New("codex version does not match the worker's pinned contract")
		}
	}
	arguments := options.Arguments
	if len(arguments) == 0 {
		arguments = []string{"app-server", "--stdio"}
	}
	// #nosec G204 -- the owner pins this host-local executable; task input cannot supply an executable or arguments.
	command := exec.CommandContext(ctx, options.Executable, arguments...)
	command.WaitDelay = 2 * time.Second
	hideProcess(command)
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	// Raw diagnostics can contain account or repository data. Protocol failures
	// are classified below instead of copying child stderr into LAN events.
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		_ = input.Close()
		return nil, errors.New("could not start the pinned Codex process")
	}
	contain, err := containProcess(command)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = input.Close()
		return nil, errors.New("could not contain the Codex process tree")
	}
	process := &Process{command: command, input: input, contain: contain, pending: make(map[string]chan RPCMessage), events: make(chan RPCMessage, 256), done: make(chan struct{}), waitDone: make(chan struct{})}
	go process.read(output)
	go func() {
		_ = command.Wait()
		process.closeOnce.Do(func() { close(process.done) })
		close(process.waitDone)
	}()
	var initialized map[string]any
	initializeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := process.Call(initializeCtx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "brainslug_codex", "title": "Brainslug Codex", "version": "0.1.0"}, "capabilities": map[string]bool{"experimentalApi": false}}, &initialized); err != nil {
		_ = process.Close()
		return nil, err
	}
	if err := process.Notify("initialized", map[string]any{}); err != nil {
		_ = process.Close()
		return nil, err
	}
	return process, nil
}

func (p *Process) read(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 65536), 4<<20)
	defer p.closeOnce.Do(func() { close(p.done) })
	for scanner.Scan() {
		var message RPCMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return
		}
		if len(message.ID) != 0 && message.Method == "" {
			p.mu.Lock()
			pending := p.pending[string(message.ID)]
			p.mu.Unlock()
			if pending != nil {
				select {
				case pending <- message:
				default:
				}
			}
			continue
		}
		select {
		case p.events <- message:
		case <-p.done:
			return
		}
	}
}

func (p *Process) write(message RPCMessage) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if len(data) > 4<<20 {
		return errors.New("codex message is too large")
	}
	_, err = p.input.Write(append(data, '\n'))
	return err
}

// Call executes one correlated protocol request without retrying a lost reply.
func (p *Process) Call(ctx context.Context, method string, params, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	p.nextID++
	id := strconv.FormatUint(p.nextID, 10)
	reply := make(chan RPCMessage, 1)
	p.pending[id] = reply
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := p.write(RPCMessage{ID: json.RawMessage(id), Method: method, Params: data}); err != nil {
		return errors.New("codex protocol connection was lost")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return errors.New("codex process ended with execution state unknown")
	case message := <-reply:
		if message.Error != nil {
			return fmt.Errorf("codex protocol request %s failed with code %d", method, message.Error.Code)
		}
		if out != nil {
			if err := json.Unmarshal(message.Result, out); err != nil {
				return errors.New("codex returned an invalid protocol response")
			}
		}
		return nil
	}
}

// Notify sends a local protocol notification.
func (p *Process) Notify(method string, params any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return p.write(RPCMessage{Method: method, Params: data})
}

// Respond resolves one server request using its exact correlation ID.
func (p *Process) Respond(id json.RawMessage, result any) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return p.write(RPCMessage{ID: id, Result: data})
}

// Reject refuses an unsupported server interaction without fabricating its response type.
func (p *Process) Reject(id json.RawMessage) error {
	return p.write(RPCMessage{ID: id, Error: &RPCError{Code: -32601, Message: "interaction is not supported by this worker"}})
}

// Events exposes only this child's event stream.
func (p *Process) Events() <-chan RPCMessage { return p.events }

// Done signals process or protocol termination.
func (p *Process) Done() <-chan struct{} { return p.done }

// Close terminates the owned child and waits for it to be reaped.
func (p *Process) Close() error {
	p.stopOnce.Do(func() {
		_ = p.input.Close()
		_ = p.contain()
		if p.command.Process != nil {
			_ = p.command.Process.Kill()
		}
	})
	<-p.waitDone
	return nil
}
