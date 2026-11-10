#!/usr/bin/env python3
"""Render real command output as terminal screenshots for the report."""

import csv
import html
import json
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SHOTS = ROOT / "reports" / "screenshots"
CHROME = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"

TERM_CSS = """
@page { margin: 0; }
* { box-sizing: border-box; }
body { margin: 0; background: #0E1720; font-family: "SFMono-Regular", Menlo, monospace; }
.frame { width: 1080px; padding: 18px 20px 22px 20px; }
.bar { display: flex; align-items: center; gap: 7px; margin-bottom: 14px; }
.dot { width: 11px; height: 11px; border-radius: 50%; display: inline-block; }
.red { background: #E76F51; } .yellow { background: #E9C46A; } .green { background: #2A9D8F; }
.title { color: #8A98A6; font-size: 12px; margin-left: 8px; }
pre { margin: 0; color: #D8E2EA; font-size: 12.5px; line-height: 1.42; white-space: pre-wrap; }
.p { color: #5FD1B8; } .ok { color: #8FD694; } .dim { color: #8A98A6; } .warn { color: #F4C95D; }
"""


def page(title, body):
    return f"""<!doctype html><html><head><meta charset="utf-8"><style>{TERM_CSS}</style></head>
<body><div class="frame"><div class="bar"><span class="dot red"></span><span class="dot yellow"></span>
<span class="dot green"></span><span class="title">{html.escape(title)}</span></div>
<pre>{body}</pre></div></body></html>"""


def run(cmd, cwd=ROOT):
    p = subprocess.run(cmd, cwd=cwd, text=True, capture_output=True)
    out = p.stdout + p.stderr
    return out


def colorize(text):
    lines = []
    for line in text.splitlines():
        esc = html.escape(line)
        if line.startswith("$"):
            esc = f'<span class="p">{esc}</span>'
        elif line.startswith("ok ") or line.startswith("PASS") or "PASS" in line:
            esc = f'<span class="ok">{esc}</span>'
        elif line.startswith("#") or line.startswith("claims="):
            esc = f'<span class="dim">{esc}</span>'
        lines.append(esc)
    return "\n".join(lines)


def shot(name, title, body, height):
    (SHOTS / f"{name}.html").write_text(page(title, body))
    subprocess.run(
        [
            CHROME,
            "--headless=new",
            "--disable-gpu",
            "--hide-scrollbars",
            f"--screenshot={SHOTS / (name + '.png')}",
            f"--window-size=1080,{height}",
            f"file://{SHOTS / (name + '.html')}",
        ],
        check=True,
        capture_output=True,
    )
    (SHOTS / f"{name}.html").unlink()


def main():
    SHOTS.mkdir(parents=True, exist_ok=True)
    run(["go", "clean", "-testcache"])
    tests = (
        "$ go test ./...\n"
        + run(["go", "test", "./..."])
        + "\n$ python3 scripts/check_claims.py\n"
        + run(["python3", "scripts/check_claims.py"])
    )
    shot("tests", "bales · test and claims verification", colorize(tests), 560)

    demo = run(["zsh", "scripts/demo.sh"])
    stats_text = demo.split("== /debug/stats ==", 1)[1].split("== telemetry ==", 1)[0]
    stats = json.loads(stats_text)
    rows = list(csv.DictReader((ROOT / "evidence" / "runs" / "demo.csv").open()))
    c = stats["counters"]
    a = stats["audit"]
    q = stats["queue"]
    lines = [
        "$ ./scripts/demo.sh",
        "== /debug/stats ==",
        f"policy        {stats['policy']}",
        f"admitted {q['Admitted']}   rejected {q['Rejected']}   holds {c['holds']}   released {c['released']}   spills {c['spills']}   leader_ready {c['leader_ready']}",
        f"audit         precision {a['Precision']:.3f}   recall {a['Recall']:.3f}   samples {a['Samples']}",
        "",
        "== telemetry (selected columns) ==",
        f"{'request':8} {'repl':>4} {'prompt':>6} {'cached':>6} {'hold_outcome':<12} {'hold_ms':>8} {'ttft_ms':>8} {'status':>6}",
    ]
    for r in rows:
        lines.append(
            f"{r['request_id']:8} {r['replica']:>4} {r['prompt_tokens']:>6} {r['engine_cached_tokens']:>6} "
            f"{r['hold_outcome']:<12} {float(r['hold_ms']):>8.1f} {float(r['ttft_ms']):>8.1f} {r['status']:>6}"
        )
    shot(
        "demo",
        "bales · four-request coalescing demo (selected fields)",
        colorize("\n".join(lines)),
        360,
    )
    print("screenshots written")


if __name__ == "__main__":
    main()
