// Every listener and every vite command in the web tree names the loopback,
// and this gate holds it.
//
// A stub that calls server.listen(port) with no host binds every interface:
// it answers on the LAN as well as on the loopback, and a finished run leaves
// that listener holding its port for the rest of the machine's day. The rule
// here is that every listener names 127.0.0.1 (or localhost, or ::1), so the
// answer never leaves the host.
//
// The gate reads every source file through the TypeScript compiler's parser,
// not a hand tokenizer: a regular expression, a template expression or a
// call that spans lines is a node in the tree and cannot hide a call. It
// reads three things and fails naming each offender:
//   1. every listen call in every source file under web/ that carries a
//      source extension (.mjs, .js, .cjs, .mts, .cts, .ts, .tsx, .jsx; the
//      .d.ts family holds no code and is not read): the host it names must
//      be the loopback, and every host key of an options object is judged. The reach is the
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
//   3. every package.json under web/: a script that runs vite with a --host
//      other than the loopback fails. A plain "vite" with no --host binds
//      "localhost" (vite's resolveHostname sets host = "localhost" when the
//      option is undefined or false, node_modules/vite/dist/node/chunks/node.js
//      line 2581 at vite 8.3.1), so it passes.
//
// The gate fails closed: a listen property that is not a direct call
// (.listen.apply, .listen.call, .listen.bind, ["listen"], a destructured
// listen, const l = server.listen), a host the gate cannot read, and a vite
// command it cannot read are each named as a shape the gate cannot prove.
//
// A file the parser reports diagnostics for fails the gate, naming the file
// and the first diagnostic: a tree the parser recovered badly from is not a
// tree the gate has read.
//
// The allow list holds one entry, for one reason, pinned to the one call it
// exists for, and an entry that stops being needed fails the gate too, so it
// cannot rot into a wide hole.
//
// The gate's stated limits, which it does not enforce and does not claim to:
//   - a computed key (server[name](), server["lis" + "ten"]()) and a listener
//     that is not a .listen call (a WebSocketServer, hono's serve, a dgram
//     socket);
//   - vite started programmatically (createServer, preview), a server.host in
//     a vite config, "npm run dev -- --host" (the script is read, the extra
//     argument is not), and a server other than vite in a webServer command.
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, extname, join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const webRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = join(webRoot, "..");

// The hosts a test listener may name. "localhost" resolves to the loopback,
// and ::1 is the IPv6 loopback: the same machine.
const LOOPBACK_HOSTS = new Set(["127.0.0.1", "localhost", "::1"]);

// The extensions a source file can carry. A stub or a config can be written
// in any of them, and a gate that read only one would miss a listener in the
// others: web/apps/front-door/test/free-ports.ts already holds .listen calls.
const SOURCE_EXTENSIONS = new Set([".mjs", ".mts", ".cts", ".ts", ".tsx", ".js", ".cjs", ".jsx"]);

// A declaration file (.d.ts, .d.mts, .d.cts) holds types and no code.
function isSourceFile(file) {
  return SOURCE_EXTENSIONS.has(extname(file)) && !/\.d\.[mc]?ts$/.test(file);
}

// Two kinds of directory are skipped. node_modules and .git are skipped at any
// depth: they hold no source of ours. The output directories a package's own
// tooling writes (dist, build, coverage and the like) are skipped only when
// they sit directly beside a package.json, which is where a build writes
// them. A directory of one of those names inside a test directory
// (test/build/s.mjs) is source and is read. I chose the package.json rule
// over reading git's tracked list because the gate then needs no git and
// reads a fresh checkout and a working tree alike.
const ALWAYS_SKIPPED = new Set(["node_modules", ".git"]);
const OUTPUT_DIRECTORIES = new Set(["dist", "build", "coverage", "playwright-report", "test-results", ".vite"]);

// The allow list: one entry, and the reason it exists.
//
// The acceptance-real console double runs INSIDE the box's container, where
// the published port only reaches it when it binds every interface of that
// container's own network. box.sh publishes that container on 127.0.0.1 only,
// so the published port is never reachable off this host. A console double
// that binds only its own container's loopback would answer nothing at all.
//
// The entry is pinned to that one call: the host it names and the first line
// of the call as written. Any other listener in the file, and a second copy
// of that call, is judged like every other.
const ALLOWED = new Map([
  [
    "web/apps/money-desk/test/acceptance-real/console-double.mjs",
    {
      host: "0.0.0.0",
      call: 'server.listen(port, "0.0.0.0", () => {',
      reason: "it runs inside the box's container and box.sh publishes that container on 127.0.0.1 only",
    },
  ],
]);

const CANNOT_PROVE = "the gate cannot prove";

function repoPath(file) {
  return relative(repoRoot, file).split(sep).join("/");
}

function filesUnder(dir, out = []) {
  const entries = readdirSync(dir, { withFileTypes: true });
  const isPackageRoot = entries.some((entry) => entry.name === "package.json" && !entry.isDirectory());
  for (const entry of entries) {
    if (ALWAYS_SKIPPED.has(entry.name)) continue;
    if (isPackageRoot && entry.isDirectory() && OUTPUT_DIRECTORIES.has(entry.name)) continue;
    const full = join(dir, entry.name);
    if (entry.isDirectory()) filesUnder(full, out);
    else out.push(full);
  }
  return out;
}

// Every source file under web/: a stub, a Playwright configuration or a
// harness factory can be written in any of them, in an app or in a package.
function webSourceFilesUnder(root) {
  return filesUnder(root).filter(isSourceFile);
}

function webSourceFiles() {
  return webSourceFilesUnder(webRoot);
}

// Every package.json under web/, outside the generated directories.
function packageFilesUnder(root) {
  return filesUnder(root).filter((file) => file.endsWith(`${sep}package.json`));
}

function packageFiles() {
  return packageFilesUnder(webRoot);
}

