#!/usr/bin/env python3
"""Render report figures from the checked-in simulator CSVs."""

import csv
import math
from collections import defaultdict
from pathlib import Path
from xml.sax.saxutils import escape

ROOT = Path(__file__).resolve().parent.parent
RUNS = ROOT / "evidence" / "runs"
OUT = ROOT / "evidence" / "figures"

TEAL = "#2A9D8F"
GOLD = "#E9C46A"
CORAL = "#E76F51"
NAVY = "#264653"
GRAY = "#7A828E"
LIGHT = "#E8ECEF"
INK = "#16232E"
PALETTE = [TEAL, CORAL, GOLD, NAVY, GRAY]


def read(name):
    with (RUNS / name).open(newline="") as f:
        return list(csv.DictReader(f))


def fnum(v):
    try:
        return float(v)
    except (TypeError, ValueError):
        return 0.0


def nice_max(v, floor=1):
    if v <= 0:
        return floor
    mag = 10 ** math.floor(math.log10(v))
    for m in (1, 2, 2.5, 5, 10):
        if v <= m * mag:
            return m * mag
    return 10 * mag


def svg(w, h, body):
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w} {h}" width="{w}" height="{h}">'
        f'<rect width="{w}" height="{h}" fill="#FFFFFF"/>'
        f"<style>text{{font-family:Inter,Helvetica,Arial,sans-serif;fill:{INK};}}"
        f".axis{{stroke:{GRAY};stroke-width:1;}} .grid{{stroke:{LIGHT};stroke-width:1;}}"
        f".label{{font-size:12px;fill:{GRAY};}} .title{{font-size:16px;font-weight:600;}}"
        f".legend{{font-size:12px;}}</style>" + body + "</svg>\n"
    )


def text(x, y, s, size=12, anchor="start", weight="400", fill=INK):
    return (
        f'<text x="{x:.1f}" y="{y:.1f}" font-size="{size}" text-anchor="{anchor}" '
        f'font-weight="{weight}" fill="{fill}">{escape(str(s))}</text>'
    )


def rect(x, y, w, h, fill, rx=2):
    return f'<rect x="{x:.1f}" y="{y:.1f}" width="{max(w, 0):.1f}" height="{max(h, 0):.1f}" rx="{rx}" fill="{fill}"/>'


def line(x1, y1, x2, y2, cls="grid"):
    return (
        f'<line x1="{x1:.1f}" y1="{y1:.1f}" x2="{x2:.1f}" y2="{y2:.1f}" class="{cls}"/>'
    )


def polyline(points, color, width=2.5):
    pts = " ".join(f"{x:.1f},{y:.1f}" for x, y in points)
    return f'<polyline points="{pts}" fill="none" stroke="{color}" stroke-width="{width}" stroke-linejoin="round" stroke-linecap="round"/>'


def rot_text(x, y, s, size=11):
    return (
        f'<text x="{x}" y="{y}" font-size="{size}" text-anchor="middle" fill="{GRAY}" '
        f'transform="rotate(-90 {x} {y})">{escape(str(s))}</text>'
    )


def axes(
    w, h, ymax, ylabel, xlabels, xlabel, left=68, right=24, top=54, bottom=64, ymin=0
):
    plot_w = w - left - right
    plot_h = h - top - bottom
    parts = []
    for i in range(6):
        v = ymin + (ymax - ymin) * i / 5
        y = top + plot_h - plot_h * i / 5
        parts.append(line(left, y, left + plot_w, y))
        parts.append(text(left - 8, y + 4, f"{v:.0f}", 11, "end", fill=GRAY))
    n = len(xlabels)
    for i, lab in enumerate(xlabels):
        x = left + (plot_w * (i + 0.5) / n if n else left)
        parts.append(text(x, top + plot_h + 22, lab, 11, "middle", fill=GRAY))
    parts.append(
        text(left + plot_w / 2, top + plot_h + 46, xlabel, 11, "middle", fill=GRAY)
    )
    parts.append(rot_text(18, top + plot_h / 2, ylabel))
    return "".join(parts)


