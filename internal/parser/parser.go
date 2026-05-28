package parser

import (
	"archive/zip"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/xuri/excelize/v2"
)

// Supported file suffixes
var SupportedSuffixes = map[string]bool{
	".csv": true, ".tsv": true, ".txt": true,
	".xlsx": true, ".xlsm": true, ".xls": true,
}

var ExcelSuffixes = map[string]bool{
	".xlsx": true, ".xlsm": true, ".xls": true,
}

var (
	cleanTextPipePrefixRe = regexp.MustCompile(`^\s*\|\s*`)
	numberCleanupRe       = regexp.MustCompile(`[^\d.\-+]`)
	digitsOnlyRe          = regexp.MustCompile(`^\d+$`)
	excelSerialDatetimeRe = regexp.MustCompile(`^\d+(\.\d+)?$`)
	directionAliases      = map[string]string{
		"D": "出", "借": "出", "借方": "出", "支出": "出",
		"转出": "出", "取": "出", "支": "出", "出账": "出", "O": "出",
		"C": "进", "贷": "进", "贷方": "进", "收入": "进",
		"转入": "进", "存": "进", "入": "进", "入账": "进",
		"收": "进", "+": "进", "-": "出", "进": "进", "出": "出",
	}
)

// NormalizeHeader cleans a header cell value
func NormalizeHeader(v interface{}) string {
	if v == nil {
		return ""
	}
	s := fmt.Sprint(v)
	s = strings.ReplaceAll(s, "\ufeff", "")
	s = strings.ReplaceAll(s, "\u3000", "")
	return strings.TrimSpace(s)
}

// CellToText converts a cell to text
func CellToText(v interface{}) string {
	if v == nil {
		return ""
	}
	s := fmt.Sprint(v)
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

// CleanText cleans a value for NA checks
func CleanText(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	s = cleanTextPipePrefixRe.ReplaceAllString(s, "")
	if s == "" || s == "nan" || s == "None" || s == "NaT" {
		return nil
	}
	return s
}

// ToNumber converts string to float64, handling Chinese currency symbols
func ToNumber(s interface{}) float64 {
	if s == nil {
		return 0
	}
	text := fmt.Sprint(s)
	text = strings.ReplaceAll(text, ",", "")
	text = strings.ReplaceAll(text, "￥", "")
	text = strings.ReplaceAll(text, "¥", "")
	text = strings.ReplaceAll(text, "元", "")
	// Remove non-numeric chars except . - +
	text = numberCleanupRe.ReplaceAllString(text, "")
	val, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0
	}
	return val
}

