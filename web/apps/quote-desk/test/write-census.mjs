// The census of the app's writes: every call in src that reaches a write method of the client (a method of QdClient
// whose generated operation sends a PUT, POST, PATCH or DELETE; the set is read off qd-client.ts and the generated
// client, not listed by hand), and where in its function it sits. A write whose send expression is reached only after
// an await, from a then(), catch() or finally() callback, from a timer, from a callback its callee runs later, or from
// a method that is itself called from such a position, is DEFERRED: it can outlive the person who asked for it and the
// quote it was asked on. A deferred write is allowed one way only, as the closure handed to deferredSend
// (src/deferred-send.ts), which captured the session and the quote when the person acted and checks them at send time.
// The capture belongs at the person's action too: a captureSend written inside the sender's own call is evaluated at
// send time, so every check compares now with now, and a row sent that way is a violation like any other. Anything
// else deferred is a violation. test/write-census.test.mjs holds the src to it.
import { join, relative } from "node:path";
import ts from "typescript";
import { appDir, makeProgram, symbolOf, walk } from "./await-census.mjs";

const srcDir = join(appDir, "src");
const WRITE_VERBS = new Set(["PUT", "POST", "PATCH", "DELETE"]);
const TIMERS = new Set(["setTimeout", "setInterval", "requestAnimationFrame", "queueMicrotask", "requestIdleCallback"]);
/** Callees that run an arrow synchronously, in place: the arrow sits where the call sits. */
const IN_PLACE = new Set(["forEach", "map", "filter", "some", "every", "find", "findIndex", "reduce", "flatMap"]);
const SENDER = "deferredSend";
const CAPTURE = "captureSend";

function isFunctionLike(n) {
  return ts.isArrowFunction(n) || ts.isFunctionExpression(n) || ts.isFunctionDeclaration(n) || ts.isMethodDeclaration(n) || ts.isConstructorDeclaration(n) || ts.isGetAccessor(n) || ts.isSetAccessor(n);
}

function enclosingFunction(node) {
  for (let n = node.parent; n; n = n.parent) if (isFunctionLike(n)) return n;
  return undefined;
}

/** The write methods of the client: name to HTTP method, derived from qd-client.ts and the generated operations it calls. */
export function writeMethods(program) {
  const checker = program.getTypeChecker();
  const sf = program.getSourceFile(join(srcDir, "qd-client.ts"));
  const found = new Map();
  const verbOf = (decl) => {
    let verb = null;
    (function step(n) {
      if (ts.isPropertyAssignment(n) && n.name.getText() === "method" && ts.isStringLiteralLike(n.initializer)) verb = n.initializer.text;
      ts.forEachChild(n, step);
    })(decl);
    return verb;
  };
  (function visit(n) {
    if (ts.isClassDeclaration(n) && n.name?.text === "QdClient") {
      for (const member of n.members) {
        if (!ts.isMethodDeclaration(member) || member.body === undefined) continue;
        const verbs = new Set();
        (function calls(c) {
          if (ts.isCallExpression(c)) {
            const symbol = checker.getSymbolAtLocation(ts.isPropertyAccessExpression(c.expression) ? c.expression.name : c.expression);
            for (const decl of symbol?.declarations ?? []) {
              if (ts.isFunctionDeclaration(decl) && decl.getSourceFile().fileName.includes("/api-client/")) {
                const verb = verbOf(decl);
                if (verb !== null && WRITE_VERBS.has(verb)) verbs.add(verb);
              }
            }
          }
          ts.forEachChild(c, calls);
        })(member.body);
        if (verbs.size > 0) found.set(member.name.getText(), [...verbs].sort().join("/"));
      }
    }
    ts.forEachChild(n, visit);
  })(sf);
  return found;
}

/** The declaration a call's callee resolves to, or undefined. */
function calleeDeclaration(checker, call) {
  const target = ts.isPropertyAccessExpression(call.expression) ? call.expression.name : call.expression;
  let symbol = checker.getSymbolAtLocation(target);
  if (symbol !== undefined && symbol.flags & ts.SymbolFlags.Alias) symbol = checker.getAliasedSymbol(symbol);
  return symbol?.declarations?.[0];
}

/** True when an await (or for await) of the same function ends before this node starts. */
function awaitBefore(fn, node) {
  const at = node.getStart();
  let found = false;
  (function step(n) {
    if (found) return;
    if (n !== fn && isFunctionLike(n)) return;
    if ((ts.isAwaitExpression(n) || (ts.isForOfStatement(n) && n.awaitModifier !== undefined)) && n.getEnd() <= at) {
      found = true;
      return;
    }
    ts.forEachChild(n, step);
  })(fn.body ?? fn);
  return found;
}

/**
 * Why this node is at a deferred position, or null when it runs in the same tick the person acted in. `seen` stops a
 * cycle of methods calling each other.
 */
