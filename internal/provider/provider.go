package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/etl/backend/internal/parser"
)

// Provider defines the interface for processing provider-specific files
type Provider interface {
	Name() string
	ProcessDirectory(sourceDir, outputDir string) (*Result, error)
	ProcessFile(path string) (*Result, error)
}

// Result holds the processing result
type Result struct {
	OutputPath  string                 `json:"output_path"`
	Sources     []Source               `json:"sources"`
	TableRows   map[string]int         `json:"table_rows"`
	UnifiedRows int                    `json:"unified_rows"`
	Quality     map[string]interface{} `json:"quality"`
}

// Source describes a processed source file/sheet
type Source struct {
	Path      string   `json:"path"`
	SheetName string   `json:"sheet_name,omitempty"`
	TableType string   `json:"table_type"`
	HeaderRow int      `json:"header_row"`
	Rows      int      `json:"rows"`
	Columns   []string `json:"columns"`
	Notes     []string `json:"notes,omitempty"`
}

// NewProvider returns the appropriate provider based on name
func NewProvider(name string) (Provider, error) {
	switch strings.ToLower(name) {
	case "alipay":
		return &AlipayProvider{}, nil
	case "wechat":
		return &WechatProvider{}, nil
	case "bank":
		return &BankProvider{}, nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
}

// DetectProvider detects the provider from file content
func DetectProvider(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if parser.ExcelSuffixes[ext] {
		return detectFromExcel(path)
	}
	return detectFromCSV(path)
}

func detectFromExcel(path string) string {
	sheets, err := parser.ReadExcelFile(path)
	if err != nil {
		return "unknown"
	}
	firstSheet := ""
	for name := range sheets {
		firstSheet = name
		break
	}
	rows := sheets[firstSheet]
	if len(rows) == 0 {
		return "unknown"
	}
	text := strings.Join(rows[0], " ") + " " + strings.Join(rows[min(1, len(rows)-1)], " ")
	return classifyText(text)
}

func detectFromCSV(path string) string {
	rows, err := parser.ReadCSVFile(path)
	if err != nil || len(rows) == 0 {
		return "unknown"
	}
	text := strings.Join(rows[0], " ") + " " + strings.Join(rows[min(1, len(rows)-1)], " ")
	return classifyText(text)
}

func classifyText(text string) string {
	lower := strings.ToLower(text)
	if strings.Contains(text, "支付宝") || strings.Contains(lower, "alipay") {
		return "alipay"
	}
	if strings.Contains(text, "微信") || strings.Contains(lower, "wechat") || strings.Contains(text, "财付通") {
		return "wechat"
	}
	if strings.Contains(text, "银行") || strings.Contains(lower, "bank") || strings.Contains(text, "账户") {
		return "bank"
	}
	return "unknown"
}

func scanFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if parser.SupportedSuffixes[ext] {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- Alipay Provider ---

type AlipayProvider struct{}

func (p *AlipayProvider) Name() string { return "alipay" }

func (p *AlipayProvider) ProcessDirectory(sourceDir, outputDir string) (*Result, error) {
	alipayResult, err := parser.ProcessAlipayDirectory(sourceDir, outputDir, "strict")
	if err != nil {
		return nil, err
	}
	return &Result{
		OutputPath:  alipayResult.OutputPath,
		Sources:     convertAlipaySources(alipayResult.Sources),
		TableRows:   alipayResult.TableRows,
		UnifiedRows: alipayResult.UnifiedRows,
		Quality:     alipayResult.Quality,
	}, nil
}

func (p *AlipayProvider) ProcessFile(path string) (*Result, error) {
	return p.ProcessDirectory(filepath.Dir(path), filepath.Join(filepath.Dir(path), "output"))
}

func convertAlipaySources(sources []parser.AlipaySource) []Source {
	result := make([]Source, len(sources))
	for i, s := range sources {
		result[i] = Source{
			Path: s.Path, SheetName: s.SheetName,
			TableType: s.TableType, HeaderRow: s.HeaderRow,
			Rows: s.Rows, Columns: s.Columns, Notes: s.Notes,
		}
	}
	return result
}

// --- Wechat Provider ---

type WechatProvider struct{}

func (p *WechatProvider) Name() string { return "wechat" }

func (p *WechatProvider) ProcessDirectory(sourceDir, outputDir string) (*Result, error) {
	wechatResult, err := parser.ProcessWechatDirectory(sourceDir, outputDir)
	if err != nil {
		return nil, err
	}
	return &Result{
		OutputPath:  wechatResult.OutputPath,
		Sources:     convertWechatSources(wechatResult.Sources),
		TableRows:   wechatResult.TableRows,
		UnifiedRows: wechatResult.UnifiedRows,
		Quality:     wechatResult.Quality,
	}, nil
}

func (p *WechatProvider) ProcessFile(path string) (*Result, error) {
	return p.ProcessDirectory(filepath.Dir(path), filepath.Join(filepath.Dir(path), "output"))
}

func convertWechatSources(sources []parser.WechatSource) []Source {
	result := make([]Source, len(sources))
	for i, s := range sources {
		result[i] = Source{
			Path: s.Path, SheetName: s.SheetName,
			TableType: s.TableType, HeaderRow: s.HeaderRow,
			Rows: s.Rows, Columns: s.Columns, Notes: s.Notes,
		}
	}
	return result
}

// --- Bank Provider ---

type BankProvider struct{}

func (p *BankProvider) Name() string { return "bank" }

func (p *BankProvider) ProcessDirectory(sourceDir, outputDir string) (*Result, error) {
	return ProcessBankDirectory(sourceDir, outputDir)
}

func (p *BankProvider) ProcessFile(path string) (*Result, error) {
	return p.ProcessDirectory(filepath.Dir(path), filepath.Join(filepath.Dir(path), "output"))
}
