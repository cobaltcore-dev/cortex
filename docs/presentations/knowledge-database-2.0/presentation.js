// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

"use strict";
document.documentElement.classList.add("js");

const slides = [...document.querySelectorAll(".slide")];
const viewport = document.getElementById("viewport");
const overview = document.getElementById("overview-dialog");
const notes = document.getElementById("notes-dialog");
const help = document.getElementById("help-dialog");
const dialogs = [overview, notes, help];
const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
const downloads = [...document.querySelectorAll("a[download]")].map(link => ({ link, href: link.getAttribute("href") }));
const publicSource = "https://github.com/cobaltcore-dev/cortex/blob/knowledge-db-2.0/docs/presentations/knowledge-database-2.0/";
const comparisons = [...document.querySelectorAll(".comparison")].map(root => ({ root, slide: root.closest(".slide"), view: "after" }));
const scenes = comparisons.filter(item => item.root.dataset.phase).map(item => ({
  ...item,
  kind: item.root.id === "knowledge-comparison" ? "knowledge" : "pipeline",
  durations: item.root.id === "knowledge-comparison" ? [900, 2200, 2400, 4800] : [0, 1300, 900, 0],
  phase: reducedMotion.matches ? 3 : 1,
  wantsPlayback: !reducedMotion.matches,
  playing: false,
  remaining: item.root.id === "knowledge-comparison" ? 2200 : 1300,
  deadline: 0,
  timer: null
}));
let current = -1;
let printing = false;
let printSnapshot = null;
let toastTimer;

function fitDeck() {
  document.documentElement.style.setProperty("--deck-scale", String(Math.min(viewport.clientWidth / 1440, viewport.clientHeight / 810)));
}

function activeScene() {
  return scenes.find(scene => scene.slide === slides[current]);
}

function sceneFor(item) {
  return scenes.find(scene => scene.root === item.root);
}

function renderCandidates() {
  const scene = scenes.find(item => item.kind === "pipeline");
  const threshold = Number(document.getElementById("threshold").value);
  const values = [80, 8, 42];
  let kept = 0;
  scene.root.querySelectorAll(".candidate").forEach(row => {
    const keep = values[Number(row.dataset.host)] < threshold;
    if (keep) kept++;
    row.classList.toggle("rejected", !keep);
    const verdict = row.querySelector(".host-verdict");
    verdict.firstChild.textContent = `${keep} `;
    verdict.querySelector("b").textContent = keep ? "✓" : "×";
    verdict.setAttribute("aria-label", keep ? "host is kept" : "host is removed");
  });
  document.getElementById("threshold-code").textContent = String(threshold);
  document.getElementById("threshold-label").textContent = `${threshold}%`;
  document.getElementById("candidate-count").textContent = scene.root.dataset.view === "before" ? "Compiled steps" : scene.phase === 1 ? "Evaluating…" : `${kept} / 3 eligible`;
}

function renderPhase(scene, phase) {
  scene.phase = Math.max(0, Math.min(3, phase));
  scene.remaining = scene.durations[scene.phase];
  scene.root.dataset.phase = String(scene.phase);
  if (scene.kind === "knowledge") {
    const actions = ["next\nextraction", "resolve\ninputs", "evaluate\nexpression", "publish\nfeatures"];
    document.getElementById("knowledge-action").textContent = actions[scene.phase];
    scene.slide.querySelectorAll("[data-phase-label]").forEach(label => label.classList.toggle("current", Number(label.dataset.phaseLabel) === scene.phase));
    scene.root.querySelector(".ready-badge").textContent = scene.phase === 3 ? "✓ Ready" : "◌ Extracting";
  } else {
    renderCandidates();
  }
}

function pauseSceneTimer(scene) {
  clearTimeout(scene.timer);
  if (scene.playing) scene.remaining = Math.max(0, scene.deadline - Date.now());
  scene.playing = false;
}

function syncScenes() {
  const obscured = printing || document.hidden || dialogs.some(dialog => dialog.open);
  document.documentElement.classList.toggle("motion-suspended", obscured);
  scenes.forEach(scene => {
    const visible = scene.slide === slides[current] && scene.root.dataset.view === "after" && !obscured;
    if (!visible || !scene.wantsPlayback) {
      pauseSceneTimer(scene);
    } else if (!scene.playing) {
      scene.playing = true;
      scene.deadline = Date.now() + scene.remaining;
      scene.timer = setTimeout(() => {
        scene.playing = false;
        if (scene.kind === "pipeline" && scene.phase === 2) {
          renderPhase(scene, 3);
          scene.wantsPlayback = false;
        } else {
          renderPhase(scene, scene.phase === 3 ? 0 : scene.phase + 1);
        }
        syncScenes();
      }, scene.remaining);
    }
    scene.root.dataset.playing = String(scene.playing);
    scene.root.dataset.userPaused = String(!scene.wantsPlayback);
    const play = scene.slide.querySelector(".scene-play");
    if (play) {
      play.textContent = scene.playing ? "Ⅱ Pause" : "▶ Play";
      play.setAttribute("aria-pressed", String(scene.playing));
      play.setAttribute("aria-label", `${scene.playing ? "Pause" : "Play"} knowledge animation`);
    }
  });
}

