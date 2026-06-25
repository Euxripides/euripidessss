package api

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

const duneAuthRefreshSkew = 60 * time.Second

func duneStoredAuthNeedsRefresh(auth duneStoredAuth, now time.Time) bool {
	expiresAt, ok := duneStoredAuthExpiresAt(auth)
	if !ok {
		return false
	}
	return !expiresAt.After(now.Add(duneAuthRefreshSkew))
}

func duneStoredAuthCacheExpiry(auth duneStoredAuth, now time.Time, maxAge time.Duration) time.Time {
	expiresAt := now.Add(maxAge)
	jwtExpiresAt, ok := duneStoredAuthExpiresAt(auth)
	if !ok {
		return expiresAt
	}
	refreshAt := jwtExpiresAt.Add(-duneAuthRefreshSkew)
	if refreshAt.Before(now) {
		return now
	}
	if refreshAt.Before(expiresAt) {
		return refreshAt
	}
	return expiresAt
}

func duneStoredAuthExpiresAt(auth duneStoredAuth) (time.Time, bool) {
	token := duneCookieValue(auth.Cookie, "auth-id-token")
	if token == "" {
		token = duneBearerToken(auth.Authorization)
	}
	if token == "" {
		return time.Time{}, false
	}
	return duneJWTExpiresAt(token)
}

func duneBearerToken(authorization string) string {
	authorization = strings.TrimSpace(authorization)
	if authorization == "" {
		return ""
	}
	fields := strings.Fields(authorization)
	if len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
		return fields[1]
	}
	return authorization
}

func duneJWTExpiresAt(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var payload struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil || payload.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(payload.Exp, 0), true
}
