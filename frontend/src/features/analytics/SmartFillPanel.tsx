// SmartFillPanel 智能数据补充面板（Smart Download Orchestrator 前端展示）。
//
// 交互流程（设计文档 §15）：
//   选中地址 → Inspector「智能补充」→ 本面板：
//   1) 数据需求分析 + 本地覆盖检查（Coverage Resolver，已覆盖自动跳过）
//   2) Provider 选择展示（✓ SQD / ✓ RPC / ⚠ 手动）
//   3) 开始补充 → /api/scheduler/expand → 轮询 status → 任务级进度
//   4) 终态后「刷新图谱」联动更新地址关系图
import { useEffect, useRef, useState } from "react";
import { Alert, Button, Checkbox, message, Modal, Progress, Tag } from "antd";
import { CheckCircleOutlined, CloudDownloadOutlined, ReloadOutlined, ThunderboltOutlined } from "@ant-design/icons";
import {
  DATASET_LABELS,
  expandSmartFill,
  fetchCoverage,
  fetchCloudRuntime,
  fetchSchedulerStatus,
  PROVIDER_LABELS,
  type CoverageResult,
  type CloudRuntimeStatus,
  type Dataset,
  type PlanStatus,
  type SchedulerPlan,
  type SchedulerTask,
} from "./schedulerApi";
import "./smart-fill.css";

const DEFAULT_DATASETS: Dataset[] = ["transactions", "balance"];

interface SmartFillPanelProps {
  open: boolean;
  address: string;
  chain: string;
  onClose: () => void;
  /** 完成后刷新图谱（GraphPage 重新拉取 graph 数据）。 */
  onRefreshGraph: () => void;
}

const TERMINAL: PlanStatus[] = ["READY_FOR_GRAPH", "FAILED"];

/** V3 健康降级判定：候选 Provider 的原因中含降级/冷却/熔断/成功率低关键词。 */
const isDegraded = (t: SchedulerTask): boolean =>
  t.candidates.some((c) => c.reasons.some((r) => /降级|冷却|熔断|偏低|503/.test(r)));

const STATUS_LABELS: Record<PlanStatus, string> = {
  ANALYZING_REQUIREMENT: "分析需求",
  SELECTING_PROVIDER: "选择数据源",
  BUILDING_PLAN: "生成计划",
  EXECUTING: "执行中",
  RETRYING: "重试中",
  FALLBACK: "切换数据源",
  VALIDATING: "校验数据",
  MERGING: "合并资产",
  CLOUD_ADMISSION: "应急 Cloud 准入",
  CLOUD_QUEUED: "应急 Cloud 排队",
  CLOUD_RUNNING: "应急 Cloud 执行",
  WAITING_RETRY: "等待重试",
  READY_FOR_GRAPH: "完成",
  FAILED: "失败",
};