def legend(x, y, items):
    parts = []
    for i, (label, color) in enumerate(items):
        yy = y + i * 18
        parts.append(rect(x, yy - 9, 12, 12, color))
        parts.append(text(x + 18, yy + 1, label, 12))
    return "".join(parts)


def grouped_bars(path, title, labels, series, ylabel, colors=None, series_labels=None):
    w, h = 820, 430
    left, right, top, bottom = 70, 24, 78, 92
    plot_w, plot_h = w - left - right, h - top - bottom
    colors = colors or PALETTE
    ymax = nice_max(max(max(vals) for vals in series))
    body = [text(left, 32, title, 16, weight="600")]
    body.append(axes(w, h, ymax, ylabel, labels, "", left, right, top, bottom))
    n = len(labels)
    m = len(series)
    group_w = plot_w / n
    bar_w = min(34, group_w * 0.72 / m)
    for i in range(n):
        gx = left + group_w * i + group_w / 2
        for j, vals in enumerate(series):
            v = vals[i]
            x = gx - (m * bar_w) / 2 + j * bar_w
            bh = plot_h * v / ymax
            body.append(
                rect(x, top + plot_h - bh, bar_w - 2, bh, colors[j % len(colors)])
            )
            body.append(
                text(
                    x + (bar_w - 2) / 2,
                    top + plot_h - bh - 5,
                    f"{v:.0f}",
                    10,
                    "middle",
                    fill=GRAY,
                )
            )
    if m > 1:
        names = series_labels or [f"Series {j + 1}" for j in range(m)]
        body.append(
            legend(
                w - 230,
                30,
                [(names[j], colors[j % len(colors)]) for j in range(m)],
            )
        )
    (OUT / path).write_text(svg(w, h, "".join(body)))


def series_bar(path, title, labels, values, ylabel, color=TEAL):
    grouped_bars(path, title, labels, [values], ylabel, [color])


def load_chart(path, rows):
    rates = [4, 8, 12, 16, 20]
    policies = ["round_robin", "llmd_style", "bales_ect_linear", "bales_ect"]
    data = defaultdict(list)
    for r in rows:
        data[(r["policy"], int(fnum(r["parameter"].split("=")[1])))].append(
            fnum(r["p99_ms"])
        )
    w, h = 820, 430
    left, right, top, bottom = 72, 150, 60, 70
    plot_w, plot_h = w - left - right, h - top - bottom
    values = [sum(v) / len(v) for v in data.values() if v]
    ymax = nice_max(max(values))
    body = [
        text(left, 32, "Offered load: mean P99 TTFT over 3 seeds", 16, weight="600")
    ]
    body.append(
        axes(
            w,
            h,
            ymax,
            "P99 TTFT (ms)",
            [str(r) for r in rates],
            "Requests per second",
            left,
            right,
            top,
            bottom,
        )
    )
    for j, p in enumerate(policies):
        pts = []
        for i, rate in enumerate(rates):
            v = data[(p, rate)]
            v = sum(v) / len(v) if v else 0
            x = left + plot_w * (i + 0.5) / len(rates)
            y = top + plot_h - plot_h * v / ymax
            pts.append((x, y))
        body.append(polyline(pts, PALETTE[j]))
        for x, y in pts:
            body.append(
                f'<circle cx="{x:.1f}" cy="{y:.1f}" r="3.5" fill="{PALETTE[j]}"/>'
            )
    body.append(
        legend(w - 140, top + 8, [(p, PALETTE[j]) for j, p in enumerate(policies)])
    )
    (OUT / path).write_text(svg(w, h, "".join(body)))


