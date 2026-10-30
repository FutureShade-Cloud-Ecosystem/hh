// Every listener and every vite command in the web tree names the loopback,
// and this gate holds it.
//
// A stub that calls server.listen(port) with no host binds every interface:
// it answers on the LAN as well as on the loopback, and a finished run leaves
// that listener holding its port for the rest of the machine's day. The rule
// here is that every listener names 127.0.0.1 (or localhost, or ::1), so the
// answer never leaves the host.
//
// The gate reads two things and fails naming each offender:
//   1. every listen call in every source file under web/, whatever extension
//      it carries: the host it names must be the loopback. The reach is the
//      whole tree and not only an app's own test directory, because the pro
//      kit's stub, the api client's transport tests and the design system's
//      harness all start a server, and a wildcard bind in any of them must be
//      named. A host held in a constant the file itself declares is read as
//      that constant, so a file that declares the loopback and calls it by
//      name passes;
//   2. every command anywhere under web/ that starts vite: the command must
//      carry --host 127.0.0.1. That command lives in a Playwright config or
//      in a harness factory that writes one, and it can live in a package's
//      own config, not only in an app's, so the gate reads the whole tree.
//      The command can be written as a string, as a template, as an array of
//      words, or held in a name the file declares, and the key can be
//      quoted: the gate reads every one of those shapes, and a shape it
//      cannot read is named as unreadable rather than passed in silence.
//
// The allow list holds one entry, for one reason, and an entry that stops
// being needed fails the gate too, so it cannot rot into a wide hole.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { dirname, extname, join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

const webRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = join(webRoot, "..");

// The hosts a test listener may name. "localhost" resolves to the loopback,
// and ::1 is the IPv6 loopback: the same machine.
const LOOPBACK_HOSTS = new Set(["127.0.0.1", "localhost", "::1"]);

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