// The parse diagnostics of one file, each with its line and message.
function parseProblemsIn(text, name) {
  const sf = parse(text, name);
  return sf.parseDiagnostics.map((diagnostic) => ({
    line: sf.getLineAndCharacterOfPosition(diagnostic.start ?? 0).line + 1,
    message: ts.flattenDiagnosticMessageText(diagnostic.messageText, " "),
  }));
}

// The listeners one file holds that the gate names, after the allow list.
function listenerOffendersIn(rel, text, file = rel) {
  const allowed = ALLOWED.get(rel);
  const offenders = [];
  let used = false;
  for (const offender of listenersIn(text, file)) {
    if (allowed !== undefined && !used && offender.host === allowed.host && offender.call === allowed.call) {
      used = true;
      continue;
    }
    offenders.push(offender);
  }
  return { offenders, used };
}

function parse(text, name) {
  const kind = name.endsWith(".tsx")
    ? ts.ScriptKind.TSX
    : /\.[mc]?ts$/.test(name)
      ? ts.ScriptKind.TS
      : ts.ScriptKind.JSX;
  return ts.createSourceFile(name, text, ts.ScriptTarget.Latest, true, kind);
}

function walk(node, visit) {
  visit(node);
  ts.forEachChild(node, (child) => walk(child, visit));
}

function lineAt(sf, node) {
  return sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1;
}

// The parentheses, `as` casts and `!` that wrap an expression do not change
// what it is.
function bare(node) {
  let current = node;
  while (
    ts.isParenthesizedExpression(current) ||
    ts.isAsExpression(current) ||
    ts.isNonNullExpression(current) ||
    ts.isSatisfiesExpression(current) ||
    ts.isTypeAssertionExpression(current)
  ) {
    current = current.expression;
  }
  return current;
}

// The names a file declares, and the assignments made to them. A name is
// read as a literal only when the file declares it exactly once, with a
// literal, and never assigns to it again (P3-g): a let or var that is
// reassigned anywhere is unreadable, and so is a name that two scopes
// declare.
function bindingsOf(sf) {
  const declared = new Map();
  const assigned = new Set();
  function declare(nameNode, init, isConst) {
    if (ts.isIdentifier(nameNode)) {
      const list = declared.get(nameNode.text) ?? [];
      list.push({ init, isConst });
      declared.set(nameNode.text, list);
    } else {
      for (const element of nameNode.elements) {
        if (ts.isBindingElement(element)) declare(element.name, undefined, false);
      }
    }
  }
  function markAssigned(target) {
    const node = bare(target);
    if (ts.isIdentifier(node)) assigned.add(node.text);
    else if (ts.isObjectLiteralExpression(node)) {
      for (const property of node.properties) {
        if (ts.isShorthandPropertyAssignment(property)) assigned.add(property.name.text);
        else if (ts.isPropertyAssignment(property)) markAssigned(property.initializer);
        else if (ts.isSpreadAssignment(property)) markAssigned(property.expression);
      }
    } else if (ts.isArrayLiteralExpression(node)) {
      for (const element of node.elements) markAssigned(element);
    }
  }
  walk(sf, (node) => {
    if (ts.isVariableDeclaration(node)) {
      const isConst = (ts.getCombinedNodeFlags(node) & ts.NodeFlags.Const) !== 0;
      declare(node.name, node.initializer, isConst);
    } else if (ts.isParameter(node)) {
      declare(node.name, undefined, false);
    } else if (ts.isFunctionDeclaration(node) && node.name) {
      declare(node.name, undefined, false);
    } else if (ts.isClassDeclaration(node) && node.name) {
      declare(node.name, undefined, false);
    } else if (ts.isImportSpecifier(node) || ts.isImportClause(node) || ts.isNamespaceImport(node)) {
      if (node.name) declare(node.name, undefined, false);
    } else if (
      ts.isBinaryExpression(node) &&
      node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment &&
      node.operatorToken.kind <= ts.SyntaxKind.LastAssignment
    ) {
      markAssigned(node.left);
    } else if (
      (ts.isPrefixUnaryExpression(node) || ts.isPostfixUnaryExpression(node)) &&
      (node.operator === ts.SyntaxKind.PlusPlusToken || node.operator === ts.SyntaxKind.MinusMinusToken)
    ) {
      markAssigned(node.operand);
    } else if ((ts.isForInStatement(node) || ts.isForOfStatement(node)) && !ts.isVariableDeclarationList(node.initializer)) {
      markAssigned(node.initializer);
    }
  });
  return { declared, assigned };
}

// The text of a string the expression builds from literals alone, and null
// when it builds from anything else. A template with an expression in it
// reads as its text with the expression marked; a concatenation reads as
// the parts joined; a name reads as the constant it is. `holes` says whether
// an unreadable part was marked.
function foldString(node, bindings, seen = new Set()) {
  const expr = bare(node);
  if (ts.isStringLiteral(expr) || ts.isNoSubstitutionTemplateLiteral(expr)) return { text: expr.text, hole: false };
  if (ts.isTemplateExpression(expr)) {
    let text = expr.head.text;
    let hole = false;
    for (const span of expr.templateSpans) {
      const inner = foldString(span.expression, bindings, seen);
      if (inner === null) {
        text += "${}";
        hole = true;
      } else {
        text += inner.text;
        hole = hole || inner.hole;
      }
      text += span.literal.text;
    }
    return { text, hole };
  }
  if (ts.isBinaryExpression(expr) && expr.operatorToken.kind === ts.SyntaxKind.PlusToken) {
    const left = foldString(expr.left, bindings, seen);
    const right = foldString(expr.right, bindings, seen);
    if (left === null && right === null) return null;
    return {
      text: `${left === null ? "${}" : left.text}${right === null ? "${}" : right.text}`,
      hole: left === null || right === null || left.hole || right.hole,
    };
  }
  if (ts.isArrayLiteralExpression(expr)) {
    const words = [];
    let hole = false;
    for (const element of expr.elements) {
      const word = foldString(element, bindings, seen);
      if (word === null) {
        words.push("${}");
        hole = true;
      } else {
        words.push(word.text);
        hole = hole || word.hole;
      }
    }
    return { text: words.join(" "), hole };
  }
  if (ts.isIdentifier(expr)) {
    const value = constantOf(expr.text, bindings, seen);
    return value === null ? null : { text: value, hole: false };
  }
  return null;
}

