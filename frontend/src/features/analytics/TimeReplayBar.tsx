// V3 时间轴资金回放（设计 §3-§5、§48-1）。
import { Button, Slider, Space, Tag } from "antd";
import { PauseOutlined, PlayCircleOutlined } from "@ant-design/icons";

interface TimeReplayBarProps {
  minTime: number;
  maxTime: number;
  currentTime: number;
  playing: boolean;
  speed: number;
  onTogglePlay: () => void;
  onChange: (t: number) => void;
  onSpeed: (s: number) => void;
}

function fmt(t: number): string {
  if (!t) return "—";
  return new Date(t * 1000).toISOString().slice(0, 16).replace("T", " ");
}

export default function TimeReplayBar({
  minTime,
  maxTime,
  currentTime,
  playing,
  speed,
  onTogglePlay,
  onChange,
  onSpeed,
}: TimeReplayBarProps) {
  const disabled = maxTime <= minTime;
  return (
    <div className="flow-replay-bar">
      <Button
        size="small"
        icon={playing ? <PauseOutlined /> : <PlayCircleOutlined />}
        disabled={disabled}
        onClick={onTogglePlay}
      >
        {playing ? "暂停" : "回放"}
      </Button>
      <span className="flow-replay-time">{fmt(currentTime)}</span>
      <Slider
        className="flow-replay-slider"
        min={minTime}
        max={Math.max(minTime + 1, maxTime)}
        value={Math.max(minTime, Math.min(maxTime, currentTime))}
        disabled={disabled}
        onChange={onChange}
        tooltip={{ formatter: (v) => fmt(Number(v ?? 0)) }}
      />
      <Space size={4}>
        {[1, 2, 5].map((s) => (
          <Tag
            key={s}
            color={speed === s ? "blue" : "default"}
            onClick={() => onSpeed(s)}
            style={{ cursor: "pointer" }}
          >
            {s}x
          </Tag>
        ))}
      </Space>
    </div>
  );
}