function deferredReason(node, env, seen = new Set(), local = false) {
  const { checker, files } = env;
  const fn = enclosingFunction(node);
  if (fn === undefined) return null;
  if (awaitBefore(fn, node)) return "after an await";
  if (inLoopWithAwait(fn, node)) return "in a loop that awaits, so a later turn comes after an await";
  if (ts.isArrowFunction(fn) || ts.isFunctionExpression(fn)) {
    let outer = fn.parent;
    while (ts.isParenthesizedExpression(outer)) outer = outer.parent;
    if (ts.isCallExpression(outer) && outer.arguments.includes(fn)) {
      const callee = outer.expression;
      const name = ts.isPropertyAccessExpression(callee) ? callee.name.text : ts.isIdentifier(callee) ? callee.text : "";
      if (name === "then" || name === "catch" || name === "finally") return `a ${name} callback`;
      if (TIMERS.has(name)) return `a ${name} callback`;
      if (name === "addEventListener") return null;
      if (IN_PLACE.has(name)) return deferredReason(outer, env, seen, local);
      const decl = calleeDeclaration(checker, outer);
      const index = outer.arguments.indexOf(fn);
      const param = decl !== undefined && isFunctionLike(decl) ? decl.parameters[index] : undefined;
      if (param === undefined || !ts.isIdentifier(param.name)) return `a callback of ${name || "a call"} that may run later`;
      // The callback runs where the callee runs it: deferred when any call of it inside the callee is.
      const key = `cb:${decl.getSourceFile().fileName}:${decl.pos}:${index}`;
      if (seen.has(key)) return null;
      seen.add(key);
      let reason = null;
      (function step(n) {
        if (reason !== null) return;
        if (ts.isIdentifier(n) && n.text === param.name.text && n !== param.name && !ts.isPropertyAccessExpression(n.parent)) {
          const parent = n.parent;
          if (ts.isCallExpression(parent) && parent.expression === n) reason = deferredReason(parent, env, seen, true);
          else {
            // Handed on: to then(), a timer or a queue it runs later; anywhere else it is not known to run in place.
            const handedTo = ts.isCallExpression(parent) ? (ts.isPropertyAccessExpression(parent.expression) ? parent.expression.name.text : parent.expression.getText()) : "somewhere";
            reason = `handed to ${handedTo}`;
          }
        }
        ts.forEachChild(n, step);
      })(decl.body ?? decl);
      if (reason !== null) return `a callback ${name} runs it ${reason}`;
      return deferredReason(outer, env, seen, local);
    }
  }
  // A method or a function value of its own: it runs when it is called, so it is deferred when any call of it is.
  if (local) return null;
  let decl = fn;
  if (ts.isArrowFunction(fn) || ts.isFunctionExpression(fn)) {
    const holder = fn.parent;
    if (ts.isPropertyDeclaration(holder) || ts.isVariableDeclaration(holder)) decl = holder;
    else return null;
  }
  const key = `fn:${decl.getSourceFile().fileName}:${decl.pos}`;
  if (seen.has(key)) return null;
  seen.add(key);
  const nameNode = decl.name;
  if (nameNode === undefined) return null;
  const target = checker.getSymbolAtLocation(nameNode);
  if (target === undefined) return null;
  for (const file of files) {
    let reason = null;
    (function step(n) {
      if (reason !== null) return;
      if (ts.isCallExpression(n) && n !== node) {
        const callee = ts.isPropertyAccessExpression(n.expression) ? n.expression.name : n.expression;
        if (checker.getSymbolAtLocation(callee) === target) {
          const why = deferredReason(n, env, seen);
          if (why !== null) reason = `a call of ${nameNode.getText()} in ${symbolOf(n, file)} is deferred (${why})`;
        }
      }
      ts.forEachChild(n, step);
    })(file);
    if (reason !== null) return reason;
  }
  return null;
}

/** True when the node sits in a loop of its function whose body awaits anywhere: the next turn comes after that await. */
function inLoopWithAwait(fn, node) {
  for (let n = node.parent; n && n !== fn; n = n.parent) {
    if (ts.isForStatement(n) || ts.isForOfStatement(n) || ts.isForInStatement(n) || ts.isWhileStatement(n) || ts.isDoStatement(n)) {
      let awaits = ts.isForOfStatement(n) && n.awaitModifier !== undefined;
      (function step(c) {
        if (awaits) return;
        if (c !== n && isFunctionLike(c)) return;
        if (ts.isAwaitExpression(c)) awaits = true;
        ts.forEachChild(c, step);
      })(n);
      if (awaits) return true;
    }
  }
  return false;
}