// The literal a name holds, when the file declares it once, as a const or as
// a let or var nothing assigns to again, with a literal; otherwise null.
function constantOf(name, bindings, seen = new Set()) {
  if (seen.has(name)) return null;
  const list = bindings.declared.get(name);
  if (list === undefined || list.length !== 1) return null;
  const [only] = list;
  if (only.init === undefined) return null;
  if (!only.isConst && bindings.assigned.has(name)) return null;
  if (only.isConst && bindings.assigned.has(name)) return null;
  const folded = foldString(only.init, bindings, new Set([...seen, name]));
  return folded === null || folded.hole ? null : folded.text;
}

function hostWhy(host, held = "") {
  if (LOOPBACK_HOSTS.has(host)) return { host, why: `its host is ${host}${held}` };
  return { host, why: `its host is ${host}${held}, and not a loopback host` };
}

function unprovable(detail) {
  return { host: null, why: `${detail}, so ${CANNOT_PROVE} it binds the loopback` };
}

// The host a literal-or-name expression holds.
function hostFrom(expr, bindings, sf) {
  const node = bare(expr);
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return hostWhy(node.text);
  if (ts.isIdentifier(node)) {
    const value = constantOf(node.text, bindings);
    if (value !== null) return hostWhy(value, `, held in ${node.text}`);
    return unprovable(`its host is ${node.text}, and the file does not hold it as a literal that nothing reassigns`);
  }
  return { host: null, why: `its host is ${node.getText(sf)}, and not a loopback literal` };
}

// The host a listen call names, and why it names none when it names none.
function hostOf(call, bindings, sf) {
  const args = call.arguments;
  if (args.length === 0) return { host: null, why: "it names no arguments at all" };
  if (args.some((arg) => ts.isSpreadElement(arg))) return unprovable("it spreads its arguments");
  const first = bare(args[0]);
  if (ts.isObjectLiteralExpression(first)) {
    if (first.properties.some((property) => ts.isSpreadAssignment(property))) {
      return unprovable("its options object spreads another object");
    }
    // Node uses the last host key and a reader the first, so every one is
    // judged: the first that is not the loopback is the one named.
    const judged = [];
    for (const property of first.properties) {
      const key = property.name !== undefined && (ts.isIdentifier(property.name) || ts.isStringLiteral(property.name)) ? property.name.text : null;
      if (key !== "host") continue;
      if (ts.isShorthandPropertyAssignment(property)) judged.push(hostFrom(property.name, bindings, sf));
      else if (ts.isPropertyAssignment(property)) judged.push(hostFrom(property.initializer, bindings, sf));
      else judged.push(unprovable("its host is not a property the gate reads"));
    }
    if (judged.length === 0) return { host: null, why: "its options object names no host" };
    return judged.find((one) => one.host === null || !LOOPBACK_HOSTS.has(one.host)) ?? judged[0];
  }
  if (args.length < 2) return { host: null, why: "it names a port and no host" };
  const second = bare(args[1]);
  if (ts.isArrowFunction(second) || ts.isFunctionExpression(second)) {
    return { host: null, why: "it names a port and a callback, and no host" };
  }
  return hostFrom(second, bindings, sf);
}

function isListenName(node) {
  return ts.isIdentifier(node) && node.text === "listen";
}

// The listeners a file holds that do not name the loopback, each with the
// line it sits on and the reason it is named.
function listenersIn(text, name = "probe.mjs") {
  const sf = parse(text, name);
  const bindings = bindingsOf(sf);
  const out = [];
  walk(sf, (node) => {
    // x.listen(...): a direct call is read.
    if (ts.isCallExpression(node)) {
      const callee = bare(node.expression);
      if (ts.isPropertyAccessExpression(callee) && isListenName(callee.name)) {
        const { host, why } = hostOf(node, bindings, sf);
        if (host === null || !LOOPBACK_HOSTS.has(host)) {
          out.push({ line: lineAt(sf, callee.name), host, why, call: node.getText(sf).split("\n")[0] });
        }
        return;
      }
    }
    // x.listen anywhere else: a reference the gate cannot follow.
    if (ts.isPropertyAccessExpression(node) && isListenName(node.name)) {
      const parent = node.parent;
      const direct = ts.isCallExpression(parent) && bare(parent.expression) === node;
      if (!direct) {
        out.push({ line: lineAt(sf, node.name), host: null, why: `it reaches listen other than by calling it (${node.getText(sf)}), so ${CANNOT_PROVE} what host it binds` });
      }
      return;
    }
    // x["listen"]: the same property by another spelling.
    if (ts.isElementAccessExpression(node)) {
      const arg = bare(node.argumentExpression);
      if ((ts.isStringLiteral(arg) || ts.isNoSubstitutionTemplateLiteral(arg)) && arg.text === "listen") {
        out.push({ line: lineAt(sf, node), host: null, why: `it reaches listen by a bracket (${node.getText(sf)}), so ${CANNOT_PROVE} what host it binds` });
      }
      return;
    }
    // const { listen } = server, and ({ listen } = server).
    if (ts.isBindingElement(node)) {
      const key = node.propertyName ?? node.name;
      const literalKey = ts.isIdentifier(key) || ts.isStringLiteral(key) ? key.text : null;
      if (literalKey === "listen") {
        out.push({ line: lineAt(sf, node), host: null, why: `it destructures listen out of an object, so ${CANNOT_PROVE} what host it binds` });
      }
      return;
    }
    if (ts.isShorthandPropertyAssignment(node) && node.name.text === "listen" && isAssignmentTarget(node)) {
      out.push({ line: lineAt(sf, node), host: null, why: `it destructures listen out of an object, so ${CANNOT_PROVE} what host it binds` });
    }
  });
  return out;
}

