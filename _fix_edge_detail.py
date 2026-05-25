import sys

with open('internal/api/handlers.go', 'r', encoding='utf-8') as f:
    content = f.read()

old_stub = '''// HandleFlowEdgeDetail returns edge detail for a job
func HandleFlowEdgeDetail(c *gin.Context) {
\tjobID := c.Query("job_id")
\tsource := c.Query("source")
\ttarget := c.Query("target")
\tif jobID == "" || source == "" || target == "" {
\t\tc.JSON(400, gin.H{"detail": "job_id, source, target required"})
\t\treturn
\t}
\tc.JSON(200, gin.H{"job_id": jobID, "source": source, "target": target, "rows": []map[string]interface{}{}})
}'''

new_impl = '''// HandleFlowEdgeDetail reads the cleaned output file and returns rows matching source/target
func HandleFlowEdgeDetail(c *gin.Context) {
\tjobID := c.Query("job_id")
\tsource := c.Query("source")
\ttarget := c.Query("target")
\tif jobID == "" || source == "" || target == "" {
\t\tc.JSON(400, gin.H{"detail": "job_id, source, target required"})
\t\treturn
\t}

\tpattern := filepath.Join(cfg.OutputDir, "*"+jobID+"*")
\tmatches, err := filepath.Glob(pattern)
\tif err != nil || len(matches) == 0 {
\t\tc.JSON(404, gin.H{"detail": "\u8f93\u51fa\u6587\u4ef6\u4e0d\u5b58\u5728\u6216\u5df2\u88ab\u6e05\u7406\u3002"})
\t\treturn
\t}

\tpath := matches[0]
\text := strings.ToLower(filepath.Ext(path))
\tif !parser.SupportedSuffixes[ext] {
\t\tc.JSON(400, gin.H{"detail": "\u4e0d\u652f\u6301\u7684\u6587\u4ef6\u683c\u5f0f"})
\t\treturn
\t}

\tvar rows [][]string
\tif parser.ExcelSuffixes[ext] {
\t\tsheets, err := parser.ReadExcelFile(path)
\t\tif err != nil {
\t\t\tc.JSON(500, gin.H{"detail": "\u8bfb\u53d6Excel\u6587\u4ef6\u5931\u8d25: " + err.Error()})
\t\t\treturn
\t\t}
\t\tfor _, s := range sheets {
\t\t\trows = append(rows, s...)
\t\t}
\t} else {
\t\trows, err = parser.ReadCSVFile(path)
\t\tif err != nil {
\t\t\tc.JSON(500, gin.H{"detail": "\u8bfb\u53d6CSV\u6587\u4ef6\u5931\u8d25: " + err.Error()})
\t\t\treturn
\t\t}
\t}

\tif len(rows) < 2 {
\t\tc.JSON(200, gin.H{"job_id": jobID, "source": source, "target": target, "rows": []map[string]interface{}{}, "columns": []string{}, "total_rows": 0})
\t\treturn
\t}

\theaders := rows[0]
\tcolIdx := make(map[string]int)
\tfor i, h := range headers {
\t\tcolIdx[parser.NormalizeHeader(h)] = i
\t}

\tgetVal := func(name string, row []string) string {
\t\tif idx, ok := colIdx[parser.NormalizeHeader(name)]; ok && idx < len(row) {
\t\t\treturn row[idx]
\t\t}
\t\treturn ""
\t}

\tvar result []map[string]interface{}
\tfor _, row := range rows[1:] {
\t\town := getVal("\u4ea4\u6613\u5361\u53f7", row)
\t\tif own == "" {
\t\t\town = getVal("\u4ea4\u6613\u8d26\u53f7", row)
\t\t}
\t\tif own == "" {
\t\t\town = getVal("\u4ea4\u6613\u6237\u540d", row)
\t\t}
\t\tif own == "" {
\t\t\town = "\u672c\u65b9\u672a\u77e5"
\t\t}

\t\tcounter := getVal("\u4ea4\u6613\u5bf9\u624b\u8d26\u5361\u53f7", row)
\t\tif counter == "" {
\t\t\tcounter = getVal("\u5bf9\u624b\u6237\u540d", row)
\t\t}
\t\tif counter == "" {
\t\t\tcounter = "\u5bf9\u624b\u672a\u77e5"
\t\t}

\t\tdir := getVal("\u6536\u4ed8\u6807\u5fd7", row)
\t\tvar rowSource, rowTarget string
\t\tif dir == "\u51fa" {
\t\t\trowSource, rowTarget = own, counter
\t\t} else if dir == "\u8fdb" {
\t\t\trowSource, rowTarget = counter, own
\t\t} else {
\t\t\tcontinue
\t\t}

\t\tif rowSource != source || rowTarget != target {
\t\t\tcontinue
\t\t}

\t\tm := make(map[string]interface{})
\t\tfor j, h := range headers {
\t\t\tif j < len(row) {
\t\t\t\tm[parser.NormalizeHeader(h)] = row[j]
\t\t\t}
\t\t}
\t\tresult = append(result, m)
\t}

\tvar columns []string
\tif len(result) > 0 {
\t\tfor k := range result[0] {
\t\t\tcolumns = append(columns, k)
\t\t}
\t}

\tvar totalAmount float64
\tfor _, row := range result {
\t\tif v, ok := row[parser.NormalizeHeader("\u4ea4\u6613\u91d1\u989d")]; ok {
\t\t\tif s, ok := v.(string); ok {
\t\t\t\ttotalAmount += parser.ToNumber(s)
\t\t\t}
\t\t}
\t}

\tc.JSON(200, gin.H{
\t\t"job_id":        jobID,
\t\t"source":        source,
\t\t"target":        target,
\t\t"total_rows":    len(result),
\t\t"returned_rows": len(result),
\t\t"amount":        totalAmount,
\t\t"columns":       columns,
\t\t"rows":          result,
\t\t"truncated":     false,
\t})
}'''

if old_stub in content:
    content = content.replace(old_stub, new_impl, 1)
    with open('internal/api/handlers.go', 'w', encoding='utf-8') as f:
        f.write(content)
    print("OK: replacement succeeded")
else:
    print("ERROR: old stub not found in file")
    idx = content.find('HandleFlowEdgeDetail returns')
    if idx >= 0:
        print("Found at", idx)
        print(repr(content[idx:idx+300]))