// The type keywords a TypeScript annotation can use. A command key in a
// type position names no command, so it is not read as one.
const TYPE_KEYWORDS = new Set([
  "string",
  "number",
  "boolean",
  "bigint",
  "symbol",
  "object",
  "unknown",
  "any",
  "never",
  "void",
  "undefined",
  "null",
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

// Every source file under web/: the vite command lives in a Playwright
// configuration, but it can live in a harness factory that writes one (the
// pos kit's console harness does), and it can live in a package's own
// configuration, not only in an app's. The gate reads the whole tree, so a
// package's webServer is held too.
function webSourceFiles() {
  return filesUnder(webRoot).filter((file) => SOURCE_EXTENSIONS.has(extname(file)));
}

// The files the listener leg reads. The gate holds every listener in the
// tree and not only the ones under an app's own test directory: a wildcard
// bind in a package's stub or in a package's own test must be named too.
function listenerFiles() {
  return webSourceFiles();
}

// The end of a string that starts at start, and the offset just past it. A
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

// The end of a regular expression that starts at start, and the offset just
// past it. A character class can hold a slash, and a backslash escapes.
function skipRegex(text, start) {
  let i = start + 1;
  let inClass = false;
  while (i < text.length) {
    const ch = text[i];
    if (ch === "\\") {
      i += 2;
      continue;
    }
    if (ch === "[") {
      inClass = true;
      i += 1;
      continue;
    }
    if (ch === "]") {
      inClass = false;
      i += 1;
      continue;
    }
    if (ch === "/" && !inClass) {
      i += 1;
      while (i < text.length && /[A-Za-z]/.test(text[i])) i += 1;
      return i;
    }
    if (ch === "\n") return i;
    i += 1;
  }
  return text.length;
}

// Whether a slash starts a regular expression and not a division. The token
// before it decides: after an operator, an opening bracket or a comma a
// slash starts a regular expression, and after a name, a number or a closing
// bracket it divides.
function startsRegex(text, index) {
  let i = index - 1;
  while (i >= 0 && /\s/.test(text[i])) i -= 1;
  if (i < 0) return true;
  const ch = text[i];
  if ("(,=:[!&|?{};+-*%~^".includes(ch)) return true;
  const word = /([A-Za-z_$][A-Za-z0-9_$]*)$/.exec(text.slice(0, i + 1));
  if (word !== null) {
    return ["return", "typeof", "case", "in", "of", "do", "else", "yield", "await", "new", "delete", "void"].includes(
      word[1],
    );
  }
  return false;
}

// The offset just past the token that starts at start: a string, a template
// or a regular expression. A regular expression can hold a quote, so a
// scanner that only knows strings loses its place in a file that holds one.
function skipToken(text, start) {
  const ch = text[start];
  if (ch === '"' || ch === "'" || ch === "`") return skipString(text, start);
  if (ch === "/" && startsRegex(text, start)) return skipRegex(text, start);
  return start + 1;
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
      i = skipToken(text, i);
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
    if (ch === "/" && startsRegex(out, out.length)) {
      // A regular expression is not code the gate reads, and it can hold a
      // quote: a scanner that treats that quote as a string loses its place
      // in the file and reads nothing after it.
      const end = skipRegex(text, i);
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
      i = skipToken(text, i);
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
    if (ch === "/" && startsRegex(text, i)) {
      const end = skipRegex(text, i);
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

// Every listen call in a file, with the text of its arguments. A call that
// lives inside a string is not a call, so it is skipped. The paren does not
// have to sit next to the method name: a space or a newline between them is
// still a call, and the gate reads it as one.
function listenCalls(source) {
  const calls = [];
  let i = 0;
  while (i < source.text.length) {
    if (source.text.startsWith(".listen", i) && source.inString[i] === 0) {
      const after = i + ".listen".length;
      const next = source.text[after];
      const named = next !== undefined && /[A-Za-z0-9_$]/.test(next);
      if (!named) {
        let open = after;
        while (open < source.text.length && /\s/.test(source.text[open])) open += 1;
        if (source.text[open] === "(") {
          const close = matchParen(source.text, open);
          if (close !== -1) {
            calls.push({ at: i, args: source.text.slice(open + 1, close) });
            i = close + 1;
            continue;
          }
        }
      }
    }
    i += 1;
  }
  return calls;
}

// The host a call names, and why it names none when it names none. The
// options form (listen({ port, host })) carries the host in the object. A
// host held in a name is read through the resolver, which finds the
// constant the file declares.
function hostOf(args, resolve) {
  if (args.length === 0) return { host: null, why: "it names no arguments at all" };
  if (args[0].startsWith("{")) {
    const found = /\bhost\s*:\s*(?:"([^"]*)"|'([^']*)')/.exec(args[0]);
    if (found === null) return { host: null, why: "its options object names no host" };
    return hostWhy(found[1] ?? found[2]);
  }
  if (args.length < 2) return { host: null, why: "it names a port and no host" };
  const second = args[1];
  if (second.startsWith("(") || second.startsWith("async") || second.includes("=>")) {
    return { host: null, why: "it names a port and a callback, and no host" };
  }
  const literal = /^"([^"]*)"$/.exec(second) ?? /^'([^']*)'$/.exec(second);
  if (literal !== null) return hostWhy(literal[1]);
  const name = /^[A-Za-z_$][A-Za-z0-9_$]*$/.exec(second);
  if (name !== null) {
    const value = resolve(name[0]);
    if (value !== null) return hostWhy(value, `, held in ${name[0]}`);
    return { host: null, why: `its host is ${name[0]}, and the file does not declare it as a literal` };
  }
  return { host: null, why: `its host is ${second}, and not a loopback literal` };
}

// The host a call names, named in the message, so a reader of a red log
// learns what the listener bound without opening the file.
function hostWhy(host, held = "") {
  if (LOOPBACK_HOSTS.has(host)) return { host, why: `its host is ${host}${held}` };
  return { host, why: `its host is ${host}${held}, and not a loopback host` };
}

function lineOf(text, index) {
  let line = 1;
  for (let i = 0; i < index && i < text.length; i += 1) {
    if (text[i] === "\n") line += 1;
  }
  return line;
}

// The value of a constant the file holds, when the file holds one as a
// literal. A host held in a constant that is provably the loopback has to be
// read as that constant, or a file that declares the loopback and calls it
// by name would be named an offender.
function constantOf(source, name) {
  const pattern = new RegExp(`\\b(?:const|let|var)\\s+${name}\\s*(?::[^=]*)?=\\s*`, "g");
  const found = pattern.exec(source.text);
  if (found === null) return null;
  const expression = readExpression(source.text, found.index + found[0].length);
  const literal = literalText(expression);
  if (literal !== null) return literal;
  const words = arrayWords(expression);
  if (words !== null) return words.join(" ");
  return null;
}

// The listeners a file holds that do not name the loopback, each with the
// line it sits on and the reason it is named.
function listenersIn(text) {
  const source = stripComments(text);
  const out = [];
  for (const call of listenCalls(source)) {
    const { host, why } = hostOf(splitArgs(call.args), (name) => constantOf(source, name));
    if (host !== null && LOOPBACK_HOSTS.has(host)) continue;
    out.push({ line: lineOf(text, call.at), host, why });
  }
  return out;
}

// The expression that follows a key or an equals sign, up to the comma or
// the brace that ends the object it sits in.
function readExpression(text, start) {
  let i = start;
  let depth = 0;
  while (i < text.length) {
    const ch = text[i];
    if (ch === '"' || ch === "'" || ch === "`") {
      i = skipToken(text, i);
      continue;
    }
    if (ch === "(" || ch === "[" || ch === "{") {
      depth += 1;
      i += 1;
      continue;
    }
    if (ch === ")" || ch === "]" || ch === "}") {
      if (depth === 0) break;
      depth -= 1;
      i += 1;
      continue;
    }
    if (ch === "," && depth === 0) break;
    if (ch === ";" && depth === 0) break;
    i += 1;
  }
  return text.slice(start, i).trim();
}

// The text of a string or a template, and null when the expression is not
// one.
function literalText(expression) {
  const double = /^"([^"]*)"$/.exec(expression);
  if (double !== null) return double[1];
  const single = /^'([^']*)'$/.exec(expression);
  if (single !== null) return single[1];
  const template = /^`([^`]*)`$/.exec(expression);
  if (template !== null) return template[1];
  return null;
}

// The words of an array literal, and null when one of them is not a
// literal. Playwright's webServer command takes an array of words as well
// as one string, so the gate reads both.
function arrayWords(expression) {
  if (!expression.startsWith("[") || !expression.endsWith("]")) return null;
  const words = [];
  for (const part of splitArgs(expression.slice(1, -1))) {
    const literal = literalText(part);
    if (literal === null) return null;
    words.push(literal);
  }
  return words;
}

// Whether an expression names a type and not a value. A command key in a
// type position names no command, so it is not read as one.
function isTypeExpression(expression) {
  return TYPE_KEYWORDS.has(expression);
}

// The command texts the tree holds under a named property. A command held
// in an object's command property (a harness that takes its stub command
// from its caller) is read from the tree's own object of that shape, so the
// gate can prove what that harness runs.
function propertyCommands(texts) {
  const map = new Map();
  const pattern = /\b([A-Za-z_$][A-Za-z0-9_$]*)\s*:\s*\{[^{}]*?\bcommand\s*:\s*("[^"]*"|'[^']*'|`[^`]*`)/g;
  for (const text of texts.values()) {
    pattern.lastIndex = 0;
    let found = pattern.exec(text);
    while (found !== null) {
      const literal = literalText(found[2]);
      if (literal !== null) {
        if (!map.has(found[1])) map.set(found[1], new Set());
        map.get(found[1]).add(literal);
      }
      found = pattern.exec(text);
    }
  }
  return map;
}

