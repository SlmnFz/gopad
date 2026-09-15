import { decryptReveal } from "./decrypt.js";

const DEFAULT_SPEED = 1;
const MIN_PLAYBACK_DELAY = 95;

export function clampSequence(sequence, minSeq, maxSeq) {
  const value = Number.isFinite(Number(sequence)) ? Number(sequence) : minSeq;
  return Math.max(Number(minSeq) || 0, Math.min(Number(maxSeq) || 0, value));
}

export function nextSequence(sequence, direction, minSeq, maxSeq) {
  return clampSequence(Number(sequence) + (direction < 0 ? -1 : 1), minSeq, maxSeq);
}

export function formatSequence(sequence, width = 6) {
  return String(Math.max(0, Math.trunc(Number(sequence) || 0))).padStart(width, "0");
}

function formatTimestamp(timestamp) {
  if (!timestamp) {
    return "TIME // UNKNOWN";
  }
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) {
    return "TIME // UNKNOWN";
  }
  return new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(date).toUpperCase();
}

function button(label, action, className = "") {
  return `<button class="history-control ${className}" type="button" data-history-action="${action}">${label}</button>`;
}

export function renderScrubber(container, slug, options = {}) {
  if (!container) {
    return { open() {}, close() {}, toggle() {}, destroy() {} };
  }

  container.classList.add("history-scrubber");
  container.hidden = true;
  container.innerHTML = `
    <div class="history-backdrop" data-history-action="close" aria-hidden="true"></div>
    <section class="history-console" role="dialog" aria-modal="true" aria-labelledby="history-title">
      <div class="history-console-head">
        <div>
          <p class="history-kicker"><span>ARCHIVE // TEMPORAL RECONSTRUCTION</span><span data-history-state>STANDBY</span></p>
          <h2 id="history-title">HISTORY SCRUBBER</h2>
        </div>
        ${button("CLOSE ×", "close", "history-close")}
      </div>
      <div class="history-signal-line" aria-hidden="true"><span></span></div>
      <div class="history-readout">
        <div class="history-sequence-readout">
          <span class="history-label">SEQUENCE</span>
          <strong data-history-sequence>000000</strong>
          <span data-history-range>/ 000000</span>
        </div>
        <div class="history-time-readout">
          <span class="history-label">CAPTURE TIME</span>
          <time data-history-time>TIME // WAITING FOR RANGE</time>
        </div>
      </div>
      <div class="history-viewer" data-history-viewer tabindex="0" aria-live="polite">
        <div class="history-viewer-chrome"><span>READ-ONLY MEMORY</span><span data-history-checksum>NODE // IDLE</span></div>
        <pre class="history-text" data-history-text dir="auto">Awaiting durable history…</pre>
      </div>
      <div class="history-timeline">
        <div class="history-timeline-labels"><span data-history-min-label>ORIGIN // 000000</span><span data-history-position-label>POSITION // 000000</span><span data-history-max-label>LIVE // 000000</span></div>
        <input class="history-range" data-history-range-input type="range" min="0" max="0" value="0" step="1" aria-label="History sequence">
        <div class="history-ruler" aria-hidden="true"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></div>
      </div>
      <div class="history-transport">
        <div class="history-transport-group history-transport-nav" aria-label="History navigation">
          ${button("|<", "first", "history-icon-control")}
          ${button("<", "previous", "history-icon-control")}
          ${button("▶ PLAY", "play", "history-play")}
          ${button(">", "next", "history-icon-control")}
          ${button(">|", "last", "history-icon-control")}
          ${button("■ STOP", "stop", "history-stop")}
        </div>
        <label class="history-speed"><span>RATE</span><select data-history-speed aria-label="Playback speed"><option value="0.25">0.25×</option><option value="0.5">0.5×</option><option value="1" selected>1×</option><option value="2">2×</option><option value="4">4×</option><option value="8">8×</option></select></label>
        <button class="history-live-control" type="button" data-history-action="close">RETURN TO LIVE <span>↗</span></button>
      </div>
      <div class="history-console-foot"><span data-history-status>INITIALIZING ARCHIVE CHANNEL…</span><span>SPACE PLAY // ← → STEP // ESC LIVE</span></div>
    </section>
  `;

  const rangeInput = container.querySelector("[data-history-range-input]");
  const textElement = container.querySelector("[data-history-text]");
  const stateElement = container.querySelector("[data-history-state]");
  const statusElement = container.querySelector("[data-history-status]");
  const sequenceElement = container.querySelector("[data-history-sequence]");
  const rangeElement = container.querySelector("[data-history-range]");
  const timeElement = container.querySelector("[data-history-time]");
  const positionLabel = container.querySelector("[data-history-position-label]");
  const minLabel = container.querySelector("[data-history-min-label]");
  const maxLabel = container.querySelector("[data-history-max-label]");
  const checksumElement = container.querySelector("[data-history-checksum]");
  const playButton = container.querySelector('[data-history-action="play"]');
  const speedSelect = container.querySelector("[data-history-speed]");
  const cache = new Map();
  let minSeq = 0;
  let maxSeq = 0;
  let sequence = 0;
  let speed = DEFAULT_SPEED;
  let opened = false;
  let playing = false;
  let debounceTimer = null;
  let playbackTimer = null;
  let requestToken = 0;
  let rangeLoaded = false;

  const setState = (state, message = state) => {
    stateElement.textContent = state.toUpperCase();
    stateElement.dataset.state = state;
    statusElement.textContent = message.toUpperCase();
  };

  const updateReadout = () => {
    const formatted = formatSequence(sequence);
    sequenceElement.textContent = formatted;
    rangeElement.textContent = `/ ${formatSequence(maxSeq)}`;
    positionLabel.textContent = `POSITION // ${formatted}`;
    minLabel.textContent = `ORIGIN // ${formatSequence(minSeq)}`;
    maxLabel.textContent = `LIVE // ${formatSequence(maxSeq)}`;
    rangeInput.value = String(sequence);
  };

  const renderPoint = (point) => {
    sequence = clampSequence(point.sequence, minSeq, maxSeq);
    updateReadout();
    timeElement.textContent = formatTimestamp(point.timestamp);
    checksumElement.textContent = `SEQ // ${formatSequence(sequence)} // VERIFIED`;
    void decryptReveal(textElement, point.text || "", { duration: 260, frames: 12 });
  };

  const fetchPoint = async (requestedSequence, immediate = false) => {
    const target = clampSequence(requestedSequence, minSeq, maxSeq);
    sequence = target;
    updateReadout();
    if (!immediate) {
      if (debounceTimer !== null) {
        window.clearTimeout(debounceTimer);
      }
      setState(playing ? "playing" : "buffering", `BUFFERING SEQUENCE ${formatSequence(target)}`);
      debounceTimer = window.setTimeout(() => {
        debounceTimer = null;
        void fetchPoint(target, true);
      }, 150);
      return;
    }
    const cached = cache.get(target);
    if (cached) {
      renderPoint(cached);
      setState(playing ? "playing" : "paused", playing ? "PLAYBACK LINK ACTIVE" : "CHECKPOINT LOADED");
      return;
    }
    const token = ++requestToken;
    setState(playing ? "playing" : "buffering", `REPLAYING FROM DURABLE LOG // ${formatSequence(target)}`);
    try {
      const response = await fetch(`/api/documents/${encodeURIComponent(slug)}/history?seq=${target}`);
      if (!response.ok) {
        throw new Error(`history request failed (${response.status})`);
      }
      const point = await response.json();
      if (token !== requestToken || !opened) {
        return;
      }
      cache.set(target, point);
      if (cache.size > 96) {
        cache.delete(cache.keys().next().value);
      }
      renderPoint(point);
      setState(playing ? "playing" : "paused", playing ? "PLAYBACK LINK ACTIVE" : "CHECKPOINT LOADED");
    } catch (error) {
      if (token !== requestToken) {
        return;
      }
      stopPlayback();
      setState("error", error instanceof Error ? error.message : "HISTORY REQUEST FAILED");
    }
  };

  const stopPlayback = () => {
    playing = false;
    if (playbackTimer !== null) {
      window.clearTimeout(playbackTimer);
      playbackTimer = null;
    }
    if (playButton) {
      playButton.textContent = "▶ PLAY";
    }
  };

  const playbackStep = () => {
    if (!playing) {
      return;
    }
    const target = nextSequence(sequence, 1, minSeq, maxSeq);
    if (target === sequence) {
      stopPlayback();
      setState("paused", "END OF ARCHIVE // LIVE CHECKPOINT REACHED");
      return;
    }
    void fetchPoint(target, true).finally(() => {
      if (playing) {
        playbackTimer = window.setTimeout(playbackStep, Math.max(MIN_PLAYBACK_DELAY, 420 / speed));
      }
    });
  };

  const startPlayback = () => {
    if (sequence >= maxSeq) {
      sequence = minSeq;
      void fetchPoint(sequence, true);
    }
    playing = true;
    playButton.textContent = "Ⅱ PAUSE";
    setState("playing", "PLAYBACK LINK ACTIVE");
    if (playbackTimer === null) {
      playbackStep();
    }
  };

  const togglePlayback = () => {
    if (playing) {
      stopPlayback();
      setState("paused", "PLAYBACK PAUSED");
    } else {
      startPlayback();
    }
  };

  const loadRange = async () => {
    setState("buffering", "LOCATING DURABLE ARCHIVE RANGE…");
    try {
      const response = await fetch(`/api/documents/${encodeURIComponent(slug)}/history/range`);
      if (!response.ok) {
        throw new Error(`range request failed (${response.status})`);
      }
      const range = await response.json();
      minSeq = Number(range.minSeq) || 0;
      maxSeq = Math.max(minSeq, Number(range.maxSeq) || Number(range.currentSeq) || 0);
      sequence = clampSequence(Number(range.currentSeq), minSeq, maxSeq);
      rangeInput.min = String(minSeq);
      rangeInput.max = String(maxSeq);
      rangeInput.disabled = minSeq === maxSeq;
      rangeLoaded = true;
      updateReadout();
      await fetchPoint(sequence, true);
    } catch (error) {
      setState("error", error instanceof Error ? error.message : "HISTORY RANGE UNAVAILABLE");
    }
  };

  const step = (direction) => {
    stopPlayback();
    const target = nextSequence(sequence, direction, minSeq, maxSeq);
    void fetchPoint(target, true);
  };

  const open = () => {
    if (opened) {
      return;
    }
    opened = true;
    container.hidden = false;
    document.body.classList.add("history-active");
    options.onOpen?.();
    if (rangeLoaded) {
      void loadRange();
    } else {
      void loadRange();
    }
    window.setTimeout(() => container.querySelector("[data-history-range-input]")?.focus(), 0);
  };

  const close = () => {
    if (!opened) {
      return;
    }
    opened = false;
    stopPlayback();
    requestToken += 1;
    if (debounceTimer !== null) {
      window.clearTimeout(debounceTimer);
      debounceTimer = null;
    }
    container.hidden = true;
    document.body.classList.remove("history-active");
    options.onClose?.();
  };

  const onAction = (event) => {
    const action = event.target.closest?.("[data-history-action]")?.dataset.historyAction;
    if (!action) {
      return;
    }
    if (action === "close") close();
    if (action === "first") { stopPlayback(); void fetchPoint(minSeq, true); }
    if (action === "previous") step(-1);
    if (action === "play") togglePlayback();
    if (action === "next") step(1);
    if (action === "last") { stopPlayback(); void fetchPoint(maxSeq, true); }
    if (action === "stop") { stopPlayback(); void fetchPoint(minSeq, true); }
  };

  const onRangeInput = () => {
    stopPlayback();
    void fetchPoint(Number(rangeInput.value));
  };

  const onSpeedChange = () => {
    speed = Math.max(0.25, Number(speedSelect.value) || DEFAULT_SPEED);
  };

  const onKeydown = (event) => {
    if (!opened) return;
    const tagName = event.target?.tagName;
    if (tagName === "SELECT" || tagName === "BUTTON") return;
    if (event.key === "Escape") { event.preventDefault(); close(); }
    if (event.key === " ") { event.preventDefault(); togglePlayback(); }
    if (event.key === "ArrowLeft") { event.preventDefault(); step(-1); }
    if (event.key === "ArrowRight") { event.preventDefault(); step(1); }
  };

  container.addEventListener("click", onAction);
  rangeInput.addEventListener("input", onRangeInput);
  speedSelect.addEventListener("change", onSpeedChange);
  window.addEventListener("keydown", onKeydown);

  return {
    open,
    close,
    toggle() { opened ? close() : open(); },
    destroy() {
      close();
      container.removeEventListener("click", onAction);
      rangeInput.removeEventListener("input", onRangeInput);
      speedSelect.removeEventListener("change", onSpeedChange);
      window.removeEventListener("keydown", onKeydown);
      container.replaceChildren();
    },
  };
}
