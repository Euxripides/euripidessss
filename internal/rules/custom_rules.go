package rules

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"crypto/sha1"
	"sort"
)

var customRulesPath string

func SetCustomRulesPath(path string) {
	customRulesPath = path
}

// CustomRulesData represents the structure of custom_rules.json
type CustomRulesData struct {
	Providers         map[string][]map[string]interface{} `json:"providers"`
	FlowMappingRules  []map[string]interface{}            `json:"flow_mapping_rules,omitempty"`
	DirectionAliases  map[string]string                   `json:"direction_aliases,omitempty"`
}

func LoadCustomRules() (*CustomRulesData, error) {
	if customRulesPath == "" {
		customRulesPath = filepath.Join("backend", "config", "custom_rules.json")
	}
	data := &CustomRulesData{
		Providers: map[string][]map[string]interface{}{
			"alipay": {}, "wechat": {}, "bank": {},
		},
	}
	if _, err := os.Stat(customRulesPath); os.IsNotExist(err) {
		return data, nil
	}
	raw, err := os.ReadFile(customRulesPath)
	if err != nil {
		return nil, fmt.Errorf("read custom rules: %w", err)
	}
	if err := json.Unmarshal(raw, data); err != nil {
		return nil, fmt.Errorf("parse custom rules: %w", err)
	}
	return data, nil
}

func SaveCustomRule(provider string, rule map[string]interface{}) (*CustomRulesData, error) {
	data, err := LoadCustomRules()
	if err != nil {
		return nil, err
	}
	rules := data.Providers[provider]
	sig, _ := rule["signature"].(string)
	if sig != "" {
		filtered := make([]map[string]interface{}, 0)
		for _, r := range rules {
			if r["signature"] != sig {
				filtered = append(filtered, r)
			}
		}
		rules = filtered
	}
	rules = append(rules, rule)
	data.Providers[provider] = rules
	if err := saveCustomRules(data); err != nil {
		return nil, err
	}
	return data, nil
}

func CustomTableCandidates(provider string) []map[string]interface{} {
	data, err := LoadCustomRules()
	if err != nil {
		return nil
	}
	rules, ok := data.Providers[provider]
	if !ok {
		return nil
	}
	return rules
}

func FlowMappingRule(signature string) map[string]interface{} {
	if signature == "" {
		return nil
	}
	data, err := LoadCustomRules()
	if err != nil {
		return nil
	}
	for _, rule := range data.FlowMappingRules {
		if rSig, ok := rule["signature"].(string); ok && rSig == signature {
			return rule
		}
	}
	return nil
}

func SaveFlowMappingRule(rule map[string]interface{}) (*CustomRulesData, error) {
	sig, _ := rule["signature"].(string)
	sig = strings.TrimSpace(sig)
	if sig == "" {
		return nil, fmt.Errorf("字段映射规则缺少表头签名")
	}
	data, err := LoadCustomRules()
	if err != nil {
		return nil, err
	}
	filtered := make([]map[string]interface{}, 0)
	for _, r := range data.FlowMappingRules {
		if rSig, ok := r["signature"].(string); ok && rSig != sig {
			filtered = append(filtered, r)
		}
	}
	filtered = append(filtered, rule)
	data.FlowMappingRules = filtered
	if err := saveCustomRules(data); err != nil {
		return nil, err
	}
	return data, nil
}

func LoadDirectionAliases() map[string]string {
	data, err := LoadCustomRules()
	if err != nil {
		return nil
	}
	result := make(map[string]string)
	for k, v := range data.DirectionAliases {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key != "" && (val == "进" || val == "出") {
			result[key] = val
		}
	}
	return result
}

func SaveDirectionAliases(aliases map[string]string) (*CustomRulesData, error) {
	data, err := LoadCustomRules()
	if err != nil {
		return nil, err
	}
	if data.DirectionAliases == nil {
		data.DirectionAliases = make(map[string]string)
	}
	for k, v := range aliases {
		key := strings.TrimSpace(k)
		val := strings.TrimSpace(v)
		if key != "" && (val == "进" || val == "出") {
			data.DirectionAliases[key] = val
		}
	}
	if err := saveCustomRules(data); err != nil {
		return nil, err
	}
	return data, nil
}


// GenerateColumnSignature creates a deterministic hash based on the sorted column list
// Used to detect if we've seen this column combination before
func GenerateColumnSignature(columns []string) string {
	sorted := make([]string, len(columns))
	copy(sorted, columns)
	sort.Strings(sorted)
	h := sha1.New()
	for _, c := range sorted {
		h.Write([]byte(c))
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func saveCustomRules(data *CustomRulesData) error {
	dir := filepath.Dir(customRulesPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(customRulesPath, raw, 0644)
}

