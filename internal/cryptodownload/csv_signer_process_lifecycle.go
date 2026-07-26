package cryptodownload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

func (p *csvSignerProcess) Version() csvSignerVersion {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.version
}

func (p *csvSignerProcess) abandonedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.abandoned)
}

func (p *csvSignerProcess) markStale() {
	p.mu.Lock()
	p.stale = true
	p.mu.Unlock()
}

func (p *csvSignerProcess) consumeStale() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	stale := p.stale
	p.stale = false
	return stale
}

func (p *csvSignerProcess) Reload(ctx context.Context) (csvSignerVersion, error) {
	response, err := p.request(ctx, "reload", nil)
	if err != nil {
		return p.Version(), err
	}
	return versionFromResponse(response), nil
}

func (p *csvSignerProcess) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	stdin, command, done := p.stdin, p.cmd, p.done
	if stdin != nil {
		_ = stdin.Close()
	}
	p.mu.Unlock()
	if command == nil || done == nil {
		return nil
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		if command.Process == nil {
			return nil
		}
		return errors.Join(command.Process.Kill(), &csvSignerFailure{Kind: ErrCSVSignerProcess, Version: p.Version(), Detail: "graceful close timed out"})
	}
}

func (p *csvSignerProcess) oneShot(ctx context.Context, payload csvSignerRequest, allowLegacy bool) (csvSignerResponse, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return csvSignerResponse{}, fmt.Errorf("encode one-shot request: %w", err)
	}
	if len(encoded) > csvSignerMaxFrameBytes {
		return csvSignerResponse{}, &csvSignerFailure{Kind: ErrCSVSignerProtocol, Version: p.Version(), Detail: "one-shot request exceeds limit"}
	}
	command := exec.CommandContext(ctx, p.nodePath, "--experimental-vm-modules", p.scriptPath, "--oneshot")
	command.Stdin = bytes.NewReader(encoded)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return csvSignerResponse{}, fmt.Errorf("open one-shot stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return csvSignerResponse{}, fmt.Errorf("open one-shot stderr: %w", err)
	}
	if err := command.Start(); err != nil {
		return csvSignerResponse{}, &csvSignerFailure{Kind: ErrCSVSignerProcess, Version: p.Version(), Detail: "one-shot start failed", Cause: err}
	}
	stderrProof := make(chan string, 1)
	go func() {
		hasher := sha256.New()
		count, _ := io.Copy(hasher, stderr)
		stderrProof <- fmt.Sprintf("stderr-bytes=%d sha256=%x", count, hasher.Sum(nil)[:8])
	}()
	output, readErr := io.ReadAll(io.LimitReader(stdout, csvSignerMaxFrameBytes+1))
	waitErr := command.Wait()
	proof := <-stderrProof
	if readErr != nil || waitErr != nil {
		return csvSignerResponse{}, &csvSignerFailure{Kind: ErrCSVSignerProcess, Version: p.Version(), Detail: "one-shot failed " + proof, Cause: errors.Join(readErr, waitErr)}
	}
	if len(output) > csvSignerMaxFrameBytes {
		return csvSignerResponse{}, &csvSignerFailure{Kind: ErrCSVSignerProtocol, Version: p.Version(), Detail: "one-shot response exceeds limit"}
	}
	var response csvSignerResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return csvSignerResponse{}, &csvSignerFailure{Kind: ErrCSVSignerProtocol, Version: p.Version(), Detail: "one-shot response invalid", Cause: err}
	}
	if len(response.Headers) == 0 {
		return csvSignerResponse{}, &csvSignerFailure{Kind: ErrCSVSignerProtocol, Version: versionFromResponse(response), Detail: "one-shot response has no headers"}
	}
	if !allowLegacy && (response.ProtocolVersion != csvSignerProtocolVersion || response.BuildFingerprint == "") {
		return csvSignerResponse{}, &csvSignerFailure{Kind: ErrCSVSignerProtocol, Version: versionFromResponse(response), Detail: "one-shot response has wrong version shape"}
	}
	return response, nil
}
