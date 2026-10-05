// The census of the app's asynchronous calls: every await, every then(), every timer, every promise made by hand and
// every call started without waiting for it, counted per file, symbol (the method or function it sits in) and kind.
// test/await-ledger.test.mjs holds the ledger to it.
import { readdirSync, readFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const srcDir = join(appDir, "src");

const TIMERS = new Set(["setTimeout", "setInterval", "requestAnimationFrame", "queueMicrotask", "requestIdleCallback"]);

export function* walk(dir, accept) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name !== "node_modules" && entry.name !== "stub") yield* walk(path, accept);
    } else if (accept(entry.name)) yield path;
  }
}

/** The name of the method, function or arrow property a node sits in. */
function symbolOf(node, sf) {
  for (let n = node.parent; n; n = n.parent) {
    const cls = (c) => (c && ts.isClassLike(c) && c.name ? `${c.name.getText(sf)}.` : "");
    if (ts.isMethodDeclaration(n) || ts.isFunctionDeclaration(n) || ts.isGetAccessor(n) || ts.isSetAccessor(n)) {
      return `${cls(n.parent)}${n.name ? n.name.getText(sf) : "(anonymous)"}`;
    }
    if (ts.isConstructorDeclaration(n)) return `${cls(n.parent)}constructor`;
    if (ts.isPropertyDeclaration(n) && n.initializer && (ts.isArrowFunction(n.initializer) || ts.isFunctionExpression(n.initializer))) {
      return `${cls(n.parent)}${n.name.getText(sf)}`;
    }
    if (ts.isVariableDeclaration(n) && n.initializer && (ts.isArrowFunction(n.initializer) || ts.isFunctionExpression(n.initializer))) return n.name.getText(sf);
  }
  return "(module)";
}

/** Every asynchronous call of the app's src, counted per file, symbol and kind. */
export function census() {
  const groups = new Map();
  for (const file of [...walk(srcDir, (name) => name.endsWith(".ts") && !name.endsWith(".d.ts"))].sort()) {
    const sf = ts.createSourceFile(file, readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true);
    const rel = relative(srcDir, file);
    const asyncNames = new Set();
    (function collect(n) {
      if ((ts.isMethodDeclaration(n) || ts.isFunctionDeclaration(n)) && n.name && n.modifiers?.some((m) => m.kind === ts.SyntaxKind.AsyncKeyword)) asyncNames.add(n.name.getText(sf));
      ts.forEachChild(n, collect);
    })(sf);
    const add = (node, kind) => {
      const key = `${rel}\t${symbolOf(node, sf)}\t${kind}`;
      groups.set(key, (groups.get(key) ?? 0) + 1);
    };
    (function visit(n) {
      if (ts.isAwaitExpression(n)) add(n, "await");
      else if (ts.isCallExpression(n)) {
        const e = n.expression;
        if (ts.isPropertyAccessExpression(e) && e.name.text === "then") add(n, "then");
        else if (ts.isIdentifier(e) && TIMERS.has(e.text)) add(n, e.text);
        else if (ts.isPropertyAccessExpression(e) && ts.isIdentifier(e.expression) && (e.expression.text === "window" || e.expression.text === "globalThis") && TIMERS.has(e.name.text)) add(n, e.name.text);
      } else if (ts.isNewExpression(n) && ts.isIdentifier(n.expression) && n.expression.text === "Promise") add(n, "new Promise");
      else if (ts.isVoidExpression(n) && ts.isCallExpression(n.expression)) add(n, "void call");
      if (ts.isExpressionStatement(n) && ts.isCallExpression(n.expression)) {
        // A call to an async method of the same file that nobody waits for and nobody marked with void.
        const e = n.expression.expression;
        if (ts.isPropertyAccessExpression(e) && e.expression.kind === ts.SyntaxKind.ThisKeyword && asyncNames.has(e.name.text)) add(n, "floating call");
      }
      ts.forEachChild(n, visit);
    })(sf);
  }
  return groups;
}