export default function SmartFillPanel({ open, address, chain, onClose, onRefreshGraph }: SmartFillPanelProps) {
  const [coverage, setCoverage] = useState<CoverageResult | null>(null);
  const [cloudRuntime, setCloudRuntime] = useState<CloudRuntimeStatus | null>(null);
  const [checked, setChecked] = useState<Dataset[]>(DEFAULT_DATASETS);
  const [plan, setPlan] = useState<SchedulerPlan | null>(null);
  const [starting, setStarting] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const pollTimer = useRef<number | null>(null);
  const autoRefreshed = useRef(false);

  // 打开时：重置状态 + 覆盖检查
  useEffect(() => {
    if (!open) return;
    setPlan(null);
    setChecked(DEFAULT_DATASETS);
    setCoverage(null);
    setCloudRuntime(null);
    autoRefreshed.current = false;
    let alive = true;
    void fetchCoverage(chain, [address]).then((res) => {
      if (!alive || !res) return;
      setCoverage(res);
      // 已覆盖的数据集默认不勾选（避免重复下载）；balance/labels 为实时/外部信息默认勾选
      const have = new Set(res.items.filter((i) => i.have).map((i) => i.dataset));
      setChecked((prev) => prev.filter((d) => !have.has(d) || d === "balance"));
    });
    void fetchCloudRuntime().then((res) => {
      if (alive && res) setCloudRuntime(res.runtime);
    });
    return () => {
      alive = false;
      if (pollTimer.current !== null) window.clearInterval(pollTimer.current);
      pollTimer.current = null;
    };
  }, [open, address, chain]);

  // 轮询计划状态（连续失败 5 次自动停止，避免请求风暴）
  const stopPolling = () => {
    if (pollTimer.current !== null) {
      window.clearInterval(pollTimer.current);
      pollTimer.current = null;
    }
  };
  const poll = (planId: string) => {
    stopPolling();
    let failures = 0;
    pollTimer.current = window.setInterval(async () => {
      const res = await fetchSchedulerStatus(planId);
      if (!res?.plan) {
        failures += 1;
        if (failures >= 5) {
          stopPolling();
          void message.error("查询计划状态多次失败，请稍后手动刷新页面查看");
        }
        return;
      }
      failures = 0;
      setPlan(res.plan);
      if (TERMINAL.includes(res.plan.status)) {
        stopPolling();
        if (res.plan.status === "READY_FOR_GRAPH") {
          void message.success("智能数据补充完成，数据已就绪");
          // Phase 5 §32-33：Cloud 数据索引后 Graph 自动增量刷新（无需人工点击）
          if (!autoRefreshed.current) {
            autoRefreshed.current = true;
            void onRefresh();
          }
        } else {
          void message.warning("智能数据补充失败，详见任务列表");
        }
      }
    }, 2000);
  };

  const onStart = async () => {
    if (checked.length === 0) {
      void message.info("请至少勾选一类数据");
      return;
    }
    setStarting(true);
    try {
      const res = await expandSmartFill(address, chain, checked);
      if (!res?.plan) {
        void message.error("启动失败：后端返回错误");
        return;
      }
      setPlan(res.plan);
      void message.success("智能数据补充已启动");
      poll(res.plan_id);
    } catch {
      void message.error("启动智能数据补充失败");
    } finally {
      setStarting(false);
    }
  };

  const onRefresh = async () => {
    setRefreshing(true);
    try {
      await onRefreshGraph();
    } finally {
      setRefreshing(false);
    }
  };

  const done = plan != null && TERMINAL.includes(plan.status);
  const running = plan != null && !done;
  const failed = plan?.status === "FAILED";
  const cloudActive = plan != null && ["CLOUD_ADMISSION", "CLOUD_QUEUED", "CLOUD_RUNNING"].includes(plan.status);

  return (
    <Modal
      open={open}
      onCancel={onClose}
      footer={null}
      width={560}
      centered
      destroyOnHidden
      className="smart-fill-modal"
      title={
        <span className="smart-fill-title">
          <ThunderboltOutlined /> 智能数据补充
          <span className="smart-fill-address">{address}</span>
        </span>
      }
    >
      <div className="smart-fill-body">
        {/* 需求分析 + Provider 展示 */}
        <section className="smart-fill-section">
          <h4>数据需求分析（自动选择最佳数据源）</h4>
          {coverage == null ? (
            <div className="smart-fill-loading">正在检查本地数据覆盖…</div>
          ) : (
            <ul className="smart-fill-requirements">
              {coverage.items.map((item) => {
                const disabled = running || done;
                const isManual = item.dataset === "labels";
                const covered = item.have;
                return (
                  <li key={item.dataset}>
                    <Checkbox
                      checked={checked.includes(item.dataset)}
                      disabled={disabled}
                      onChange={(e) => {
                        setChecked((prev) =>
                          e.target.checked
                            ? [...prev, item.dataset]
                            : prev.filter((d) => d !== item.dataset),
                        );
                      }}
                    >
                      {DATASET_LABELS[item.dataset]}
                    </Checkbox>
                    <span className="smart-fill-provider">
                      {isManual ? (
                        <Tag className="smart-fill-tag-manual">⚠ 需手动</Tag>
                      ) : item.dataset === "token_transfer" ? (
                        <Tag className="smart-fill-tag-auto">✓ SQD / ⚡ RPC 恢复 / ☁ 应急兜底</Tag>
                      ) : (
                        <Tag className="smart-fill-tag-auto">✓ {PROVIDER_LABELS[item.dataset === "transactions" ? "aws" : "rpc"]}</Tag>
                      )}
                    </span>
                    <span className={`smart-fill-coverage ${covered ? "is-covered" : ""}`}>
                      {covered ? `已有数据（${item.tx_count} 笔），默认跳过` : item.note}
                    </span>
                  </li>
                );
              })}
            </ul>
          )}
        </section>

        {/* 执行进度 */}
        {plan != null && (
          <section className="smart-fill-section">
            <h4>
              执行进度
              <span className={`smart-fill-plan-status ${done ? (failed ? "is-failed" : "is-done") : ""}`}>
                {STATUS_LABELS[plan.status]}
                {running && <span className="smart-fill-spinner" />}
              </span>
            </h4>
            {cloudActive && (
              <Alert
                className="smart-fill-cloud-alert"
                type="warning"
                showIcon
                message="应急 Cloud 通道已启用"
                description="常规数据源当前均不可用，系统已自动启用应急 Cloud 数据通道（Tier 100 最后兜底）。"
              />
            )}
            {cloudRuntime != null && !cloudRuntime.available && (
              <div className="smart-fill-task-detail">
                Cloud Worker：{cloudRuntime.state}
                {cloudRuntime.reason ? `（${cloudRuntime.reason}）` : ""}
              </div>
            )}
            {cloudRuntime != null && cloudRuntime.available && (
              <div className="smart-fill-task-detail">
                Cloud Worker：{cloudRuntime.state}
                {cloudRuntime.mode ? ` / ${cloudRuntime.mode}` : ""}
                {" · "}排队 {cloudRuntime.queued_jobs}
                {" · "}执行中 {cloudRuntime.leased_jobs}
                {cloudRuntime.current_chunk ? ` · Chunk ${cloudRuntime.current_chunk}` : ""}
                {cloudRuntime.rows_exported ? ` · ${cloudRuntime.rows_exported} 行` : ""}
              </div>
            )}
            <ul className="smart-fill-tasks">
              {plan.tasks.map((t) => (
                <li key={t.id}>
                  <div className="smart-fill-task-head">
                    <span className="smart-fill-task-name">
                      {DATASET_LABELS[t.requirement.dataset]}
                      <Tag className="smart-fill-task-provider">[{PROVIDER_LABELS[t.provider] ?? t.provider}]</Tag>
                      {isDegraded(t) && (
                        <Tag className="smart-fill-tag-degraded" title={t.candidates.map((c) => c.reasons.join("；")).join("；")}>
                          ⚠ 降级
                        </Tag>
                      )}
                    </span>
                    <span className={`smart-fill-task-status is-${t.status}`}>
                      {t.status === "done" && <CheckCircleOutlined />}
                      {TASK_LABELS[t.status]}
                    </span>
                  </div>
                  {t.requirement.note && <div className="smart-fill-task-detail">{t.requirement.note}</div>}
                  {t.status === "running" && (
                    <Progress percent={Math.round(t.progress * 100)} size="small" showInfo={false} strokeColor="var(--fg-accent)" />
                  )}
                  {t.status === "failed" && <div className="smart-fill-task-error">{t.error}</div>}
                  {t.status === "skipped" && t.error && <div className="smart-fill-task-error">{t.error}</div>}
                  {t.status === "done" && t.result?.summary && <div className="smart-fill-task-detail">{t.result.summary}</div>}
                </li>
              ))}
            </ul>
            {plan.stage_detail && <div className="smart-fill-stage">{plan.stage_detail}</div>}
          </section>
        )}

        {/* 操作区 */}
        <div className="smart-fill-footer">
          {!running && !done && (
            <Button type="primary" icon={<CloudDownloadOutlined />} loading={starting} onClick={onStart} className="smart-fill-start">
              开始补充
            </Button>
          )}
          {done && !failed && (
            <Button type="primary" icon={<ReloadOutlined />} loading={refreshing} onClick={onRefresh} className="smart-fill-start">
              刷新图谱
            </Button>
          )}
          <Button onClick={onClose}>关闭</Button>
        </div>
      </div>
    </Modal>
  );
}

const TASK_LABELS: Record<string, string> = {
  pending: "等待",
  running: "执行中",
  done: "完成",
  failed: "失败",
  skipped: "跳过",
};
