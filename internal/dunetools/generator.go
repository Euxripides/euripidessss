package dunetools

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode"
)

type classCounts struct {
	upper  int
	lower  int
	digit  int
	symbol int
}

func GenerateAccount(domain string) (Account, error) {
	return generateAccount(domain, time.Now().UTC())
}

func generateAccount(domain string, now time.Time) (Account, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return Account{}, fmt.Errorf("domain is required")
	}
	id, err := randomHex(4)
	if err != nil {
		return Account{}, fmt.Errorf("generate account id: %w", err)
	}
	password, err := GeneratePassword()
	if err != nil {
		return Account{}, fmt.Errorf("generate password: %w", err)
	}

	emailLocal := "dune_" + id
	// Gmail alias: use + suffix format
	if strings.HasSuffix(domain, "gmail.com") || strings.HasSuffix(domain, "googlemail.com") {
		emailLocal = "dune_" + id
	}
	return Account{
		Email:     fmt.Sprintf("%s@%s", emailLocal, domain),
		Username:  "u" + id,
		Password:  password,
		Status:    AccountStatusPending,
		CreatedAt: now.Format(time.RFC3339),
	}, nil
}

func GeneratePassword() (string, error) {
	parts := []struct {
		chars string
		count int
	}{
		{"ABCDEFGHJKLMNPQRSTUVWXYZ", 4},
		{"abcdefghijkmnpqrstuvwxyz", 8},
		{"23456789", 2},
		{"!@#$%^&*", 2},
	}
	password := make([]byte, 0, 16)
	for _, part := range parts {
		for range part.count {
			ch, err := randChar(part.chars)
			if err != nil {
				return "", err
			}
			password = append(password, ch)
		}
	}
	if err := shuffle(password); err != nil {
		return "", err
	}
	return string(password), nil
}

func randomHex(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func randChar(charset string) (byte, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
	if err != nil {
		return 0, fmt.Errorf("read random index: %w", err)
	}
	return charset[n.Int64()], nil
}

func shuffle(values []byte) error {
	for i := len(values) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return fmt.Errorf("read shuffle index: %w", err)
		}
		values[i], values[j.Int64()] = values[j.Int64()], values[i]
	}
	return nil
}

func passwordClassCounts(password string) classCounts {
	var counts classCounts
	for _, ch := range password {
		switch {
		case unicode.IsUpper(ch):
			counts.upper++
		case unicode.IsLower(ch):
			counts.lower++
		case unicode.IsDigit(ch):
			counts.digit++
		default:
			counts.symbol++
		}
	}
	return counts
}
