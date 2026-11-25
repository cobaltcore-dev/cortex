// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

"use strict";
document.documentElement.classList.add("js");

const slides = [...document.querySelectorAll(".slide")];
const viewport = document.getElementById("viewport");
const previousButton = document.getElementById("previous-button");
const nextButton = document.getElementById("next-button");
const overview = document.getElementById("overview-dialog");
const notes = document.getElementById("notes-dialog");
const help = document.getElementById("help-dialog");
const dialogs = [overview, notes, help];
const demo = document.getElementById("demo");
const demoSlideIndex = slides.indexOf(demo.closest(".slide"));
const demoPlay = document.getElementById("demo-play");
const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
const printDownloads = [...document.querySelectorAll("a[download]")].map(link => ({ link, href: link.getAttribute("href") }));
const printSourceURL = "https://github.com/cobaltcore-dev/cortex/blob/knowledge-db-2.0/docs/presentations/knowledge-database-2.0/";
const demoDurations = [800, 2000, 3000, 3000, 3000, 4000];
let current = -1;
let demoStep = 1;
let demoWantsPlayback = !reducedMotion.matches;
let demoPlaying = false;
let demoHasPlayed = false;
let demoRemaining = demoDurations[1];
let demoDeadline = 0;
let demoTimer;
let progressAnimation;
let toastTimer;
let printing = false;
let printedDemo = null;

const demoStages = [
  { label: "METRICS + NOVA → KNOWLEDGE", count: "2 sources", status: "Next extraction cycle…", caption: "The same Cortex operator runs your expression over the declared inputs." },
  { label: "DATASOURCES / CACHED INPUTS", count: "2 sources", status: "Metrics and hypervisors ingested", caption: "Cortex caches CPU contention samples and the Nova hypervisor inventory." },
  { label: "JOIN / Node = Hostname", count: "3 hosts", status: "Metrics matched to compute hosts", caption: "The expression groups samples by node and joins them to each hypervisor's hostname." },
  { label: "AGGREGATE / RANK HOSTS", count: "3 features", status: "Per-host contention extracted", caption: "Samples become average and maximum contention. Hosts are ordered by their average." },
  { label: "OUTPUT / KNOWLEDGE STATUS", count: "3 features", status: "Written to status.raw.features", caption: "The resulting status YAML is ready for scheduling consumers. No Cortex code changes." },
  { label: "KNOWLEDGE / HOST CONTENTION", count: "3 features", status: "Lower contention → higher preference", caption: "Cortex smart weighing can consume these features to prefer a less contended host." }
];
const lastDemoStep = demoStages.length - 1;

const hostSamples = ["74, 86", "6, 10", "38, 46"];
const hostAverages = [80, 8, 42];

function fitDeck() {
  document.documentElement.style.setProperty("--deck-scale", String(Math.min(viewport.clientWidth / 1440, viewport.clientHeight / 810)));
}

