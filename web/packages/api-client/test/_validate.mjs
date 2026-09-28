// Structural validation of a decoded response against a generated descriptor
// (web/packages/api-client/src/gen/descriptors.ts). This is the test-side
// interpreter the generator's header names. It mirrors the contract tools'
// fixture check (core/api/tools/fixtures.mjs, ajv with formats unchecked):
// types, required properties, enum vocabularies, array and string bounds, and
// numeric bounds, with formats left alone. A fixture that does not validate
// is a finding, never a schema to loosen.
import { SCHEMA_DESCRIPTORS } from "../src/testing.ts";

/**
 * Validate one value against one descriptor; returns the list of problems,
 * empty when the value fits. `path` is the value's location for messages.
 */
export function validateValue(value, descriptor, path = "value", schemas = SCHEMA_DESCRIPTORS) {
  const problems = [];
  visit(value, descriptor, path, problems, schemas);
  return problems;
}

/** Validate against a named wire type; throws with every problem listed. */
export function assertValid(t, value, schemaName) {
  const problems = validateValue(value, schemas_of(schemaName));
  t.equal(problems.length, 0, `${schemaName}: ${problems.join("; ")}`);
}

function schemas_of(name) {
  const descriptor = SCHEMA_DESCRIPTORS[name];
  if (descriptor === undefined) throw new Error(`no descriptor for schema ${name}`);
  return descriptor;
}

function fits(value, descriptor, schemas) {
  return visit(value, descriptor, "value", [], schemas).length === 0;
}

function visit(value, d, path, problems, schemas) {
  switch (d.k) {
    case "unknown":
      return problems;
    case "null":
      if (value !== null) problems.push(`${path}: expected null, got ${kindOf(value)}`);
      return problems;
    case "string": {
      if (typeof value !== "string") {
        problems.push(`${path}: expected a string, got ${kindOf(value)}`);
        return problems;
      }
      if (d.minLength !== undefined && value.length < d.minLength) problems.push(`${path}: shorter than minLength ${d.minLength}`);
      if (d.maxLength !== undefined && value.length > d.maxLength) problems.push(`${path}: longer than maxLength ${d.maxLength}`);
      if (d.pattern !== undefined && new RegExp(d.pattern).test(value) === false) problems.push(`${path}: does not match the pattern ${d.pattern}`);
      return problems;
    }
    case "integer": {
      if (typeof value !== "number" || !Number.isInteger(value)) {
        problems.push(`${path}: expected an integer, got ${kindOf(value)}`);
        return problems;
      }
      return numericBounds(value, d, path, problems);
    }
    case "number": {
      if (typeof value !== "number" || Number.isNaN(value)) {
        problems.push(`${path}: expected a number, got ${kindOf(value)}`);
        return problems;
      }
      return numericBounds(value, d, path, problems);
    }
    case "boolean":
      if (typeof value !== "boolean") problems.push(`${path}: expected a boolean, got ${kindOf(value)}`);
      return problems;
    case "lit":
      if (!d.v.some((option) => JSON.stringify(option) === JSON.stringify(value))) {
        problems.push(`${path}: ${JSON.stringify(value)} is not in the vocabulary ${JSON.stringify(d.v)}`);
      }
      return problems;
    case "union": {
      if (d.of.some((leg) => fits(value, leg, schemas))) return problems;
      problems.push(`${path}: ${kindOf(value)} matches no option of the union (${d.of.map((leg) => leg.k).join(", ")})`);
      return problems;
    }
    case "allOf": {
      for (const [i, leg] of d.of.entries()) {
        const legProblems = [];
        visit(value, leg, path, legProblems, schemas);
        for (const p of legProblems) problems.push(`${path}: intersection leg ${i}: ${p.replace(`${path}: `, "")}`);
      }
      return problems;
    }
    case "array": {
      if (!Array.isArray(value)) {
        problems.push(`${path}: expected an array, got ${kindOf(value)}`);
        return problems;
      }
      if (d.minItems !== undefined && value.length < d.minItems) problems.push(`${path}: fewer than minItems ${d.minItems}`);
      if (d.maxItems !== undefined && value.length > d.maxItems) problems.push(`${path}: more than maxItems ${d.maxItems}`);
      value.forEach((item, i) => visit(item, d.of, `${path}[${i}]`, problems, schemas));
      return problems;
    }
    case "object": {
      if (value === null || typeof value !== "object" || Array.isArray(value)) {
        problems.push(`${path}: expected an object, got ${kindOf(value)}`);
        return problems;
      }
      for (const name of d.req) {
        if (value[name] === undefined) problems.push(`${path}: missing required property ${name}`);
      }
      if (d.closed) {
        for (const name of Object.keys(value)) {
          if (d.props[name] === undefined) problems.push(`${path}: unexpected property ${name} on a closed object`);
        }
      }
      for (const [name, leg] of Object.entries(d.props)) {
        if (value[name] !== undefined) visit(value[name], leg, `${path}.${name}`, problems, schemas);
      }
      return problems;
    }
    case "record": {
      if (value === null || typeof value !== "object" || Array.isArray(value)) {
        problems.push(`${path}: expected an object, got ${kindOf(value)}`);
        return problems;
      }
      for (const [name, item] of Object.entries(value)) visit(item, d.of, `${path}.${name}`, problems, schemas);
      return problems;
    }
    case "ref": {
      const target = schemas[d.name] ?? SCHEMA_DESCRIPTORS[d.name];
      if (target === undefined) {
        problems.push(`${path}: no descriptor for the referenced schema ${d.name}`);
        return problems;
      }
      return visit(value, target, path, problems, schemas);
    }
    default:
      problems.push(`${path}: unknown descriptor kind ${d.k}`);
      return problems;
  }
}

function numericBounds(value, d, path, problems) {
  if (d.minimum !== undefined && value < d.minimum) problems.push(`${path}: below the minimum ${d.minimum}`);
  if (d.maximum !== undefined && value > d.maximum) problems.push(`${path}: above the maximum ${d.maximum}`);
  if (d.exclusiveMinimum !== undefined && value <= d.exclusiveMinimum) problems.push(`${path}: not above the exclusive minimum ${d.exclusiveMinimum}`);
  if (d.exclusiveMaximum !== undefined && value >= d.exclusiveMaximum) problems.push(`${path}: not below the exclusive maximum ${d.exclusiveMaximum}`);
  return problems;
}

function kindOf(value) {
  if (value === null) return "null";
  if (Array.isArray(value)) return "an array";
  return `${typeof value} ${JSON.stringify(value)}`;
}
