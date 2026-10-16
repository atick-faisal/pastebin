"use strict";

// Mirrors textstate.MaxBoxes on the server.
const MAX_BOXES = 10;
const LIMIT_MESSAGE = `Box limit of ${MAX_BOXES} reached. Remove a box to add another.`;
// Counts are recomputed once typing pauses, so a 10 MiB box is not rescanned per keystroke.
const COUNT_DELAY_MS = 150;
const REMOVED_MESSAGE = "A box you were editing was removed by someone else. Its unsaved text was lost.";

const status = document.getElementById("status");
const notice = document.getElementById("notice");
const container = document.getElementById("boxes");
const add = document.getElementById("add");
const addStatus = document.getElementById("add-status");
const template = document.getElementById("box-template");
const events = new EventSource("/events");
let generation = 0;
let connected = false;
// Box IDs in server order, keyed to their elements, server text and write queue.
let boxes = new Map();
// IDs this browser dropped after a 404. IDs are never reused within a server
// process, so an older box list arriving later must not bring one back.
let gone = new Set();
// -1 means nothing is known yet, so even a revision-zero snapshot applies.
let listRevision = -1;
let noticeTimer;

function showNotice(message) {
  notice.textContent = message;
  clearTimeout(noticeTimer);
  noticeTimer = setTimeout(() => { notice.textContent = ""; }, 5000);
}

// live reports whether a request's box still belongs to the current connection.
function live(box, requestGeneration) {
  return requestGeneration === generation && boxes.get(box.id) === box;
}

// countLines counts lines the way an editor does: an empty box has none, and
// a trailing newline starts another line. It avoids split() so a box with
// 100,000 lines does not allocate 100,000 strings.
function countLines(text) {
  if (text === "") return 0;
  let lines = 1;
  for (let at = text.indexOf("\n"); at !== -1; at = text.indexOf("\n", at + 1)) lines++;
  return lines;
}

function plural(count, word) {
  return `${count.toLocaleString("en-US")} ${word}${count === 1 ? "" : "s"}`;
}

// updateCounts shows lines and UTF-8 bytes, the unit of the server's 10 MiB limit.
function updateCounts(box) {
  const text = box.textarea.value;
  const bytes = new TextEncoder().encode(text).length;
  box.counts.textContent = `${plural(countLines(text), "line")} · ${plural(bytes, "byte")}`;
}

function scheduleCounts(box) {
  clearTimeout(box.countTimer);
  box.countTimer = setTimeout(() => updateCounts(box), COUNT_DELAY_MS);
}

function render(box) {
  // Keep typing and rejected drafts visible. Assigning value does not fire input,
  // and skipping equal values also preserves the caret on a sender's echo.
  if (!box.writing && !box.pending && !box.failure && box.textarea.value !== box.text) {
    box.textarea.value = box.text;
    scheduleCounts(box);
  }
  box.status.textContent = box.failure || (box.writing || box.pending ? "Saving…" : "Saved.");
}

function renderControls() {
  const full = boxes.size >= MAX_BOXES;
  add.disabled = !connected || full;
  addStatus.textContent = full ? LIMIT_MESSAGE : "";
  for (const box of boxes.values()) {
    box.textarea.disabled = !connected;
    // There is always at least one box (Q2).
    box.remove.disabled = !connected || boxes.size === 1 || box.removing;
  }
}