// The command a member expression names, when the tree holds it.
function memberCommand(expression, properties) {
  const parts = expression.split(".").map((part) => part.trim());
  if (parts.length < 2) return null;
  const found = properties.get(parts[parts.length - 2]);
  if (found === undefined) return null;
  return [...found].join(" ");
}

// The command a key names, read in every shape the tree writes one in.
function commandValue(source, expression, properties) {
  if (expression === "") {
    return { kind: "unreadable", value: expression, reason: "the key names no value" };
  }
  if (isTypeExpression(expression)) return { kind: "type", value: expression };
  const literal = literalText(expression);
  if (literal !== null) return { kind: "command", command: literal, value: expression };
  const words = arrayWords(expression);
  if (words !== null) return { kind: "command", command: words.join(" "), value: expression };
  if (/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(expression)) {
    const constant = constantOf(source, expression);
    if (constant !== null) return { kind: "command", command: constant, value: expression };
    return { kind: "unreadable", value: expression, reason: "the file does not declare it as a literal" };
  }
  if (/^[A-Za-z_$][A-Za-z0-9_$]*(\s*\.\s*[A-Za-z_$][A-Za-z0-9_$]*)+$/.test(expression)) {
    const member = memberCommand(expression, properties);
    if (member !== null) return { kind: "command", command: member, value: expression };
    return { kind: "unreadable", value: expression, reason: "the tree does not hold the command it names" };
  }
  return { kind: "unreadable", value: expression, reason: "it is not a shape this gate reads" };
}