// NormalizeDatetime converts various date/time formats to standard format
func NormalizeDatetime(s interface{}) string {
	if s == nil {
		return ""
	}
	text := NormalizeHeader(s)
	if text == "" {
		return ""
	}
	text = strings.Trim(text, `"'`)
	if isCanonicalDatetime(text) {
		return text
	}
	if normalized := normalizeExcelSerialDatetime(text); normalized != "" {
		return normalized
	}

	if digitsOnlyRe.MatchString(text) {
		switch len(text) {
		case 8:
			return fmt.Sprintf("%s-%s-%s 00:00:00", text[:4], text[4:6], text[6:8])
		case 10:
			if strings.HasPrefix(text, "19") || strings.HasPrefix(text, "20") {
				return fmt.Sprintf("%s-%s-%s %s:00:00", text[:4], text[4:6], text[6:8], text[8:10])
			}
		case 12:
			return fmt.Sprintf("%s-%s-%s %s:%s:00", text[:4], text[4:6], text[6:8], text[8:10], text[10:12])
		case 14:
			return fmt.Sprintf("%s-%s-%s %s:%s:%s", text[:4], text[4:6], text[6:8], text[8:10], text[10:12], text[12:14])
		case 13:
			if ts, err := strconv.ParseInt(text, 10, 64); err == nil && ts > 0 {
				return time.UnixMilli(ts).Local().Format("2006-01-02 15:04:05")
			}
		}
	}

	if ts, err := strconv.ParseInt(text, 10, 64); err == nil && ts >= 946684800 && ts <= 4102444800 {
		return time.Unix(ts, 0).Local().Format("2006-01-02 15:04:05")
	}

	candidates := datetimeCandidates(text)
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-1-2 15:04:05",
		"2006-1-2 15:4:5",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04",
		"2006-1-2 15:4",
		"2006-1-2 15:04",
		"2006-01-02",
		"2006-1-2",
		"2006-01-02-15.04.05.999999",
		"2006-01-02-15.04.05",
		"2006.01.02",
		"2006.1.2",
		"2006/01/02 15:04:05",
		"2006/1/2 15:04:05",
		"2006/1/2 15:4:5",
		"2006/01/02 15:04",
		"2006/1/2 15:04",
		"2006/1/2 15:4",
		"2006/01/02",
		"2006/1/2",
		"20060102 150405",
		"20060102 1504",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05 MST",
		"2006-01-02 03:04:05 PM",
		"2006-1-2 3:04:05 PM",
		"02-01-2006 15:04:05",
		"2-1-2006 15:04:05",
		"02/01/2006 15:04:05",
		"2/1/2006 15:04:05",
		"01/02/2006 15:04:05",
		"1/2/2006 15:04:05",
		"02-01-2006",
		"2-1-2006",
		"02/01/2006",
		"2/1/2006",
		"01/02/2006",
		"1/2/2006",
	}
	for _, candidate := range candidates {
		for _, f := range formats {
			if t, err := time.ParseInLocation(f, candidate, time.Local); err == nil {
				return t.Format("2006-01-02 15:04:05")
			}
		}
	}
	return text
}

func isCanonicalDatetime(text string) bool {
	if len(text) != len("2006-01-02 15:04:05") {
		return false
	}
	if text[4] != '-' || text[7] != '-' || text[10] != ' ' || text[13] != ':' || text[16] != ':' {
		return false
	}
	for idx := 0; idx < len(text); idx++ {
		switch idx {
		case 4, 7, 10, 13, 16:
			continue
		}
		if text[idx] < '0' || text[idx] > '9' {
			return false
		}
	}
	_, err := time.ParseInLocation("2006-01-02 15:04:05", text, time.Local)
	return err == nil
}

func normalizeExcelSerialDatetime(text string) string {
	if !excelSerialDatetimeRe.MatchString(text) || len(strings.Split(text, ".")[0]) > 5 {
		return ""
	}
	serial, err := strconv.ParseFloat(text, 64)
	if err != nil || serial < 1 || serial > 100000 {
		return ""
	}
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.Local)
	wholeDays := math.Floor(serial)
	seconds := math.Round((serial - wholeDays) * 86400)
	return base.AddDate(0, 0, int(wholeDays)).Add(time.Duration(seconds) * time.Second).Format("2006-01-02 15:04:05")
}

func datetimeCandidates(text string) []string {
	replacer := strings.NewReplacer(
		"\u00a0", " ",
		"T", " ",
		"年", "-",
		"月", "-",
		"日", " ",
		"时", ":",
		"時", ":",
		"分", ":",
		"秒", "",
	)
	normalized := strings.Join(strings.Fields(replacer.Replace(text)), " ")
	normalized = strings.TrimSuffix(normalized, ":")
	candidates := []string{text, normalized}
	if strings.HasSuffix(normalized, "Z") {
		candidates = append(candidates, strings.TrimSuffix(normalized, "Z")+" +0000")
	}
	if strings.Contains(normalized, "/") {
		candidates = append(candidates, strings.ReplaceAll(normalized, "/", "-"))
	}
	if strings.Contains(normalized, ".") {
		candidates = append(candidates, strings.ReplaceAll(normalized, ".", "-"))
	}
	return uniqueStrings(candidates)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

// NormalizeDirection maps direction strings to 进/出
func NormalizeDirection(s interface{}) string {
	if s == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(s))
	if v, ok := directionAliases[text]; ok {
		return v
	}
	return text
}

// IsValidDirection checks if direction is 进 or 出
func IsValidDirection(s string) bool {
	return s == "进" || s == "出"
}