def queue_frontier(path, rows):
    policies = ["fifo", "strict_priority", "edf_batch_800"]
    colors = {
        "fifo": GRAY,
        "strict_priority": CORAL,
        "edf": TEAL,
        "edf_batch_800": NAVY,
    }
    names = {
        "fifo": "FIFO",
        "strict_priority": "Strict",
        "edf": "EDF 5s",
        "edf_batch_800": "EDF 0.8s",
    }
    points = []
    for r in rows:
        if r["seed"] != "1" or r["parameter"] != "rate=8":
            continue
        p = r["policy"]
        if p not in policies:
            continue
        points.append((fnum(r["stream_p99_ms"]), fnum(r["batch_p99_wait_ms"]), p))
    w, h = 760, 430
    left, right, top, bottom = 78, 40, 60, 76
    plot_w, plot_h = w - left - right, h - top - bottom
    xmax = nice_max(max(p[0] for p in points))
    ymax = nice_max(max(p[1] for p in points))
    body = [text(left, 32, "Queue tradeoff at 8 req/s, seed 1", 16, weight="600")]
    body.append(
        axes(w, h, ymax, "Batch P99 wait (ms)", [], "Stream P99 TTFT (ms)", left, right, top, bottom)
    )
    for i in range(6):
        v = xmax * i / 5
        x = left + plot_w * i / 5
        body.append(text(x, top + plot_h + 22, f"{v:.0f}", 11, "middle", fill=GRAY))
    for x, y, p in points:
        px = left + plot_w * x / xmax
        py = top + plot_h - plot_h * y / ymax
        body.append(f'<circle cx="{px:.1f}" cy="{py:.1f}" r="6" fill="{colors[p]}"/>')
        dx, dy = 10, 4
        if p == "edf_batch_800":
            dx, dy = -82, -8
        body.append(text(px + dx, py + dy, names[p], 11, fill=colors[p]))
    (OUT / path).write_text(svg(w, h, "".join(body)))