function renderDemo(step, { announce = false } = {}) {
  demoStep = Math.max(0, Math.min(lastDemoStep, step));
  demoRemaining = demoDurations[demoStep];
  demo.dataset.step = String(demoStep);
  const state = demoStages[demoStep];
  document.getElementById("data-label").textContent = state.label;
  document.getElementById("object-count").textContent = state.count;
  document.getElementById("value-field").textContent = demoStep < 3 ? "Samples (%)" : "Avg. contention";
  document.querySelector("#demo-status strong").textContent = state.status;
  document.getElementById("demo-caption").textContent = state.caption;
  demo.querySelectorAll("[data-highlight]").forEach(mark => mark.classList.toggle("highlight", Number(mark.dataset.highlight) === demoStep));
  demo.querySelectorAll("[data-pipeline]").forEach(node => node.classList.toggle("reached", Number(node.dataset.pipeline) <= demoStep));
  demo.querySelectorAll("[data-host]").forEach(row => {
    const host = Number(row.dataset.host);
    row.setAttribute("aria-hidden", String(demoStep < 2 || demoStep >= 4));
    row.querySelector("[data-host-value]").textContent = demoStep < 3 ? hostSamples[host] : `${hostAverages[host]}%`;
    row.style.setProperty("--slot", String(demoStep < 3 ? host : [2, 0, 1][host]));
  });
  demo.querySelector(".result-chart").setAttribute("aria-hidden", String(demoStep >= 4));
  demo.querySelector(".result-status").setAttribute("aria-hidden", String(demoStep < 4));
  demo.querySelector(".aggregation-equation").setAttribute("aria-hidden", String(demoStep !== 3));
  demo.querySelector(".placement-outcome").setAttribute("aria-hidden", String(demoStep !== lastDemoStep));
  demo.querySelector(".demo-status").setAttribute("aria-hidden", String(demoStep === lastDemoStep));
  demo.querySelector(".demo-caption").setAttribute("aria-hidden", String(demoStep === lastDemoStep));
  document.querySelectorAll("[data-step-button]").forEach(button => {
    button.setAttribute("aria-current", Number(button.dataset.stepButton) === demoStep ? "step" : "false");
    button.classList.toggle("complete", Number(button.dataset.stepButton) < demoStep);
  });
  if (announce) document.querySelector(".demo-announcement").textContent = `Step ${demoStep} of ${lastDemoStep}. ${state.status}. ${state.caption}`;
  progressAnimation?.cancel();
  progressAnimation = null;
  document.querySelectorAll(".step-progress").forEach(progress => progress.style.removeProperty("transform"));
  const progress = document.querySelector(`[data-step-button="${demoStep}"] .step-progress`);
  if (progress && !reducedMotion.matches) {
    progressAnimation = progress.animate({ transform: ["scaleX(0)", "scaleX(1)"] }, { duration: demoRemaining, fill: "both" });
    progressAnimation.pause();
  } else if (progress) {
    progress.style.transform = "scaleX(1)";
  }
  updatePlayButton();
}

function updatePlayButton() {
  demo.dataset.playing = String(demoPlaying);
  demo.dataset.userPaused = String(!demoWantsPlayback);
  demoPlay.textContent = demoPlaying ? "Ⅱ Pause demo" : demoHasPlayed ? "▶ Resume loop" : "▶ Play loop";
  demoPlay.setAttribute("aria-pressed", String(demoPlaying));
  document.getElementById("demo-loop-status").textContent = demoPlaying ? "∞ AUTOPLAY / LOOP" : reducedMotion.matches && !demoHasPlayed ? "REDUCED MOTION / STILL" : "Ⅱ LOOP / PAUSED";
}

function suspendPlayback() {
  clearTimeout(demoTimer);
  if (demoPlaying) demoRemaining = Math.max(0, demoDeadline - Date.now());
  demoPlaying = false;
  progressAnimation?.pause();
}

function syncPlayback() {
  const obstructed = document.hidden || dialogs.some(dialog => dialog.open) || printing;
  document.documentElement.classList.toggle("motion-suspended", obstructed);
  const shouldPlay = demoWantsPlayback && current === demoSlideIndex && !obstructed;
  if (!shouldPlay) suspendPlayback();
  else if (!demoPlaying) {
    demoPlaying = true;
    demoHasPlayed = true;
    demoDeadline = Date.now() + demoRemaining;
    if (progressAnimation) {
      progressAnimation.currentTime = demoDurations[demoStep] - demoRemaining;
      progressAnimation.play();
    }
    demoTimer = setTimeout(() => {
      demoPlaying = false;
      renderDemo(demoStep === lastDemoStep ? 0 : demoStep + 1);
      syncPlayback();
    }, demoRemaining);
  }
  updatePlayButton();
}

function pauseDemo() {
  demoWantsPlayback = false;
  syncPlayback();
}

function playDemo(restart = false) {
  suspendPlayback();
  if (restart || (!demoHasPlayed && reducedMotion.matches && demoStep === lastDemoStep)) renderDemo(1);
  demoWantsPlayback = true;
  syncPlayback();
}

function toggleDemo() {
  if (demoPlaying) pauseDemo();
  else playDemo();
}

function selectDemoStep(step) {
  pauseDemo();
  renderDemo(step, { announce: true });
}