// CleanAccountNumber removes non-digit prefixes/suffixes
func CleanAccountNumber(s interface{}) string {
	if s == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(s))
	// Remove common prefixes: 45-, CNYO, etc.
	re := regexp.MustCompile(`^[A-Za-z]+[\-]?`)
	text = re.ReplaceAllString(text, "")
	// Remove suffixes: -1, _ABC, etc.
	re = regexp.MustCompile(`[\-_][A-Za-z0-9]+$`)
	text = re.ReplaceAllString(text, "")
	return text
}

// FindColumn finds a column in headers matching one of the candidate names
func FindColumn(headers []string, candidates []string) int {
	normHeaders := make([]string, len(headers))
	for i, h := range headers {
		normHeaders[i] = strings.ToLower(NormalizeHeader(h))
	}
	for _, candidate := range candidates {
		cn := strings.ToLower(NormalizeHeader(candidate))
		for i, h := range normHeaders {
			if h == cn || strings.Contains(h, cn) || strings.Contains(cn, h) {
				return i
			}
		}
	}
	return -1
}

// HeaderScore calculates a match score between row cells and expected columns
func HeaderScore(cells []string, expected []string) int {
	score := 0
	normCells := make(map[string]bool)
	for _, c := range cells {
		nc := strings.ToLower(NormalizeHeader(c))
		if nc != "" {
			normCells[nc] = true
		}
	}
	for _, exp := range expected {
		ne := strings.ToLower(NormalizeHeader(exp))
		if normCells[ne] {
			score++
		}
	}
	return score
}

// MakeUnique makes column names unique
func MakeUnique(values []string) []string {
	counts := make(map[string]int)
	result := make([]string, len(values))
	for i, v := range values {
		base := v
		if base == "" {
			base = fmt.Sprintf("未命名_%d", i+1)
		}
		counts[base]++
		if counts[base] == 1 {
			result[i] = base
		} else {
			result[i] = fmt.Sprintf("%s_%d", base, counts[base])
		}
	}
	return result
}

// SafeSheetName sanitizes sheet names
func SafeSheetName(name string) string {
	re := regexp.MustCompile(`[\[\]:*?/\\]`)
	name = re.ReplaceAllString(name, "_")
	if len(name) > 31 {
		name = name[:31]
	}
	return name
}

// FirstNonEmpty returns first non-empty column values
func FirstNonEmpty(data map[string][]string, names []string, row int) string {
	for _, name := range names {
		if data[name] != nil && row < len(data[name]) {
			if v := strings.TrimSpace(data[name][row]); v != "" {
				return v
			}
		}
	}
	return ""
}

// ReadCSVFile reads a CSV/TSV file into rows
func ReadCSVFile(path string) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	sep := ','
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".tsv" {
		sep = '\t'
	}

	// Try multiple encodings
	encodings := []string{"utf-8-sig", "gb18030", "utf-8"}
	var lastErr error
	for _, enc := range encodings {
		// Reset file position
		f.Seek(0, 0)
		rows, err := readCSVWithEncoding(f, sep, enc)
		if err == nil && len(rows) > 0 {
			return rows, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("read csv: %w", lastErr)
}

func readCSVWithEncoding(r io.Reader, sep rune, encoding string) ([][]string, error) {
	// For simplicity, we read raw bytes as-is (assumes UTF-8 or compatible)
	// In production, use golang.org/x/text for GB18030 conversion
	reader := csv.NewReader(r)
	reader.Comma = sep
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	return reader.ReadAll()
}

// ReadExcelFile reads all sheets from an Excel file
func ReadExcelFile(path string) (map[string][][]string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open excel: %w", err)
	}
	defer f.Close()

	result := make(map[string][][]string)
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		result[sheet] = rows
	}
	return result, nil
}

// ReadExcelSheet reads a specific sheet from Excel
func ReadExcelSheet(path, sheet string) ([][]string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open excel: %w", err)
	}
	defer f.Close()
	return f.GetRows(sheet)
}

// ReadFile reads any supported file, returns sheet_name -> rows
func ReadFile(path string) (map[string][][]string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ExcelSuffixes[ext] {
		return ReadExcelFile(path)
	}
	rows, err := ReadCSVFile(path)
	if err != nil {
		return nil, err
	}
	return map[string][][]string{"": rows}, nil
}

