package reportengine

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/etl/backend/internal/analyticsapi"
	"github.com/etl/backend/internal/entityintel"
	"github.com/etl/backend/internal/fundflow"
	invcache "github.com/etl/backend/internal/investigation/cache"
)

type fakePolisher struct{}

func (fakePolisher) Polish(_ context.Context, narrative string, _ []Finding) (string, error) {
	return narrative + " (polished by LLM)", nil
}

type fakeFlow struct {
	flows map[string][]analyticsapi.FlowEdge
	stats map[string]*analyticsapi.AddressStats
}

func (f *fakeFlow) Flows(_ context.Context, address, _ string) ([]analyticsapi.FlowEdge, error) {
	return f.flows[address], nil
}

func (f *fakeFlow) AddressStats(_ context.Context, address, _ string) (*analyticsapi.AddressStats, error) {
	if s, ok := f.stats[address]; ok {
		return s, nil
	}
	return &analyticsapi.AddressStats{}, nil
}

type fakeEnt struct {
	known map[string]*entityintel.Entity
}

func (f *fakeEnt) Resolve(_ context.Context, _, address, _ string) (*entityintel.Resolution, error) {
	if e, ok := f.known[address]; ok {
		return &entityintel.Resolution{Address: address, Entity: e, Confidence: e.Confidence,
			ConfidenceTier: string(entityintel.TierFor(e.Confidence))}, nil
	}
	return &entityintel.Resolution{Address: address, ConfidenceTier: string(entityintel.TierUnverified)}, nil
}

func fakeCoverage(_, _, _ string, _, _ uint64) (float64, bool, string) {
	return 0.9, false, "PARTIAL"
}

func newTestEngine(t *testing.T, invID string) (*Engine, *Store) {
	t.Helper()
	src := &fakeFlow{
		flows: map[string][]analyticsapi.FlowEdge{
			"0xa": {{Direction: "outgoing", Counterparty: "0xb", Token: "0xusdt", Amount: "1000", Block: "100"}},
			"0xb": {{Direction: "outgoing", Counterparty: "0xex", Token: "0xusdt", Amount: "990", Block: "101"}},
		},
		stats: map[string]*analyticsapi.AddressStats{
			"0xa":  {TotalIn: "2000", TotalOut: "1000", NetFlow: "1000", Recent30d: 5},
			"0xb":  {TotalIn: "1000", TotalOut: "990", NetFlow: "10", Recent30d: 5},
			"0xex": {TotalIn: "990", TotalOut: "0", NetFlow: "990", Recent30d: 0},
		},
	}
	ents := &fakeEnt{known: map[string]*entityintel.Entity{
		"0xex": {ID: "entity_ex", Name: "Exchange X", EntityType: entityintel.EntityExchange, Confidence: 0.97},
	}}
	ff := fundflow.NewEngine(src, ents, fundflow.NewCache(t.TempDir()), fundflow.DefaultConfig())
	store := NewStore(t.TempDir())
	invCache := invcache.NewStore(t.TempDir())
	_, _ = invCache.UpsertContext(invID, invcache.ContextSnapshot{
		ChainKey: "bsc", FocusAddress: "0xa", Goal: "cashout", FromBlock: 0, ToBlock: 200,
		Tokens: []string{"0xusdt"},
	})
	eng := NewEngine(store, ff, ents, fakeCoverage, invCache)
	return eng, store
}

func TestEvidenceHashStable(t *testing.T) {
	ev := &EvidenceRef{ID: "e1", Type: "TX", Address: "0xAAA", TxHash: "0x1"}
	ev2 := &EvidenceRef{ID: "e1", Type: "TX", Address: "0xaaa", TxHash: "0x1"}
	if EvidenceHash(ev) != EvidenceHash(ev2) {
		t.Fatal("证据哈希应忽略大小写")
	}
	if len(EvidenceHash(ev)) != 64 {
		t.Fatal("证据哈希长度错误")
	}
}

