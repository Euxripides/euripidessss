import { useCallback, useEffect, useState } from "react";
import { Card, Spin, Typography, Tag, Select, Empty, Space } from "antd";
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  Node,
  Edge,
  useNodesState,
  useEdgesState,
  Handle,
  Position,
  NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { fetchGraph, GraphData } from "./analyticsApi";
import { shortAddr } from "./format";

const { Title, Text } = Typography;

interface AddressNodeData {
  label?: string;
  risk?: number;
  degree?: number;
  kind?: string;
}

function AddressNode({ data }: NodeProps) {
  const meta = (data ?? {}) as AddressNodeData;
  const color = (risk: number) =>
    risk >= 60 ? "#f5222d" : risk >= 30 ? "#fa8c16" : "#1677ff";
  return (
    <div
      style={{
        border: `2px solid ${color(meta.risk ?? 0)}`,
        borderRadius: 6,
        padding: "6px 10px",
        background: "#fff",
        minWidth: 120,
      }}
    >
      <Handle type="target" position={Position.Top} />
      <div style={{ fontSize: 11, fontWeight: 600 }}>{shortAddr(meta.label ?? "", 10, 8)}</div>
      <div style={{ fontSize: 10, color: "#888" }}>
        {meta.kind ?? "地址"} · d={meta.degree ?? 0}
      </div>
      <Handle type="source" position={Position.Bottom} />
    </div>
  );
}

const nodeTypes = { address: AddressNode };

export default function GraphPage() {
  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  const [loading, setLoading] = useState(true);
  const [kindFilter, setKindFilter] = useState<string>("all");
  const [graph, setGraph] = useState<GraphData | null>(null);

  const build = useCallback(
    (g: GraphData, kind: string) => {
      const filtered = g.edges.filter((e) => kind === "all" || e.kind === kind);
      const nodeIds = new Set<string>();
      filtered.forEach((e) => {
        nodeIds.add(e.source);
        nodeIds.add(e.target);
      });
      const nodeMap = new Map(g.nodes.map((n) => [n.id, n]));
      const ns: Node[] = Array.from(nodeIds).map((id, i) => {
        const meta = nodeMap.get(id);
        return {
          id,
          type: "address",
          position: {
            x: 80 + ((i * 137) % 1100),
            y: 60 + Math.floor(i / 9) * 110,
          },
          data: {
            label: id,
            kind: meta?.type === "contract" ? "合约" : "地址",
            risk: meta?.risk_score ?? 0,
            degree: meta?.degree ?? 0,
          },
        };
      });
      const es: Edge[] = filtered.map((e, i) => ({
        id: `${e.source}-${e.target}-${i}`,
        source: e.source,
        target: e.target,
        label: e.kind === "TRANSFER" ? `T${e.tx_count ?? ""}` : e.kind === "INTERACTION" ? "I" : "R",
        animated: e.kind === "TRANSFER",
        style: {
          stroke: e.kind === "TRANSFER" ? "#1677ff" : e.kind === "INTERACTION" ? "#52c41a" : "#8c8c8c",
        },
      }));
      setNodes(ns);
      setEdges(es);
    },
    [setNodes, setEdges],
  );

  useEffect(() => {
    fetchGraph(500)
      .then((g) => {
        setGraph(g);
        if (g) build(g, "all");
      })
      .catch(() => setGraph(null))
      .finally(() => setLoading(false));
  }, [build]);

  const onKindChange = (kind: string) => {
    setKindFilter(kind);
    if (graph) build(graph, kind);
  };

  return (
    <div style={{ padding: 16, height: "calc(100vh - 120px)" }}>
      <Title level={4}>地址关系图谱</Title>
      <Card
        title={
          <Space>
            图谱（节点按需展开，Transfer 边流动动画）
            <Select
              value={kindFilter}
              onChange={onKindChange}
              style={{ width: 160 }}
              options={[
                { value: "all", label: "全部边" },
                { value: "TRANSFER", label: "Transfer" },
                { value: "INTERACTION", label: "Interaction" },
                { value: "COMMON_COUNTERPARTY", label: "Relation" },
              ]}
            />
            {graph && (
              <Text type="secondary">
                共 {graph.nodes.length} 节点 / {graph.edges.length} 边（当前显示 Top 500 节点子图，防止渲染卡死）
              </Text>
            )}
          </Space>
        }
        styles={{ body: { height: "calc(100vh - 220px)" } }}
      >
        {loading ? (
          <Spin size="large" style={{ display: "block", margin: "80px auto" }} />
        ) : !graph ? (
          <Empty description="graph.json 未生成，请先运行图谱分析（TestGraph_Correctness）" />
        ) : (
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            fitView
          >
            <Background />
            <Controls />
            <MiniMap pannable zoomable />
          </ReactFlow>
        )}
      </Card>
      <div style={{ marginTop: 8 }}>
        <Tag color="blue">Transfer</Tag>
        <Tag color="green">Interaction</Tag>
        <Tag color="default">Relation</Tag>
        <Tag color="red">risk ≥ 60</Tag>
        <Tag color="orange">risk 30-60</Tag>
        <Tag color="blue">risk &lt; 30</Tag>
      </div>
    </div>
  );
}
