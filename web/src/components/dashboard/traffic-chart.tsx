"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { useTheme } from "next-themes";

export type ChartSeries = { key: string; label: string; color: string; fill?: boolean };
export type ChartPoint = { at: string; values: Record<string, number | null> };

function cssVar(el: Element, name: string): string {
  return getComputedStyle(el).getPropertyValue(name).trim() || "#888";
}

/** Nice round upper bound for the y axis. */
function niceMax(v: number): number {
  if (v <= 0) return 1;
  const exp = Math.pow(10, Math.floor(Math.log10(v)));
  const f = v / exp;
  const nice = f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10;
  return nice * exp;
}

/**
 * Line chart drawn on a canvas (design §3.2): crisp on high-DPI screens,
 * themed from the CSS tokens, with a hover crosshair and tooltip. Screen
 * readers get the summary passed in `label`.
 */
export function TrafficChart({
  points,
  series,
  formatY,
  formatX,
  formatTooltipX,
  label,
  height = 260,
  integer = false,
}: {
  points: ChartPoint[];
  series: ChartSeries[];
  formatY: (v: number) => string;
  formatX: (iso: string) => string;
  formatTooltipX: (iso: string) => string;
  label: string;
  height?: number;
  /** Counts: keep y ticks on whole numbers. */
  integer?: boolean;
}) {
  const wrap = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const [width, setWidth] = useState(0);
  const [hover, setHover] = useState<number | null>(null);
  const { resolvedTheme } = useTheme();
  const pad = useMemo(() => ({ top: 12, right: 12, bottom: 28, left: 52 }), []);

  const max = useMemo(() => {
    let m = 0;
    for (const p of points) for (const s of series) m = Math.max(m, p.values[s.key] ?? 0);
    return integer ? Math.max(4, niceMax(m)) : niceMax(m);
  }, [points, series, integer]);

  useEffect(() => {
    const el = wrap.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setWidth(Math.floor(e.contentRect.width)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  useEffect(() => {
    const c = canvas.current;
    if (!c || width === 0) return;
    const dpr = window.devicePixelRatio || 1;
    c.width = width * dpr;
    c.height = height * dpr;
    const ctx = c.getContext("2d");
    if (!ctx) return;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, width, height);

    const line = cssVar(c, "--hs-line");
    const subtle = cssVar(c, "--hs-subtle");
    const plotW = width - pad.left - pad.right;
    const plotH = height - pad.top - pad.bottom;
    const n = points.length;
    const x = (i: number) => pad.left + (n <= 1 ? plotW / 2 : (i / (n - 1)) * plotW);
    const y = (v: number) => pad.top + plotH - (v / max) * plotH;

    // grid + y labels
    ctx.font = "11px var(--font-geist-sans), system-ui, sans-serif";
    ctx.textBaseline = "middle";
    ctx.textAlign = "right";
    for (let g = 0; g <= 4; g++) {
      const v = (max / 4) * g;
      const gy = y(v);
      ctx.strokeStyle = line;
      ctx.lineWidth = 1;
      ctx.setLineDash(g === 0 ? [] : [3, 4]);
      ctx.beginPath();
      ctx.moveTo(pad.left, Math.round(gy) + 0.5);
      ctx.lineTo(width - pad.right, Math.round(gy) + 0.5);
      ctx.stroke();
      ctx.fillStyle = subtle;
      ctx.fillText(formatY(v), pad.left - 8, gy);
    }
    ctx.setLineDash([]);

    // x labels (about 6)
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    const every = Math.max(1, Math.ceil(n / 6));
    for (let i = 0; i < n; i += every) {
      ctx.fillStyle = subtle;
      ctx.fillText(formatX(points[i].at), x(i), height - pad.bottom + 8);
    }

    // series
    for (const s of series) {
      const color = s.color.startsWith("--") ? cssVar(c, s.color) : s.color;
      const pts = points.map((p, i) => [x(i), p.values[s.key]] as const).filter((p): p is readonly [number, number] => p[1] != null);
      if (pts.length === 0) continue;
      const path = new Path2D();
      pts.forEach(([px, v], i) => {
        const py = y(v);
        if (i === 0) path.moveTo(px, py);
        else {
          const [qx, qv] = pts[i - 1];
          const mx = (qx + px) / 2;
          path.bezierCurveTo(mx, y(qv), mx, py, px, py);
        }
      });
      if (s.fill) {
        const area = new Path2D(path);
        area.lineTo(pts[pts.length - 1][0], y(0));
        area.lineTo(pts[0][0], y(0));
        area.closePath();
        const grad = ctx.createLinearGradient(0, pad.top, 0, pad.top + plotH);
        grad.addColorStop(0, color + "55");
        grad.addColorStop(1, color + "00");
        ctx.fillStyle = grad;
        ctx.fill(area);
      }
      ctx.strokeStyle = color;
      ctx.lineWidth = 2;
      ctx.lineJoin = "round";
      ctx.stroke(path);
    }

    // hover crosshair
    if (hover != null && points[hover]) {
      const hx = x(hover);
      ctx.strokeStyle = subtle;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(Math.round(hx) + 0.5, pad.top);
      ctx.lineTo(Math.round(hx) + 0.5, pad.top + plotH);
      ctx.stroke();
      for (const s of series) {
        const v = points[hover].values[s.key];
        if (v == null) continue;
        ctx.fillStyle = s.color.startsWith("--") ? cssVar(c, s.color) : s.color;
        ctx.beginPath();
        ctx.arc(hx, y(v), 3.5, 0, Math.PI * 2);
        ctx.fill();
      }
    }
  }, [points, series, width, height, max, hover, pad, formatX, formatY, resolvedTheme]);

  function onMove(e: React.PointerEvent) {
    const rect = canvas.current?.getBoundingClientRect();
    if (!rect || points.length === 0) return;
    const plotW = width - pad.left - pad.right;
    const rel = (e.clientX - rect.left - pad.left) / plotW;
    setHover(Math.min(points.length - 1, Math.max(0, Math.round(rel * (points.length - 1)))));
  }

  const tip = hover != null ? points[hover] : null;
  const tipX = hover != null && points.length > 1 ? pad.left + (hover / (points.length - 1)) * (width - pad.left - pad.right) : width / 2;

  return (
    <div ref={wrap} className="relative w-full" style={{ height }}>
      <canvas ref={canvas} role="img" aria-label={label} style={{ width: "100%", height }} onPointerMove={onMove} onPointerLeave={() => setHover(null)} />
      {tip && (
        <div
          className="panel pointer-events-none absolute top-2 z-10 min-w-36 rounded-lg px-3 py-2 text-xs"
          style={{ left: Math.min(Math.max(tipX + 12, 0), width - 160) }}
        >
          <div className="mb-1 font-medium text-fg">{formatTooltipX(tip.at)}</div>
          {series.map((s) => (
            <div key={s.key} className="flex items-center justify-between gap-4">
              <span className="flex items-center gap-1.5 text-muted">
                <span className="size-2 rounded-full" style={{ background: s.color.startsWith("--") ? `var(${s.color})` : s.color }} />
                {s.label}
              </span>
              <span className="text-fg tabular-nums">{tip.values[s.key] == null ? "—" : formatY(tip.values[s.key]!)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