def cost_chart(path):
    model = dict(a=6.7e-6, b=6.7e-6, f=0.05, g=5.0)

    def cost(q, c):
        return (
            model["a"] * q * c
            + model["b"] * q * (q + 1) / 2
            + model["f"] * q
            + model["g"]
        )

    contexts = list(range(0, 16001, 500))
    shares = []
    for q in contexts:
        work = cost(q, 0) - model["g"]
        attn = model["b"] * q * (q + 1) / 2
        shares.append(attn / work if work > 0 else 0)
    hit = [cost(l, 0) - model["g"] for l in contexts]
    w, h = 880, 390
    body = [
        text(24, 30, "Cost model shape (Qwen2.5-1.5B class defaults)", 16, weight="600")
    ]
    # two panels
    for panel, (vals, title, ylab, ymax) in enumerate(
        [
            (shares, "Attention share of prefill work", "Share", 1.0),
            (hit, "Value of a cached prefix", "Milliseconds", nice_max(max(hit))),
        ]
    ):
        left = 40 + panel * 440
        top, plot_w, plot_h = 70, 360, 250
        body.append(text(left, 52, title, 12, weight="600"))
        for i in range(5):
            y = top + plot_h - plot_h * i / 4
            body.append(line(left, y, left + plot_w, y))
            body.append(
                text(left - 6, y + 4, f"{ymax * i / 4:.1f}", 10, "end", fill=GRAY)
            )
        pts = []
        for x, v in zip(contexts, vals):
            px = left + plot_w * x / contexts[-1]
            py = top + plot_h - plot_h * v / ymax
            pts.append((px, py))
        body.append(polyline(pts, TEAL, 2.5))
        for tick in (0, 4000, 8000, 12000, 16000):
            px = left + plot_w * tick / contexts[-1]
            body.append(
                text(px, top + plot_h + 20, f"{tick // 1000}k", 10, "middle", fill=GRAY)
            )
        body.append(
            text(
                left + plot_w / 2,
                top + plot_h + 40,
                "Context tokens",
                10,
                "middle",
                fill=GRAY,
            )
        )
        body.append(rot_text(left - 10, top + plot_h / 2, ylab, 10))
        if panel == 0:
            cross = 2 * model["f"] / model["b"]
            px = left + plot_w * cross / contexts[-1]
            body.append(line(px, top, px, top + plot_h, "grid"))
            body.append(
                text(px + 4, top + 14, f"crossover {cross / 1000:.1f}k", 10, fill=GRAY)
            )
    (OUT / path).write_text(svg(w, h, "".join(body)))


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    burst = read("burst_summary.csv")
    labels = ["RR", "llmd-style", "ECT", "ECT+coalesce"]
    keys = ["round_robin", "llmd_style", "bales_ect", "bales_ect_coalesced"]
    by = {r["policy"]: r for r in burst}
    p50 = [fnum(by[k]["p50_ms"]) for k in keys]
    follower = [fnum(by[k]["follower_p50_ms"]) for k in keys]
    prefill = [fnum(by[k]["prefill_ms"]) for k in keys]
    grouped_bars(
        "burst_ttft.svg",
        "Cold same-prompt burst (simulated)",
        labels,
        [p50, follower],
        "TTFT (ms)",
        [TEAL, GOLD],
    )
    series_bar(
        "burst_prefill.svg",
        "Cold burst: prefill work",
        labels,
        prefill,
        "Prefill (ms)",
        CORAL,
    )
    load_chart("load_p99.svg", read("load_summary.csv"))
    long = read("longctx_summary.csv")
    long_keys = ["least_tokens", "bales_ect_linear", "bales_ect"]
    rate6 = [
        sum(
            fnum(r["p99_ms"])
            for r in long
            if r["policy"] == k and "rate=6" in r["parameter"]
        )
        / 3
        for k in long_keys
    ]
    rate10 = [
        sum(
            fnum(r["p99_ms"])
            for r in long
            if r["policy"] == k and "rate=10" in r["parameter"]
        )
        / 3
        for k in long_keys
    ]
    grouped_bars(
        "longctx_p99.svg",
        "Long-context P99 TTFT, 8k-token prefixes (simulated)",
        ["least tokens", "ECT linear", "ECT quadratic"],
        [rate6, rate10],
        "P99 TTFT (ms)",
        [TEAL, NAVY],
    )
    queue_frontier("queue_frontier.svg", read("queue_summary.csv"))
    aff = read("affinity_summary.csv")
    traces = [
        "aff-short-hot",
        "aff-medium-hot",
        "aff-long-hot",
        "aff-long-mixed",
        "aff-verylong-cold",
        "aff-medium-mixed",
    ]
    pols = ["round_robin", "cacheroute_style", "bales_ect"]
    rows = []
    for p in pols:
        vals = []
        for t in traces:
            v = [
                fnum(r["p99_ms"])
                for r in aff
                if r["policy"] == p and r["parameter"] == t
            ]
            vals.append(sum(v) / len(v) if v else 0)
        rows.append(vals)
    grouped_bars(
        "affinity_p99.svg",
        "Affinity policy sensitivity, P99 TTFT (simulated)",
        [
            "short hot",
            "medium hot",
            "long hot",
            "long mixed",
            "very long cold",
            "medium mixed",
        ],
        rows,
        "P99 TTFT (ms)",
        [TEAL, CORAL, NAVY],
    )
    abl = read("ablation_summary.csv")
    bya = {r["policy"]: r for r in abl}
    akeys = ["bales_score", "bales_ect_linear", "bales_ect", "bales_ect_coalesced"]
    alabels = ["score", "ECT linear", "ECT", "ECT+coalesce"]
    avals = [fnum(bya[k]["p50_ms"]) for k in akeys]
    series_bar(
        "ablation.svg",
        "Burst ablation: P50 TTFT (simulated)",
        alabels,
        avals,
        "P50 TTFT (ms)",
        NAVY,
    )
    cost_chart("cost_model.svg")
    print(f"wrote figures to {OUT}")


if __name__ == "__main__":
    main()
