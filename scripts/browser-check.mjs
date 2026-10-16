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

async function saved(page, value) {
  await page.waitForFunction((expected) =>
    document.getElementById("text").value === expected &&
    document.getElementById("status").textContent === "Connected. Changes saved.", value);
}

async function posts(page) {
  return page.evaluate(() => window.writeBodies.slice());
}

async function paste(page, value) {
  await page.bringToFront();
  await page.evaluate((value) => navigator.clipboard.writeText(value), value);
  await page.locator("#text").focus();
  const modifier = process.platform === "darwin" ? "Meta" : "Control";
  await page.keyboard.press(`${modifier}+A`);
  await page.keyboard.press(`${modifier}+V`);
}

// Hold an already accepted POST reply while SSE and continued typing proceed.
async function holdReply(page) {
  let release;
  const gate = new Promise((resolve) => { release = resolve; });
  let accepted;
  const ready = new Promise((resolve) => { accepted = resolve; });
  let completed;
  const finished = new Promise((resolve) => { completed = resolve; });
  await page.route("**/text", async (route) => {
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
        if (args[0] === "/text") window.writeBodies.push(args[1].body);
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
  await Promise.all([saved(a, ""), saved(b, "")]);

  const pasted = "Pasted text\n🙂 العربية\n</textarea><script>throw Error('unsafe')</script>";
  await paste(a, pasted);
  await Promise.all([saved(a, pasted), saved(b, pasted)]);
  await b.bringToFront();
  await b.locator("#copy").click();
  await b.waitForFunction(() => document.getElementById("copy-status").textContent === "Copied.");
  assert.equal(await b.evaluate(() => navigator.clipboard.readText()), pasted);
  await a.evaluate(() => {
    window.nativeWriteText = navigator.clipboard.writeText;
    navigator.clipboard.writeText = () => Promise.reject(new DOMException("denied", "NotAllowedError"));
  });
  await a.locator("#copy").click();
  await a.waitForFunction(() => document.getElementById("copy-status").textContent.includes("Could not copy"));
  await a.evaluate(() => { navigator.clipboard.writeText = window.nativeWriteText; });
  console.log("PASS: copy button copies the complete synchronized text and reports clipboard failures");
  await paste(b, "Pasted from browser B\nمرحبا 🙂");
  await Promise.all([saved(a, "Pasted from browser B\nمرحبا 🙂"), saved(b, "Pasted from browser B\nمرحبا 🙂")]);
  await b.locator("#text").fill("Edited from browser B");
  await Promise.all([saved(a, "Edited from browser B"), saved(b, "Edited from browser B")]);
  await b.locator("#text").press("End");
  await b.keyboard.type("!");
  await Promise.all([saved(a, "Edited from browser B!"), saved(b, "Edited from browser B!")]);
  console.log("PASS: editing and multiline Unicode pastes synchronize in both directions");

  const before = (await posts(a)).length;
  const overlap = await holdReply(a);
  await a.locator("#text").fill("first pending write");
  await overlap.ready;
  await saved(b, "first pending write");
  await a.locator("#text").fill("intermediate draft");
  await a.locator("#text").fill("latest local draft");
  await b.locator("#text").fill("overlapping remote edit");
  await saved(b, "overlapping remote edit");
  assert.equal(await a.locator("#text").inputValue(), "latest local draft");
  assert.equal((await posts(a)).length, before + 1, "writes must remain serialized");
  overlap.release();
  await overlap.finished;
  await Promise.all([saved(a, "latest local draft"), saved(b, "latest local draft")]);
  assert.deepEqual((await posts(a)).slice(before), ["first pending write", "latest local draft"]);
  console.log("PASS: sender echoes preserve typing; overlapping edits converge; drafts coalesce");

  const ordering = await holdReply(a);
  await a.locator("#text").fill("older accepted text");
  await ordering.ready;
  await saved(b, "older accepted text");
  await b.locator("#text").fill("newer server text");
  await saved(b, "newer server text");
  ordering.release();
  await ordering.finished;
  await Promise.all([saved(a, "newer server text"), saved(b, "newer server text")]);
  await a.evaluate(() => {
    window.testEvents.dispatchEvent(new MessageEvent("message", {
      data: JSON.stringify({ text: "stale snapshot", revision: 0 }),
    }));
  });
  await saved(a, "newer server text");
  const counts = await Promise.all([posts(a), posts(b)]);
  await new Promise((resolve) => setTimeout(resolve, 300));
  assert.deepEqual(await Promise.all([posts(a), posts(b)]), counts);
  console.log("PASS: delayed POST replies and older SSE snapshots cannot roll back state; no echo writes");

  await a.route("**/text", (route) => route.abort(), { times: 1 });
  await a.locator("#text").fill("draft after network failure");
  await a.waitForFunction(() => document.getElementById("status").textContent.includes("Could not confirm"));
  assert.equal(await a.locator("#text").inputValue(), "draft after network failure");
  await a.locator("#text").fill("retry after failure");
  await Promise.all([saved(a, "retry after failure"), saved(b, "retry after failure")]);

  await a.evaluate(() => {
    const text = document.getElementById("text");
    text.value = "🙂".repeat(10 * 1024 * 1024 / 4 + 1);
    text.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await a.waitForFunction(() => document.getElementById("status").textContent.includes("10 MiB limit"));
  await saved(b, "retry after failure");
  assert.equal(await a.locator("#text").evaluate((text) => new Blob([text.value]).size), 10 * 1024 * 1024 + 4);
  await a.locator("#text").fill("shortened after rejection");
  await Promise.all([saved(a, "shortened after rejection"), saved(b, "shortened after rejection")]);
  console.log("PASS: network failures and oversized UTF-8 writes show feedback, retain drafts, and recover");

  const reconnect = await holdReply(a);
  await a.locator("#text").fill("accepted before disconnect");
  await reconnect.ready;
  await saved(b, "accepted before disconnect");
  await a.locator("#text").fill("unsent old connection draft");
  const reconnectCount = (await posts(a)).length;
  await contexts[0].setOffline(true);
  for (const stream of streams) stream.destroy();
  await a.waitForFunction(() => document.getElementById("text").disabled);
  await b.locator("#text").fill("current state while disconnected");
  await saved(b, "current state while disconnected");
  await contexts[0].setOffline(false);
  await saved(a, "current state while disconnected");
  reconnect.release();
  await reconnect.finished;
  await saved(a, "current state while disconnected");
  assert.equal((await posts(a)).length, reconnectCount, "reconnect must not replay unsent edits");
  console.log("PASS: reconnect loads current state and discards unsent edits and old POST replies");

  await stopServer();
  await Promise.all([a, b].map((page) => page.waitForFunction(() => document.getElementById("text").disabled)));
  await startServer();
  await Promise.all([saved(a, ""), saved(b, "")]);
  await a.locator("#text").fill("write after revision reset");
  await Promise.all([saved(a, "write after revision reset"), saved(b, "write after revision reset")]);
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
