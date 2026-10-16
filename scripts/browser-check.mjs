// Run with a locally available Playwright install:
// node scripts/browser-check.mjs
// PASTEBIN_PLAYWRIGHT_MODULE may point to an existing Playwright package.
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createRequire } from "node:module";
import { createServer, request as proxyRequest } from "node:http";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PASTEBIN_PLAYWRIGHT_MODULE || "playwright");
const directory = mkdtempSync(join(tmpdir(), "pastebin-browser-"));
const binary = join(directory, "pastebin");
const address = "127.0.0.1:18083";
const url = `http://${address}`;
let server;
let browser;
let proxy;
const streams = new Set();
const errors = [];

// A controlled proxy lets the test actually sever an established SSE socket.
// Browser offline emulation alone can leave an existing stream connected.
async function startProxy() {
  proxy = createServer((request, response) => {
    const upstream = proxyRequest(url + request.url, {
      method: request.method,
      headers: request.headers,
    }, (incoming) => {
      response.writeHead(incoming.statusCode, incoming.headers);
      incoming.pipe(response);
      incoming.on("error", () => response.destroy());
    });
    upstream.on("error", () => response.destroy());
    request.pipe(upstream);
    response.on("close", () => {
      streams.delete(response);
      upstream.destroy();
    });
    if (request.url === "/events") streams.add(response);
  });
  await new Promise((resolve, reject) => {
    proxy.once("error", reject);
    proxy.listen(18084, "127.0.0.1", resolve);
  });
}

async function startServer() {
  server = spawn(binary, ["-addr", address], { stdio: ["ignore", "ignore", "pipe"] });
  let output = "";
  server.stderr.on("data", (data) => { output += data; });
  for (let attempt = 0; attempt < 100; attempt++) {
    if (server.exitCode !== null) throw new Error(output);
    try {
      if ((await fetch(url)).ok) return;
    } catch {}
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`Server did not start: ${output}`);
}

async function stopServer() {
  if (!server || server.exitCode !== null) return;
  const stopped = new Promise((resolve) => server.once("exit", resolve));
  server.kill("SIGTERM");
  await stopped;
}

const boxes = (page) => page.locator("#boxes .box");
const textarea = (page, index) => boxes(page).nth(index).locator("textarea");
const button = (page, index, name) => boxes(page).nth(index).locator(`.${name}`);

// Waits until the page shows exactly these box texts, in order, all saved.
async function saved(page, values) {
  await page.waitForFunction((expected) => {
    const sections = [...document.querySelectorAll("#boxes .box")];
    return document.getElementById("status").textContent === "Connected." &&
      sections.length === expected.length &&
      sections.every((section, index) =>
        section.querySelector("textarea").value === expected[index] &&
        section.querySelector(".status").textContent === "Saved.");
  }, values);
}

async function boxStatus(page, index, text) {
  await page.waitForFunction(([index, text]) =>
    document.querySelectorAll("#boxes .box")[index].querySelector(".status").textContent.includes(text),
  [index, text]);
}

async function writes(page) {
  return page.evaluate(() => window.writeBodies.slice());
}

async function paste(page, index, value) {
  await page.bringToFront();
  await page.evaluate((value) => navigator.clipboard.writeText(value), value);
  await textarea(page, index).focus();
  const modifier = process.platform === "darwin" ? "Meta" : "Control";
  await page.keyboard.press(`${modifier}+A`);
  await page.keyboard.press(`${modifier}+V`);
}

async function copy(page, index) {
  await page.bringToFront();
  await button(page, index, "copy").click();
  await page.waitForFunction((index) =>
    document.querySelectorAll("#boxes .box")[index].querySelector(".copy-status").textContent === "Copied.", index);
  return page.evaluate(() => navigator.clipboard.readText());
}

// Hold an already accepted PUT reply while SSE and continued typing proceed.
async function holdReply(page) {
  let release;
  const gate = new Promise((resolve) => { release = resolve; });
  let accepted;
  const ready = new Promise((resolve) => { accepted = resolve; });
  let completed;
  const finished = new Promise((resolve) => { completed = resolve; });
  await page.route("**/boxes/*", async (route) => {
    const response = await route.fetch();
    accepted();
    await gate;
    await route.fulfill({ response });
    completed();
  }, { times: 1 });
  return { ready, release, finished };
}

