package dbimport

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	maxPersistedTaskErrors = 200
	maxPersistedTaskSample = 20
)

type Store struct {
	baseDir string
	mu      sync.Mutex
}

type persistedData struct {
	Connections []Connection  `json:"connections"`
	Mappings    []MappingRule `json:"mappings"`
	Tasks       []ImportTask  `json:"tasks"`
}

func NewStore(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

func (s *Store) ListConnections() ([]PublicConnection, error) {
	data, err := s.load()
	if err != nil {
		return nil, err
	}
	items := make([]PublicConnection, 0, len(data.Connections))
	for _, conn := range data.Connections {
		items = append(items, Public(conn))
	}
	return items, nil
}

func (s *Store) SaveConnection(input Connection) (PublicConnection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.loadUnlocked()
	if err != nil {
		return PublicConnection{}, err
	}
	normalizeConnection(&input)
	if err := validateConnection(input); err != nil {
		return PublicConnection{}, err
	}

	now := time.Now()
	idx := -1
	for i, conn := range data.Connections {
		if strings.EqualFold(conn.Name, input.Name) && conn.ID != input.ID {
			return PublicConnection{}, fmt.Errorf("连接名称不能重复")
		}
		if input.ID != "" && conn.ID == input.ID {
			idx = i
		}
	}
	if input.ID == "" {
		input.ID = uuid.NewString()
		input.CreatedAt = now
	} else if idx >= 0 {
		input.CreatedAt = data.Connections[idx].CreatedAt
		if input.Password == "" && data.Connections[idx].SavePassword {
			input.Password = data.Connections[idx].Password
			input.SavePassword = data.Connections[idx].SavePassword
		}
	}
	if !input.SavePassword {
		input.Password = ""
	}
	input.UpdatedAt = now

	if idx >= 0 {
		data.Connections[idx] = input
	} else {
		data.Connections = append(data.Connections, input)
	}
	if err := s.saveUnlocked(data); err != nil {
		return PublicConnection{}, err
	}
	return Public(input), nil
}

func (s *Store) DeleteConnection(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadUnlocked()
	if err != nil {
		return err
	}
	filtered := data.Connections[:0]
	found := false
	for _, conn := range data.Connections {
		if conn.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, conn)
	}
	if !found {
		return fmt.Errorf("connection not found")
	}
	data.Connections = filtered
	return s.saveUnlocked(data)
}

func (s *Store) GetConnection(id string) (Connection, error) {
	data, err := s.load()
	if err != nil {
		return Connection{}, err
	}
	for _, conn := range data.Connections {
		if conn.ID == id {
			normalizeConnection(&conn)
			return conn, nil
		}
	}
	return Connection{}, fmt.Errorf("connection not found")
}

func (s *Store) SaveMapping(rule MappingRule) (MappingRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadUnlocked()
	if err != nil {
		return MappingRule{}, err
	}
	now := time.Now()
	if rule.ID == "" {
		rule.ID = uuid.NewString()
		rule.CreatedAt = now
	}
	rule.UpdatedAt = now
	rule.TargetVersion = "flow-v1"
	filtered := data.Mappings[:0]
	for _, item := range data.Mappings {
		sameScope := item.ConnectionID == rule.ConnectionID &&
			item.Database == rule.Database &&
			item.Schema == rule.Schema &&
			item.Table == rule.Table &&
			item.SourceColumnsHash == rule.SourceColumnsHash
		if !sameScope && item.ID != rule.ID {
			filtered = append(filtered, item)
		}
	}
	data.Mappings = append(filtered, rule)
	if err := s.saveUnlocked(data); err != nil {
		return MappingRule{}, err
	}
	return rule, nil
}

func (s *Store) FindMapping(ref TableRef, hash string) (MappingRule, bool, error) {
	data, err := s.load()
	if err != nil {
		return MappingRule{}, false, err
	}
	for _, rule := range data.Mappings {
		if rule.ConnectionID == ref.ConnectionID &&
			rule.Database == ref.Database &&
			rule.Schema == ref.Schema &&
			rule.Table == ref.Table &&
			rule.SourceColumnsHash == hash {
			return rule, true, nil
		}
	}
	return MappingRule{}, false, nil
}

func (s *Store) ListMappings(ref TableRef) ([]MappingRule, error) {
	data, err := s.load()
	if err != nil {
		return nil, err
	}
	items := []MappingRule{}
	for _, rule := range data.Mappings {
		if ref.ConnectionID != "" && rule.ConnectionID != ref.ConnectionID {
			continue
		}
		if ref.Database != "" && rule.Database != ref.Database {
			continue
		}
		if ref.Schema != "" && rule.Schema != ref.Schema {
			continue
		}
		if ref.Table != "" && rule.Table != ref.Table {
			continue
		}
		items = append(items, rule)
	}
	return items, nil
}

func (s *Store) DeleteMapping(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadUnlocked()
	if err != nil {
		return err
	}
	filtered := data.Mappings[:0]
	found := false
	for _, rule := range data.Mappings {
		if rule.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, rule)
	}
	if !found {
		return fmt.Errorf("mapping not found")
	}
	data.Mappings = filtered
	return s.saveUnlocked(data)
}

