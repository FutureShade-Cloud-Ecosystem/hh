// Every test listener in the web apps names a loopback host, and this gate
// holds it.
//
// A stub that calls server.listen(port) with no host binds every interface:
// it answers on the LAN as well as on the loopback, and a finished run leaves
// that listener holding its port for the rest of the machine's day. The rule
// here is that every listener names 127.0.0.1 (or localhost), so the answer
// never leaves the host.
//
// The gate reads two things and fails naming each offender:
//   1. every .listen( call in every web/apps/*/test file, whatever extension
//      it carries: the host it names must be the loopback;
//   2. every command anywhere under web/ that starts vite: the command must
//      carry --host 127.0.0.1. That command lives in a Playwright config or
//      in a harness factory that writes one, and it can live in a package's
//      own config, not only in an app's, so the gate reads the whole tree.
//
// The allow list holds one entry, for one reason, and an entry that stops
// being needed fails the gate too, so it cannot rot into a wide hole.
import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { dirname, extname, join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

const webRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = join(webRoot, "..");

// The hosts a test listener may name. "localhost" resolves to the loopback.
const LOOPBACK_HOSTS = new Set(["127.0.0.1", "localhost"]);

// The extensions a source file can carry. A stub or a config can be written
// in any of them, and a gate that read only one would miss a listener in the
// others: web/apps/front-door/test/free-ports.ts already holds .listen calls.
const SOURCE_EXTENSIONS = new Set([".mjs", ".ts", ".js", ".cjs"]);

// The directories the tree installs or builds. They hold no source of ours,
// and reading them would only be slow.
const GENERATED = new Set([
  "node_modules",
  "dist",
  "build",
  "coverage",
  "playwright-report",
  "test-results",
  ".git",
  ".vite",
]);

// The allow list: one entry, and the reason it exists.
//
// The acceptance-real console double runs INSIDE the box's container, where
// the published port only reaches it when it binds every interface of that
// container's own network. box.sh publishes that container on 127.0.0.1 only,
// so the published port is never reachable off this host. A console double
// that binds only its own container's loopback would answer nothing at all.
const ALLOWED = new Map([
  [
    "web/apps/money-desk/test/acceptance-real/console-double.mjs",
    "it runs inside the box's container and box.sh publishes that container on 127.0.0.1 only",
  ],
]);

function repoPath(file) {
  return relative(repoRoot, file).split(sep).join("/");
}

function filesUnder(dir, out = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (GENERATED.has(entry.name)) continue;
    const full = join(dir, entry.name);
    if (entry.isDirectory()) filesUnder(full, out);
    else out.push(full);
  }
  return out;
}

// Every test file of every web app: the stubs, the probes and the harnesses
// all live under an app's own test directory.
function appTestFiles() {
  const out = [];
  const appsDir = join(webRoot, "apps");
  if (!existsSync(appsDir)) return out;
  for (const app of readdirSync(appsDir)) {
    const testDir = join(appsDir, app, "test");
    if (!existsSync(testDir)) continue;
    for (const file of filesUnder(testDir)) {
      if (SOURCE_EXTENSIONS.has(extname(file))) out.push(file);
    }
  }
  return out;
}

// Every source file under web/: the vite command lives in a Playwright
// configuration, but it can live in a harness factory that writes one (the
// pos kit's console harness does), and it can live in a package's own
// configuration, not only in an app's. The gate reads the whole tree, so a
// package's webServer is held too.
function webSourceFiles() {
  return filesUnder(webRoot).filter((file) => SOURCE_EXTENSIONS.has(extname(file)));
}

// The end of the string that starts at start, and the offset just past it. A
// string holds a quote, a backslash escape, or (for a template) an expression
// that can hold strings of its own.
function skipString(text, start) {
  const quote = text[start];
  let i = start + 1;
  while (i < text.length) {
    const ch = text[i];
    if (ch === "\\") {
      i += 2;
      continue;
    }
    if (quote === "`" && ch === "$" && text[i + 1] === "{") {
      i = skipTemplateExpression(text, i + 2);
      continue;
    }
    if (ch === quote) return i + 1;
    i += 1;
  }
  return text.length;
}

// The inside of a template's ${ }: it can hold strings and nested templates,
// so it is walked by the same rules.
function skipTemplateExpression(text, start) {
  let i = start;
  let depth = 1;
  while (i < text.length && depth > 0) {
    const ch = text[i];
    if (ch === "{") {
      depth += 1;
      i += 1;
      continue;
    }
    if (ch === "}") {
      depth -= 1;
      i += 1;
      continue;
    }
    if (ch === '"' || ch === "'" || ch === "`") {
      i = skipString(text, i);
      continue;
    }
    if (ch === "/" && text[i + 1] === "/") {
      const nl = text.indexOf("\n", i);
      i = nl === -1 ? text.length : nl;
      continue;
    }
    if (ch === "/" && text[i + 1] === "*") {
      const end = text.indexOf("*/", i + 2);
      i = end === -1 ? text.length : end + 2;
      continue;
    }
    i += 1;
  }
  return i;
}

// The scanner: one pass over a file's text, blanking comments so a word in a
// comment is never read as a call, and marking the string regions so a
// ".listen(" that lives inside a string is never read as a call either.
// Comments are blanked rather than cut, so every offset stays true to the
// file.
function stripComments(text) {
  let out = "";
  const inString = new Uint8Array(text.length);
  let i = 0;
  while (i < text.length) {
    const ch = text[i];
    if (ch === "/" && text[i + 1] === "/") {
      const nl = text.indexOf("\n", i);
      const end = nl === -1 ? text.length : nl;
      out += " ".repeat(end - i);
      i = end;
      continue;
    }
    if (ch === "/" && text[i + 1] === "*") {
      const end = text.indexOf("*/", i + 2);
      const stop = end === -1 ? text.length : end + 2;
      out += text.slice(i, stop).replace(/[^\n]/g, " ");
      i = stop;
      continue;
    }
    if (ch === '"' || ch === "'" || ch === "`") {
      const end = skipString(text, i);
      out += text.slice(i, end);
      for (let k = i; k < end; k += 1) inString[k] = 1;
      i = end;
      continue;
    }
    out += ch;
    i += 1;
  }
  return { text: out, inString };
}

