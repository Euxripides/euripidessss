import type { CSSProperties, ReactNode } from "react";
import { Skeleton } from "antd";

export type MetricTone = "blue" | "green" | "amber" | "red" | "neutral";

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <header className="ds-page-header">
      <div>
        <h1>{title}</h1>
        {description ? <p>{description}</p> : null}
      </div>
      {actions ? <div className="ds-page-actions">{actions}</div> : null}
    </header>
  );
}

export function MetricCard({
  title,
  value,
  detail,
  icon,
  tone = "blue",
  loading = false,
}: {
  title: string;
  value: ReactNode;
  detail?: ReactNode;
  icon: ReactNode;
  tone?: MetricTone;
  loading?: boolean;
}) {
  return (
    <article className={`ds-metric-card ds-tone-${tone}`}>
      <div className="ds-metric-icon" aria-hidden="true">{icon}</div>
      <div className="ds-metric-copy">
        <span>{title}</span>
        {loading ? <Skeleton.Input active size="small" /> : <strong>{value}</strong>}
        {detail ? <small>{detail}</small> : null}
      </div>
    </article>
  );
}

export function Section({
  title,
  description,
  extra,
  className = "",
  children,
}: {
  title: string;
  description?: string;
  extra?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section className={`ds-section ${className}`.trim()}>
      <header className="ds-section-header">
        <div>
          <h2>{title}</h2>
          {description ? <p>{description}</p> : null}
        </div>
        {extra}
      </header>
      {children}
    </section>
  );
}

export function DetailPanel({
  title,
  description,
  extra,
  size = "default",
  className = "",
  style,
  children,
}: {
  title?: ReactNode;
  description?: string;
  extra?: ReactNode;
  size?: "default" | "small";
  className?: string;
  style?: CSSProperties;
  children: ReactNode;
}) {
  return (
    <section
      className={`ds-detail-panel ${size === "small" ? "ds-detail-panel-small" : ""} ${className}`.trim()}
      style={style}
    >
      {title || description || extra ? (
        <header className="ds-detail-header">
          <div>
            {title ? <h2>{title}</h2> : null}
            {description ? <p>{description}</p> : null}
          </div>
          {extra ? <div className="ds-detail-extra">{extra}</div> : null}
        </header>
      ) : null}
      <div className="ds-detail-body">{children}</div>
    </section>
  );
}

export function StatusDot({
  tone,
  label,
}: {
  tone: "success" | "warning" | "risk" | "neutral";
  label: string;
}) {
  return (
    <span className={`ds-status ds-status-${tone}`}>
      <i aria-hidden="true" />
      {label}
    </span>
  );
}