// Every command key in a file, with the offset its value starts at. The
// scan walks the text itself, so a key that lives inside a string is not a
// key: the words "command:" inside a string are text, not a configuration.
function commandKeys(source) {
  const out = [];
  let i = 0;
  while (i < source.text.length) {
    const ch = source.text[i];
    if (ch === '"' || ch === "'" || ch === "`") {
      const end = skipString(source.text, i);
      if (ch !== "`" && source.text.slice(i + 1, end - 1) === "command") {
        let after = end;
        while (after < source.text.length && /\s/.test(source.text[after])) after += 1;
        if (source.text[after] === ":") {
          out.push({ at: i, valueStart: after + 1 });
          i = after + 1;
          continue;
        }
      }
      i = end;
      continue;
    }
    if (ch === '/' && startsRegex(source.text, i)) {
      i = skipRegex(source.text, i);
      continue;
    }
    if (source.text.startsWith("command", i)) {
      const next = source.text[i + 7];
      if (next === undefined || !/[\w$]/.test(next)) {
        let after = i + 7;
        while (after < source.text.length && /\s/.test(source.text[after])) after += 1;
        if (source.text[after] === ":") {
          out.push({ at: i, valueStart: after + 1 });
          i = after + 1;
          continue;
        }
      }
    }
    i += 1;
  }
  return out;
}

// Every command a file holds, read from a source the caller already holds.
function commandsIn(source, properties = new Map()) {
  const out = [];
  for (const key of commandKeys(source)) {
    const expression = readExpression(source.text, key.valueStart);
    out.push({ at: key.at, ...commandValue(source, expression, properties) });
  }
  return out;
}

// Every command a file holds. The key can be quoted, and the value can be a
// string, a template, an array of words, or a name the file declares. A
// shape the gate cannot read is returned as unreadable rather than skipped,
// so a command the gate cannot see cannot pass it in silence.
function commandsOf(text, properties = new Map()) {
  return commandsIn(stripComments(text), properties);
}

// Whether a file starts a server. A file that starts a server has to say
// what it starts, so a command shape the gate cannot read in such a file is
// a hole the gate names rather than passes.
function startsServers(source) {
  return /\bwebServer\s*:/.test(source.text);
}