try {
  execFileSync("go", ["build", "-o", binary, "./cmd/pastebin"], { stdio: "inherit" });
  await startServer();
  await startProxy();
  browser = await chromium.launch({ headless: true });
  const contexts = await Promise.all([browser.newContext(), browser.newContext()]);
  for (const context of contexts) {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await context.addInitScript(() => {
      window.writeBodies = [];
      const nativeFetch = window.fetch;
      window.fetch = (...args) => {
        if (args[1]?.method === "PUT") window.writeBodies.push(args[1].body);
        return nativeFetch(...args);
      };
      const NativeEventSource = window.EventSource;
      window.EventSource = class extends NativeEventSource {
        constructor(...args) {
          super(...args);
          window.testEvents = this;
        }
      };
    });
  }
  const [a, b] = await Promise.all(contexts.map((context) => context.newPage()));
  for (const page of [a, b]) page.on("pageerror", (error) => errors.push(error.message));
  await Promise.all([a.goto("http://127.0.0.1:18084"), b.goto(url)]);
  await Promise.all([saved(a, [""]), saved(b, [""])]);
  assert.equal(await button(a, 0, "remove").isDisabled(), true, "the last box cannot be removed");

  const pasted = "Pasted text\n🙂 العربية\n</textarea><script>throw Error('unsafe')</script>";
  await paste(a, 0, pasted);
  await Promise.all([saved(a, [pasted]), saved(b, [pasted])]);
  await a.locator("#add").click();
  await Promise.all([saved(a, [pasted, ""]), saved(b, [pasted, ""])]);
  assert.equal(await button(b, 0, "remove").isDisabled(), false, "a second box enables Remove");
  await paste(b, 1, "Pasted from browser B\nمرحبا 🙂");
  await Promise.all([
    saved(a, [pasted, "Pasted from browser B\nمرحبا 🙂"]),
    saved(b, [pasted, "Pasted from browser B\nمرحبا 🙂"]),
  ]);
  await textarea(b, 1).fill("Edited from browser B");
  await Promise.all([saved(a, [pasted, "Edited from browser B"]), saved(b, [pasted, "Edited from browser B"])]);
  await textarea(b, 1).press("End");
  await b.keyboard.type("!");
  const other = "Edited from browser B!";
  await Promise.all([saved(a, [pasted, other]), saved(b, [pasted, other])]);
  console.log("PASS: an added box appears in both browsers; pastes and edits synchronize per box");

  assert.equal(await copy(b, 0), pasted);
  assert.equal(await copy(a, 1), other);
  await a.evaluate(() => {
    window.nativeWriteText = navigator.clipboard.writeText;
    navigator.clipboard.writeText = () => Promise.reject(new DOMException("denied", "NotAllowedError"));
  });
  await button(a, 0, "copy").click();
  await a.waitForFunction(() =>
    document.querySelector("#boxes .box .copy-status").textContent.includes("Could not copy"));
  await a.evaluate(() => { navigator.clipboard.writeText = window.nativeWriteText; });
  console.log("PASS: each copy button copies its own box's complete text and reports clipboard failures");

  const before = (await writes(a)).length;
  const overlap = await holdReply(a);
  await textarea(a, 0).fill("first pending write");
  await overlap.ready;
  await saved(b, ["first pending write", other]);
  await textarea(a, 0).fill("intermediate draft");
  await textarea(a, 0).fill("latest local draft");
  await textarea(b, 0).fill("overlapping remote edit");
  await saved(b, ["overlapping remote edit", other]);
  assert.equal(await textarea(a, 0).inputValue(), "latest local draft");
  assert.equal((await writes(a)).length, before + 1, "writes to one box must remain serialized");
  await textarea(a, 1).fill("other box saves while the first waits");
  await saved(b, ["overlapping remote edit", "other box saves while the first waits"]);
  overlap.release();
  await overlap.finished;
  const second = "other box saves while the first waits";
  await Promise.all([saved(a, ["latest local draft", second]), saved(b, ["latest local draft", second])]);
  assert.deepEqual((await writes(a)).slice(before), ["first pending write", second, "latest local draft"]);
  console.log("PASS: sender echoes preserve typing; overlapping edits converge; drafts coalesce; boxes queue separately");

  const ordering = await holdReply(a);
  await textarea(a, 0).fill("older accepted text");
  await ordering.ready;
  await saved(b, ["older accepted text", second]);
  await textarea(b, 0).fill("newer server text");
  await saved(b, ["newer server text", second]);
  ordering.release();
  await ordering.finished;
  await Promise.all([saved(a, ["newer server text", second]), saved(b, ["newer server text", second])]);
  await a.evaluate(() => {
    for (const message of [
      { type: "text", revision: 0, box: { id: 1, text: "stale text" } },
      { type: "boxes", revision: 0, ids: [1] },
    ]) {
      window.testEvents.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(message) }));
    }
  });
  await saved(a, ["newer server text", second]);
  const counts = await Promise.all([writes(a), writes(b)]);
  await new Promise((resolve) => setTimeout(resolve, 300));
  assert.deepEqual(await Promise.all([writes(a), writes(b)]), counts);
  console.log("PASS: delayed replies and older SSE events cannot roll back text or the box list; no echo writes");

  await a.route("**/boxes/*", (route) => route.abort(), { times: 1 });
  await textarea(a, 0).fill("draft after network failure");
  await boxStatus(a, 0, "Could not confirm");
  assert.equal(await textarea(a, 0).inputValue(), "draft after network failure");
  await textarea(a, 0).fill("retry after failure");
  await Promise.all([saved(a, ["retry after failure", second]), saved(b, ["retry after failure", second])]);

  await a.evaluate(() => {
    const text = document.querySelector("#boxes textarea");
    text.value = "🙂".repeat(10 * 1024 * 1024 / 4 + 1);
    text.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await boxStatus(a, 0, "10 MiB limit");
  await saved(b, ["retry after failure", second]);
  assert.equal(await textarea(a, 0).evaluate((text) => new Blob([text.value]).size), 10 * 1024 * 1024 + 4);
  const kept = "shortened after rejection";
  await textarea(a, 0).fill(kept);
  await Promise.all([saved(a, [kept, second]), saved(b, [kept, second])]);
  console.log("PASS: network failures and oversized UTF-8 writes show feedback, retain drafts, and recover");

  // Hold A's stream so its write reaches the server before A learns of the removal.
  await a.evaluate(() => {
    const deliver = window.testEvents.onmessage;
    const held = [];
    window.testEvents.onmessage = (event) => held.push(event);
    window.releaseEvents = () => {
      window.testEvents.onmessage = deliver;
      for (const event of held) deliver(event);
    };
  });
  await button(b, 1, "remove").click();
  await saved(b, [kept]);
  await textarea(a, 1).fill("draft for a removed box");
  await a.waitForFunction(() => document.getElementById("notice").textContent.includes("removed by someone else"));
  assert.equal(await boxes(a).count(), 1);
  await a.evaluate(() => window.releaseEvents());
  await Promise.all([saved(a, [kept]), saved(b, [kept])]);
  for (const page of [a, b]) {
    assert.equal(await button(page, 0, "remove").isDisabled(), true, "the last box cannot be removed");
  }
  console.log("PASS: removals synchronize; a write to a removed box drops it with a notice");

  for (let count = 1; count < 10; count++) await a.locator("#add").click();
  const full = [kept, ...Array(9).fill("")];
  await Promise.all([saved(a, full), saved(b, full)]);
  for (const page of [a, b]) {
    assert.equal(await page.locator("#add").isDisabled(), true, "Add is disabled at the limit");
    assert.match(await page.locator("#add-status").textContent(), /Box limit of 10 reached/);
  }
  await a.evaluate(() => {
    const add = document.getElementById("add");
    add.disabled = false;
    add.click();
  });
  await a.waitForFunction(() => document.getElementById("notice").textContent.includes("Box limit of 10 reached"));
  for (let index = 9; index >= 1; index--) await button(b, index, "remove").click();
  await Promise.all([saved(a, [kept]), saved(b, [kept])]);
  for (const page of [a, b]) {
    assert.equal(await page.locator("#add").isDisabled(), false, "Add is enabled below the limit");
    assert.equal(await page.locator("#add-status").textContent(), "");
  }
  console.log("PASS: Add is disabled at the 10-box limit with a message; the server rejects one more");

  const reconnect = await holdReply(a);
  await textarea(a, 0).fill("accepted before disconnect");
  await reconnect.ready;
  await saved(b, ["accepted before disconnect"]);
  await textarea(a, 0).fill("unsent old connection draft");
  const reconnectCount = (await writes(a)).length;
  await contexts[0].setOffline(true);
  for (const stream of streams) stream.destroy();
  await a.waitForFunction(() => document.querySelector("#boxes textarea").disabled);
  await textarea(b, 0).fill("current state while disconnected");
  await b.locator("#add").click();
  const current = ["current state while disconnected", ""];
  await saved(b, current);
  await contexts[0].setOffline(false);
  await saved(a, current);
  reconnect.release();
  await reconnect.finished;
  await saved(a, current);
  assert.equal((await writes(a)).length, reconnectCount, "reconnect must not replay unsent edits");
  console.log("PASS: reconnect loads current boxes and discards unsent edits and old PUT replies");

  await stopServer();
  await Promise.all([a, b].map((page) => page.waitForFunction(() => document.querySelector("#boxes textarea").disabled)));
  await startServer();
  await Promise.all([saved(a, [""]), saved(b, [""])]);
  await textarea(a, 0).fill("write after revision reset");
  await Promise.all([saved(a, ["write after revision reset"]), saved(b, ["write after revision reset"])]);
  assert.deepEqual(errors, [], "browser console must have no JavaScript errors");
  console.log("PASS: a restarted server establishes revision zero and both browsers resume writing");
} finally {
  if (browser) await browser.close();
  if (proxy) {
    proxy.closeAllConnections();
    await new Promise((resolve) => proxy.close(resolve));
  }
  await stopServer();
  rmSync(directory, { recursive: true, force: true });
}
