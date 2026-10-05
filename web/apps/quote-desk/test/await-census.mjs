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

/** The program the checker types the src with: the app's own tsconfig, so a promise is known by its type and not by its name. */
function makeProgram() {
  const configPath = join(appDir, "tsconfig.json");
  const read = ts.readConfigFile(configPath, ts.sys.readFile);
  const parsed = ts.parseJsonConfigFileContent(read.config, ts.sys, appDir);
  return ts.createProgram(parsed.fileNames, { ...parsed.options, noEmit: true });
}

/** True for a type that is, or may be, a promise (it has a then). */
function isPromiseLike(type) {
  return type.isUnion() ? type.types.some(isPromiseLike) : type.getProperty("then") !== undefined;
}

/** Counts the asynchronous calls of one source file into `groups`, per file name, symbol and kind. */
function countFile(sf, rel, typedAsPromise, groups) {
  const add = (node, kind) => {
    const key = `${rel}\t${symbolOf(node, sf)}\t${kind}`;
    groups.set(key, (groups.get(key) ?? 0) + 1);
  };
  (function visit(n) {
    if (ts.isAwaitExpression(n)) add(n, "await");
    else if (ts.isCallExpression(n)) {
      const e = n.expression;
      if (ts.isPropertyAccessExpression(e) && e.name.text === "then") add(n, "then");
      else if (ts.isPropertyAccessExpression(e) && e.name.text === "catch") add(n, "catch");
      else if (ts.isPropertyAccessExpression(e) && e.name.text === "finally") add(n, "finally");
      else if (ts.isIdentifier(e) && TIMERS.has(e.text)) add(n, e.text);
      else if (ts.isPropertyAccessExpression(e) && ts.isIdentifier(e.expression) && (e.expression.text === "window" || e.expression.text === "globalThis") && TIMERS.has(e.name.text)) add(n, e.name.text);
    } else if (ts.isNewExpression(n) && ts.isIdentifier(n.expression) && n.expression.text === "Promise") add(n, "new Promise");
    else if (ts.isVoidExpression(n) && ts.isCallExpression(n.expression)) add(n, "void call");
    // A call typed as a promise that nobody waits for and nobody marked with void: any call, to the client, a method of this file or anything else.
    if (ts.isExpressionStatement(n) && ts.isCallExpression(n.expression) && typedAsPromise(n.expression)) add(n, "floating call");
    // An arrow whose body is itself a promise (a listener or a callback that returns the work it started), which no caller awaits.
    if (ts.isArrowFunction(n) && !ts.isBlock(n.body) && !ts.isVoidExpression(n.body) && typedAsPromise(n.body)) add(n, "promise arrow");
    // A promise kept in a field or variable by assignment (this.opening = this.loadAll()): whoever reads it later is not the caller that started it.
    if (ts.isBinaryExpression(n) && n.operatorToken.kind === ts.SyntaxKind.EqualsToken && ts.isCallExpression(n.right) && typedAsPromise(n.right)) add(n, "stored promise");
    // for await (...): each turn of the loop waits.
    if (ts.isForOfStatement(n) && n.awaitModifier !== undefined) add(n, "for await");
    ts.forEachChild(n, visit);
  })(sf);
}

/** Every asynchronous call of the app's src, counted per file, symbol and kind. */
export function census() {
  const groups = new Map();
  const program = makeProgram();
  const checker = program.getTypeChecker();
  const typedAsPromise = (node) => isPromiseLike(checker.getTypeAtLocation(node));
  for (const file of [...walk(srcDir, (name) => name.endsWith(".ts") && !name.endsWith(".d.ts"))].sort()) {
    const sf = program.getSourceFile(file);
    if (sf === undefined) throw new Error(`the census could not load ${file} into the program`);
    countFile(sf, relative(srcDir, file), typedAsPromise, groups);
  }
  return groups;
}

/** The census of one piece of source text, typed on its own: what the test of the census itself feeds it. */
export function censusOfText(text) {
  const name = join(appDir, "src", "__probe__.ts");
  const options = { target: ts.ScriptTarget.ES2022, lib: ["lib.es2022.d.ts", "lib.dom.d.ts"], strict: true, noEmit: true, skipLibCheck: true };
  const host = ts.createCompilerHost(options);
  const original = host.getSourceFile.bind(host);
  host.getSourceFile = (fileName, languageVersion, ...rest) =>
    fileName === name ? ts.createSourceFile(fileName, text, languageVersion, true) : original(fileName, languageVersion, ...rest);
  const program = ts.createProgram([name], options, host);
  const checker = program.getTypeChecker();
  const groups = new Map();
  const sf = program.getSourceFile(name);
  countFile(sf, "__probe__.ts", (node) => isPromiseLike(checker.getTypeAtLocation(node)), groups);
  return groups;
}