function restartScene(scene, phase = 1) {
  if (!scene || scene.root.dataset.view !== "after") return;
  pauseSceneTimer(scene);
  scene.root.dataset.playing = "false";
  renderPhase(scene, reducedMotion.matches ? 3 : phase);
  // Reset one-shot visual animations when the rule changes during an evaluation.
  void scene.root.offsetWidth;
  scene.wantsPlayback = !reducedMotion.matches;
  syncScenes();
}

function animateCartridge(item, previousView) {
  const bay = item.root.querySelector(".cartridge-bay");
  if (!bay) return;
  bay.querySelectorAll(".cartridge-ejected").forEach(ghost => ghost.remove());
  item.root.classList.remove("cartridge-swapping");
  if (printing || reducedMotion.matches || previousView === item.view) return;
  const ghost = bay.querySelector(`[data-only="${previousView}"]`).cloneNode(true);
  ghost.removeAttribute("data-only");
  ghost.hidden = false;
  ghost.setAttribute("aria-hidden", "true");
  ghost.classList.add("cartridge-ejected");
  ghost.addEventListener("animationend", () => ghost.remove(), { once: true });
  bay.append(ghost);
  void bay.offsetWidth;
  item.root.classList.add("cartridge-swapping");
}

function setComparison(item, view, { animate = true } = {}) {
  const previousView = item.view;
  item.view = view;
  item.root.dataset.view = view;
  item.slide.querySelectorAll("[data-only]").forEach(element => { element.hidden = element.dataset.only !== view; });
  item.slide.querySelectorAll("[data-view-button]").forEach(button => button.setAttribute("aria-pressed", String(button.dataset.viewButton === view)));
  const scene = sceneFor(item);
  if (animate) animateCartridge(item, previousView);
  if (scene && view === "after" && animate) restartScene(scene);
  renderCandidates();
  syncScenes();
}

function updateNotes() {
  document.getElementById("notes-title").textContent = slides[current].dataset.title;
  document.getElementById("notes-position").textContent = `SLIDE ${current + 1} / ${slides.length}`;
  document.getElementById("notes-content").innerHTML = slides[current].querySelector(".speaker-notes").innerHTML;
  document.getElementById("notes-previous").disabled = current === 0;
  document.getElementById("notes-next").disabled = current === slides.length - 1;
}

function showSlide(index, { updateHash = true, focus = false } = {}) {
  current = Math.max(0, Math.min(slides.length - 1, index));
  slides.forEach((slide, i) => {
    slide.hidden = i !== current;
    slide.classList.toggle("active", i === current);
    slide.setAttribute("aria-hidden", String(i !== current));
  });
  document.getElementById("slide-counter").textContent = `${String(current + 1).padStart(2, "0")} / ${String(slides.length).padStart(2, "0")}`;
  document.getElementById("previous-button").disabled = current === 0;
  document.getElementById("next-button").disabled = current === slides.length - 1;
  document.getElementById("progress-bar").style.width = `${(current + 1) / slides.length * 100}%`;
  document.getElementById("announcement").textContent = `Slide ${current + 1}: ${slides[current].dataset.title}`;
  document.querySelector(".skip-link").href = `#${slides[current].id}`;
  document.title = `${current + 1}. ${slides[current].dataset.title} · Cortex Knowledge 2.0`;
  document.querySelectorAll(".overview-item").forEach((button, i) => button.setAttribute("aria-current", String(i === current)));
  if (updateHash && window.location.hash !== `#${slides[current].id}`) window.location.hash = slides[current].id;
  updateNotes();
  syncScenes();
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
  syncScenes();
  if (dialog === overview) document.querySelector(`.overview-item[data-index="${current}"]`).focus();
}