function updateNotes() {
  document.getElementById("notes-position").textContent = `SLIDE ${current + 1} / ${slides.length}`;
  document.getElementById("notes-title").textContent = slides[current].dataset.title;
  document.getElementById("notes-content").innerHTML = slides[current].querySelector(".speaker-notes").innerHTML;
  document.getElementById("notes-previous").disabled = current === 0;
  document.getElementById("notes-next").disabled = current === slides.length - 1;
}

function showSlide(index, { updateHash = true, focus = false } = {}) {
  const next = Math.max(0, Math.min(slides.length - 1, index));
  current = next;
  slides.forEach((slide, i) => {
    slide.hidden = i !== current;
    slide.classList.toggle("active", i === current);
    slide.setAttribute("aria-hidden", String(i !== current));
  });
  document.getElementById("slide-counter").textContent = `${String(current + 1).padStart(2, "0")} / ${String(slides.length).padStart(2, "0")}`;
  previousButton.disabled = current === 0;
  nextButton.disabled = current === slides.length - 1;
  document.getElementById("progress-bar").style.width = `${((current + 1) / slides.length) * 100}%`;
  document.title = `${current + 1}. ${slides[current].dataset.title} · Cortex Knowledge 2.0`;
  document.getElementById("announcement").textContent = `Slide ${current + 1} of ${slides.length}: ${slides[current].dataset.title}`;
  document.querySelector(".skip-link").href = `#${slides[current].id}`;
  document.querySelectorAll(".overview-item").forEach((button, i) => button.setAttribute("aria-current", String(i === current)));
  if (updateHash && window.location.hash !== `#${slides[current].id}`) window.location.hash = slides[current].id;
  updateNotes();
  syncPlayback();
  if (focus) slides[current].focus({ preventScroll: true });
}

