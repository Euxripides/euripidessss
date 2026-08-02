package intelligence

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Runtime Event 日志测试（V2 设计 §13）──

func TestRuntimeEventLogAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "runtime-events.log")
	log := NewRuntimeEventLog(path)

	log.TaskCreated("inv-1", &InvestigationTask{ID: "t1", Type: TaskPathTrace, Round: 1})
	log.TaskExecuted("inv-1", &InvestigationTask{ID: "t1", Type: TaskPathTrace, Round: 1}, "发现 2 条路径")
	log.TaskRetried("inv-1", &InvestigationTask{ID: "t2", Type: TaskFlowAnalysis, MaxRetries: 3, Round: 1}, 1)
	log.TaskFailed("inv-1", &InvestigationTask{ID: "t3", Type: TaskRiskScan, Round: 1}, "数据源错误")
	log.Replanned("inv-1", ReplanHighValue, 1, 2)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 5 {
		t.Fatalf("应 5 行事件, got %d", len(lines))
	}
	// 每行是合法 JSON 且含事件类型
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	types := map[string]bool{}
	for scanner.Scan() {
		line := scanner.Text()
		for _, typ := range []string{
			`"type":"task_created"`, `"type":"executed"`, `"type":"retried"`,
			`"type":"failed"`, `"type":"replanned"`,
		} {
			if strings.Contains(line, typ) {
				types[typ] = true
			}
		}
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			t.Fatalf("非 JSON 行: %s", line)
		}
	}
	if len(types) != 5 {
		t.Fatalf("应包含 5 种事件类型, got %d", len(types))
	}
}

func TestRuntimeEventLogMemoryOnly(t *testing.T) {
	log := NewRuntimeEventLog("") // 仅内存：不落盘
	log.TaskCreated("inv-1", &InvestigationTask{ID: "t1", Type: TaskPathTrace, Round: 1})
	// 无 panic、无文件
	if _, err := os.Stat("runtime-events.log"); !os.IsNotExist(err) {
		t.Fatal("仅内存模式不应生成文件")
	}
}

func TestRuntimeEventLogNil(t *testing.T) {
	var log *RuntimeEventLog // nil 安全
	log.TaskCreated("inv-1", &InvestigationTask{ID: "t1", Type: TaskPathTrace, Round: 1})
}
