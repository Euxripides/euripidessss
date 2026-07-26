package cryptodownload

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

const (
	csvSignerMaxFrameBytes = 1_000_000
	csvSignerMaxAbandoned  = 256
)

type csvSignerProcess struct {
	nodePath   string
	scriptPath string
	nextID     atomic.Uint64

	mu          sync.Mutex
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	pending     map[string]chan csvSignerResult
	abandoned   map[string]struct{}
	generation  uint64
	done        chan struct{}
	version     csvSignerVersion
	stderrProof string
	stale       bool
	closed      bool
}

func newCSVSignerProcess(nodePath, scriptPath string) *csvSignerProcess {
	return &csvSignerProcess{
		nodePath: nodePath, scriptPath: scriptPath,
		pending: make(map[string]chan csvSignerResult), abandoned: make(map[string]struct{}),
	}
}

func (p *csvSignerProcess) request(ctx context.Context, op string, payload *csvSignerRequest) (csvSignerResponse, error) {
	if op == "sign" && p.consumeStale() {
		if _, err := p.request(ctx, "reload", nil); err != nil {
			p.markStale()
			return csvSignerResponse{}, err
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		response, err := p.requestOnce(ctx, op, payload)
		if err == nil || ctx.Err() != nil || !errors.Is(err, ErrCSVSignerProcess) && !errors.Is(err, ErrCSVSignerProtocol) {
			return response, err
		}
	}
	if op == "sign" && payload != nil {
		return p.oneShot(ctx, *payload, false)
	}
	return csvSignerResponse{}, &csvSignerFailure{Kind: ErrCSVSignerProcess, Version: p.Version(), Detail: "restart limit reached"}
}

func (p *csvSignerProcess) requestOnce(ctx context.Context, op string, payload *csvSignerRequest) (csvSignerResponse, error) {
	id := fmt.Sprintf("%d", p.nextID.Add(1))
	envelope := csvSignerEnvelope{ID: id, Op: op, Payload: payload}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return csvSignerResponse{}, fmt.Errorf("encode signer request: %w", err)
	}
	if len(encoded) > csvSignerMaxFrameBytes {
		return csvSignerResponse{}, &csvSignerFailure{Kind: ErrCSVSignerProtocol, Version: p.Version(), Detail: "request frame exceeds limit"}
	}
	result := make(chan csvSignerResult, 1)
	p.mu.Lock()
	if err := p.startLocked(); err != nil {
		p.mu.Unlock()
		return csvSignerResponse{}, err
	}
	p.pending[id] = result
	if _, err := p.stdin.Write(append(encoded, '\n')); err != nil {
		delete(p.pending, id)
		p.failLocked(p.generation, fmt.Errorf("write request: %w", ErrCSVSignerProcess))
		p.mu.Unlock()
		return csvSignerResponse{}, &csvSignerFailure{Kind: ErrCSVSignerProcess, Version: p.version, Detail: "request write failed"}
	}
	p.mu.Unlock()

	select {
	case reply := <-result:
		return reply.response, reply.err
	case <-ctx.Done():
		p.mu.Lock()
		if _, waiting := p.pending[id]; waiting {
			delete(p.pending, id)
			p.abandoned[id] = struct{}{}
			if len(p.abandoned) > csvSignerMaxAbandoned {
				p.failLocked(p.generation, fmt.Errorf("canceled response backlog exceeded limit: %w", ErrCSVSignerProtocol))
			}
		}
		p.mu.Unlock()
		return csvSignerResponse{}, fmt.Errorf("wait for signer response: %w", ctx.Err())
	}
}

func (p *csvSignerProcess) startLocked() error {
	if p.closed {
		return &csvSignerFailure{Kind: ErrCSVSignerProcess, Version: p.version, Detail: "process manager is closed"}
	}
	if p.cmd != nil {
		return nil
	}
	command := exec.Command(p.nodePath, "--experimental-vm-modules", p.scriptPath, "--service")
	stdin, err := command.StdinPipe()
	if err != nil {
		return fmt.Errorf("open signer stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("open signer stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("open signer stderr: %w", err)
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return &csvSignerFailure{Kind: ErrCSVSignerProcess, Version: p.version, Detail: "process start failed", Cause: err}
	}
	p.generation++
	p.cmd, p.stdin, p.done = command, stdin, make(chan struct{})
	go p.readGeneration(p.generation, command, stdout, stderr, p.done)
	return nil
}

func (p *csvSignerProcess) readGeneration(generation uint64, command *exec.Cmd, stdout, stderr io.Reader, done chan struct{}) {
	proof := make(chan string, 1)
	go func() {
		hasher := sha256.New()
		count, _ := io.Copy(hasher, stderr)
		proof <- fmt.Sprintf("stderr-bytes=%d sha256=%x", count, hasher.Sum(nil)[:8])
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), csvSignerMaxFrameBytes+1)
	for scanner.Scan() {
		if err := p.handleLine(generation, scanner.Bytes()); err != nil {
			p.mu.Lock()
			p.failLocked(generation, err)
			p.mu.Unlock()
			break
		}
	}
	if scannerErr := scanner.Err(); scannerErr != nil {
		p.mu.Lock()
		p.failLocked(generation, fmt.Errorf("stdout framing: %w", ErrCSVSignerProtocol))
		p.mu.Unlock()
	}
	waitErr := command.Wait()
	stderrProof := <-proof
	p.mu.Lock()
	p.stderrProof = stderrProof
	if p.generation == generation && p.cmd == command {
		detail := "process exited"
		if waitErr != nil {
			detail = "process exited unexpectedly"
		}
		p.failLocked(generation, &csvSignerFailure{Kind: ErrCSVSignerProcess, Version: p.version, Detail: detail})
	}
	p.mu.Unlock()
	close(done)
}

func (p *csvSignerProcess) handleLine(generation uint64, line []byte) error {
	if len(line) > csvSignerMaxFrameBytes {
		return fmt.Errorf("oversized signer response: %w", ErrCSVSignerProtocol)
	}
	var response csvSignerResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return fmt.Errorf("decode signer response: %w", ErrCSVSignerProtocol)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.generation != generation {
		return nil
	}
	result, waiting := p.pending[response.ID]
	if !waiting {
		if _, canceled := p.abandoned[response.ID]; canceled {
			delete(p.abandoned, response.ID)
			return nil
		}
		return fmt.Errorf("unknown response id: %w", ErrCSVSignerProtocol)
	}
	delete(p.pending, response.ID)
	if err := validateSignerResponse(response, response.ID); err != nil {
		result <- csvSignerResult{err: err}
		return err
	}
	p.version = versionFromResponse(response)
	if !response.OK {
		result <- csvSignerResult{err: &csvSignerFailure{Kind: ErrCSVSignerRemote, Version: p.version, Detail: response.Error.Code}}
		return nil
	}
	result <- csvSignerResult{response: response}
	return nil
}

func (p *csvSignerProcess) failLocked(generation uint64, cause error) {
	if p.generation != generation {
		return
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	failure := &csvSignerFailure{Kind: ErrCSVSignerProcess, Version: p.version, Detail: "process unavailable", Cause: cause}
	for id, result := range p.pending {
		result <- csvSignerResult{err: failure}
		delete(p.pending, id)
	}
	clear(p.abandoned)
	p.cmd, p.stdin = nil, nil
}
