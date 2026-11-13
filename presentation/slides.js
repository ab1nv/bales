(() => {
  "use strict";

  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
  const NS = "http://www.w3.org/2000/svg";
  const slides = $$(".slide");
  const total = slides.length;
  let current = 0;
  let burstTimer = null;
  let flowTimer = null;

  // ---------- theme ----------
  const root = document.documentElement;
  function applyTheme(theme) {
    root.dataset.theme = theme;
    try {
      localStorage.setItem("bales-theme", theme);
    } catch (e) {
      /* ignore */
    }
  }
  let savedTheme = null;
  try {
    savedTheme = localStorage.getItem("bales-theme");
  } catch (e) {
    /* ignore */
  }
  const prefersDark =
    window.matchMedia &&
    window.matchMedia("(prefers-color-scheme: dark)").matches;
  const hashQuery = location.hash.includes("?")
    ? location.hash.split("?")[1]
    : "";
  const forcedTheme =
    new URLSearchParams(location.search).get("theme") ||
    new URLSearchParams(hashQuery).get("theme");
  applyTheme(
    forcedTheme === "dark" || forcedTheme === "light"
      ? forcedTheme
      : savedTheme || (prefersDark ? "dark" : "light"),
  );

  // ---------- navigation ----------
  const pad = (n) => String(n).padStart(2, "0");
  function goTo(index, updateHash = true) {
    index = Math.max(0, Math.min(total - 1, index));
    if (slides[current]) slides[current].classList.remove("active");
    current = index;
    slides[current].classList.add("active");
    $("#counter").textContent = `${pad(current + 1)} / ${pad(total)}`;
    $("#progressBar").style.width = `${((current + 1) / total) * 100}%`;
    if (updateHash) history.replaceState(null, "", `#slide-${current + 1}`);
    startSlideAnimations(current);
    slides[current].scrollTop = 0;
  }
  const next = () => goTo(current + 1);
  const prev = () => goTo(current - 1);

  document.addEventListener("keydown", (event) => {
    const tag = (event.target && event.target.tagName) || "";
    if (/INPUT|TEXTAREA|SELECT/.test(tag)) return;
    if (event.key === " " && tag === "BUTTON") return;
    if (
      event.key === "ArrowRight" ||
      event.key === "PageDown" ||
      event.key === " "
    ) {
      event.preventDefault();
      next();
    } else if (event.key === "ArrowLeft" || event.key === "PageUp") {
      event.preventDefault();
      prev();
    } else if (event.key === "Home") {
      event.preventDefault();
      goTo(0);
    } else if (event.key === "End") {
      event.preventDefault();
      goTo(total - 1);
    } else if (event.key.toLowerCase() === "t") {
      applyTheme(root.dataset.theme === "dark" ? "light" : "dark");
    }
  });

  let touchStartX = null;
  document.addEventListener(
    "touchstart",
    (e) => {
      touchStartX = e.changedTouches[0].clientX;
    },
    { passive: true },
  );
  document.addEventListener(
    "touchend",
    (e) => {
      if (touchStartX === null) return;
      const dx = e.changedTouches[0].clientX - touchStartX;
      if (Math.abs(dx) > 50) {
        dx < 0 ? next() : prev();
      }
      touchStartX = null;
    },
    { passive: true },
  );

  function initialSlide() {
    const match = /slide-(\d+)/.exec(location.hash || "");
    return match ? Math.min(total - 1, Math.max(0, Number(match[1]) - 1)) : 0;
  }

  // ---------- chart helpers ----------
  function mk(svg, tag, attrs = {}) {
    const node = document.createElementNS(NS, tag);
    for (const [key, value] of Object.entries(attrs))
      node.setAttribute(key, value);
    svg.appendChild(node);
    return node;
  }
  function clear(svg) {
    while (svg.firstChild) svg.removeChild(svg.firstChild);
  }
  function label(svg, x, y, text, opts = {}) {
    const node = mk(svg, "text", {
      x,
      y,
      "text-anchor": opts.anchor || "start",
      "font-size": opts.size || 9,
      fill: opts.fill || "var(--muted)",
    });
    node.textContent = text;
    return node;
  }
  function grid(svg, left, top, w, h, rows = 4) {
    for (let i = 0; i <= rows; i++) {
      const y = top + (h * i) / rows;
      mk(svg, "line", { x1: left, y1: y, x2: left + w, y2: y, class: "grid" });
    }
  }
  function nice(v) {
    if (v <= 0) return 1;
    const mag = Math.pow(10, Math.floor(Math.log10(v)));
    for (const m of [1, 2, 2.5, 5, 10]) if (v <= m * mag) return m * mag;
    return 10 * mag;
  }

  // ---------- model ----------
  const MODEL = { a: 6.7e-6, b: 6.7e-6, f: 0.05, g: 5 };
  function cost(q, c) {
    q = Math.max(0, q);
    c = Math.max(0, c);
    return (
      MODEL.a * q * c + (MODEL.b * q * (q + 1)) / 2 + MODEL.f * q + MODEL.g
    );
  }
  function saving(l) {
    return Math.max(0, cost(l, 0) - MODEL.g);
  }
  const blocks = (v, max, width) =>
    "█".repeat(Math.max(0, Math.min(width, Math.round((v / max) * width)))) +
    "░".repeat(Math.max(0, width - Math.round((v / max) * width)));

  // ---------- slide 2: duplicate reading ----------
  const asciiBurst = $("#asciiBurst");
  function burstFrame(step) {
    if (!asciiBurst) return;
    const n1 = Math.min(20, step);
    const n2 = Math.min(20, Math.max(0, step - 7));
    const n3 = Math.min(20, Math.max(0, step - 14));
    const bar = (n, color) =>
      `<span class="${color}">[${"█".repeat(n)}${"░".repeat(20 - n)}]</span>`;
    const hot =
      n3 >= 20
        ? '<span class="bad">duplicate</span>'
        : '<span class="dim">reading</span>';
    asciiBurst.innerHTML =
      ` user A ─┐\n` +
      ` user B ─┼── same document ──▶ <span class="teal">[ Bales ]</span>\n` +
      ` user C ─┘                         │\n` +
      `                                   ▼\n` +
      `                         ┌──────────────────────┐\n` +
      `                         │ GPU 0                │\n` +
      `                         │ pass 1 ${bar(n1, "teal")}  ${hot}\n` +
      `                         │ pass 2 ${bar(n2, "gold")}  ${hot}\n` +
      `                         │ pass 3 ${bar(n3, "gold")}  ${hot}\n` +
      `                         └──────────────────────┘`;
  }

  // ---------- slide 3: aim ----------
  const asciiAim = $("#asciiAim");
  if (asciiAim) {
    asciiAim.innerHTML =
      `       user request\n` +
      `            │\n` +
      `            ▼\n` +
      `        <span class="teal">[ Bales ]</span>\n` +
      `       ╱         ╲\n` +
      ` same prompt?     no\n` +
      `      │            │\n` +
      `      ▼            ▼\n` +
      ` <span class="gold">wait or move</span>   <span class="teal">pick the GPU</span>\n` +
      `                  <span class="teal">that finishes first</span>`;
  }

  // ---------- slide 6: pipeline ----------
  const asciiFlow = $("#asciiFlow");
  const stages = [
    "1 admit",
    "2 queue",
    "3 share",
    "4 choose",
    "5 GPU",
    "6 measure",
  ];
  function flowFrame(step) {
    if (!asciiFlow) return;
    const active = step % stages.length;
    const boxes = stages
      .map((s, i) => {
        const name = `[${s.padEnd(9)}]`;
        return i === active
          ? `<span class="stage on">${name}</span>`
          : `<span class="stage">${name}</span>`;
      })
      .join(" ─▶ ");
    asciiFlow.innerHTML =
      ` user\n` +
      `  │\n` +
      `  ▼\n` +
      `${boxes}\n` +
      `   ▲                                                                  │\n` +
      `   └──────────────────────────────────────────────────────────────────┘`;
  }

  // ---------- slide 7: cost ----------
  const qRange = $("#qRange"),
    cRange = $("#cRange");
  const asciiCost = $("#asciiCost");
  function drawCost() {
    if (!qRange || !cRange) return;
    const q = Number(qRange.value),
      c = Number(cRange.value);
    $("#qVal").textContent = q.toLocaleString();
    $("#cVal").textContent = c.toLocaleString();
    const warm = cost(q, c),
      cold = cost(q + c, 0),
      saved = Math.max(0, cold - warm);
    $("#costOut").textContent = `${warm.toFixed(0)} ms`;
    $("#coldOut").textContent = `${cold.toFixed(0)} ms`;
    $("#saveOut").textContent = `${saved.toFixed(0)} ms`;
    const work = Math.max(0.001, warm - MODEL.g);
    const attn = (MODEL.a * q * c + (MODEL.b * q * (q + 1)) / 2) / work;
    $("#attnOut").textContent = `${(Math.min(1, attn) * 100).toFixed(0)}%`;
    if (asciiCost) {
      asciiCost.innerHTML =
        `cold   <span class="cold">[${blocks(cold, cold, 26)}]</span> ${cold.toFixed(0)} ms\n` +
        `warm   <span class="warm">[${blocks(warm, cold, 26)}]</span> ${warm.toFixed(0)} ms\n` +
        `saved  <span class="saved">[${blocks(saved, cold, 26)}]</span> ${saved.toFixed(0)} ms\n\n` +
        `reading ${q.toLocaleString()} new words over ${c.toLocaleString()} shared words\n` +
        `attention is ${(Math.min(1, attn) * 100).toFixed(0)}% of the warm reading time`;
    }
  }
  if (qRange) {
    qRange.addEventListener("input", drawCost);
    cRange.addEventListener("input", drawCost);
    drawCost();
  }

  // ---------- slide 8: hold or move ----------
  const lRange = $("#lRange"),
    tauRange = $("#tauRange"),
    gapRange = $("#gapRange"),
    slackRange = $("#slackRange");
  function drawHold() {
    if (!lRange) return;
    const L = Number(lRange.value),
      tau = Number(tauRange.value);
    const gap = Number(gapRange.value),
      slack = Number(slackRange.value);
    $("#lVal").textContent = L.toLocaleString();
    $("#tauVal").textContent = tau;
    $("#gapVal").textContent = gap;
    $("#slackVal").textContent = slack;
    const saved = saving(L);
    const wait = Math.max(0, tau + gap);
    const gain = saved - wait;
    const hold = L > 0 && gain > 1 && tau <= slack && tau <= 250;
    const badge = $("#holdDecision");
    badge.textContent = hold ? "HOLD" : "SPILL";
    badge.classList.toggle("spill", !hold);
    let reason = `saved ${saved.toFixed(0)} ms, wait ${wait.toFixed(0)} ms`;
    if (L <= 0) reason = "no shared prefix";
    else if (tau > slack) reason = "the leader exceeds the request's patience";
    else if (tau > 250) reason = "the leader exceeds the 250 ms ceiling";
    else if (gain <= 1) reason = "moving is cheaper";
    $("#holdReason").textContent = reason;

    const max = Math.max(1, saved, wait);
    $("#saveBar").style.width = `${(saved / max) * 100}%`;
    $("#waitBar").style.width = `${(wait / max) * 100}%`;
    $("#saveLabel").textContent = saved.toFixed(0);
    $("#waitLabel").textContent = wait.toFixed(0);

    const holdBar =
      "█".repeat(
        Math.min(20, Math.max(0, Math.round(20 * Math.min(1, tau / 300)))),
      ) +
      "░".repeat(
        20 - Math.min(20, Math.max(0, Math.round(20 * Math.min(1, tau / 300)))),
      );
    const warm =
      "█".repeat(
        Math.min(20, Math.max(1, Math.round(20 * Math.min(1, saved / 700)))),
      ) +
      "░".repeat(
        20 -
          Math.min(20, Math.max(1, Math.round(20 * Math.min(1, saved / 700)))),
      );
    if ($("#asciiHold")) {
      $("#asciiHold").innerHTML =
        `leader    reading  <span class="teal">[${"█".repeat(20 - Math.min(20, Math.round((tau / 300) * 20)))}${"░".repeat(Math.min(20, Math.round((tau / 300) * 20)))}]</span> ${tau} ms left\n` +
        `follower  wait     <span class="gold">[${holdBar}]</span>\n` +
        `          reuse    <span class="ok">[${warm}]</span> saves ${saved.toFixed(0)} ms`;
    }
  }
  if (lRange) {
    [lRange, tauRange, gapRange, slackRange].forEach((el) =>
      el.addEventListener("input", drawHold),
    );
    drawHold();
  }

  // ---------- slide 9: queue ----------
  const dStream = $("#dStream"),
    dBatch = $("#dBatch"),
    etaRange = $("#etaRange");
  const asciiQueue = $("#asciiQueue");
  const queueJobs = [
    { id: "s1", arrival: 0, stream: true, cost: 120 },
    { id: "b1", arrival: 10, stream: false, cost: 300 },
    { id: "s2", arrival: 30, stream: true, cost: 80 },
    { id: "b2", arrival: 60, stream: false, cost: 500 },
    { id: "s3", arrival: 90, stream: true, cost: 60 },
    { id: "s4", arrival: 140, stream: true, cost: 100 },
  ];
  function drawQueue() {
    if (!dStream) return;
    const ds = Number(dStream.value),
      db = Number(dBatch.value),
      eta = Number(etaRange.value);
    $("#dStreamVal").textContent = ds;
    $("#dBatchVal").textContent = db;
    $("#etaVal").textContent = eta.toFixed(1);
    const jobs = queueJobs.map((j) => ({
      ...j,
      deadline: j.arrival + (j.stream ? ds : db) + eta * j.cost,
    }));
    jobs.sort((a, b) => a.deadline - b.deadline || a.arrival - b.arrival);
    const maxD = Math.max(...jobs.map((j) => j.deadline), 1000);
    if (asciiQueue) {
      const lines = jobs.map((j) => {
        const width = Math.max(1, Math.round((j.deadline / maxD) * 28));
        const bar = "█".repeat(width) + "░".repeat(28 - width);
        return `${j.stream ? "chat " : "batch"} ${j.id} [${j.stream ? `<span class="stream">${bar}</span>` : `<span class="batch">${bar}</span>`}] ${j.deadline.toFixed(0)} ms`;
      });
      asciiQueue.innerHTML = `0 ms ─────────────────────────────────▶ ${maxD.toFixed(0)} ms\n\n${lines.join("\n")}`;
    }
    $("#queueOrder").innerHTML = jobs
      .map(
        (j, i) =>
          `<span class="${j.stream ? "stream" : "batch"}">${i + 1}. ${j.id} · ${j.deadline.toFixed(0)} ms</span>`,
      )
      .join("");
  }
  if (dStream) {
    [dStream, dBatch, etaRange].forEach((el) =>
      el.addEventListener("input", drawQueue),
    );
    drawQueue();
  }

  // ---------- slide 10: ECT ----------
  const replicas = [
    { id: 0, cached: 4096, wait: 500, inflight: 1, kv: 72 },
    { id: 1, cached: 0, wait: 40, inflight: 0, kv: 34 },
    { id: 2, cached: 2048, wait: 10, inflight: 0, kv: 55 },
    { id: 3, cached: 0, wait: 300, inflight: 2, kv: 96 },
  ];
  let policy = "ect";
  function renderECT() {
    const ascii = $("#asciiEct");
    if (!ascii) return;
    const q = 4600,
      out = 64;
    const rows = replicas.map((r) => {
      const uncached = Math.max(0, q - r.cached);
      const prefill = cost(uncached, r.cached);
      const score = r.wait + prefill + 2 * r.inflight + 5;
      return { ...r, uncached, prefill, score, tokens: uncached + out };
    });
    let winner;
    let header = "score";
    let reason;
    if (policy === "tokens") {
      winner = rows.reduce(
        (best, r, i) => (r.tokens < rows[best].tokens ? i : best),
        0,
      );
      header = "tokens";
      reason =
        "Least tokens chooses GPU 0: it counts uncached tokens and ignores the queue.";
    } else {
      let best = Infinity;
      winner = 0;
      rows.forEach((r, i) => {
        if (r.kv >= 90) return;
        if (r.score < best) {
          best = r.score;
          winner = i;
        }
      });
      reason =
        "ECT chooses GPU 2: a short queue beats the larger cache on GPU 0.";
    }
    const lines = [`replica   cache       queue     ${header}`];
    rows.forEach((r, i) => {
      const cache = blocks(r.cached, 4096, 8);
      const skip = policy === "ect" && r.kv >= 90;
      const value = skip
        ? "skipped: KV 96%"
        : `${(policy === "tokens" ? r.tokens : r.score).toFixed(0)}${policy === "tokens" ? "" : " ms"}`;
      const mark = i === winner && !skip ? "  ◀ pick" : "";
      lines.push(
        `GPU ${r.id}     ${cache}  ${String(r.wait).padStart(4)} ms   ${value}${mark}`,
      );
    });
    lines.push("");
    lines.push(`request: ${q.toLocaleString()} tokens, ${out} output tokens`);
    ascii.innerHTML = lines
      .map((l) => (l.includes("◀ pick") ? `<span class="pick">${l}</span>` : l))
      .join("\n");
    $("#ectReason").textContent = reason;
  }
  $$(".policy-toggle button").forEach((button) => {
    button.addEventListener("click", () => {
      policy = button.dataset.policy;
      $$(".policy-toggle button").forEach((b) =>
        b.classList.toggle("on", b === button),
      );
      renderECT();
    });
  });
  if ($("#asciiEct")) renderECT();

  // ---------- slide 11: affinity ----------
  const rateRange = $("#rateRange"),
    prefixRange = $("#prefixRange");
  function drawGate() {
    if (!rateRange) return;
    const rate = Number(rateRange.value),
      prefix = Number(prefixRange.value);
    $("#rateVal").textContent = rate;
    $("#prefixVal").textContent = prefix.toLocaleString();
    const recoverable = rate * saving(prefix);
    const threshold = 0.15 * 1000 * 4;
    let action = "SPREAD";
    let count = 0;
    let loads = [25, 25, 25, 25];
    if (recoverable >= threshold) {
      count = Math.max(1, Math.min(4, Math.ceil(recoverable / 800)));
      if (count <= 1) {
        action = "PIN";
        loads = [90, 5, 5, 5];
      } else {
        action = "REPLICATE";
        if (count === 2) loads = [45, 45, 5, 5];
        else if (count === 3) loads = [32, 32, 32, 4];
        else loads = [25, 25, 25, 25];
      }
    }
    const badge = $("#gateDecision");
    badge.textContent = action;
    badge.classList.toggle("spill", action === "SPREAD");
    $("#gateReason").textContent =
      action === "SPREAD"
        ? `recoverable ${recoverable.toFixed(0)} ms/s is below the ${threshold.toFixed(0)} ms/s threshold`
        : `${count} replica${count === 1 ? "" : "s"} · recoverable ${recoverable.toFixed(0)} ms/s`;
    if ($("#asciiGate")) {
      const lines = ["GPU   load"];
      loads.forEach((load, i) => {
        lines.push(
          `GPU ${i} ${"█".repeat(Math.round(load / 5)).padEnd(20)} ${load}%`,
        );
      });
      lines.push("");
      lines.push(
        action === "SPREAD"
          ? "traffic spread across all GPUs"
          : `${action.toLowerCase()} the hot group`,
      );
      $("#asciiGate").innerHTML = lines.join("\n");
    }
  }
  if (rateRange) {
    [rateRange, prefixRange].forEach((el) =>
      el.addEventListener("input", drawGate),
    );
    drawGate();
  }

  // ---------- slide 14: compact results ----------
  function drawBurstMini() {
    const svg = $("#burstMini");
    if (!svg) return;
    clear(svg);
    svg.classList.add("chart");
    const W = 360,
      H = 220,
      left = 34,
      top = 14,
      right = 8,
      bottom = 34;
    const pw = W - left - right,
      ph = H - top - bottom;
    const vals = [504.6, 509.1, 142.5];
    const names = ["RR", "ECT", "share"];
    const yMax = 600;
    grid(svg, left, top, pw, ph, 4);
    for (let i = 0; i <= 4; i++)
      label(svg, left - 5, top + ph - (ph * i) / 4 + 3, `${(yMax * i) / 4}`, {
        anchor: "end",
        size: 8,
      });
    const bw = pw / 3;
    vals.forEach((v, i) => {
      const bh = (v / yMax) * ph;
      mk(svg, "rect", {
        x: left + i * bw + bw * 0.2,
        y: top + ph - bh,
        width: bw * 0.6,
        height: bh,
        rx: 3,
        fill: i === 2 ? "var(--accent)" : "var(--muted)",
      });
      label(svg, left + i * bw + bw / 2, top + ph - bh - 5, `${v}`, {
        anchor: "middle",
        size: 8,
        fill: "var(--ink)",
      });
      label(svg, left + i * bw + bw / 2, top + ph + 14, names[i], {
        anchor: "middle",
        size: 8,
      });
    });
    label(svg, left + pw / 2, H - 2, "P50 TTFT (ms)", {
      anchor: "middle",
      size: 8,
    });
  }
  function drawQueueMini() {
    const svg = $("#queueMini");
    if (!svg) return;
    clear(svg);
    svg.classList.add("chart");
    const W = 360,
      H = 220,
      left = 38,
      top = 14,
      right = 8,
      bottom = 34;
    const pw = W - left - right,
      ph = H - top - bottom;
    const pts = [
      { name: "FIFO", x: 5025, y: 4649, c: "var(--muted)" },
      { name: "strict", x: 562, y: 5109, c: "var(--bad)" },
      { name: "EDF .8", x: 4662, y: 4893, c: "var(--accent)" },
    ];
    const maxX = 6000,
      maxY = 6000;
    grid(svg, left, top, pw, ph, 4);
    for (let i = 0; i <= 4; i++) {
      label(
        svg,
        left - 5,
        top + ph - (ph * i) / 4 + 3,
        `${(maxY * i) / 4 / 1000}s`,
        { anchor: "end", size: 8 },
      );
      label(
        svg,
        left + (pw * i) / 4,
        top + ph + 14,
        `${(maxX * i) / 4 / 1000}s`,
        { anchor: "middle", size: 8 },
      );
    }
    const offsets = { FIFO: [6, -8], strict: [6, 3], "EDF .8": [-40, 2] };
    pts.forEach((p) => {
      const x = left + (p.x / maxX) * pw,
        y = top + ph - (p.y / maxY) * ph;
      mk(svg, "circle", { cx: x, cy: y, r: 4, fill: p.c });
      const off = offsets[p.name] || [6, 3];
      label(svg, x + off[0], y + off[1], p.name, { size: 8, fill: p.c });
    });
    label(svg, left + pw / 2, H - 2, "stream P99 vs batch P99", {
      anchor: "middle",
      size: 8,
    });
  }
  function drawLongMini() {
    const svg = $("#longMini");
    if (!svg) return;
    clear(svg);
    svg.classList.add("chart");
    const W = 360,
      H = 220,
      left = 34,
      top = 14,
      right = 8,
      bottom = 34;
    const pw = W - left - right,
      ph = H - top - bottom;
    const yMax = 4200;
    grid(svg, left, top, pw, ph, 4);
    for (let i = 0; i <= 4; i++)
      label(svg, left - 5, top + ph - (ph * i) / 4 + 3, `${(yMax * i) / 4}`, {
        anchor: "end",
        size: 8,
      });
    const groups = [
      { name: "6/s", least: 3036, ect: 1209 },
      { name: "10/s", least: 3847, ect: 1319 },
    ];
    const bw = pw / 2;
    groups.forEach((g, gi) => {
      [
        ["least", g.least, "var(--muted)"],
        ["ECT", g.ect, "var(--accent)"],
      ].forEach(([name, value, color], si) => {
        const barW = bw * 0.2;
        const x = left + gi * bw + bw * 0.22 + si * barW * 1.4;
        const bh = (value / yMax) * ph;
        mk(svg, "rect", {
          x,
          y: top + ph - bh,
          width: barW,
          height: bh,
          rx: 3,
          fill: color,
        });
        label(svg, x + barW / 2, top + ph - bh - 5, `${value}`, {
          anchor: "middle",
          size: 8,
          fill: "var(--ink)",
        });
      });
      label(svg, left + gi * bw + bw / 2, top + ph + 14, g.name, {
        anchor: "middle",
        size: 8,
      });
    });
    label(svg, left + pw / 2, H - 2, "P99 TTFT (ms)", {
      anchor: "middle",
      size: 8,
    });
  }
  if ($("#burstMini")) {
    drawBurstMini();
    drawQueueMini();
    drawLongMini();
  }

  // ---------- animation control ----------
  function startSlideAnimations(index) {
    if (burstTimer) {
      clearInterval(burstTimer);
      burstTimer = null;
    }
    if (flowTimer) {
      clearInterval(flowTimer);
      flowTimer = null;
    }
    const reduce =
      window.matchMedia &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (index === 1 && asciiBurst) {
      let step = 0;
      burstFrame(reduce ? 30 : 0);
      if (!reduce)
        burstTimer = setInterval(() => {
          step = step >= 34 ? 0 : step + 1;
          burstFrame(step);
        }, 110);
    }
    if (index === 5 && asciiFlow) {
      let step = 0;
      flowFrame(0);
      if (!reduce)
        flowTimer = setInterval(() => {
          step++;
          flowFrame(step);
        }, 650);
    }
  }

  goTo(initialSlide(), false);
  window.addEventListener("hashchange", () => goTo(initialSlide(), false));
})();