/** True when the node sits in the closure handed to the sender. */
function insideSender(node, checker) {
  for (let n = node.parent; n; n = n.parent) {
    if (!isFunctionLike(n) || !(ts.isArrowFunction(n) || ts.isFunctionExpression(n))) continue;
    let outer = n.parent;
    while (ts.isParenthesizedExpression(outer)) outer = outer.parent;
    if (ts.isCallExpression(outer) && outer.arguments.includes(n)) {
      const decl = calleeDeclaration(checker, outer);
      if (decl !== undefined && decl.name?.getText() === SENDER && decl.getSourceFile().fileName.endsWith("/deferred-send.ts")) return true;
    }
  }
  return false;
}

/** The call of the sender the node is written inside: the write's own send, or the send a captureSend sits in the arguments of. */
function senderCallAbove(node, checker) {
  for (let n = node.parent; n; n = n.parent) {
    if (!ts.isCallExpression(n)) continue;
    const decl = calleeDeclaration(checker, n);
    if (decl !== undefined && decl.name?.getText() === SENDER && decl.getSourceFile().fileName.endsWith("/deferred-send.ts")) return n;
  }
  return undefined;
}

/**
 * True when a captureSend call is itself taken inside the send: written within the sender's own call, in the closure
 * handed to it or beside it among its arguments, so the session and the quote are read at send time and every check
 * compares now with now.
 */
function captureInsideSender(node, checker) {
  return senderCallAbove(node, checker) !== undefined;
}

/**
 * Every write of the given source files: file, symbol, client method, HTTP verb, line, and how it is sent: immediate,
 * sender (deferred, through deferredSend) or VIOLATION (deferred any other way, with the reason).
 */
export function writeCensus(program, sourceFiles) {
  const checker = program.getTypeChecker();
  const methods = writeMethods(program);
  const files = [...walk(srcDir, (name) => name.endsWith(".ts") && !name.endsWith(".d.ts"))].sort().map((f) => program.getSourceFile(f));
  const env = { checker, files: [...new Set([...files, ...sourceFiles])] };
  const rows = [];
  for (const sf of sourceFiles) {
    if (sf.fileName.endsWith("/qd-client.ts")) continue;
    const captures = [];
    (function find(n) {
      if (ts.isCallExpression(n) && ts.isIdentifier(n.expression) && n.expression.text === CAPTURE) {
        const decl = calleeDeclaration(checker, n);
        if (decl !== undefined && decl.name?.getText() === CAPTURE && decl.getSourceFile().fileName.endsWith("/deferred-send.ts")) captures.push(n);
      }
      ts.forEachChild(n, find);
    })(sf);
    (function visit(n) {
      if (ts.isCallExpression(n) && ts.isPropertyAccessExpression(n.expression) && methods.has(n.expression.name.text)) {
        const decl = calleeDeclaration(checker, n);
        const owner = decl?.parent;
        if (owner !== undefined && ts.isClassDeclaration(owner) && owner.name?.text === "QdClient") {
          const reason = deferredReason(n, env);
          const sender = insideSender(n, checker);
          let way = sender ? "sender" : reason === null ? "immediate" : "VIOLATION";
          let why = reason ?? "";
          if (way === "sender") {
            const send = senderCallAbove(n, checker);
            const takenInside = send !== undefined && captures.some((cs) => captureInsideSender(cs, checker) && cs.getStart() >= send.getStart() && cs.getEnd() <= send.getEnd());
            if (takenInside) {
              way = "VIOLATION";
              why = "capture taken inside the sender closure";
            }
          }
          rows.push({
            file: relative(srcDir, sf.fileName),
            symbol: symbolOf(n, sf),
            call: n.expression.name.text,
            verb: methods.get(n.expression.name.text),
            line: sf.getLineAndCharacterOfPosition(n.getStart()).line + 1,
            way,
            reason: way === "immediate" ? "" : why,
          });
        }
      }
      ts.forEachChild(n, visit);
    })(sf);
  }
  return rows;
}

/** The census of the app's own src. */
export function srcWriteCensus() {
  const program = makeProgram();
  const files = [...walk(srcDir, (name) => name.endsWith(".ts") && !name.endsWith(".d.ts"))].sort().map((f) => program.getSourceFile(f));
  return writeCensus(program, files);
}

/** The census of one piece of source text placed in the src, so it imports the real client and the real sender: what the census's own tests feed it. */
export function writeCensusOfText(text) {
  const name = join(srcDir, "__probe__.ts");
  const read = ts.readConfigFile(join(appDir, "tsconfig.json"), ts.sys.readFile);
  const parsed = ts.parseJsonConfigFileContent(read.config, ts.sys, appDir);
  const options = { ...parsed.options, noEmit: true };
  const host = ts.createCompilerHost(options);
  const original = host.getSourceFile.bind(host);
  host.getSourceFile = (fileName, languageVersion, ...rest) => (fileName === name ? ts.createSourceFile(fileName, text, languageVersion, true) : original(fileName, languageVersion, ...rest));
  const program = ts.createProgram([...parsed.fileNames, name], options, host);
  return writeCensus(program, [program.getSourceFile(name)]);
}
