// Mutant hygiene: every mutant of test/mutants.json is a valid edit. It must apply once, leave the file free of syntax
// errors, and add no type error, so a mutant that goes red does so on the assertion of its own test and not because the
// build broke. Each mutated file is typed in memory with the language service; the sources on disk are never touched.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const mutants = JSON.parse(readFileSync(join(appDir, "test", "mutants.json"), "utf8"));

test("every mutant leaves the file it edits free of syntax errors and type errors", () => {
  const configPath = join(appDir, "tsconfig.json");
  const parsed = ts.parseJsonConfigFileContent(ts.readConfigFile(configPath, ts.sys.readFile).config, ts.sys, appDir);
  const overrides = new Map();
  const versions = new Map();
  const host = {
    getScriptFileNames: () => parsed.fileNames,
    getScriptVersion: (name) => String(versions.get(name) ?? 0),
    getScriptSnapshot: (name) => {
      const text = overrides.has(name) ? overrides.get(name) : ts.sys.readFile(name);
      return text === undefined ? undefined : ts.ScriptSnapshot.fromString(text);
    },
    getCurrentDirectory: () => appDir,
    getCompilationSettings: () => ({ ...parsed.options, noEmit: true }),
    getDefaultLibFileName: (options) => ts.getDefaultLibFilePath(options),
    fileExists: ts.sys.fileExists,
    readFile: ts.sys.readFile,
    readDirectory: ts.sys.readDirectory,
    directoryExists: ts.sys.directoryExists,
    getDirectories: ts.sys.getDirectories,
  };
  const service = ts.createLanguageService(host, ts.createDocumentRegistry());
  const problems = [];
  const baseline = new Map();
  const errorsOf = (file) => [...service.getSyntacticDiagnostics(file), ...service.getSemanticDiagnostics(file)].map((d) => `${d.code} ${ts.flattenDiagnosticMessageText(d.messageText, " ")}`);
  for (const mutant of mutants) {
    const file = join(appDir, mutant.file);
    if (!baseline.has(file)) baseline.set(file, errorsOf(file));
    const original = readFileSync(file, "utf8");
    const parts = original.split(mutant.find);
    if (parts.length !== 2) {
      problems.push(`${mutant.id}: its edit applies ${parts.length - 1} times, it must apply once`);
      continue;
    }
    overrides.set(file, parts.join(mutant.replace));
    versions.set(file, (versions.get(file) ?? 0) + 1);
    const before = new Set(baseline.get(file));
    for (const error of errorsOf(file)) if (!before.has(error)) problems.push(`${mutant.id}: ${error}`);
    overrides.delete(file);
    versions.set(file, (versions.get(file) ?? 0) + 1);
  }
  assert.deepEqual(problems, []);
});