function createBox(id) {
  const section = template.content.firstElementChild.cloneNode(true);
  const box = {
    id,
    section,
    textarea: section.querySelector("textarea"),
    status: section.querySelector(".status"),
    counts: section.querySelector(".counts"),
    countTimer: 0,
    copyStatus: section.querySelector(".copy-status"),
    remove: section.querySelector(".remove"),
    text: "",
    revision: -1,
    writing: null,
    pending: false,
    failure: "",
    removing: false,
  };
  section.dataset.id = id;
  box.textarea.id = `box-${id}`;
  section.querySelector("label").htmlFor = box.textarea.id;
  section.querySelector("label").textContent = `Box ${id}`;
  section.querySelector(".copy").setAttribute("aria-label", `Copy box ${id}`);
  box.remove.setAttribute("aria-label", `Remove box ${id}`);

  box.textarea.addEventListener("input", () => {
    scheduleCounts(box);
    // A single flag coalesces intermediate input into the latest visible draft.
    box.pending = true;
    submit(box);
  });
  section.querySelector(".copy").addEventListener("click", () => copyBox(box));
  box.remove.addEventListener("click", () => removeBox(box));
  updateCounts(box);
  return box;
}

async function copyBox(box) {
  try {
    // navigator.clipboard is undefined outside secure contexts (plain HTTP on a LAN),
    // so that case throws here too and shares the failure message.
    await navigator.clipboard.writeText(box.textarea.value);
    box.copyStatus.textContent = "Copied.";
  } catch {
    box.copyStatus.textContent = "Could not copy. Select the text and copy it manually.";
  }
}

function drop(box) {
  // Someone else's removal wins, so an unsaved draft in that box is lost (Q4).
  if (!box.removing && (box.writing || box.pending || box.failure)) {
    showNotice(REMOVED_MESSAGE);
  }
  if (box.writing) box.writing.abort();
  clearTimeout(box.countTimer);
  boxes.delete(box.id);
  box.section.remove();
}

function applyList(ids, revision) {
  // Replies and SSE events can arrive in either order; a list is the whole order.
  if (revision <= listRevision) return;
  listRevision = revision;
  const kept = new Set(ids);
  for (const box of boxes.values()) {
    if (!kept.has(box.id)) drop(box);
  }
  // The server only appends boxes (Q1), so new ones always belong at the end.
  for (const id of ids) {
    if (boxes.has(id) || gone.has(id)) continue;
    const box = createBox(id);
    boxes.set(id, box);
    container.append(box.section);
    render(box);
  }
  renderControls();
}

function applyText(id, text, revision) {
  const box = boxes.get(id);
  if (!box || revision <= box.revision) return;
  box.text = text;
  box.revision = revision;
  render(box);
}

function applySnapshot(snapshot) {
  if (!connected) {
    // The first snapshot of a connection is a new baseline, even at revision
    // zero after a server restart, so nothing from before it is kept.
    connected = true;
    for (const box of boxes.values()) {
      clearTimeout(box.countTimer);
      box.section.remove();
    }
    boxes = new Map();
    gone = new Set();
    listRevision = -1;
    status.textContent = "Connected.";
  }
  applyList(snapshot.boxes.map((box) => box.id), snapshot.revision);
  for (const box of snapshot.boxes) applyText(box.id, box.text, snapshot.revision);
}

async function submit(box) {
  if (!connected || box.writing || !box.pending) return;
  const requestGeneration = generation;
  const controller = new AbortController();
  box.writing = controller;
  box.pending = false;
  box.failure = "";
  const draft = box.textarea.value;
  render(box);
  const timeout = setTimeout(() => controller.abort(), 30000);
  try {
    const response = await fetch(`/boxes/${box.id}`, {
      method: "PUT",
      headers: { "Content-Type": "text/plain; charset=utf-8" },
      body: draft,
      signal: controller.signal,
    });
    if (!live(box, requestGeneration)) return;
    if (response.status === 404) {
      gone.add(box.id);
      drop(box);
      renderControls();
      return;
    }
    if (!response.ok) {
      throw new Error(response.status === 413
        ? "Text exceeds the 10 MiB limit. Your draft was not saved; shorten it to retry."
        : `Could not save (HTTP ${response.status}). Your draft is still here; edit it to retry.`);
    }
    const reply = await response.json();
    if (!live(box, requestGeneration)) return;
    applyText(reply.box.id, reply.box.text, reply.revision);
  } catch (error) {
    if (!live(box, requestGeneration)) return;
    box.failure = error instanceof TypeError || error.name === "AbortError"
      ? "Could not confirm the save. Your draft is still here; edit it to retry."
      : error.message;
  } finally {
    clearTimeout(timeout);
    // An old connection's or a removed box's completion must not touch a live draft.
    if (live(box, requestGeneration)) {
      box.writing = null;
      if (box.pending) {
        submit(box);
      } else {
        render(box);
      }
    }
  }
}

