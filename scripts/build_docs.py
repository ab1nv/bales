#!/usr/bin/env python3
"""Build the report and study-guide PDFs with headless Chrome."""
import os
import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CHROME = os.environ.get("CHROME", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")


def build(src: Path, out: Path) -> int:
    subprocess.run([
        CHROME,
        "--headless=new",
        "--disable-gpu",
        "--allow-file-access-from-files",
        "--no-pdf-header-footer",
        "--virtual-time-budget=8000",
        f"--print-to-pdf={out}",
        f"file://{src}",
    ], check=True, capture_output=True)
    data = out.read_bytes()
    pages = len(re.findall(rb"/Type\s*/Page[^s]", data))
    print(f"{out.relative_to(ROOT)}: {pages} pages")
    return pages


def main() -> None:
    report_pages = build(ROOT / "reports" / "report.html", ROOT / "reports" / "REPORT.pdf")
    study_pages = build(ROOT / "archive" / "study.html", ROOT / "archive" / "STUDY.pdf")
    if report_pages < 15:
        raise SystemExit("report is unexpectedly short")
    if not 12 <= study_pages <= 24:
        raise SystemExit("study guide page count is outside the expected range")


if __name__ == "__main__":
    main()