function toast(text) {
  const element = document.getElementById("toast");
  clearTimeout(toastTimer);
  element.textContent = text;
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

slides.forEach((slide, i) => {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "overview-item";
  button.dataset.index = String(i);
  const number = document.createElement("span");
  number.textContent = String(i + 1).padStart(2, "0");
  const title = document.createElement("strong");
  title.textContent = slide.dataset.title;
  button.append(number, title);
  button.addEventListener("click", () => { overview.close(); showSlide(i, { focus: true }); });
  document.getElementById("overview-grid").append(button);
});

comparisons.forEach(item => {
  item.slide.querySelectorAll("[data-view-button]").forEach(button => button.addEventListener("click", () => setComparison(item, button.dataset.viewButton)));
});
scenes.forEach(scene => {
  scene.slide.querySelector(".scene-replay").addEventListener("click", () => restartScene(scene));
  scene.slide.querySelector(".scene-play")?.addEventListener("click", () => {
    scene.wantsPlayback = !scene.playing;
    syncScenes();
  });
});
document.getElementById("threshold").addEventListener("input", () => {
  renderCandidates();
  restartScene(scenes.find(scene => scene.kind === "pipeline"), 2);
});
document.getElementById("previous-button").addEventListener("click", () => showSlide(current - 1));
document.getElementById("next-button").addEventListener("click", () => showSlide(current + 1));
document.getElementById("notes-previous").addEventListener("click", () => showSlide(current - 1));
document.getElementById("notes-next").addEventListener("click", () => showSlide(current + 1));
document.getElementById("overview-button").addEventListener("click", () => toggleDialog(overview));
document.getElementById("notes-button").addEventListener("click", () => toggleDialog(notes));
document.getElementById("help-button").addEventListener("click", () => toggleDialog(help));
document.getElementById("fullscreen-button").addEventListener("click", toggleFullscreen);
document.getElementById("print-button").addEventListener("click", () => { help.close(); window.print(); });
document.querySelectorAll(".close-dialog").forEach(button => button.addEventListener("click", () => button.closest("dialog").close()));
dialogs.forEach(dialog => {
  dialog.addEventListener("close", syncScenes);
  dialog.addEventListener("click", event => {
    if (event.target !== dialog) return;
    const bounds = dialog.getBoundingClientRect();
    if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) dialog.close();
  });
});

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
  if (!notes.open) {
    const comparison = comparisons.find(item => item.slide === slides[current]);
    const scene = activeScene();
    if (comparison && (key === "b" || key === "a")) { event.preventDefault(); setComparison(comparison, key === "b" ? "before" : "after"); return; }
    if (scene && key === "r") { event.preventDefault(); restartScene(scene); return; }
    if (scene?.kind === "knowledge" && key === "p") { event.preventDefault(); scene.wantsPlayback = !scene.playing; syncScenes(); return; }
  }
  if (key === " " && event.target.closest("button, a")) return;
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
  touchStart = event.touches.length === 1 && !event.target.closest("button, a, input, pre") ? { x: event.touches[0].clientX, y: event.touches[0].clientY } : null;
}, { passive: true });
viewport.addEventListener("touchend", event => {
  if (!touchStart || !event.changedTouches.length) return;
  const dx = event.changedTouches[0].clientX - touchStart.x;
  const dy = event.changedTouches[0].clientY - touchStart.y;
  if (Math.abs(dx) > 55 && Math.abs(dx) > Math.abs(dy) * 1.5) showSlide(current + (dx < 0 ? 1 : -1));
  touchStart = null;
}, { passive: true });
viewport.addEventListener("touchcancel", () => { touchStart = null; }, { passive: true });
document.addEventListener("visibilitychange", syncScenes);
window.addEventListener("resize", fitDeck);
window.addEventListener("fullscreenchange", fitDeck);
window.addEventListener("hashchange", () => { if (indexFromHash() !== current) showSlide(indexFromHash(), { updateHash: false }); });
reducedMotion.addEventListener("change", () => {
  if (!reducedMotion.matches) return;
  document.querySelectorAll(".cartridge-ejected").forEach(ghost => ghost.remove());
  scenes.forEach(scene => { pauseSceneTimer(scene); scene.wantsPlayback = false; renderPhase(scene, 3); });
  syncScenes();
});
window.addEventListener("beforeprint", () => {
  printing = true;
  syncScenes();
  if (!printSnapshot) printSnapshot = comparisons.map(item => {
    const scene = sceneFor(item);
    return { item, view: item.view, phase: scene?.phase, remaining: scene?.remaining, wantsPlayback: scene?.wantsPlayback };
  });
  dialogs.forEach(dialog => { if (dialog.open) dialog.close(); });
  comparisons.forEach(item => {
    setComparison(item, "after", { animate: false });
    const scene = sceneFor(item);
    if (scene) renderPhase(scene, 3);
  });
  downloads.forEach(({ link, href }) => { link.href = new URL(href, publicSource).href; });
  slides.forEach(slide => slide.removeAttribute("aria-hidden"));
});
window.addEventListener("afterprint", () => {
  downloads.forEach(({ link, href }) => link.setAttribute("href", href));
  printSnapshot?.forEach(snapshot => {
    setComparison(snapshot.item, snapshot.view, { animate: false });
    const scene = sceneFor(snapshot.item);
    if (scene) {
      renderPhase(scene, snapshot.phase);
      scene.remaining = snapshot.remaining;
      scene.wantsPlayback = snapshot.wantsPlayback;
    }
  });
  printSnapshot = null;
  printing = false;
  showSlide(current, { updateHash: false });
  fitDeck();
});

scenes.forEach(scene => renderPhase(scene, scene.phase));
showSlide(indexFromHash(), { updateHash: false });
fitDeck();