// change sends an add or remove and applies the box list it replies with.
// It returns the failed response, or null when the list was applied or the
// connection changed while waiting.
async function change(path, method) {
  const requestGeneration = generation;
  const response = await fetch(path, { method });
  if (!response.ok) return response;
  const reply = await response.json();
  if (requestGeneration === generation) applyList(reply.ids, reply.revision);
  return null;
}

async function removeBox(box) {
  const requestGeneration = generation;
  box.removing = true;
  renderControls();
  try {
    const failed = await change(`/boxes/${box.id}`, "DELETE");
    if (!failed || !live(box, requestGeneration)) return;
    if (failed.status === 404) {
      // Someone else removed it first, which is the outcome this click wanted.
      gone.add(box.id);
      drop(box);
      return;
    }
    showNotice(failed.status === 409
      ? "The last box cannot be removed."
      : `Could not remove the box (HTTP ${failed.status}).`);
  } catch {
    if (live(box, requestGeneration)) showNotice("Could not confirm the removal. Try again.");
  } finally {
    if (live(box, requestGeneration)) box.removing = false;
    renderControls();
  }
}

async function addBox() {
  const requestGeneration = generation;
  try {
    const failed = await change("/boxes", "POST");
    if (!failed || requestGeneration !== generation) return;
    showNotice(failed.status === 409 ? LIMIT_MESSAGE : `Could not add a box (HTTP ${failed.status}).`);
  } catch {
    if (requestGeneration === generation) showNotice("Could not confirm the new box. Try again.");
  }
}

add.addEventListener("click", addBox);

// The box that holds keyboard focus, whether on its textarea or one of its buttons.
function focusedBox() {
  const section = document.activeElement?.closest(".box");
  return [...boxes.values()].find((box) => box.section === section);
}

// Each shortcut reuses its button's code and respects the button's disabled state,
// so a shortcut works exactly when the button would.
const shortcuts = {
  KeyN: () => { if (!add.disabled) addBox(); },
  KeyC: () => {
    const box = focusedBox();
    if (box) copyBox(box);
  },
  Backspace: () => {
    const box = focusedBox();
    if (box && !box.remove.disabled) removeBox(box);
  },
};

// Matches event.code, not event.key: Alt changes the typed character on macOS
// (Alt+N is a dead key, Alt+C types "ç"), but the physical key stays the same.
document.addEventListener("keydown", (event) => {
  // Ctrl+Alt is how Windows types AltGr characters, so leave every extra modifier alone.
  // Ignoring key repeat stops a held key from adding or removing boxes in bulk.
  if (!event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
  const digit = /^Digit([1-9])$/.exec(event.code);
  const run = digit
    ? () => [...boxes.values()][Number(digit[1]) - 1]?.textarea.focus()
    : shortcuts[event.code];
  if (!run) return;
  // Stop the browser typing the Alt character into the focused textarea.
  event.preventDefault();
  if (!event.repeat) run();
});

events.onmessage = (event) => {
  const message = JSON.parse(event.data);
  if (message.type === "snapshot") {
    applySnapshot(message);
  } else if (message.type === "boxes") {
    applyList(message.ids, message.revision);
  } else if (message.type === "text") {
    applyText(message.box.id, message.box.text, message.revision);
  }
};

events.onerror = () => {
  generation++;
  connected = false;
  for (const box of boxes.values()) {
    if (box.writing) box.writing.abort();
  }
  renderControls();
  status.textContent = "Disconnected. Reconnecting… Unsent edits will be discarded when shared text reloads.";
};