func TestStoreVersioning(t *testing.T) {
	store := NewStore(t.TempDir())
	if store.NextVersion("inv-1") != 1 {
		t.Fatal("首个版本应为 1")
	}
	r1 := &InvestigationReport{ID: "r1", InvestigationID: "inv-1", Version: 1, Status: StatusReady}
	if err := store.Save("inv-1", r1, &ReportSnapshot{ID: "s1"}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if store.NextVersion("inv-1") != 2 {
		t.Fatal("第二版本应为 2")
	}
	r2 := &InvestigationReport{ID: "r2", InvestigationID: "inv-1", Version: 2, Status: StatusReady}
	if err := store.Save("inv-1", r2, &ReportSnapshot{ID: "s2"}, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	list := store.List("inv-1")
	if len(list) != 2 || list[0].Version != 2 {
		t.Fatalf("列表错误: %+v", list)
	}
	got, _, _, _, _ := store.Get("inv-1", "report_v1")
	if got == nil || got.ID != "r1" {
		t.Fatal("读取 v1 失败")
	}
}

func TestEngineGenerate(t *testing.T) {
	eng, _ := newTestEngine(t, "inv-1")
	res, err := eng.Generate(context.Background(), "inv-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if res.Report == nil || res.Report.Version != 1 {
		t.Fatalf("报告生成失败: %+v", res.Report)
	}
	if len(res.Report.Sections) == 0 {
		t.Fatal("报告无章节")
	}
	if len(res.Timeline) == 0 {
		t.Fatal("时间线为空")
	}
	if res.Report.Certification.OverallStatus != "PARTIAL" {
		t.Fatalf("覆盖 90%% 应标记 PARTIAL: %+v", res.Report.Certification)
	}
	if len(res.Report.EvidenceIndex) == 0 {
		t.Fatal("证据索引为空")
	}
	// 版本 2
	res2, err := eng.Generate(context.Background(), "inv-1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Version != 2 {
		t.Fatalf("版本应递增: %d", res2.Version)
	}
}

func TestNarrativeConsistency(t *testing.T) {
	eng, _ := newTestEngine(t, "inv-2")
	res, err := eng.Generate(context.Background(), "inv-2", 3)
	if err != nil {
		t.Fatal(err)
	}
	// 重新用 findings 渲染一次并检查 metrics 出现在叙事中
	_, _, _, findings, _ := eng.Get("inv-2", "report_v1")
	inputs := &AnalysisInputs{RootAddress: "0xa", ChainKey: "bsc", Goal: "cashout"}
	_ = inputs
	_ = findings
	// 直接校验已生成章节：metrics 应出现在各自章节 narrative
	metricsOk := 0
	total := 0
	for _, sec := range res.Report.Sections {
		for _, f := range sec.Findings {
			total++
			if metricsInNarrative(sec.Narrative, f.Metrics) {
				metricsOk++
			}
		}
	}
	if total == 0 || metricsOk != total {
		t.Fatalf("叙事一致性未通过: %d/%d", metricsOk, total)
	}
}

func TestExportFormats(t *testing.T) {
	eng, _ := newTestEngine(t, "inv-3")
	_, err := eng.Generate(context.Background(), "inv-3", 3)
	if err != nil {
		t.Fatal(err)
	}
	report, snapshot, timeline, findings, evidence := eng.Get("inv-3", "report_v1")
	jsonBytes, err := ExportJSON(report, snapshot, timeline, findings, evidence)
	if err != nil || len(jsonBytes) == 0 {
		t.Fatalf("JSON 导出失败: %v", err)
	}
	xlsxBytes, err := ExportXLSX(report, findings, evidence)
	if err != nil || len(xlsxBytes) == 0 {
		t.Fatalf("XLSX 导出失败: %v", err)
	}
	docxBytes, err := ExportDOCX(report, snapshot, timeline, findings, evidence)
	if err != nil || len(docxBytes) == 0 {
		t.Fatalf("DOCX 导出失败: %v", err)
	}
	pdfBytes, err := ExportPDF(report, timeline, findings)
	if err != nil || len(pdfBytes) == 0 {
		t.Fatalf("PDF 导出失败: %v", err)
	}
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF-")) {
		t.Fatal("PDF 头错误")
	}
	zipBytes, err := ExportCasePackage("inv-3", 1, report, snapshot, timeline, findings, evidence)
	if err != nil || len(zipBytes) == 0 {
		t.Fatalf("Case Package 导出失败: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["report/report.json"] || !names["manifests/manifest.json"] {
		t.Fatalf("Case Package 缺少文件: %v", names)
	}
}

func TestDiff(t *testing.T) {
	eng, _ := newTestEngine(t, "inv-4")
	_, _ = eng.Generate(context.Background(), "inv-4", 3)
	_, _ = eng.Generate(context.Background(), "inv-4", 3)
	d := eng.Diff("inv-4", "report_v1", "report_v2")
	if d == nil || d.ReportA != "report_v1" || d.ReportB != "report_v2" {
		t.Fatalf("Diff 结果错误: %+v", d)
	}
}

func TestEvidenceLookup(t *testing.T) {
	eng, _ := newTestEngine(t, "inv-5")
	_, _ = eng.Generate(context.Background(), "inv-5", 3)
	_, _, _, _, evidence := eng.Get("inv-5", "report_v1")
	if len(evidence) == 0 {
		t.Fatal("证据索引为空")
	}
}

func TestReportLanguageInstitutionSignPolish(t *testing.T) {
	eng, _ := newTestEngine(t, "inv-6")
	eng.SetPolisher(fakePolisher{})
	res, err := eng.GenerateWithOptions(context.Background(), "inv-6", 3, "en", "Test Institution")
	if err != nil {
		t.Fatal(err)
	}
	if res.Report.Language != "en" || res.Report.Institution != "Test Institution" {
		t.Fatalf("语言/机构模板错误: %+v", res.Report)
	}
	if res.Report.Sections[0].Title != "Summary" {
		t.Fatalf("英文章节标题错误: %s", res.Report.Sections[0].Title)
	}
	sig, err := eng.SignReport("inv-6", "report_v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sig.Hash) != 64 || sig.Method != "SHA256_LOCAL" {
		t.Fatalf("签名错误: %+v", sig)
	}
	polished, ok, err := eng.PolishSection(context.Background(), "inv-6", "report_v1", "summary")
	if err != nil || !ok || !strings.Contains(polished, "polished by LLM") {
		t.Fatalf("润色失败: %v %v %q", err, ok, polished)
	}
	report, _, _, _, _ := eng.Get("inv-6", "report_v1")
	if report.Signature == nil || report.Signature.Hash != sig.Hash {
		t.Fatal("签名未持久化")
	}
	if !strings.Contains(report.Sections[0].Narrative, "polished by LLM") {
		t.Fatal("润色结果未写回")
	}
}

var _ io.Reader
var _ = time.Now
