package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/etl/backend/internal/dunetools"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

var (
	duneBatchMu      sync.Mutex
	duneBatchManager *dunetools.Manager
	allAccounts      []dunetools.Account
	allAccountsMu    sync.Mutex
)

func registerDuneBatchRoutes(api *gin.RouterGroup) {
	api.POST("/dune/batch/start", HandleDuneBatchStart)
	api.POST("/dune/batch/stop", HandleDuneBatchStop)
	api.GET("/dune/batch/status", HandleDuneBatchStatus)
	api.GET("/dune/batch/accounts", HandleDuneBatchAccounts)
	api.GET("/dune/batch/export", HandleDuneBatchExport)
	api.POST("/dune/batch/captcha-resume", HandleDuneBatchCaptchaResume)
	api.DELETE("/dune/batch/accounts", HandleDuneBatchDeleteAccounts)
}

func HandleDuneBatchStart(c *gin.Context) {
	var payload dunetools.StartRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "invalid request: " + err.Error()})
		return
	}
	snapshot, err := currentDuneBatchManager().Start(c.Request.Context(), payload)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}
	c.JSON(http.StatusOK, snapshotWithSavedAccounts(snapshot))
}

func HandleDuneBatchStop(c *gin.Context) {
	c.JSON(http.StatusOK, snapshotWithSavedAccounts(currentDuneBatchManager().Stop()))
}

func HandleDuneBatchStatus(c *gin.Context) {
	c.JSON(http.StatusOK, snapshotWithSavedAccounts(currentDuneBatchManager().Status()))
}

func HandleDuneBatchAccounts(c *gin.Context) {
	manager := currentDuneBatchManager()
	allAccountsMu.Lock()
	accs := make([]dunetools.Account, len(allAccounts))
	copy(accs, allAccounts)
	allAccountsMu.Unlock()
	// Merge with current batch accounts
	batch := manager.Accounts()
	merged := mergeAccounts(accs, batch)
	c.JSON(http.StatusOK, gin.H{"accounts": merged})
}

func HandleDuneBatchCaptchaResume(c *gin.Context) {
	var payload struct {
		Email string `json:"email"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil || payload.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "email required"})
		return
	}
	accounts := currentDuneBatchManager().Accounts()
	for _, a := range accounts {
		if a.Email == payload.Email {
			// Create signal file so next bridge run doesn't re-trigger CAPTCHA immediately
			if a.CaptchaFile != "" {
				os.WriteFile(a.CaptchaFile, []byte("1"), 0600)
			}
			// Re-register this account
			currentDuneBatchManager().RetryCaptcha(dunetools.Account{Email: a.Email, Username: a.Username, Password: a.Password})
			c.JSON(http.StatusOK, gin.H{"status": "ok", "email": a.Email})
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"detail": "account not found"})
}

func HandleDuneBatchExport(c *gin.Context) {
	manager := currentDuneBatchManager()
	allAccountsMu.Lock()
	accs := make([]dunetools.Account, len(allAccounts))
	copy(accs, allAccounts)
	allAccountsMu.Unlock()
	batch := manager.Accounts()
	accounts := mergeAccounts(accs, batch)
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="dune_accounts.csv"`)
	writer := csv.NewWriter(c.Writer)
	if err := writer.Write([]string{"email", "username", "password", "status", "created_at", "team_id", "cookie", "authorization", "access_token", "error"}); err != nil {
		log.Warn().Err(err).Msg("dune_batch_export_header_failed")
		return
	}
	for _, account := range accounts {
		if err := writer.Write([]string{
			account.Email,
			account.Username,
			account.Password,
			string(account.Status),
			account.CreatedAt,
			strconv.FormatInt(account.TeamID, 10),
			account.Cookie,
			account.Authorization,
			account.AccessToken,
			account.Error,
		}); err != nil {
			log.Warn().Err(err).Msg("dune_batch_export_row_failed")
			return
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		log.Warn().Err(err).Msg("dune_batch_export_flush_failed")
	}
}

func currentDuneBatchManager() *dunetools.Manager {
	duneBatchMu.Lock()
	defer duneBatchMu.Unlock()
	if duneBatchManager != nil {
		return duneBatchManager
	}
	root := "."
	if cfg != nil {
		root = cfg.RootDir
	}
	// Load persisted accounts
	loadAllAccounts(root)

	duneBatchManager = dunetools.NewDefaultManager(root, func(ctx context.Context, account dunetools.Account) {
		saveDuneBatchAuth(account)
		persistAccount(root, account)
	})
	return duneBatchManager
}

func accountsFilePath(root string) string {
	return filepath.Join(root, "backend", "data", "dune", "accounts.json")
}

func loadAllAccounts(root string) {
	allAccountsMu.Lock()
	defer allAccountsMu.Unlock()
	path := accountsFilePath(root)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if err := json.Unmarshal(data, &allAccounts); err != nil {
		log.Warn().Err(err).Msg("dune_batch_load_accounts_failed")
		allAccounts = nil
	}
}

func persistAccount(root string, account dunetools.Account) {
	allAccountsMu.Lock()
	// Update or append
	found := false
	for i, a := range allAccounts {
		if a.Email == account.Email {
			allAccounts[i] = account
			found = true
			break
		}
	}
	if !found {
		allAccounts = append(allAccounts, account)
	}
	toSave := make([]dunetools.Account, len(allAccounts))
	copy(toSave, allAccounts)
	allAccountsMu.Unlock()

	path := accountsFilePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		log.Warn().Err(err).Msg("dune_batch_mkdir_failed")
		return
	}
	data, err := json.MarshalIndent(toSave, "", "  ")
	if err != nil {
		log.Warn().Err(err).Msg("dune_batch_marshal_failed")
		return
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		log.Warn().Err(err).Msg("dune_batch_save_accounts_failed")
	}
}

func HandleDuneBatchDeleteAccounts(c *gin.Context) {
	var payload struct {
		Emails []string `json:"emails"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil || len(payload.Emails) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"detail": "emails required"})
		return
	}
	root := "."
	if cfg != nil {
		root = cfg.RootDir
	}
	emailSet := make(map[string]bool, len(payload.Emails))
	for _, e := range payload.Emails {
		emailSet[e] = true
	}

	// Delete from persisted accounts
	allAccountsMu.Lock()
	var kept []dunetools.Account
	deleted := 0
	for _, a := range allAccounts {
		if emailSet[a.Email] {
			deleted++
		} else {
			kept = append(kept, a)
		}
	}
	allAccounts = kept
	allAccountsMu.Unlock()

	// Also remove from current batch task if running
	mgr := currentDuneBatchManager()
	mgr.RemoveAccounts(payload.Emails)

	// Persist
	path := accountsFilePath(root)
	data, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "marshal failed"})
		return
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "save failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": deleted})
}

func replaceDuneBatchManagerForTest(manager *dunetools.Manager) func() {
	duneBatchMu.Lock()
	old := duneBatchManager
	duneBatchManager = manager
	duneBatchMu.Unlock()
	return func() {
		duneBatchMu.Lock()
		duneBatchManager = old
		duneBatchMu.Unlock()
	}
}

func saveDuneBatchAuth(account dunetools.Account) {
	if account.Cookie == "" {
		return
	}
	if err := saveDuneStoredAuth(duneStoredAuth{
		Cookie:        account.Cookie,
		Authorization: account.Authorization,
		AccessToken:   account.AccessToken,
		TeamID:        account.TeamID,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		log.Warn().Err(err).Str("email", account.Email).Msg("dune_batch_save_auth_failed")
	}
}
