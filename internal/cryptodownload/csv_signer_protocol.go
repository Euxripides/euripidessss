package cryptodownload

import (
	"errors"
	"fmt"
)

const csvSignerProtocolVersion = "1"

var (
	ErrCSVSignerProcess  = errors.New("csv signer process failure")
	ErrCSVSignerProtocol = errors.New("csv signer protocol failure")
	ErrCSVSignerRemote   = errors.New("csv signer remote failure")
)

type csvSignerRequest struct {
	Method   string `json:"method"`
	URL      string `json:"url"`
	Body     string `json:"body"`
	Chain    string `json:"chain"`
	Address  string `json:"address"`
	DeviceID string `json:"deviceId"`
}

type csvSignerEnvelope struct {
	ID      string            `json:"id"`
	Op      string            `json:"op"`
	Payload *csvSignerRequest `json:"payload,omitempty"`
}

type csvSignerResponse struct {
	ID               string                `json:"id"`
	OK               bool                  `json:"ok"`
	ProtocolVersion  string                `json:"protocolVersion"`
	BuildFingerprint string                `json:"buildFingerprint"`
	EntryModule      string                `json:"entryModule"`
	Headers          map[string]string     `json:"headers"`
	Error            *csvSignerRemoteError `json:"error"`
}

type csvSignerRemoteError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type csvSignerVersion struct {
	ProtocolVersion  string
	BuildFingerprint string
	EntryModule      string
}

func (v csvSignerVersion) String() string {
	if v.BuildFingerprint == "" {
		return "protocol=" + firstNonEmpty(v.ProtocolVersion, "unknown") + " build=unknown"
	}
	fingerprint := v.BuildFingerprint
	if len(fingerprint) > 16 {
		fingerprint = fingerprint[:16]
	}
	return fmt.Sprintf("protocol=%s build=%s", firstNonEmpty(v.ProtocolVersion, "unknown"), fingerprint)
}

type csvSignerResult struct {
	response csvSignerResponse
	err      error
}

type csvSignerFailure struct {
	Kind      error
	Version   csvSignerVersion
	Detail    string
	Cause     error
	Retryable bool
}

func (e *csvSignerFailure) Error() string {
	message := fmt.Sprintf("OKLink signer %s: %s", e.Version.String(), e.Detail)
	if e.Cause != nil {
		return message + ": " + e.Cause.Error()
	}
	return message
}

func (e *csvSignerFailure) Unwrap() []error {
	return []error{e.Kind, e.Cause}
}

type csvSignerVersionFailure struct {
	Version csvSignerVersion
	Cause   error
}

func (e *csvSignerVersionFailure) Error() string {
	return fmt.Sprintf("OKLink CSV 请求签名失效 (50113; signer %s): %v", e.Version.String(), e.Cause)
}

func (e *csvSignerVersionFailure) Unwrap() []error {
	return []error{ErrPermanentSignature, e.Cause}
}

func versionFromResponse(response csvSignerResponse) csvSignerVersion {
	return csvSignerVersion{
		ProtocolVersion:  response.ProtocolVersion,
		BuildFingerprint: response.BuildFingerprint,
		EntryModule:      response.EntryModule,
	}
}

func validateSignerResponse(response csvSignerResponse, requestID string) error {
	if response.ID != requestID {
		return fmt.Errorf("response id mismatch: %w", ErrCSVSignerProtocol)
	}
	if response.ProtocolVersion != csvSignerProtocolVersion {
		return fmt.Errorf("unsupported protocol version: %w", ErrCSVSignerProtocol)
	}
	if response.OK && response.Error != nil {
		return fmt.Errorf("success response contains error: %w", ErrCSVSignerProtocol)
	}
	if !response.OK && response.Error == nil {
		return fmt.Errorf("failure response is missing error: %w", ErrCSVSignerProtocol)
	}
	return nil
}
