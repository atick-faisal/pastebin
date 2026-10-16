"use strict";

const text = document.getElementById("text");
const status = document.getElementById("status");
const copy = document.getElementById("copy");
const copyStatus = document.getElementById("copy-status");
const events = new EventSource("/events");
let generation = 0;
let latest = null;
let writing = null;
let pending = false;
let failure = "";

function remember(snapshot) {
  // POST replies and SSE echoes can arrive in either order.
  if (latest === null || snapshot.revision > latest.revision) {
    latest = snapshot;
  }
}

function render() {
  // Keep typing and rejected drafts visible. Assigning value does not fire input,
  // and skipping equal values also preserves the caret on a sender's echo.
  if (!writing && !pending && !failure && text.value !== latest.text) {
    text.value = latest.text;
  }
  status.textContent = failure || (writing || pending ? "Saving…" : "Connected. Changes saved.");
}

async function submit() {
  if (latest === null || writing || !pending) return;
  const currentGeneration = generation;
  const controller = new AbortController();
  writing = controller;
  pending = false;
  failure = "";
  const draft = text.value;
  render();
  const timeout = setTimeout(() => controller.abort(), 30000);
  try {
    const response = await fetch("/text", {
      method: "POST",
      headers: { "Content-Type": "text/plain; charset=utf-8" },
      body: draft,
      signal: controller.signal,
    });
    if (!response.ok) {
      throw new Error(response.status === 413
        ? "Text exceeds the 10 MiB limit. Your draft was not saved; shorten it to retry."
        : `Could not save (HTTP ${response.status}). Your draft is still here; edit it to retry.`);
    }
    const snapshot = await response.json();
    if (currentGeneration !== generation) return;
    remember(snapshot);
  } catch (error) {
    if (currentGeneration !== generation) return;
    failure = error instanceof TypeError || error.name === "AbortError"
      ? "Could not confirm the save. Your draft is still here; edit it to retry."
      : error.message;
  } finally {
    clearTimeout(timeout);
    // An old connection's completion must not touch a new connection's draft.
    if (currentGeneration === generation) {
      writing = null;
      if (pending) {
        submit();
      } else {
        render();
      }
    }
  }
}

text.addEventListener("input", () => {
  if (latest === null) return;
  // A single flag coalesces intermediate input into the latest visible draft.
  pending = true;
  submit();
});

copy.addEventListener("click", async () => {
  try {
    // navigator.clipboard is undefined outside secure contexts (plain HTTP on a LAN),
    // so that case throws here too and shares the failure message.
    await navigator.clipboard.writeText(text.value);
    copyStatus.textContent = "Copied.";
  } catch {
    copyStatus.textContent = "Could not copy. Select the text and copy it manually.";
  }
});

events.onmessage = (event) => {
  const snapshot = JSON.parse(event.data);
  if (latest === null) {
    // The first snapshot establishes a new baseline, even at revision zero.
    failure = "";
    text.value = snapshot.text;
    text.disabled = false;
  }
  remember(snapshot);
  render();
};

events.onerror = () => {
  generation++;
  if (writing) writing.abort();
  writing = null;
  pending = false;
  latest = null;
  failure = "";
  text.disabled = true;
  status.textContent = "Disconnected. Reconnecting… Unsent edits will be discarded when shared text reloads.";
};