// Whether an object literal sits on the left of an assignment, which makes
// it a destructuring pattern and not a value.
function isAssignmentTarget(property) {
  let node = property.parent;
  while (node !== undefined) {
    const parent = node.parent;
    if (parent === undefined) return false;
    if (ts.isBinaryExpression(parent) && parent.operatorToken.kind === ts.SyntaxKind.EqualsToken && parent.left === node) {
      return true;
    }
    if (ts.isPropertyAssignment(parent) || ts.isArrayLiteralExpression(parent) || ts.isParenthesizedExpression(parent)) {
      node = parent;
      continue;
    }
    return false;
  }
  return false;
}

// Whether a word of a command is the vite binary: vite, or a path that ends
// in /vite, or its script.
function isViteWord(word) {
  return /(^|[\\/])vite(\.js|\.mjs)?$/.test(word);
}

// A word of a shell command with the quotes, the closing parenthesis and the
// semicolon around it taken off, and an @version taken off the name: the word
// 'vite@5 and the word "vite are the vite binary.
function cleanWord(word) {
  return word.replace(/^['"`(]+|['"`);]+$/g, "").replace(/(?<=.)@[\w.^~*-]+$/, "");
}

// The commands a shell line holds, each as its cleaned words. A semicolon, an
// &&, a | and a lone & end one command and begin the next, so a command's
// words never run into the next command's.
function commandsOf(text) {
  const commands = [[]];
  for (const raw of text.replace(/[;&|]+/g, " ; ").split(/\s+/)) {
    if (raw === "") continue;
    if (raw === ";") {
      commands.push([]);
      continue;
    }
    const word = cleanWord(raw);
    if (word !== "") commands[commands.length - 1].push(word);
  }
  return commands.filter((words) => words.length > 0);
}

const unquote = (value) => value.replace(/^['"`]+|['"`]+$/g, "");

// Every host a command names, in order: each --host is judged and not only
// the first.
function hostsOfWords(words) {
  const hosts = [];
  for (let i = 0; i < words.length; i += 1) {
    const word = words[i];
    if (word === "--host") {
      const value = words[i + 1];
      hosts.push(value === undefined || value.startsWith("-") ? "" : unquote(value));
    } else if (word.startsWith("--host=")) {
      hosts.push(unquote(word.slice("--host=".length)));
    }
  }
  return hosts;
}

function loopbackWord(host) {
  return host === "127.0.0.1" || host === "localhost" || host === "::1";
}

// Whether a string, or the words of an array, starts vite, and the host it
// names. A command is a shell line, so it is split on whitespace.
const RUNNERS = new Set(["npx", "npm", "pnpm", "yarn", "bunx", "exec", "run", "dlx", "sh", "bash"]);

function holeFollowsRunner(text) {
  const words = text.split(/\s+/).filter((word) => word !== "" && !word.startsWith("-"));
  return words.some((word, i) => word === "${}" && i > 0 && RUNNERS.has(words[i - 1]));
}

// Whether a vite command serves. vite build and vite optimize bind nothing;
// bare vite, dev, serve, preview and anything the gate does not know do.
function bindsAnInterface(words) {
  const at = words.findIndex(isViteWord);
  const sub = words.slice(at + 1).find((word) => !word.startsWith("-"));
  return sub !== "build" && sub !== "optimize";
}

// The commands of a shell line that start vite, each as its words, and null
// when none does.
function viteCommandOf(text) {
  const found = commandsOf(text).filter((words) => words.some(isViteWord));
  return found.length > 0 ? found : null;
}

// Whether a string literal is the module specifier of an import, an export
// or a dynamic import: the name "vite" there is the package and no command.
function isModuleSpecifier(node) {
  const parent = node.parent;
  if (ts.isImportDeclaration(parent) || ts.isExportDeclaration(parent)) return parent.moduleSpecifier === node;
  if (ts.isExternalModuleReference(parent)) return true;
  if (ts.isLiteralTypeNode(parent) && ts.isImportTypeNode(parent.parent)) return true;
  if (ts.isCallExpression(parent)) {
    const callee = parent.expression;
    return callee.kind === ts.SyntaxKind.ImportKeyword || (ts.isIdentifier(callee) && callee.text === "require");
  }
  return false;
}

// The offenders one source file holds among its vite commands, each with the
// line it sits on and the reason it is named. Every string, template and
// array that starts vite is read; a command key whose value the gate cannot
// read, in a file that starts vite or a server, is named.
function viteOffendersIn(text, rel, properties = new Set(), name = rel) {
  const sf = parse(text, name);
  const bindings = bindingsOf(sf);
  const offenders = [];
  const consumed = new Set();
  let holdsVite = false;
  let startsServer = false;
  const commands = [];

  function judge(node, words) {
    if (!bindsAnInterface(words)) return;
    const hosts = hostsOfWords(words);
    if (hosts.length > 0 && hosts.every(loopbackWord)) return;
    offenders.push({ line: lineAt(sf, node), why: words.join(" ") });
  }

  walk(sf, (node) => {
    if (ts.isPropertyAssignment(node) || ts.isShorthandPropertyAssignment(node)) {
      const key = ts.isPropertyAssignment(node) ? node.name : node.name;
      const keyText = ts.isIdentifier(key) || ts.isStringLiteral(key) ? key.text : null;
      if (keyText === "webServer") startsServer = true;
      if (keyText === "command") commands.push(node);
    }
  });

  // Every maximal expression that builds a string: the outermost
  // concatenation, template or array, so a vite word split across parts is
  // read as the command it makes.
  function visit(node) {
    const expr = node;
    const isBuilder =
      ts.isStringLiteral(expr) ||
      ts.isNoSubstitutionTemplateLiteral(expr) ||
      ts.isTemplateExpression(expr) ||
      (ts.isBinaryExpression(expr) && expr.operatorToken.kind === ts.SyntaxKind.PlusToken) ||
      ts.isArrayLiteralExpression(expr);
    if (isBuilder) {
      if ((ts.isStringLiteral(expr) || ts.isNoSubstitutionTemplateLiteral(expr)) && isModuleSpecifier(expr)) return;
      const folded = foldStringShallow(expr);
      if (folded !== null) {
        const commands = viteCommandOf(folded.text);
        if (commands !== null) {
          holdsVite = true;
          for (const words of commands) judge(expr, words);
          return;
        }
      }
    }
    ts.forEachChild(node, visit);
  }
  // Names are not followed here: a name that holds a vite string holds it as
  // a literal the walk reaches where it is declared.
  function foldStringShallow(expr) {
    return foldString(expr, { declared: new Map(), assigned: new Set() });
  }
  visit(sf);

  for (const node of commands) {
    const value = ts.isPropertyAssignment(node) ? node.initializer : node.name;
    const folded = foldString(value, bindings);
    if (folded !== null && !folded.hole) continue;
    // A command with a hole in it is read as far as it goes: one that holds
    // vite was judged where it is written, and one that runs a script by
    // node names the script and not a tool the hole could turn into vite.
    // A hole where a runner (npx, npm, pnpm, yarn) takes its tool is the
    // shape that could be vite, so it is named.
    if (folded !== null && !holeFollowsRunner(folded.text) && !/^\$\{\}/.test(folded.text)) continue;
    const expr = bare(value);
    if (ts.isPropertyAccessExpression(expr) && memberCommand(expr, properties)) continue;
    if (ts.isTypeNode(value)) continue;
    if (!holdsVite && !startsServer && !/vite/i.test(value.getText(sf))) continue;
    offenders.push({
      line: lineAt(sf, node),
      why: `it names a command this gate cannot read (${value.getText(sf)}), so ${CANNOT_PROVE} it carries --host 127.0.0.1`,
    });
  }
  return offenders.map((offender) => ({ ...offender, rel }));
}

// The names the tree gives an object that carries a literal command, so a
// harness that takes its stub command from its caller (options.appStub.command)
// reads as the tree's own object of that name. The literal itself is read
// where it is written.
function propertyCommands(texts) {
  const map = new Set();
  for (const [file, text] of texts) {
    walk(parse(text, file), (node) => {
      if (!ts.isPropertyAssignment(node)) return;
      const value = bare(node.initializer);
      if (!ts.isObjectLiteralExpression(value)) return;
      const key = ts.isIdentifier(node.name) || ts.isStringLiteral(node.name) ? node.name.text : null;
      if (key === null) return;
      const holdsCommand = value.properties.some(
        (property) =>
          ts.isPropertyAssignment(property) &&
          ts.isIdentifier(property.name) &&
          property.name.text === "command" &&
          foldString(property.initializer, { declared: new Map(), assigned: new Set() }) !== null,
      );
      if (holdsCommand) map.add(key);
    });
  }
  return map;
}

// Whether a member expression reads a command from an object the tree holds
// with a literal command in it.
function memberCommand(expr, properties) {
  const owner = bare(expr.expression);
  return ts.isPropertyAccessExpression(owner) && properties.has(owner.name.text);
}

// The offenders one package.json holds among its scripts: a script that runs
// vite with a --host other than the loopback.
function packageScriptOffendersIn(text, rel = "package.json") {
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    return [{ rel, line: 1, why: `it is not JSON, so ${CANNOT_PROVE} what its scripts run` }];
  }
  const offenders = [];
  for (const [script, command] of Object.entries(json.scripts ?? {})) {
    if (typeof command !== "string") continue;
    for (const words of viteCommandOf(command) ?? []) {
      if (hostsOfWords(words).every(loopbackWord)) continue;
      offenders.push({ rel, line: 1, why: `script ${script}: ${command}` });
      break;
    }
  }
  return offenders;
}

test("every test listener under web names a loopback host", () => {
  const offenders = [];
  const stale = [];
  const used = new Set();
  for (const file of webSourceFiles()) {
    const rel = repoPath(file);
    const text = readFileSync(file, "utf8");
    const result = listenerOffendersIn(rel, text, file);
    if (result.used) used.add(rel);
    for (const offender of result.offenders) offenders.push(`${rel}:${offender.line} ${offender.why}`);
  }
  for (const [rel, { reason }] of ALLOWED) {
    if (!used.has(rel)) stale.push(`${rel} is on the allow list but names a loopback host now, so the entry is stale (${reason})`);
  }
  assert.deepEqual(
    offenders,
    [],
    `every test listener must name 127.0.0.1, localhost or ::1 as its host:\n${offenders.join("\n")}`,
  );
  assert.deepEqual(stale, [], `the allow list must hold only the listeners that still need it:\n${stale.join("\n")}`);
});

test("every source file under web parses without a diagnostic", () => {
  const problems = [];
  for (const file of webSourceFiles()) {
    const [first] = parseProblemsIn(readFileSync(file, "utf8"), file);
    if (first !== undefined) problems.push(`${repoPath(file)}:${first.line} ${first.message}`);
  }
  assert.deepEqual(problems, [], `a file the parser reports on is not a file the gate has read:\n${problems.join("\n")}`);
});

test("every vite command under web names the loopback host", () => {
  const offenders = [];
  const sources = new Map();
  for (const file of webSourceFiles()) sources.set(file, readFileSync(file, "utf8"));
  const properties = propertyCommands(sources);
  for (const file of packageFiles()) {
    const rel = repoPath(file);
    for (const offender of packageScriptOffendersIn(readFileSync(file, "utf8"), rel)) {
      offenders.push(`${rel}:${offender.line} ${offender.why}`);
    }
  }
  for (const [file, text] of sources) {
    // The gate's own file holds the wildcard shapes it is tested with as
    // data, so it is the one file the vite leg does not read.
    if (file === fileURLToPath(import.meta.url)) continue;
    const rel = repoPath(file);
    for (const offender of viteOffendersIn(text, rel, properties, file)) {
      offenders.push(`${rel}:${offender.line} ${offender.why}`);
    }
  }
  assert.deepEqual(
    offenders,
    [],
    `every vite command must carry --host 127.0.0.1:\n${offenders.join("\n")}`,
  );
});

test("the gate reads every extension a source file carries", () => {
  const read = webSourceFiles().map((file) => extname(file));
  for (const extension of [".mjs", ".ts"]) assert.ok(read.includes(extension), `the gate must read ${extension} files`);
});

test("the gate catches a listener that sits outside an app's own test directory", () => {
  const source = 'const server = createServer();\nserver.listen(4959, "0.0.0.0", () => {});\n';
  assert.equal(listenersIn(source).length, 1, "the gate must name a listener that binds every interface");
  assert.ok(
    webSourceFiles().some((file) => file.includes("/packages/")),
    "the gate must read the sources outside an app's own test directory",
  );
});

test("the gate catches a listener whose call is not spelled .listen(", () => {
  const offenders = listenersIn('server.listen\n  (4959, () => console.log("up"));\n');
  assert.equal(offenders.length, 1, "the gate must name a listener whose paren is not next to the method name");
  assert.match(String(offenders[0].why ?? ""), /no host/, "the gate must say the call names no host");
});

test("the gate names the host a listener bound", () => {
  const offenders = listenersIn('server.listen(4959, "0.0.0.0", () => {});\n');
  assert.equal(offenders.length, 1, "the gate must name a listener that binds every interface");
  assert.match(String(offenders[0].why ?? ""), /0\.0\.0\.0/, "the gate must name the host the listener bound, not undefined");
});

test("a listener held on the allow list that the tree no longer needs fails the gate", () => {
  assert.equal(listenersIn('server.listen(0, "127.0.0.1");').length, 0, "a loopback listener needs no allow list entry");
});

// The parser legs: every shape the reread of 420 probed, fed to the gate's own
// scanner. A "caught" shape is a wildcard bind the gate has to name; a "named"
// shape is one the gate cannot prove and has to say so; a "pass" shape binds
// the loopback and has to pass.
const UNPROVABLE = /the gate cannot prove/;

const LISTEN_SHAPES = [
  ["L1 listen(port) no host", "server.listen(4959);", "caught"],
  ["L2 listen({port}) options object", "server.listen({ port: 4959 });", "caught"],
  ["L3 listen(port, 0.0.0.0)", 'server.listen(4959, "0.0.0.0");', "caught"],
  ["L4 createServer().listen across lines", 'createServer(handler)\n  .listen(4959,\n    "0.0.0.0");', "caught"],
  ["L5 .listen then newline then paren", 'server.listen\n  (4959, "0.0.0.0");', "caught"],
  ["L6 .listen.apply", 'server.listen.apply(server, [4959, "0.0.0.0"]);', "named"],
  ["L7 listen through a bound variable", 'const l = server.listen;\nl.call(server, 4959, "0.0.0.0");', "named"],
  ["L8 listen inside a template expression", 'const s = `${server.listen(4959, "0.0.0.0")}`;', "caught"],
  ["L9 host from process.env", "server.listen(4959, process.env.HOST);", "caught"],
  ["L10 host from process.env via a const", "const HOST = process.env.HOST;\nserver.listen(4959, HOST);", "caught"],
  ["L11 a comment that holds .listen(", '// server.listen(4959, "0.0.0.0")\nconst x = 1;', "pass"],
  ["L12 a real listener after a comment", '// note\nserver.listen(4959, "0.0.0.0");', "caught"],
  ["L13 regex holding a quote, then a wildcard listen", 'const f = (s) => /"/.test(s);\nserver.listen(4959, "0.0.0.0");', "caught"],
  ["L14 regex after a closing paren, then a wildcard listen", 'if (ok) /"/.test(s);\nserver.listen(4959, "0.0.0.0");', "caught"],
  ["L15 regex with a paren in a listen callback", 'server.listen(4959, "0.0.0.0", () => /(x/.test(a));', "caught"],
  ["L15b regex with a paren in the listen options", "server.listen({ port: 4959, host: \"0.0.0.0\", tag: /(x/ });", "caught"],
  ["R1 regex after =>", 'const f = () => /"/;\nserver.listen(4959, "0.0.0.0");', "caught"],
  ["R2 regex after )", 'if (a) /\'/.test(b);\nserver.listen(4959, "0.0.0.0");', "caught"],
  ["R3 regex after ]", 'const r = [/"/][0];\nserver.listen(4959, "0.0.0.0");', "caught"],
  ["H1 let reassigned to a wildcard", 'let HOST = "127.0.0.1";\nHOST = "0.0.0.0";\nserver.listen(4959, HOST);', "caught"],
  ["H1b var reassigned to a wildcard", 'var HOST = "127.0.0.1";\nHOST = "0.0.0.0";\nserver.listen(4959, HOST);', "caught"],
  ["H2 const in another file", "server.listen(4959, HOST);", "caught"],
  ["H4 host in a template literal", "server.listen(4959, `0.0.0.0`);", "caught"],
  ["A1 .listen.call", 'server.listen.call(server, 4959, "0.0.0.0");', "named"],
  ["A2 destructured listen", 'const { listen } = server;\nlisten.call(server, 4959);', "named"],
  ["A3 bracket notation", 'server["listen"](4959, "0.0.0.0");', "named"],
  ["A3b .listen.bind", 'const l = server.listen.bind(server);\nl(4959);', "named"],
  ["P1 loopback literal", 'server.listen(0, "127.0.0.1");', "pass"],
  ["P2 localhost", "server.listen(0, 'localhost', () => {});", "pass"],
  ["P3 ::1", 'server.listen(0, "::1");', "pass"],
  ["P4 options object with the loopback", 'server.listen({ port: 0, host: "127.0.0.1" });', "pass"],
  ["P5 const loopback by name", 'const HOST = "127.0.0.1";\nserver.listen(0, HOST);', "pass"],
  ["P6 let never reassigned", 'let HOST = "127.0.0.1";\nserver.listen(0, HOST);', "pass"],
  ["P7 a string that holds .listen(", 'const s = "server.listen(4959)";', "pass"],
  ["P8 a regex that holds .listen(", "const r = /server.listen\\(4959\\)/;", "pass"],
];

for (const [name, source, expect] of LISTEN_SHAPES) {
  test(`listener shape ${name} is ${expect}`, () => {
    const offenders = listenersIn(source, "probe.mjs");
    if (expect === "pass") {
      assert.deepEqual(offenders, [], "a loopback shape must pass");
      return;
    }
    assert.ok(offenders.length >= 1, "a wildcard or unprovable shape must be named");
    if (expect === "named") {
      assert.match(String(offenders[0].why ?? ""), UNPROVABLE, "an unprovable shape must say the gate cannot prove it");
    }
  });
}

const VITE_SHAPES = [
  ["V1 bare vite command", 'export default { webServer: { command: "npx vite --port 3000" } };', "caught"],
  ["V2 vite preview", 'export default { webServer: { command: "npx vite preview --port 3000" } };', "caught"],
  ["V3 vite in a template literal", "export default { webServer: { command: `npx vite --port ${port}` } };", "caught"],
  ["V4 vite in a variable", 'const cmd = "npx vite --port 3000";\nexport default { webServer: { command: cmd } };', "caught"],
  ["V5 vite in an array", 'export default { webServer: { command: ["npx", "vite", "--port", "3000"] } };', "caught"],
  ["V6 vite under a quoted key", 'export default { webServer: { "command": "npx vite --port 3000" } };', "caught"],
  ["V7 vite built by concatenation", 'const c = "npx " + "vite --port 3000";\nrun(c);', "caught"],
  ["V8 concatenation with a name, in a webServer file", 'export default { webServer: { command: "npx " + base } };', "named"],
  ["V9 command from a call, in a webServer file", "export default { webServer: { command: build() } };", "named"],
  ["V11 vite handed to spawn", 'spawn("npx", ["vite", "--port", "3000"]);', "caught"],
  ["V12 vite with a wildcard host", 'export default { webServer: { command: "npx vite --host 0.0.0.0" } };', "caught"],
  ["VP1 vite with the loopback host", 'export default { webServer: { command: "npx vite --host 127.0.0.1 --port 3000" } };', "pass"],
  ["VP2 import of vite", 'import { defineConfig } from "vite";\nexport default defineConfig({});', "pass"],
  ["VP3 a command that is not vite", 'export default { webServer: { command: "node stub.mjs" } };', "pass"],
  ["VP4 a comment that holds vite", '// npx vite --port 3000\nexport default {};', "pass"],
];

for (const [name, source, expect] of VITE_SHAPES) {
  test(`vite shape ${name} is ${expect}`, () => {
    const offenders = viteOffendersIn(source, "web/apps/probe-app/playwright.config.ts");
    if (expect === "pass") {
      assert.deepEqual(offenders, [], "a loopback shape must pass");
      return;
    }
    assert.ok(offenders.length >= 1, "a vite command without the loopback host must be named");
    if (expect === "named") {
      assert.match(String(offenders[0].why ?? ""), UNPROVABLE, "an unreadable command must say the gate cannot prove it");
    }
  });
}

const PACKAGE_SHAPES = [
  ["V10 vite --host 0.0.0.0 in a script", '{ "scripts": { "dev": "vite --host 0.0.0.0" } }', "caught"],
  ["V10b vite --host with no value", '{ "scripts": { "dev": "vite --host" } }', "caught"],
  ["V10c vite --host=0.0.0.0", '{ "scripts": { "dev": "vite --host=0.0.0.0 --port 5173" } }', "caught"],
  ["V10d vite preview --host 0.0.0.0", '{ "scripts": { "preview": "vite preview --host 0.0.0.0" } }', "caught"],
  ["VP5 plain vite binds localhost", '{ "scripts": { "dev": "vite", "build": "vite build" } }', "pass"],
  ["VP6 vite --host 127.0.0.1", '{ "scripts": { "dev": "vite --host 127.0.0.1" } }', "pass"],
  ["VP7 vite --host localhost", '{ "scripts": { "dev": "vite --host localhost" } }', "pass"],
  ["VP8 no scripts", '{ "name": "x" }', "pass"],
];

for (const [name, source, expect] of PACKAGE_SHAPES) {
  test(`package script shape ${name} is ${expect}`, () => {
    const offenders = packageScriptOffendersIn(source, "web/apps/probe-app/package.json");
    if (expect === "pass") {
      assert.deepEqual(offenders, [], "a loopback shape must pass");
      return;
    }
    assert.ok(offenders.length >= 1, "a vite script with a wildcard host must be named");
  });
}

// Fix round 3 legs (420, the third read).

// A scratch tree under /tmp/s-420-fix3-*, removed by its exact path. It holds
// files only, never a server.
function withScratchTree(files, body) {
  const root = mkdtempSync(join(tmpdir(), "s-420-fix3-"));
  try {
    for (const [name, text] of Object.entries(files)) {
      const full = join(root, name);
      mkdirSync(dirname(full), { recursive: true });
      writeFileSync(full, text);
    }
    return body(root);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
}

const WILDCARD = 'server.listen(4000, "0.0.0.0");\n';

test("P2-1 the gate reads .mts, .cts and .jsx, and not the declaration files", () => {
  withScratchTree(
    {
      "a/s.mts": WILDCARD,
      "a/s.cts": WILDCARD,
      "a/s.jsx": WILDCARD,
      "a/s.mjs": WILDCARD,
      "a/types.d.ts": "export {};\n",
      "a/types.d.mts": "export {};\n",
      "a/types.d.cts": "export {};\n",
    },
    (root) => {
      const read = webSourceFilesUnder(root).map((file) => relative(root, file)).sort();
      assert.deepEqual(read, ["a/s.cts", "a/s.jsx", "a/s.mjs", "a/s.mts"]);
    },
  );
});

test("P2-3 a test directory named build, dist or coverage is read; output at a package's top level is not", () => {
  withScratchTree(
    {
      "apps/x/package.json": "{}",
      "apps/x/test/build/s.mjs": WILDCARD,
      "apps/x/test/dist/s.mjs": WILDCARD,
      "apps/x/test/coverage/s.mjs": WILDCARD,
      "apps/x/dist/out.mjs": WILDCARD,
      "apps/x/build/out.mjs": WILDCARD,
      "apps/x/coverage/out.mjs": WILDCARD,
      "apps/x/node_modules/dep/index.mjs": WILDCARD,
      "apps/x/test/node_modules/dep/index.mjs": WILDCARD,
      "apps/x/.git/hook.mjs": WILDCARD,
    },
    (root) => {
      const read = webSourceFilesUnder(root).map((file) => relative(root, file)).sort();
      assert.deepEqual(read, ["apps/x/test/build/s.mjs", "apps/x/test/coverage/s.mjs", "apps/x/test/dist/s.mjs"]);
    },
  );
});

test("P3-2 every host key of an options object is judged", () => {
  assert.equal(listenersIn('server.listen({ host: "127.0.0.1", port: 4000, host: "0.0.0.0" });').length, 1, "a later wildcard host key");
  assert.equal(listenersIn('server.listen({ host: "0.0.0.0", port: 4000, host: "127.0.0.1" });').length, 1, "an earlier wildcard host key");
  assert.equal(listenersIn('server.listen({ host: "127.0.0.1", port: 4000, host: "localhost" });').length, 0, "two loopback host keys pass");
});

test("P3-3 a file with parse diagnostics is named with its first diagnostic", () => {
  const problems = parseProblemsIn("const a = `oops;\nserver.listen(4000, \"0.0.0.0\");\n", "probe.mjs");
  assert.ok(problems.length >= 1, "an unterminated template is a parse diagnostic");
  assert.equal(typeof problems[0].line, "number");
  assert.match(String(problems[0].message ?? ""), /\S/);
  assert.deepEqual(parseProblemsIn('server.listen(0, "127.0.0.1");\n', "probe.mjs"), [], "a clean file has none");
});

const CONSOLE_DOUBLE = "web/apps/money-desk/test/acceptance-real/console-double.mjs";
const DOUBLE_CALL = 'server.listen(port, "0.0.0.0", () => {\n  ready();\n});\n';

test("P3-5 the console double's allow entry covers its one 0.0.0.0 call and no other listener", () => {
  const only = listenerOffendersIn(CONSOLE_DOUBLE, DOUBLE_CALL);
  assert.deepEqual(only.offenders, [], "the one call passes");
  assert.equal(only.used, true, "the entry is used");
  const more = listenerOffendersIn(
    CONSOLE_DOUBLE,
    `${DOUBLE_CALL}server2.listen(5000);\nnew Foo().listen({ port: 6000 });\nserver3.listen(7000, "0.0.0.0");\n`,
  );
  assert.equal(more.offenders.length, 3, "every other listener in the file is judged");
  const twice = listenerOffendersIn(CONSOLE_DOUBLE, `${DOUBLE_CALL}${DOUBLE_CALL}`);
  assert.equal(twice.offenders.length, 1, "the same call a second time is judged");
  const loopback = listenerOffendersIn(CONSOLE_DOUBLE, 'server.listen(port, "127.0.0.1");\n');
  assert.equal(loopback.used, false, "a loopback call leaves the entry stale");
});

const asCommand = (command) => `export default { webServer: { command: ${JSON.stringify(command)} } };`;
const asScripts = (command) => JSON.stringify({ scripts: { dev: command } });

const WORD_SHAPES = [
  ["W1 vite@5", "npx vite@5 --port 3000", "caught"],
  ["W1b vite@latest", "npx vite@latest --port 3000", "caught"],
  ["W2 single quoted in sh -c", "sh -c 'vite --port 3000'", "caught"],
  ["W3 double quoted in bash -c", 'bash -c "vite --port 3000"', "caught"],
  ["W5 vite followed by a semicolon", "vite; echo done", "caught"],
  ["W5b vite then &&", "vite&&echo done", "caught"],
  ["W5c vite piped", "vite --port 3000 | cat", "caught"],
  ["W6 two hosts, the second a wildcard", "npx vite --host 127.0.0.1 --host 0.0.0.0", "caught"],
  ["W6b two hosts, the first a wildcard", "npx vite --host 0.0.0.0 --host 127.0.0.1", "caught"],
  ["W7 quoted wildcard host", "sh -c 'vite --host 0.0.0.0'", "caught"],
  ["W8 host= quoted wildcard", 'vite --host="0.0.0.0"', "caught"],
  ["WP1 quoted loopback", "sh -c 'vite --host 127.0.0.1 --port 3000'", "pass"],
  ["WP2 versioned loopback", "npx vite@5 --host 127.0.0.1", "pass"],
  ["WP3 loopback twice", "vite --host 127.0.0.1 --host localhost", "pass"],
  ["WP4 build in quotes", "sh -c 'vite build'", "pass-package"],
];

for (const [name, command, expect] of WORD_SHAPES) {
  test(`command word shape ${name} is ${expect}`, () => {
    const found = viteOffendersIn(asCommand(command), "web/apps/probe-app/playwright.config.ts");
    if (expect === "caught") assert.ok(found.length >= 1, "a vite command without the loopback host must be named");
    else if (expect === "pass") assert.deepEqual(found, []);
    const scripts = packageScriptOffendersIn(asScripts(command), "web/apps/probe-app/package.json");
    if (expect === "caught" && /0\.0\.0\.0|--host/.test(command)) assert.ok(scripts.length >= 1, "a vite script with a wildcard host must be named");
    if (expect === "pass" || expect === "pass-package") assert.deepEqual(scripts, []);
  });
}