// The index of the parenthesis that closes the one at open.
function matchParen(text, open) {
  let i = open + 1;
  let depth = 1;
  while (i < text.length) {
    const ch = text[i];
    if (ch === '"' || ch === "'" || ch === "`") {
      i = skipString(text, i);
      continue;
    }
    if (ch === "(") depth += 1;
    else if (ch === ")") {
      depth -= 1;
      if (depth === 0) return i;
    }
    i += 1;
  }
  return -1;
}

// The arguments of a call, split at the commas that sit outside every nest.
function splitArgs(text) {
  const parts = [];
  let current = "";
  let depth = 0;
  let i = 0;
  while (i < text.length) {
    const ch = text[i];
    if (ch === '"' || ch === "'" || ch === "`") {
      const end = skipString(text, i);
      current += text.slice(i, end);
      i = end;
      continue;
    }
    if (ch === "(" || ch === "[" || ch === "{") depth += 1;
    else if (ch === ")" || ch === "]" || ch === "}") depth -= 1;
    if (ch === "," && depth === 0) {
      parts.push(current.trim());
      current = "";
      i += 1;
      continue;
    }
    current += ch;
    i += 1;
  }
  if (current.trim() !== "") parts.push(current.trim());
  return parts;
}

// Every .listen( call in a file, with the text of its arguments. A call that
// lives inside a string is not a call, so it is skipped.
function listenCalls(text) {
  const calls = [];
  const source = stripComments(text);
  let i = 0;
  while (i < source.text.length) {
    if (source.text.startsWith(".listen(", i) && source.inString[i] === 0) {
      const open = i + ".listen".length;
      const close = matchParen(source.text, open);
      if (close !== -1) {
        calls.push({ at: i, args: source.text.slice(open + 1, close) });
        i = close + 1;
        continue;
      }
    }
    i += 1;
  }
  return calls;
}

// The host a call names, and why it names none when it names none. The
// options form (listen({ port, host })) carries the host in the object.
function hostOf(args) {
  if (args.length === 0) return { host: null, why: "it names no arguments at all" };
  if (args[0].startsWith("{")) {
    const found = /\bhost\s*:\s*(?:"([^"]*)"|'([^']*)')/.exec(args[0]);
    if (found === null) return { host: null, why: "its options object names no host" };
    return { host: found[1] ?? found[2] };
  }
  if (args.length < 2) return { host: null, why: "it names a port and no host" };
  const second = args[1];
  if (second.startsWith("(") || second.startsWith("async") || second.includes("=>")) {
    return { host: null, why: "it names a port and a callback, and no host" };
  }
  const literal = /^"([^"]*)"$/.exec(second) ?? /^'([^']*)'$/.exec(second);
  if (literal === null) return { host: null, why: `its host is ${second}, and not a loopback literal` };
  return { host: literal[1] };
}

function lineOf(text, index) {
  let line = 1;
  for (let i = 0; i < index && i < text.length; i += 1) {
    if (text[i] === "\n") line += 1;
  }
  return line;
}

// Every command string a configuration holds.
function commandsOf(text) {
  const source = stripComments(text).text;
  const out = [];
  const pattern = /command:\s*(`[^`]*`|"[^"\n]*"|'[^'\n]*')/g;
  let found = pattern.exec(source);
  while (found !== null) {
    out.push({ at: found.index, command: found[1].slice(1, -1) });
    found = pattern.exec(source);
  }
  return out;
}

test("every test listener in the web apps names a loopback host", () => {
  const offenders = [];
  const stale = [];
  const used = new Set();
  for (const file of appTestFiles()) {
    const rel = repoPath(file);
    const text = readFileSync(file, "utf8");
    const allowed = ALLOWED.get(rel);
    for (const call of listenCalls(text)) {
      const line = lineOf(text, call.at);
      const { host, why } = hostOf(splitArgs(call.args));
      const loopback = host !== null && LOOPBACK_HOSTS.has(host);
      if (loopback) continue;
      if (allowed !== undefined) {
        used.add(rel);
        continue;
      }
      offenders.push(`${rel}:${line} ${why}`);
    }
  }
  for (const [rel, reason] of ALLOWED) {
    if (!used.has(rel)) stale.push(`${rel} is on the allow list but names a loopback host now, so the entry is stale (${reason})`);
  }
  assert.deepEqual(
    offenders,
    [],
    `every test listener must name 127.0.0.1 or localhost as its host:\n${offenders.join("\n")}`,
  );
  assert.deepEqual(stale, [], `the allow list must hold only the listeners that still need it:\n${stale.join("\n")}`);
});

test("every vite command under web names the loopback host", () => {
  const offenders = [];
  for (const file of webSourceFiles()) {
    const rel = repoPath(file);
    const text = readFileSync(file, "utf8");
    for (const entry of commandsOf(text)) {
      if (!/\bvite\b/.test(entry.command)) continue;
      if (/--host\s+127\.0\.0\.1(\s|$)/.test(entry.command)) continue;
      offenders.push(`${rel}:${lineOf(text, entry.at)} ${entry.command}`);
    }
  }
  assert.deepEqual(
    offenders,
    [],
    `every vite command must carry --host 127.0.0.1:\n${offenders.join("\n")}`,
  );
});
