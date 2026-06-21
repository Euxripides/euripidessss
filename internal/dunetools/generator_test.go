package dunetools

import (
	"regexp"
	"strings"
	"testing"
)

func TestGenerateAccount_usesDuneAlias_whenDomainProvided(t *testing.T) {
	// Given
	domain := "aurore.online"

	// When
	account, err := GenerateAccount(domain)

	// Then
	if err != nil {
		t.Fatalf("GenerateAccount returned error: %v", err)
	}
	if ok, err := regexp.MatchString(`^dune_[0-9a-f]{8}@aurore\.online$`, account.Email); err != nil || !ok {
		t.Fatalf("email has unexpected format: %q", account.Email)
	}
	if ok, err := regexp.MatchString(`^u[0-9a-f]{8}$`, account.Username); err != nil || !ok {
		t.Fatalf("username has unexpected format: %q", account.Username)
	}
	if account.Status != AccountStatusPending {
		t.Fatalf("status = %q", account.Status)
	}
	if strings.TrimSpace(account.Password) == "" {
		t.Fatalf("password should be populated")
	}
}

func TestGeneratePassword_containsRequiredClasses_whenGenerated(t *testing.T) {
	// When
	password, err := GeneratePassword()

	// Then
	if err != nil {
		t.Fatalf("GeneratePassword returned error: %v", err)
	}
	if len(password) != 16 {
		t.Fatalf("password length = %d", len(password))
	}
	counts := passwordClassCounts(password)
	if counts.upper != 4 || counts.lower != 8 || counts.digit != 2 || counts.symbol != 2 {
		t.Fatalf("password class counts = %+v, password=%q", counts, password)
	}
}
