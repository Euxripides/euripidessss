package dunetools

import "testing"

func TestExtractVerificationLink_returnsDuneVerifyURL_whenHtmlMessageContainsLink(t *testing.T) {
	// Given
	raw := []byte("From: hello@dune.com\r\nSubject: Verify your account\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<html><body><a href=\"https://dune.com/verify-email?token=abc&amp;email=dune_test@aurore.online\">Verify</a></body></html>")

	// When
	link, err := ExtractVerificationLink(raw)

	// Then
	if err != nil {
		t.Fatalf("ExtractVerificationLink returned error: %v", err)
	}
	if link != "https://dune.com/verify-email?token=abc&email=dune_test@aurore.online" {
		t.Fatalf("link = %q", link)
	}
}

func TestExtractVerificationLink_decodesQuotedPrintableBody_whenLinkIsWrapped(t *testing.T) {
	// Given
	raw := []byte("From: hello@dune.com\r\nSubject: Verify\r\nContent-Transfer-Encoding: quoted-printable\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nOpen https://dune.com/confirm-email?token=3Dabc123 to verify.")

	// When
	link, err := ExtractVerificationLink(raw)

	// Then
	if err != nil {
		t.Fatalf("ExtractVerificationLink returned error: %v", err)
	}
	if link != "https://dune.com/confirm-email?token=abc123" {
		t.Fatalf("link = %q", link)
	}
}
