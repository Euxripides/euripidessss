package dunetools

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"regexp"
	"strings"
)

var urlRe = regexp.MustCompile(`https?://[^\s"'<>()]+`)

func ExtractVerificationLink(raw []byte) (string, error) {
	text := string(raw)
	if parsed, err := decodeMailBody(raw); err == nil && parsed != "" {
		text = parsed + "\n" + text
	}
	var candidates []string
	for _, candidate := range urlRe.FindAllString(text, -1) {
		cleaned := cleanCandidateURL(candidate)
		lower := strings.ToLower(cleaned)
		if strings.Contains(lower, "dune.com") && looksLikeVerificationLink(lower) {
			candidates = append(candidates, cleaned)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("Dune verification link not found")
	}
	// Prefer URLs with unique tokens (email= or token= or code=) over generic fallback links
	for _, c := range candidates {
		lower := strings.ToLower(c)
		if strings.Contains(lower, "token=") || strings.Contains(lower, "code=") {
			return c, nil
		}
	}
	for _, c := range candidates {
		if strings.Contains(strings.ToLower(c), "email=") && !strings.Contains(strings.ToLower(c), "client_id=") {
			return c, nil
		}
	}
	// Fallback: return the first one
	return candidates[0], nil
}

func decodeMailBody(raw []byte) (string, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("read message: %w", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		data, readErr := decodeTransfer(msg.Header.Get("Content-Transfer-Encoding"), msg.Body)
		if readErr != nil {
			return "", readErr
		}
		return string(data), nil
	}
	reader := multipart.NewReader(msg.Body, params["boundary"])
	var parts []string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read message part: %w", err)
		}
		data, err := decodeTransfer(part.Header.Get("Content-Transfer-Encoding"), part)
		if err != nil {
			return "", err
		}
		parts = append(parts, string(data))
	}
	return strings.Join(parts, "\n"), nil
}

func decodeTransfer(encoding string, reader io.Reader) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		data, err := io.ReadAll(quotedPrintableReader(reader))
		if err != nil {
			return nil, fmt.Errorf("decode quoted-printable: %w", err)
		}
		return data, nil
	default:
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		return data, nil
	}
}

func cleanCandidateURL(value string) string {
	value = html.UnescapeString(value)
	value = strings.TrimRight(value, ".,;:)]}")
	return strings.ReplaceAll(value, "=\r\n", "")
}

func looksLikeVerificationLink(value string) bool {
	return strings.Contains(value, "verify") ||
		strings.Contains(value, "verification") ||
		strings.Contains(value, "confirm") ||
		strings.Contains(value, "email")
}