func (s *Store) SaveTask(task ImportTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadUnlocked()
	if err != nil {
		return err
	}
	compactTask(&task)
	filtered := data.Tasks[:0]
	for _, item := range data.Tasks {
		if item.ID != task.ID {
			filtered = append(filtered, item)
		}
	}
	data.Tasks = append(filtered, task)
	return s.saveUnlocked(data)
}

func (s *Store) GetTask(id string) (ImportTask, error) {
	data, err := s.load()
	if err != nil {
		return ImportTask{}, err
	}
	for _, task := range data.Tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return ImportTask{}, fmt.Errorf("task not found")
}

func (s *Store) ListTasks() ([]ImportTask, error) {
	data, err := s.load()
	if err != nil {
		return nil, err
	}
	return data.Tasks, nil
}

func Public(conn Connection) PublicConnection {
	return PublicConnection{
		ID:              conn.ID,
		Name:            conn.Name,
		Type:            conn.Type,
		Host:            conn.Host,
		Port:            conn.Port,
		DefaultDatabase: conn.DefaultDatabase,
		Username:        conn.Username,
		SavePassword:    conn.SavePassword,
		HasPassword:     conn.Password != "",
		SSL:             conn.SSL,
		TimeoutSeconds:  conn.TimeoutSeconds,
		Group:           conn.Group,
		Remark:          conn.Remark,
		CreatedAt:       conn.CreatedAt,
		UpdatedAt:       conn.UpdatedAt,
	}
}

func normalizeConnection(conn *Connection) {
	conn.Name = strings.TrimSpace(conn.Name)
	conn.Host = strings.TrimSpace(conn.Host)
	conn.Username = strings.TrimSpace(conn.Username)
	conn.DefaultDatabase = strings.TrimSpace(conn.DefaultDatabase)
	if conn.Type == "pgsql" || conn.Type == "postgres" {
		conn.Type = DBTypePostgres
	}
	if conn.Type == DBTypeMySQL && conn.Port == 0 {
		conn.Port = 3306
	}
	if conn.Type == DBTypePostgres && conn.Port == 0 {
		conn.Port = 5432
	}
	if conn.TimeoutSeconds <= 0 {
		conn.TimeoutSeconds = 10
	}
}

func validateConnection(conn Connection) error {
	if conn.Name == "" || conn.Type == "" || conn.Host == "" || conn.Port <= 0 || conn.Username == "" {
		return fmt.Errorf("连接名称、类型、主机、端口和用户名为必填项")
	}
	if conn.Type != DBTypeMySQL && conn.Type != DBTypePostgres {
		return fmt.Errorf("数据库类型必须是 mysql 或 postgresql")
	}
	return nil
}

func (s *Store) load() (*persistedData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadUnlocked()
}

func (s *Store) loadUnlocked() (*persistedData, error) {
	path := s.path()
	data := &persistedData{}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return data, nil
		}
		return nil, err
	}
	plain, err := decrypt(raw, s.keyMaterial())
	if err != nil {
		return nil, fmt.Errorf("读取数据库连接配置失败")
	}
	if err := json.Unmarshal(plain, data); err != nil {
		return nil, err
	}
	if compactPersistedData(data) {
		_ = s.saveUnlocked(data)
	}
	return data, nil
}

func (s *Store) saveUnlocked(data *persistedData) error {
	if err := os.MkdirAll(s.baseDir, 0700); err != nil {
		return err
	}
	compactPersistedData(data)
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	encrypted, err := encrypt(raw, s.keyMaterial())
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(), encrypted, 0600)
}

func compactPersistedData(data *persistedData) bool {
	changed := false
	for i := range data.Tasks {
		if compactTask(&data.Tasks[i]) {
			changed = true
		}
	}
	return changed
}

func compactTask(task *ImportTask) bool {
	changed := false
	if len(task.Errors) > maxPersistedTaskErrors {
		task.Errors = task.Errors[:maxPersistedTaskErrors]
		changed = true
	}
	if len(task.Sample) > maxPersistedTaskSample {
		task.Sample = task.Sample[:maxPersistedTaskSample]
		changed = true
	}
	return changed
}

func (s *Store) path() string {
	return filepath.Join(s.baseDir, "db_import_config.enc")
}

func (s *Store) keyMaterial() string {
	current, _ := user.Current()
	host, _ := os.Hostname()
	return s.baseDir + "|" + host + "|" + current.Username
}

func encrypt(plain []byte, material string) ([]byte, error) {
	key := sha256.Sum256([]byte(material))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := append(nonce, gcm.Seal(nil, nonce, plain, nil)...)
	return []byte(base64.StdEncoding.EncodeToString(sealed)), nil
}

func decrypt(raw []byte, material string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(material))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(decoded) < gcm.NonceSize() {
		return nil, fmt.Errorf("invalid config")
	}
	nonce := decoded[:gcm.NonceSize()]
	ciphertext := decoded[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
