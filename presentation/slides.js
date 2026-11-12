(() => {
  "use strict";

  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
  const NS = "http://www.w3.org/2000/svg";
  const slides = $$(".slide");
  const total = slides.length;
  let current = 0;
  let burstTimer = null;
  let pipeTimer = null;

  // ---------- theme ----------
  const root = document.documentElement;
  const themeButton = $("#themeToggle");
  function applyTheme(theme) {
    root.dataset.theme = theme;
    themeButton.textContent = theme === "dark" ? "Light" : "Dark";
    themeButton.setAttribute(
      "aria-label",
      theme === "dark" ? "Switch to light theme" : "Switch to dark theme",
    );
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
  const initialTheme =
    forcedTheme === "dark" || forcedTheme === "light"
      ? forcedTheme
      : savedTheme || (prefersDark ? "dark" : "light");
  applyTheme(initialTheme);
  themeButton.addEventListener("click", () => {
    applyTheme(root.dataset.theme === "dark" ? "light" : "dark");
  });

  // ---------- navigation ----------
  function pad(n) {
    return String(n).padStart(2, "0");
  }
  function goTo(index, updateHash = true) {
    index = Math.max(0, Math.min(total - 1, index));
    if (slides[current]) slides[current].classList.remove("active");
    current = index;
    slides[current].classList.add("active");
    $("#counter").textContent = `${pad(current + 1)} / ${pad(total)}`;
    $("#progressBar").style.width = `${((current + 1) / total) * 100}%`;
    if (updateHash) {
      history.replaceState(null, "", `#slide-${current + 1}`);
    }
    startSlideAnimations(current);
    slides[current].scrollTop = 0;
  }
  function next() {
    goTo(current + 1);
  }
  function prev() {
    goTo(current - 1);
  }

  document.addEventListener("keydown", (event) => {
    const tag = (event.target && event.target.tagName) || "";
    if (/INPUT|TEXTAREA|SELECT/.test(tag)) return;
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
      themeButton.click();
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
    if (!match) return 0;
    return Math.min(total - 1, Math.max(0, Number(match[1]) - 1));
  }

  // ---------- SVG helpers ----------
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
      class: opts.class || "",
      "font-size": opts.size || 10,
      fill: opts.fill || "var(--muted)",
    });
    node.textContent = text;
    if (opts.rotate) node.setAttribute("transform", `rotate(-90 ${x} ${y})`);
    return node;
  }
  function grid(svg, left, top, w, h, rows = 5) {
    for (let i = 0; i <= rows; i++) {
      const y = top + (h * i) / rows;
      mk(svg, "line", { x1: left, y1: y, x2: left + w, y2: y, class: "grid" });
    }
  }
  function niceMax(v) {
    if (!v || v <= 0) return 1;
    const mag = Math.pow(10, Math.floor(Math.log10(v)));
    for (const m of [1, 2, 2.5, 5, 10]) {
      if (v <= m * mag) return m * mag;
    }
    return 10 * mag;
  }

  // ---------- cost model ----------
  const MODEL = { a: 6.7e-6, b: 6.7e-6, f: 0.05, g: 5 };
  function cost(q, c) {
    q = Math.max(0, q);
    c = Math.max(0, c);
    return (
      MODEL.a * q * c + (MODEL.b * q * (q + 1)) / 2 + MODEL.f * q + MODEL.g
    );
  }
  function saving(q, l) {
    return Math.max(0, cost(q + l, 0) - cost(q, l));
  }

  const qRange = $("#qRange"),
    cRange = $("#cRange");
  const costChart = $("#costChart");
  function drawCost() {
    const q = Number(qRange.value),
      c = Number(cRange.value);
    $("#qVal").textContent = q.toLocaleString();
    $("#cVal").textContent = c.toLocaleString();
    const warm = cost(q, c),
      cold = cost(q + c, 0);
    $("#costOut").textContent = `${warm.toFixed(1)} ms`;
    $("#coldOut").textContent = `${cold.toFixed(1)} ms`;
    $("#saveOut").textContent = `${Math.max(0, cold - warm).toFixed(1)} ms`;
    const work = Math.max(0.001, warm - MODEL.g);
    const attn = (MODEL.a * q * c + (MODEL.b * q * (q + 1)) / 2) / work;
    $("#attnOut").textContent = `${(Math.min(1, attn) * 100).toFixed(1)}%`;

    clear(costChart);
    costChart.classList.add("chart");
    const W = 540,
      H = 300,
      left = 46,
      top = 16,
      right = 16,
      bottom = 38;
    const pw = W - left - right,
      ph = H - top - bottom;
    const qMax = 8192,
      yMax = niceMax(cost(qMax + c, 0));
    grid(costChart, left, top, pw, ph);
    for (let i = 0; i <= 5; i++) {
      const v = (yMax * i) / 5;
      label(costChart, left - 7, top + ph - (ph * i) / 5 + 3, v.toFixed(0), {
        anchor: "end",
      });
      const qv = (qMax * i) / 5;
      label(
        costChart,
        left + (pw * i) / 5,
        top + ph + 16,
        `${(qv / 1000).toFixed(0)}k`,
        { anchor: "middle" },
      );
    }
    label(costChart, left + pw / 2, H - 4, "new query tokens", {
      anchor: "middle",
    });
    label(costChart, 12, top + ph / 2, "ms", {
      anchor: "middle",
      rotate: true,
    });

    function path(warmLine) {
      let d = "";
      for (let x = 0; x <= qMax; x += 128) {
        const px = left + (pw * x) / qMax;
        const value = warmLine ? cost(x, c) : cost(x + c, 0);
        const py = top + ph - (ph * Math.min(yMax, value)) / yMax;
        d += `${d ? " L" : "M"}${px.toFixed(1)} ${py.toFixed(1)}`;
      }
      return d;
    }
    mk(costChart, "path", { d: path(false), class: "series gold" });
    mk(costChart, "path", { d: path(true), class: "series" });
    const mx = left + (pw * q) / qMax;
    const my = top + ph - (ph * Math.min(yMax, warm)) / yMax;
    mk(costChart, "circle", { cx: mx, cy: my, r: 4, fill: "var(--accent)" });
    label(costChart, mx + 6, my - 6, `${warm.toFixed(0)} ms`, {
      class: "label",
    });
  }
  if (qRange && cRange) {
    qRange.addEventListener("input", drawCost);
    cRange.addEventListener("input", drawCost);
    drawCost();
  }

  // ---------- hold or spill ----------
  const lRange = $("#lRange"),
    tauRange = $("#tauRange"),
    gapRange = $("#gapRange"),
    slackRange = $("#slackRange");
  function drawHold() {
    const L = Number(lRange.value),
      tau = Number(tauRange.value);
    const gap = Number(gapRange.value),
      slack = Number(slackRange.value);
    $("#lVal").textContent = L.toLocaleString();
    $("#tauVal").textContent = tau;
    $("#gapVal").textContent = gap;
    $("#slackVal").textContent = slack;
    const save = saving(0, L);
    const wait = Math.max(0, tau + gap);
    const gain = save - wait;
    const withinSlack = tau <= slack;
    const withinCap = tau <= 250;
    const hold = L > 0 && gain > 1 && withinSlack && withinCap;
    const badge = $("#holdDecision");
    badge.textContent = hold ? "HOLD" : "SPILL";
    badge.classList.toggle("spill", !hold);
    let reason = `saving ${save.toFixed(0)} ms, wait ${wait.toFixed(0)} ms`;
    if (!withinSlack) reason = "leader exceeds request slack";
    else if (!withinCap) reason = "remaining prefill exceeds the 250 ms cap";
    else if (L <= 0) reason = "no shared prefix";
    else if (gain <= 1) reason = "spill is cheaper";
    $("#holdReason").textContent = reason;

    const max = Math.max(1, save, wait);
    $("#saveBar").style.width = `${(save / max) * 100}%`;
    $("#waitBar").style.width = `${(wait / max) * 100}%`;
    $("#saveLabel").textContent = save.toFixed(0);
    $("#waitLabel").textContent = wait.toFixed(0);

    const leaderFull = Math.max(
      0,
      Math.min(20, Math.round(20 * (1 - Math.min(1, tau / 300)))),
    );
    const waitFill = Math.max(
      0,
      Math.min(20, Math.round(20 * Math.min(1, tau / 300))),
    );
    const warm = Math.max(
      1,
      Math.min(20, Math.round(20 * Math.min(1, save / 600))),
    );
    const leader = "█".repeat(leaderFull) + "░".repeat(20 - leaderFull);
    const follower = "█".repeat(waitFill) + "░".repeat(20 - waitFill);
    const warmBar = "█".repeat(warm) + "░".repeat(20 - warm);
    $("#asciiHold").innerHTML =
      `leader    prefill  <span class="teal">[${leader}]</span>  τ ${tau} ms\n` +
      `follower  wait     <span class="gold">[${follower}]</span>\n` +
      `          warm     <span class="ok">[${warmBar}]</span>  ${save.toFixed(0)} ms saved`;
  }
  if (lRange) {
    [lRange, tauRange, gapRange, slackRange].forEach((el) =>
      el.addEventListener("input", drawHold),
    );
    drawHold();
  }

  // ---------- queue ----------
  const dStream = $("#dStream"),
    dBatch = $("#dBatch"),
    etaRange = $("#etaRange");
  const queueChart = $("#queueChart");
  const queueJobs = [
    { id: "s1", arrival: 0, stream: true, cost: 120 },
    { id: "b1", arrival: 10, stream: false, cost: 300 },
    { id: "s2", arrival: 30, stream: true, cost: 80 },
    { id: "b2", arrival: 60, stream: false, cost: 500 },
    { id: "s3", arrival: 90, stream: true, cost: 60 },
    { id: "s4", arrival: 140, stream: true, cost: 100 },
  ];
  function drawQueue() {
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
    const maxD = Math.max(...jobs.map((j) => j.deadline), 1000) * 1.08;
    clear(queueChart);
    queueChart.classList.add("chart");
    const W = 540,
      H = 260,
      left = 46,
      top = 20,
      right = 16,
      bottom = 40;
    const pw = W - left - right,
      ph = H - top - bottom;
    grid(queueChart, left, top, pw, ph, 4);
    for (let i = 0; i <= 4; i++) {
      label(
        queueChart,
        left + (pw * i) / 4,
        top + ph + 16,
        `${((maxD * i) / 4 / 1000).toFixed(1)}s`,
        { anchor: "middle" },
      );
    }
    label(queueChart, left + pw / 2, H - 5, "virtual deadline", {
      anchor: "middle",
    });
    const rowH = ph / jobs.length;
    jobs.forEach((j, i) => {
      const y = top + i * rowH + rowH / 2;
      label(queueChart, left - 6, y + 4, j.id, {
        anchor: "end",
        class: "label",
      });
      mk(queueChart, "line", {
        x1: left,
        y1: y,
        x2: left + pw,
        y2: y,
        class: "grid",
      });
      mk(queueChart, "rect", {
        x: left + (j.arrival / maxD) * pw,
        y: y - 6,
        width: Math.max(2, ((j.deadline - j.arrival) / maxD) * pw),
        height: 12,
        rx: 3,
        fill: j.stream ? "var(--accent)" : "var(--gold)",
        opacity: j.stream ? 0.85 : 0.9,
      });
    });
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

  // ---------- ECT ----------
  const ectReplicas = [
    { id: 0, cached: 4096, wait: 500, inflight: 1, kv: 72 },
    { id: 1, cached: 0, wait: 40, inflight: 0, kv: 34 },
    { id: 2, cached: 2048, wait: 10, inflight: 0, kv: 55 },
    { id: 3, cached: 0, wait: 300, inflight: 2, kv: 96 },
  ];
  const ectRequest = { q: 4600, out: 64 };
  let ectPolicy = "ect";
  function ectScores() {
    return ectReplicas.map((r) => {
      const uncached = Math.max(0, ectRequest.q - r.cached);
      const prefill = cost(uncached, r.cached);
      const score =
        r.wait +
        prefill +
        2 * r.inflight +
        0.5 * (8 + 0.02 * Math.min(uncached, 512) - 8);
      const tokens = uncached + ectRequest.out;
      return { ...r, uncached, prefill, score, tokens };
    });
  }
  function renderECT() {
    const scores = ectScores();
    let winner = 0;
    let reason = "";
    if (ectPolicy === "rr") {
      winner = 0;
      reason = "Round robin takes the next replica in rotation.";
    } else if (ectPolicy === "tokens") {
      winner = scores.reduce(
        (best, r, i) => (r.tokens < scores[best].tokens ? i : best),
        0,
      );
      reason = "Least tokens counts uncached prompt plus predicted output.";
    } else {
      let best = Infinity;
      scores.forEach((r, i) => {
        if (r.kv >= 90) return;
        if (r.score < best) {
          best = r.score;
          winner = i;
        }
      });
      reason = `ECT chooses replica ${winner}: queue wait plus calibrated prefill under KV and ITL guards.`;
    }
    const grid = $("#replicaGrid");
    grid.innerHTML = scores
      .map((r, i) => {
        const skip = ectPolicy === "ect" && r.kv >= 90;
        const shown =
          ectPolicy === "tokens"
            ? `${r.tokens} tok`
            : ectPolicy === "rr"
              ? "rotation"
              : `${r.score.toFixed(0)} ms`;
        return `<div class="replica-card ${i === winner && !skip ? "win" : ""} ${skip ? "skip" : ""}">
        <div class="rid">replica ${r.id}</div>
        <div class="score">${skip ? "guard" : shown}</div>
        <dl>
          <div><span>cached</span><b>${r.cached}</b></div>
          <div><span>queue</span><b>${r.wait} ms</b></div>
          <div><span>inflight</span><b>${r.inflight}</b></div>
          <div><span>KV</span><b>${r.kv}%</b></div>
        </dl>
      </div>`;
      })
      .join("");
    $("#ectReason").textContent = reason;
  }
  $$(".policy-toggle button").forEach((button) => {
    button.addEventListener("click", () => {
      ectPolicy = button.dataset.policy;
      $$(".policy-toggle button").forEach((b) =>
        b.classList.toggle("on", b === button),
      );
      renderECT();
    });
  });
  if ($("#replicaGrid")) renderECT();

  // ---------- affinity gate ----------
  const rateRange = $("#rateRange"),
    prefixRange = $("#prefixRange");
  function drawGate() {
    const rate = Number(rateRange.value),
      prefix = Number(prefixRange.value);
    $("#rateVal").textContent = rate;
    $("#prefixVal").textContent = prefix.toLocaleString();
    const recoverable = rate * saving(0, prefix);
    const threshold = 0.15 * 1000 * 4;
    let action = "SPREAD";
    let count = 0;
    if (recoverable >= threshold) {
      count = Math.max(1, Math.min(4, Math.ceil(recoverable / 800)));
      action = count <= 1 ? "PIN" : "REPLICATE";
    }
    const badge = $("#gateDecision");
    badge.textContent = action;
    badge.classList.toggle("spill", action === "SPREAD");
    $("#gateReason").textContent =
      action === "SPREAD"
        ? `recoverable ${recoverable.toFixed(0)} ms/s is below the ${threshold.toFixed(0)} ms/s threshold`
        : `${count} replica${count === 1 ? "" : "s"} · recoverable ${recoverable.toFixed(0)} ms/s · threshold ${threshold.toFixed(0)} ms/s`;
    $("#gateFill").style.width =
      `${Math.min(100, (recoverable / (threshold * 1.6)) * 100)}%`;
  }
  if (rateRange) {
    [rateRange, prefixRange].forEach((el) =>
      el.addEventListener("input", drawGate),
    );
    drawGate();
  }

  // ---------- burst results ----------
  const burstData = {
    p50: [504.6, 1957.6, 509.1, 142.5],
    prefill: [3886.6, 3886.6, 3886.6, 509.4],
    dup: [96.9, 96.9, 96.9, 66.7],
  };
  const burstLabels = ["round robin", "llmd style", "ECT", "ECT + coalesce"];
  const burstColors = [
    "var(--muted)",
    "var(--gold)",
    "var(--accent)",
    "var(--good)",
  ];
  let burstMetric = "p50";
  function drawBurst() {
    const svg = $("#burstChart");
    if (!svg) return;
    const values = burstData[burstMetric];
    const unit = burstMetric === "dup" ? "%" : "ms";
    const yMax = niceMax(Math.max(...values) * 1.12);
    clear(svg);
    svg.classList.add("chart");
    const W = 720,
      H = 360,
      left = 56,
      top = 18,
      right = 18,
      bottom = 62;
    const pw = W - left - right,
      ph = H - top - bottom;
    grid(svg, left, top, pw, ph, 5);
    for (let i = 0; i <= 5; i++) {
      label(
        svg,
        left - 8,
        top + ph - (ph * i) / 5 + 3,
        `${((yMax * i) / 5).toFixed(0)}`,
        { anchor: "end" },
      );
    }
    const bw = pw / values.length;
    values.forEach((v, i) => {
      const bh = (v / yMax) * ph;
      const x = left + i * bw + bw * 0.22;
      const w = bw * 0.56;
      const rect = mk(svg, "rect", {
        x,
        y: top + ph - bh,
        width: w,
        height: bh,
        rx: 4,
        fill: burstColors[i],
      });
      const title = document.createElementNS(NS, "title");
      title.textContent = `${burstLabels[i]}: ${v}${unit}`;
      rect.appendChild(title);
      label(svg, x + w / 2, top + ph - bh - 8, `${v.toFixed(1)}${unit}`, {
        anchor: "middle",
        class: "label",
      });
      label(svg, x + w / 2, top + ph + 20, burstLabels[i], {
        anchor: "middle",
      });
    });
    label(
      svg,
      left + pw / 2,
      H - 6,
      burstMetric === "dup"
        ? "duplicate prefill ratio"
        : burstMetric === "prefill"
          ? "prefill work"
          : "time to first token",
      { anchor: "middle" },
    );
  }
  $$(".metric-toggle button").forEach((button) => {
    button.addEventListener("click", () => {
      burstMetric = button.dataset.metric;
      $$(".metric-toggle button").forEach((b) =>
        b.classList.toggle("on", b === button),
      );
      drawBurst();
    });
  });
  if ($("#burstChart")) drawBurst();

  // ---------- tradeoff and long context ----------
  const tradeData = {
    5: {
      fifo: [1535.4, 1224.0],
      strict: [563.7, 1423.6],
      edf: [1187.2, 1239.6],
    },
    8: {
      fifo: [5024.9, 4648.9],
      strict: [562.2, 5108.8],
      edf: [4661.9, 4893.1],
    },
    11: {
      fifo: [9584.0, 8837.0],
      strict: [614.6, 11156.1],
      edf: [8641.0, 9061.9],
    },
    14: {
      fifo: [22772.2, 22050.8],
      strict: [620.5, 22257.9],
      edf: [20553.9, 22257.9],
    },
  };
  const tradeColors = {
    fifo: "var(--muted)",
    strict: "var(--bad)",
    edf: "var(--accent)",
  };
  const tradeNames = { fifo: "FIFO", strict: "strict", edf: "EDF 0.8s" };
  function drawTrade() {
    const svg = $("#tradeChart");
    const rate = $("#rateSelect").value;
    const data = tradeData[rate];
    const maxX = niceMax(
      Math.max(...Object.values(data).map((v) => v[0])) * 1.12,
    );
    const maxY = niceMax(
      Math.max(...Object.values(data).map((v) => v[1])) * 1.12,
    );
    clear(svg);
    svg.classList.add("chart");
    const W = 520,
      H = 280,
      left = 54,
      top = 18,
      right = 18,
      bottom = 46;
    const pw = W - left - right,
      ph = H - top - bottom;
    grid(svg, left, top, pw, ph, 4);
    for (let i = 0; i <= 4; i++) {
      label(
        svg,
        left - 7,
        top + ph - (ph * i) / 4 + 3,
        `${((maxY * i) / 4 / 1000).toFixed(1)}s`,
        { anchor: "end" },
      );
      label(
        svg,
        left + (pw * i) / 4,
        top + ph + 16,
        `${((maxX * i) / 4 / 1000).toFixed(1)}s`,
        { anchor: "middle" },
      );
    }
    label(svg, left + pw / 2, H - 5, "stream P99 TTFT", { anchor: "middle" });
    label(svg, 12, top + ph / 2, "batch P99 wait", {
      anchor: "middle",
      rotate: true,
    });
    for (const [key, value] of Object.entries(data)) {
      const x = left + (value[0] / maxX) * pw;
      const y = top + ph - (value[1] / maxY) * ph;
      const circle = mk(svg, "circle", {
        cx: x,
        cy: y,
        r: 6,
        fill: tradeColors[key],
      });
      const title = document.createElementNS(NS, "title");
      title.textContent = `${tradeNames[key]} at ${rate} req/s: stream ${value[0]} ms, batch ${value[1]} ms`;
      circle.appendChild(title);
      const offsets = { fifo: [9, -10], strict: [9, 4], edf: [-55, 4] };
      const off = offsets[key] || [9, 4];
      label(svg, x + off[0], y + off[1], tradeNames[key], {
        fill: tradeColors[key],
        size: 10,
      });
    }
    $("#tradeLegend").innerHTML = Object.keys(data)
      .map(
        (key) =>
          `<span><span class="dot" style="background:${tradeColors[key]}"></span>${tradeNames[key]}</span>`,
      )
      .join("");
  }
  const longData = [
    { label: "least tokens", six: 3036.1, ten: 3847.3, color: "var(--muted)" },
    { label: "Bales ECT", six: 1209.2, ten: 1318.7, color: "var(--accent)" },
  ];
  function drawLong() {
    const svg = $("#longChart");
    if (!svg) return;
    clear(svg);
    svg.classList.add("chart");
    const W = 520,
      H = 280,
      left = 54,
      top = 18,
      right = 16,
      bottom = 52;
    const pw = W - left - right,
      ph = H - top - bottom;
    const yMax = 4200;
    grid(svg, left, top, pw, ph, 4);
    for (let i = 0; i <= 4; i++) {
      label(
        svg,
        left - 7,
        top + ph - (ph * i) / 4 + 3,
        `${((yMax * i) / 4).toFixed(0)}`,
        { anchor: "end" },
      );
    }
    const groups = ["6 req/s", "10 req/s"];
    const bw = pw / 2;
    groups.forEach((group, gi) => {
      longData.forEach((series, si) => {
        const value = gi === 0 ? series.six : series.ten;
        const barW = bw * 0.22;
        const x = left + gi * bw + bw * 0.24 + si * barW * 1.25;
        const bh = (value / yMax) * ph;
        const rect = mk(svg, "rect", {
          x,
          y: top + ph - bh,
          width: barW,
          height: bh,
          rx: 3,
          fill: series.color,
        });
        const title = document.createElementNS(NS, "title");
        title.textContent = `${series.label} ${group}: ${value} ms`;
        rect.appendChild(title);
        label(svg, x + barW / 2, top + ph - bh - 6, `${value.toFixed(0)}`, {
          anchor: "middle",
          size: 9,
        });
      });
      label(svg, left + gi * bw + bw / 2, top + ph + 18, group, {
        anchor: "middle",
      });
    });
    label(svg, left + pw / 2, H - 6, "P99 TTFT (ms)", { anchor: "middle" });
  }
  if ($("#rateSelect")) {
    $("#rateSelect").addEventListener("change", drawTrade);
    drawTrade();
    drawLong();
  }

  // ---------- tabs ----------
  $$(".tab-buttons button").forEach((button) => {
    button.addEventListener("click", () => {
      const name = button.dataset.tab;
      $$(".tab-buttons button").forEach((b) =>
        b.classList.toggle("on", b === button),
      );
      $$(".tab-panel").forEach((panel) =>
        panel.classList.toggle("on", panel.dataset.panel === name),
      );
    });
  });

  // ---------- ascii animations ----------
  const asciiBurst = $("#asciiBurst");
  function burstFrame(step) {
    const cycle = step % 36;
    const n = Math.min(20, cycle);
    const bar = "█".repeat(n) + "░".repeat(20 - n);
    const status =
      cycle >= 20
        ? '<span class="bad">duplicate prefill</span>'
        : '<span class="dim">prefilling…</span>';
    asciiBurst.innerHTML =
      ` prompt <span class="teal">"explain prefix caching ..."</span>\n` +
      `        │\n` +
      `   ┌────┴─────┐\n` +
      `   ▼          ▼\n` +
      ` GPU 0       GPU 1\n` +
      ` <span class="teal">[${bar}]</span>  <span class="gold">[${bar}]</span>\n` +
      ` 2048 tok    2048 tok   ${status}\n` +
      `        ╲        ╱\n` +
      `         ╲      ╱\n` +
      `       <span class="bad">same KV built twice</span>`;
  }
  const asciiPipe = $("#asciiPipe");
  function pipeFrame(step) {
    const names = ["client", "admit", "queue", "coalesce", "ECT", "GPU"];
    const padded = names.map((n) => n.padEnd(8));
    const active = step % names.length;
    const line = padded
      .map((n, i) =>
        i === active
          ? `<span class="on">[${n}]</span>`
          : `<span class="dim">[${n}]</span>`,
      )
      .join(" ─▶ ");
    const pos =
      padded.slice(0, active).reduce((acc, n) => acc + n.length + 4, 0) + 2;
    asciiPipe.innerHTML = `${line}\n${" ".repeat(pos)}<span class="teal">▲</span>`;
  }
  function startSlideAnimations(index) {
    if (burstTimer) {
      clearInterval(burstTimer);
      burstTimer = null;
    }
    if (pipeTimer) {
      clearInterval(pipeTimer);
      pipeTimer = null;
    }
    if (index === 1 && asciiBurst) {
      let step = 0;
      burstFrame(step);
      burstTimer = setInterval(() => {
        step++;
        if (step > 40) step = 0;
        burstFrame(step);
      }, 110);
    }
    if (index === 4 && asciiPipe) {
      let step = 0;
      pipeFrame(step);
      pipeTimer = setInterval(() => {
        step++;
        pipeFrame(step);
      }, 650);
    }
  }

  // ---------- boot ----------
  goTo(initialSlide(), false);
  window.addEventListener("hashchange", () => goTo(initialSlide(), false));
})();