// DetectDelimiter detects the delimiter in a text sample
func DetectDelimiter(lines []string, path string) string {
	if strings.ToLower(filepath.Ext(path)) == ".tsv" {
		return "\t"
	}
	sample := strings.Join(lines, "\n")
	candidates := []string{",", "\t", "|", ";"}
	best := ","
	bestCount := 0
	for _, c := range candidates {
		count := strings.Count(sample, c)
		if count > bestCount {
			bestCount = count
			best = c
		}
	}
	return best
}

// NormalizeEmbeddedCSVRows handles rows where single column contains CSV data
func NormalizeEmbeddedCSVRows(rows [][]string) [][]string {
	result := make([][]string, len(rows))
	for i, row := range rows {
		if len(row) <= 2 && len(row) > 0 && strings.Count(row[0], ",") >= 3 {
			r := csv.NewReader(strings.NewReader(row[0]))
			parsed, _ := r.Read()
			if parsed != nil {
				result[i] = parsed
				continue
			}
		}
		result[i] = row
	}
	return result
}

// TrimRows removes trailing empty cells
func TrimRows(rows [][]string) [][]string {
	result := make([][]string, len(rows))
	for i, row := range rows {
		cleaned := make([]string, len(row))
		for j, cell := range row {
			cleaned[j] = CellToText(cell)
		}
		// Remove trailing empties
		for len(cleaned) > 0 && cleaned[len(cleaned)-1] == "" {
			cleaned = cleaned[:len(cleaned)-1]
		}
		result[i] = cleaned
	}
	return result
}

// DataFrameFromHeader creates data from header row onwards
func DataFrameFromHeader(rows [][]string, headerRow int) ([][]string, []string) {
	if headerRow >= len(rows) {
		return [][]string{}, nil
	}
	headers := rows[headerRow]
	// Clean headers
	for i, h := range headers {
		headers[i] = NormalizeHeader(h)
	}
	data := rows[headerRow+1:]
	return data, headers
}

// Round2 rounds to 2 decimal places
func Round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// FloatToStr formats a float64 to string with 2 decimal places
func FloatToStr(v float64) string {
	return fmt.Sprintf("%.2f", v)
}

// ExtractZip extracts data files from a zip archive
func ExtractZip(zipPath string, targetDir string) ([]string, error) {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, err
	}
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	allowedExts := map[string]bool{
		".xlsx": true, ".xlsm": true, ".xls": true,
		".csv": true, ".tsv": true,
	}
	var extracted []string

	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		filename := filepath.Base(f.Name)
		ext := strings.ToLower(filepath.Ext(filename))
		if !allowedExts[ext] {
			continue
		}
		// Safety: prevent path traversal
		if strings.Contains(f.Name, "..") || filepath.IsAbs(f.Name) {
			continue
		}
		outPath := filepath.Join(targetDir, filename)
		rc, err := f.Open()
		if err != nil {
			continue
		}
		outFile, err := os.Create(outPath)
		if err != nil {
			rc.Close()
			continue
		}
		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			continue
		}
		extracted = append(extracted, outPath)
	}
	if len(extracted) == 0 {
		return nil, fmt.Errorf("no data files found in zip: %s", filepath.Base(zipPath))
	}
	return extracted, nil
}

// GetFileModTime returns file modification time
func GetFileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// Abs64 returns absolute value of float64
func Abs64(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// IsDigit checks if a string represents a number
func IsDigit(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) && r != '.' && r != '-' && r != '+' {
			return false
		}
	}
	return true
}

// SourceLocation generates source location string like Python's source_locations
func SourceLocation(path string, rowIdx int, headerRow int) string {
	return fmt.Sprintf("%s:%d", path, rowIdx+headerRow+2)
}

// SourceLocations generates source location strings for all rows
func SourceLocations(path string, count int, headerRow int) []string {
	locs := make([]string, count)
	for i := 0; i < count; i++ {
		locs[i] = SourceLocation(path, i, headerRow)
	}
	return locs
}