function indexFromHash() {
  const match = window.location.hash.match(/^#slide-(\d+)$/);
  return match ? Math.max(0, Math.min(slides.length - 1, Number(match[1]) - 1)) : 0;
}

function toggleDialog(dialog) {
  if (dialog.open) { dialog.close(); return; }
  dialogs.forEach(other => { if (other.open) other.close(); });
  if (dialog === notes) updateNotes();
  dialog.showModal();
  syncPlayback();
  if (dialog === overview) document.querySelector(`.overview-item[data-index="${current}"]`).focus();
}

function toast(message) {
  const element = document.getElementById("toast");
  clearTimeout(toastTimer);
  element.textContent = message;
  element.hidden = false;
  toastTimer = setTimeout(() => { element.hidden = true; }, 3000);
}

async function toggleFullscreen() {
  try {
    if (document.fullscreenElement) await document.exitFullscreen();
    else if (document.documentElement.requestFullscreen) await document.documentElement.requestFullscreen();
    else toast("Use your browser's fullscreen command.");
  } catch { toast("Use your browser's fullscreen command."); }
}

slides.forEach((slide, index) => {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "overview-item";
  button.dataset.index = String(index);
  const number = document.createElement("span");
  number.textContent = String(index + 1).padStart(2, "0");
  const title = document.createElement("strong");
  title.textContent = slide.dataset.title;
  button.append(number, title);
  button.addEventListener("click", () => { overview.close(); showSlide(index, { focus: true }); });
  document.getElementById("overview-grid").append(button);
});

previousButton.addEventListener("click", () => showSlide(current - 1));
nextButton.addEventListener("click", () => showSlide(current + 1));
document.getElementById("notes-previous").addEventListener("click", () => showSlide(current - 1));
document.getElementById("notes-next").addEventListener("click", () => showSlide(current + 1));
document.getElementById("overview-button").addEventListener("click", () => toggleDialog(overview));
document.getElementById("notes-button").addEventListener("click", () => toggleDialog(notes));
document.getElementById("help-button").addEventListener("click", () => toggleDialog(help));
document.getElementById("fullscreen-button").addEventListener("click", toggleFullscreen);
document.getElementById("print-button").addEventListener("click", () => { help.close(); window.print(); });
document.querySelectorAll(".close-dialog").forEach(button => button.addEventListener("click", () => button.closest("dialog").close()));
dialogs.forEach(dialog => dialog.addEventListener("click", event => {
  if (event.target !== dialog) return;
  const b = dialog.getBoundingClientRect();
  if (event.clientX < b.left || event.clientX > b.right || event.clientY < b.top || event.clientY > b.bottom) dialog.close();
}));
dialogs.forEach(dialog => dialog.addEventListener("close", syncPlayback));
demoPlay.addEventListener("click", toggleDemo);
document.getElementById("demo-replay").addEventListener("click", () => playDemo(true));
document.querySelectorAll("[data-step-button]").forEach(button => button.addEventListener("click", () => selectDemoStep(Number(button.dataset.stepButton))));

document.addEventListener("keydown", event => {
  if (event.ctrlKey || event.metaKey || event.altKey || event.target.closest("input, textarea, select, [contenteditable]")) return;
  const key = event.key.toLowerCase();
  if (["o", "n", "f", "?"].includes(key)) {
    event.preventDefault();
    if (key === "o") toggleDialog(overview);
    if (key === "n") toggleDialog(notes);
    if (key === "f") toggleFullscreen();
    if (key === "?") toggleDialog(help);
    return;
  }
  if (overview.open || help.open) return;
  if (key === " " && event.target.closest("button, a")) return;
  if (current === demoSlideIndex && !notes.open) {
    if (key === "p" || (key === " " && !event.shiftKey)) { event.preventDefault(); toggleDemo(); return; }
    if (key === "r") { event.preventDefault(); playDemo(true); return; }
    if (/^[1-5]$/.test(key)) { event.preventDefault(); selectDemoStep(Number(key)); return; }
  }
  if (["arrowright", "arrowdown", "pagedown", " "].includes(key)) {
    event.preventDefault(); showSlide(current + (key === " " && event.shiftKey ? -1 : 1));
  } else if (["arrowleft", "arrowup", "pageup"].includes(key)) {
    event.preventDefault(); showSlide(current - 1);
  } else if (key === "home" || key === "end") {
    event.preventDefault(); showSlide(key === "home" ? 0 : slides.length - 1);
  }
});

let touchStart;
viewport.addEventListener("touchstart", event => {
  touchStart = event.touches.length === 1 && !event.target.closest("button, a, pre") ? { x: event.touches[0].clientX, y: event.touches[0].clientY } : null;
}, { passive: true });
viewport.addEventListener("touchend", event => {
  if (!touchStart || !event.changedTouches.length) return;
  const dx = event.changedTouches[0].clientX - touchStart.x;
  const dy = event.changedTouches[0].clientY - touchStart.y;
  if (Math.abs(dx) > 55 && Math.abs(dx) > Math.abs(dy) * 1.5) showSlide(current + (dx < 0 ? 1 : -1));
  touchStart = null;
}, { passive: true });
viewport.addEventListener("touchcancel", () => { touchStart = null; }, { passive: true });
document.addEventListener("visibilitychange", syncPlayback);
reducedMotion.addEventListener("change", () => {
  if (!reducedMotion.matches) return;
  pauseDemo();
  renderDemo(lastDemoStep);
});
window.addEventListener("resize", fitDeck);
window.addEventListener("fullscreenchange", fitDeck);
window.addEventListener("hashchange", () => { if (indexFromHash() !== current) showSlide(indexFromHash(), { updateHash: false }); });
window.addEventListener("beforeprint", () => {
  printing = true;
  syncPlayback();
  printDownloads.forEach(({ link, href }) => { link.href = new URL(href, printSourceURL).href; });
  dialogs.forEach(dialog => { if (dialog.open) dialog.close(); });
  if (printedDemo === null) printedDemo = { step: demoStep, remaining: demoRemaining };
  renderDemo(lastDemoStep);
  slides.forEach(slide => slide.removeAttribute("aria-hidden"));
});
window.addEventListener("afterprint", () => {
  printDownloads.forEach(({ link, href }) => { link.setAttribute("href", href); });
  if (printedDemo !== null) {
    renderDemo(printedDemo.step);
    demoRemaining = printedDemo.remaining;
    printedDemo = null;
  }
  printing = false;
  showSlide(current, { updateHash: false });
  fitDeck();
});

renderDemo(reducedMotion.matches ? lastDemoStep : 1);
showSlide(indexFromHash(), { updateHash: false });
fitDeck();
