package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

type duneAuthRequest struct {
	APIKey        string `json:"api_key"`
	Cookie        string `json:"cookie"`
	Authorization string `json:"authorization"`
	AccessToken   string `json:"access_token"`
}

type duneStoredAuth struct {
	APIKey        string    `json:"api_key,omitempty"`
	Cookie        string    `json:"cookie,omitempty"`
	Authorization string    `json:"authorization,omitempty"`
	AccessToken   string    `json:"access_token,omitempty"`
	TeamID        int64     `json:"team_id,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func HandleDuneAuthStatus(c *gin.Context) {
	auth, _ := loadDuneStoredAuth()
	c.JSON(http.StatusOK, gin.H{
		"has_api_key":   auth.APIKey != "" || strings.TrimSpace(os.Getenv("DUNE_API_KEY")) != "",
		"has_cookie":    auth.Cookie != "",
		"has_web_auth":  auth.AccessToken != "" && (auth.Authorization != "" || duneCookieValue(auth.Cookie, "auth-id-token") != ""),
		"source":        duneAuthSource(auth),
		"login_url":     "https://dune.com/",
		"cookie":        auth.Cookie,
		"authorization": auth.Authorization,
		"access_token":  auth.AccessToken,
		"team_id":       auth.TeamID,
	})
}

func HandleSaveDuneAuth(c *gin.Context) {
	var payload duneAuthRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request: " + err.Error()})
		return
	}
	auth := duneStoredAuth{
		APIKey:        strings.TrimSpace(payload.APIKey),
		Cookie:        strings.TrimSpace(payload.Cookie),
		Authorization: normalizeDuneAuthorization(payload.Authorization),
		AccessToken:   strings.TrimSpace(payload.AccessToken),
		UpdatedAt:     time.Now().UTC(),
	}
	// Preserve existing team_id from stored auth
	if existing, err := loadDuneStoredAuth(); err == nil && existing.TeamID > 0 {
		auth.TeamID = existing.TeamID
	}
	if auth.APIKey == "" && auth.Cookie == "" && auth.AccessToken == "" && auth.Authorization == "" {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "请填写 Dune API Key、Cookie 或官网 Token"})
		return
	}
	if err := saveDuneStoredAuth(auth); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "has_api_key": auth.APIKey != "", "has_cookie": auth.Cookie != "", "has_web_auth": auth.AccessToken != ""})
}

func resolveDuneAPIKey(input string) (string, error) {
	if key := strings.TrimSpace(input); key != "" {
		return key, nil
	}
	if key := strings.TrimSpace(os.Getenv("DUNE_API_KEY")); key != "" {
		return key, nil
	}
	auth, err := loadDuneStoredAuth()
	if err != nil {
		return "", err
	}
	if auth.APIKey != "" {
		return auth.APIKey, nil
	}
	return "", errDuneAuthRequired
}

func resolveDuneCookie(input string) (string, error) {
	if cookie := strings.TrimSpace(input); cookie != "" {
		return cookie, nil
	}
	auth, err := loadDuneStoredAuth()
	if err != nil {
		return "", err
	}
	if auth.Cookie != "" {
		return auth.Cookie, nil
	}
	return "", errDuneAuthRequired
}

type duneWebAuth struct {
	Cookie        string
	Authorization string
	AccessToken   string
}

func resolveDuneWebAuth(cookieInput, authorizationInput, accessTokenInput string) (duneWebAuth, error) {
	auth, err := loadDuneStoredAuth()
	if err != nil {
		log.Warn().Err(err).Msg("dune_load_stored_auth_failed")
		return duneWebAuth{}, err
	}
	cookie := firstDuneNonEmpty(cookieInput, auth.Cookie)
	authorization := normalizeDuneAuthorization(firstDuneNonEmpty(authorizationInput, auth.Authorization))
	webAuth := duneWebAuth{
		Cookie:        strings.TrimSpace(cookie),
		Authorization: authorization,
		AccessToken:   strings.TrimSpace(firstDuneNonEmpty(accessTokenInput, auth.AccessToken)),
	}
	if webAuth.Cookie == "" {
		log.Warn().Msg("dune_web_auth_incomplete_no_cookie")
		return duneWebAuth{}, errDuneAuthRequired
	}
	return webAuth, nil
}

func loadDuneStoredAuth() (duneStoredAuth, error) {
	path := duneAuthFilePath()
	log.Info().Str("path", path).Msg("dune_loading_auth")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Warn().Str("path", path).Msg("dune_auth_file_not_found")
			return duneStoredAuth{}, nil
		}
		return duneStoredAuth{}, err
	}
	var auth duneStoredAuth
	if err := json.Unmarshal(data, &auth); err != nil {
		return duneStoredAuth{}, err
	}
	auth.APIKey = strings.TrimSpace(auth.APIKey)
	auth.Cookie = strings.TrimSpace(auth.Cookie)
	auth.Authorization = normalizeDuneAuthorization(auth.Authorization)
	auth.AccessToken = strings.TrimSpace(auth.AccessToken)
	log.Info().Int("cookie_len", len(auth.Cookie)).Int("auth_len", len(auth.Authorization)).Int("token_len", len(auth.AccessToken)).Msg("dune_stored_auth_loaded")
	return auth, nil
}

func saveDuneStoredAuth(auth duneStoredAuth) error {
	path := duneAuthFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func normalizeDuneAuthorization(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		return value
	}
	return "Bearer " + value
}

func duneCookieValue(cookie, name string) string {
	for _, part := range strings.Split(cookie, ";") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if found && key == name {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func duneAuthFilePath() string {
	root := "."
	if cfg != nil {
		root = cfg.RootDir
	}
	return filepath.Join(root, "backend", "data", "dune", "auth.json")
}

func duneAuthSource(auth duneStoredAuth) string {
	if strings.TrimSpace(os.Getenv("DUNE_API_KEY")) != "" {
		return "env"
	}
	if auth.APIKey != "" {
		return "saved"
	}
	return "missing"
}
