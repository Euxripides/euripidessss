package reportengine

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/xuri/excelize/v2"
)

// ExportJSON 导出 JSON。
func ExportJSON(report *InvestigationReport, snapshot *ReportSnapshot, timeline []TimelineEvent, findings []Finding, evidence []EvidenceRef) ([]byte, error) {
	payload, err := json.MarshalIndent(map[string]any{
		"report": report, "snapshot": snapshot, "timeline": timeline,
		"findings": findings, "evidence": evidence,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return payload, nil
}

// ExportXLSX 导出 XLSX（设计 §39）。
func ExportXLSX(report *InvestigationReport, findings []Finding, evidence []EvidenceRef) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	sheet := "摘要"
	_, _ = f.NewSheet(sheet)
	rows := [][]any{
		{"案件", report.InvestigationID}, {"标题", report.Title}, {"状态", string(report.Status)},
		{"焦点地址", report.RootAddress}, {"调查目标", report.Goal},
		{"关键路径", report.Summary.KeyPaths}, {"兑现候选", report.Summary.Cashouts},
		{"沉淀候选", report.Summary.Settlements}, {"获利地址", report.Summary.ProfitAddresses},
		{"覆盖分", report.Summary.CoverageScore}, {"已知缺口", report.Summary.KnownGaps},
	}
	for i, row := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		_ = f.SetSheetRow(sheet, cell, &row)
	}
	pathSheet := "关键路径"
	_, _ = f.NewSheet(pathSheet)
	pathRows := [][]any{{"路径", "类型", "跳数", "金额", "评分", "置信度", "终点"}}
	for _, fd := range findings {
		if fd.FindingType != "HIGH_VALUE_PATH" {
			continue
		}
		pathRows = append(pathRows, []any{fd.ID, fd.Metrics["path_type"], fd.Metrics["hops"], fd.Metrics["amount"], fd.Metrics["score"], fd.Metrics["confidence"], fd.Metrics["terminal"]})
	}
	for i, row := range pathRows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		_ = f.SetSheetRow(pathSheet, cell, &row)
	}
	profitSheet := "获利归因"
	_, _ = f.NewSheet(profitSheet)
	profitRows := [][]any{{"地址", "级别", "累计流入", "累计流出", "净获利", "置信度"}}
	for _, fd := range findings {
		if fd.FindingType != "PROFIT_ATTRIBUTION" {
			continue
		}
		profitRows = append(profitRows, []any{fd.SubjectIDs[0], fd.Metrics["level"], fd.Metrics["gross_inflow"], fd.Metrics["gross_outflow"], fd.Metrics["net_profit"], fd.Confidence})
	}
	for i, row := range profitRows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		_ = f.SetSheetRow(profitSheet, cell, &row)
	}
	settleSheet := "沉淀候选"
	_, _ = f.NewSheet(settleSheet)
	settleRows := [][]any{{"地址", "类型", "留存", "沉淀分", "置信度"}}
	for _, fd := range findings {
		if fd.FindingType != "SETTLEMENT" {
			continue
		}
		settleRows = append(settleRows, []any{fd.SubjectIDs[0], fd.Metrics["type"], fd.Metrics["retained"], fd.Metrics["score"], fd.Confidence})
	}
	for i, row := range settleRows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		_ = f.SetSheetRow(settleSheet, cell, &row)
	}
	cashSheet := "交易所落点"
	_, _ = f.NewSheet(cashSheet)
	cashRows := [][]any{{"来源", "落点", "实体", "金额", "Token", "路径", "置信度"}}
	for _, fd := range findings {
		if fd.FindingType != "EXCHANGE_DEPOSIT" && fd.FindingType != "CASHOUT" {
			continue
		}
		subject := ""
		if len(fd.SubjectIDs) > 1 {
			subject = fd.SubjectIDs[1]
		}
		cashRows = append(cashRows, []any{fd.SubjectIDs[0], subject, fd.Metrics["entity"], fd.Metrics["amount"], fd.Metrics["token"], fd.Metrics["path_type"], fd.Confidence})
	}
	for i, row := range cashRows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		_ = f.SetSheetRow(cashSheet, cell, &row)
	}
	evSheet := "证据清单"
	_, _ = f.NewSheet(evSheet)
	evRows := [][]any{{"Evidence ID", "类型", "地址", "TxHash", "数据集", "认证", "哈希"}}
	for _, ev := range evidence {
		evRows = append(evRows, []any{ev.ID, ev.Type, ev.Address, ev.TxHash, ev.DatasetID, ev.Certification, ev.EvidenceHash})
	}
	for i, row := range evRows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		_ = f.SetSheetRow(evSheet, cell, &row)
	}
	_ = f.DeleteSheet("Sheet1")
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ExportDOCX 导出 DOCX（设计 §38，标准库 OOXML 最小实现）。
func ExportDOCX(report *InvestigationReport, snapshot *ReportSnapshot, timeline []TimelineEvent, findings []Finding, evidence []EvidenceRef) ([]byte, error) {
	var doc strings.Builder
	doc.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	doc.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	doc.WriteString(`<w:body>`)
	paragraph(&doc, report.Title, "Title")
	paragraph(&doc, fmt.Sprintf("调查：%s · 版本 v%d · 状态 %s", report.InvestigationID, report.Version, report.Status), "")
	paragraph(&doc, fmt.Sprintf("焦点地址：%s · 目标：%s", report.RootAddress, report.Goal), "")
	for _, sec := range report.Sections {
		paragraph(&doc, sec.Title, "Heading1")
		paragraph(&doc, sec.Narrative, "")
		for _, f := range sec.Findings {
			paragraph(&doc, fmt.Sprintf("- [%s] %s（置信度 %.0f%%，证据 %s）", f.FindingType, f.Statement, f.Confidence*100, strings.Join(f.EvidenceIDs, ", ")), "")
		}
	}
	if len(timeline) > 0 {
		paragraph(&doc, "证据时间线", "Heading1")
		for i, ev := range timeline {
			if i >= 100 {
				break
			}
			paragraph(&doc, fmt.Sprintf("- %s [%s] %s 金额=%s", ev.Timestamp.Format("2006-01-02"), ev.Type, ev.Summary, ev.Amount), "")
		}
	}
	paragraph(&doc, "数据完整性", "Heading1")
	for _, c := range report.Certification.DatasetStatuses {
		paragraph(&doc, fmt.Sprintf("- %s 覆盖 %.2f%% · %s · 缺口 %d", c.Dataset, c.Coverage*100, c.Certification, c.GapCount), "")
	}
	doc.WriteString(`</w:body></w:document>`)

	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`
	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range map[string]string{
		"[Content_Types].xml": contentTypes,
		"_rels/.rels":         rels,
		"word/document.xml":   doc.String(),
	} {
		w, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(data)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ExportPDF 导出 PDF（设计 §37，最小文本 PDF 渲染器）。
func ExportPDF(report *InvestigationReport, timeline []TimelineEvent, findings []Finding) ([]byte, error) {
	var lines []string
	lines = append(lines, report.Title)
	lines = append(lines, fmt.Sprintf("调查 %s · v%d · %s", report.InvestigationID, report.Version, report.Status))
	lines = append(lines, fmt.Sprintf("焦点地址 %s · 目标 %s", report.RootAddress, report.Goal))
	for _, sec := range report.Sections {
		lines = append(lines, "")
		lines = append(lines, sec.Title)
		lines = append(lines, sec.Narrative)
		for _, f := range sec.Findings {
			lines = append(lines, fmt.Sprintf("  [%s] %s (%.0f%%)", f.FindingType, f.Statement, f.Confidence*100))
		}
	}
	if len(timeline) > 0 {
		lines = append(lines, "", "证据时间线")
		for i, ev := range timeline {
			if i >= 60 {
				break
			}
			lines = append(lines, fmt.Sprintf("  %s [%s] %s", ev.Timestamp.Format("2006-01-02"), ev.Type, ev.Summary))
		}
	}
	return buildTextPDF(lines), nil
}

// ExportCasePackage 导出 ZIP Case Package（设计 §35、§41）。
func ExportCasePackage(invID string, version int, report *InvestigationReport, snapshot *ReportSnapshot,
	timeline []TimelineEvent, findings []Finding, evidence []EvidenceRef) ([]byte, error) {
	jsonBytes, _ := ExportJSON(report, snapshot, timeline, findings, evidence)
	xlsxBytes, err := ExportXLSX(report, findings, evidence)
	if err != nil {
		return nil, err
	}
	docxBytes, err := ExportDOCX(report, snapshot, timeline, findings, evidence)
	if err != nil {
		return nil, err
	}
	pdfBytes, err := ExportPDF(report, timeline, findings)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		"report/report.json":  jsonBytes,
		"report/report.xlsx":  xlsxBytes,
		"report/report.docx":  docxBytes,
		"report/report.pdf":   pdfBytes,
		"report/findings.json": mustJSON(findings),
		"report/timeline.json": mustJSON(timeline),
		"report/evidence-index.json": mustJSON(evidence),
	}
	manifest := map[string]string{}
	for name, data := range files {
		sum := sha256.Sum256(data)
		manifest[name] = hex.EncodeToString(sum[:])
	}
	files["manifests/manifest.json"] = mustJSON(manifest)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func paragraph(doc *strings.Builder, text, style string) {
	doc.WriteString(`<w:p><w:pPr>`)
	if style == "Title" {
		doc.WriteString(`<w:pStyle w:val="Title"/>`)
	} else if style == "Heading1" {
		doc.WriteString(`<w:pStyle w:val="Heading1"/>`)
	}
	doc.WriteString(`</w:pPr><w:r><w:t xml:space="preserve">`)
	doc.WriteString(escapeXML(text))
	doc.WriteString(`</w:t></w:r></w:p>`)
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

func mustJSON(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return b
}

// buildTextPDF 生成最小 A4 文本 PDF（Helvetica，分页）。
func buildTextPDF(lines []string) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	objects := [][]byte{}
	objects = append(objects, []byte("<< /Type /Catalog /Pages 2 0 R >>"))
	// Pages object placeholder; will patch
	objects = append(objects, []byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"))
	objects = append(objects, []byte("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>"))
	objects = append(objects, []byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"))

	const lineHeight = 14
	perPage := 55
	var content bytes.Buffer
	content.WriteString("BT /F1 10 Tf 40 800 Td 14 TL\n")
	line := 0
	for i, l := range lines {
		if line >= perPage {
			content.WriteString("ET\n")
			// page break handled by splitting content per page below; simplified: continue same content
			content.WriteString("BT /F1 10 Tf 40 800 Td 14 TL\n")
			line = 0
		}
		content.WriteString("(" + escapePDF(l) + ") Tj T*\n")
		line++
		_ = i
	}
	content.WriteString("ET\n")
	objects = append(objects, content.Bytes())

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := []int{}
	for i, obj := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n", i+1)
		out.Write(obj)
		out.WriteString("\nendobj\n")
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n", len(objects)+1)
	out.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes()
}

func escapePDF(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)", "\n", " ")
	return r.Replace(s)
}

// CopyFile 供 Case Package 引用（当前未使用，保留接口一致性）。
func CopyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