test("every test listener under web names a loopback host", () => {
  const offenders = [];
  const stale = [];
  const used = new Set();
  for (const file of listenerFiles()) {
    const rel = repoPath(file);
    const text = readFileSync(file, "utf8");
    const allowed = ALLOWED.get(rel);
    for (const offender of listenersIn(text)) {
      if (allowed !== undefined) {
        used.add(rel);
        continue;
      }
      offenders.push(`${rel}:${offender.line} ${offender.why}`);
    }
  }
  for (const [rel, reason] of ALLOWED) {
    if (!used.has(rel)) stale.push(`${rel} is on the allow list but names a loopback host now, so the entry is stale (${reason})`);
  }
  assert.deepEqual(
    offenders,
    [],
    `every test listener must name 127.0.0.1, localhost or ::1 as its host:\n${offenders.join("\n")}`,
  );
  assert.deepEqual(stale, [], `the allow list must hold only the listeners that still need it:\n${stale.join("\n")}`);
});

test("every vite command under web names the loopback host", () => {
  const offenders = [];
  const texts = new Map();
  for (const file of webSourceFiles()) texts.set(file, readFileSync(file, "utf8"));
  const properties = propertyCommands(texts);
  for (const [file, text] of texts) {
    const rel = repoPath(file);
    const source = stripComments(text);
    const entries = commandsIn(source, properties);
    const holdsVite = entries.some((entry) => entry.kind === "command" && /\bvite\b/.test(entry.command));
    const startsServer = startsServers(source);
    for (const entry of entries) {
      if (entry.kind === "type") continue;
      if (entry.kind === "unreadable") {
        // A shape the gate cannot read is a hole when the gate cannot show
        // that the command is not a vite command: the value names vite, or
        // the file runs vite, or the file starts a server at all. Then the
        // gate names the file rather than passing the shape in silence.
        if (!/vite/i.test(entry.value) && !holdsVite && !startsServer) continue;
        offenders.push(
          `${rel}:${lineOf(text, entry.at)} it names a command this gate cannot read (${entry.reason}), so the gate cannot prove it carries --host 127.0.0.1`,
        );
        continue;
      }
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

test("the gate catches a listener that sits outside an app's own test directory", () => {
  // The shape the read proved: a stub in a package that binds every
  // interface. The gate has to read the file at all, and it has to name the
  // listener it holds.
  const source = 'const server = createServer();\nserver.listen(4959, "0.0.0.0", () => {});\n';
  const offenders = listenersIn(source);
  assert.equal(offenders.length, 1, "the gate must name a listener that binds every interface");
  const read = typeof listenerFiles === "function" ? listenerFiles() : [];
  assert.ok(
    read.some((file) => file.includes("/packages/")),
    "the gate must read the sources outside an app's own test directory",
  );
});

test("the gate catches a listener whose call is not spelled .listen(", () => {
  // The shape the read proved: a newline between the method name and the
  // paren. It binds every interface at runtime, so the gate has to read it.
  const source = 'server.listen\n  (4959, () => console.log("up"));\n';
  const offenders = listenersIn(source);
  assert.equal(offenders.length, 1, "the gate must name a listener whose paren is not next to the method name");
  assert.match(String(offenders[0].why ?? ""), /no host/, "the gate must say the call names no host");
});

test("the gate reads a vite command held in a variable, an array and a quoted key", () => {
  const source = [
    "const viteCommand = `npx vite --port 3000 --strictPort`;",
    "const held = { command: viteCommand };",
    'const quoted = { "command": "npx vite --port 3001 --strictPort" };',
    'const words = { command: ["npx", "vite", "--port", "3002"] };',
  ].join("\n");
  const found = commandsOf(source).filter((entry) => /\bvite\b/.test(entry.command ?? ""));
  assert.equal(
    found.length,
    3,
    "the gate must read a vite command held in a variable, in an array and under a quoted key",
  );
});

test("the gate names the host a listener bound", () => {
  const source = 'server.listen(4959, "0.0.0.0", () => {});\n';
  const offenders = listenersIn(source);
  assert.equal(offenders.length, 1, "the gate must name a listener that binds every interface");
  assert.match(
    String(offenders[0].why ?? ""),
    /0\.0\.0\.0/,
    "the gate must name the host the listener bound, not undefined",
  );
});
