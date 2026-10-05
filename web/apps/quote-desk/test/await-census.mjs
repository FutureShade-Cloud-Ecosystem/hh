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
    // A field initializer is its own site: the promise a field starts belongs to the field's name.
    if (ts.isPropertyDeclaration(n)) return `${cls(n.parent)}${n.name.getText(sf)}`;
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

/** The return statements that belong to this function body itself: a nested function's returns are not ours. */
function ownReturns(body) {
  const found = [];
  (function step(node) {
    if (ts.isReturnStatement(node)) {
      found.push(node);
      return;
    }
    if (ts.isFunctionDeclaration(node) || ts.isFunctionExpression(node) || ts.isArrowFunction(node)) return;
    ts.forEachChild(node, step);
  })(body);
  return found;
}

/**
 * True when the promise started at this site is taken over by an enclosing form that the ledger can rule by its own
 * row: an await, a return, an arrow the promise flows out of, or a field, property or variable the promise is kept in
 * (the stored promise row). A statement or a void is the dead end the floating call row is for.
 */
function trackedFrom(node) {
  for (let cur = node; cur; cur = cur.parent) {
    if (
      ts.isAwaitExpression(cur) ||
      ts.isReturnStatement(cur) ||
      ts.isArrowFunction(cur) ||
      ts.isVariableDeclaration(cur) ||
      ts.isPropertyDeclaration(cur) ||
      ts.isPropertyAssignment(cur) ||
      ts.isBinaryExpression(cur)
    ) {
      return true;
    }
    if (ts.isExpressionStatement(cur) || ts.isVoidExpression(cur)) return false;
  }
  return false;
}

/**
 * The promise typed calls a short circuit or ternary statement starts, through parentheses: each is a floating call,
 * because the arm that runs is chosen at run time and nobody waits for the one it picks.
 */
function floatingArms(node) {
  if (ts.isParenthesizedExpression(node)) return floatingArms(node.expression);
  if (ts.isCallExpression(node)) return [node];
  if (ts.isBinaryExpression(node)) {
    const kind = node.operatorToken.kind;
    if (kind === ts.SyntaxKind.AmpersandAmpersandToken || kind === ts.SyntaxKind.BarBarToken || kind === ts.SyntaxKind.QuestionQuestionToken) {
      return [...floatingArms(node.left), ...floatingArms(node.right)];
    }
    return [];
  }
  if (ts.isConditionalExpression(node)) return [...floatingArms(node.whenTrue), ...floatingArms(node.whenFalse)];
  return [];
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
    // A short circuit or ternary used as a statement: the promise an arm starts floats the same way a bare call does.
    if (ts.isExpressionStatement(n) && !ts.isCallExpression(n.expression)) {
      for (const arm of floatingArms(n.expression)) if (typedAsPromise(arm)) add(arm, "floating call");
    }
    // A promise handed to another call as an argument: the callee is not the awaiter, unless an enclosing await,
    // return, arrow or store takes it over (trackedFrom), so from this site it floats.
    if (ts.isCallExpression(n) && typedAsPromise(n) && ts.isCallExpression(n.parent) && n.parent.arguments.includes(n) && !trackedFrom(n.parent)) add(n, "floating call");
    // An arrow whose body is itself a promise (a listener or a callback that returns the work it started), which no caller awaits.
    if (ts.isArrowFunction(n) && !ts.isBlock(n.body) && !ts.isVoidExpression(n.body) && typedAsPromise(n.body)) add(n, "promise arrow");
    // A block bodied arrow that returns the promise it starts: the same blind spot with braces.
    if (ts.isArrowFunction(n) && ts.isBlock(n.body)) {
      for (const ret of ownReturns(n.body)) {
        if (ret.expression !== undefined && typedAsPromise(ret.expression)) add(ret, "promise arrow");
      }
    }
    // A promise kept by assignment or declaration (a field, a property, this.x = ..., const p = ...): whoever reads
    // it later is not the caller that started it.
    if (ts.isBinaryExpression(n) && n.operatorToken.kind === ts.SyntaxKind.EqualsToken && ts.isCallExpression(n.right) && typedAsPromise(n.right)) add(n, "stored promise");
    if (ts.isVariableDeclaration(n) && n.initializer !== undefined && ts.isCallExpression(n.initializer) && typedAsPromise(n.initializer)) add(n.initializer, "stored promise");
    if (ts.isPropertyDeclaration(n) && n.initializer !== undefined && ts.isCallExpression(n.initializer) && typedAsPromise(n.initializer)) add(n.initializer, "stored promise");
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
