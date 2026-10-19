// Source slicing for the source reading tests: take a function's own body by its declared name, never the text
// between two section comments. A section comment that is moved or renamed used to widen the slice (indexOf
// answering -1 hands slice() the rest of the file) or empty it, and an assertion over the wrong text passes or
// fails for the wrong reason. A missing function, or a body that reads empty, throws by name instead.

/** The body of `function <name>(...)` (exported or not), braces included, from `source`. Throws when absent. */
export function functionBody(source, name) {
  const declaration = new RegExp(`^(?:export\\s+)?(?:async\\s+)?function\\s+${name}\\s*\\(`, "m").exec(source);
  if (declaration === null) {
    throw new Error(`function ${name}( is not declared in the source: the slice cannot be taken, so the assertions would read nothing`);
  }
  // Walk the parameter list to its close, then to the brace that opens the body (a return type sits between).
  let i = declaration.index + declaration[0].length;
  for (let depth = 1; i < source.length && depth > 0; i++) {
    if (source[i] === "(") depth++;
    else if (source[i] === ")") depth--;
  }
  const open = source.indexOf("{", i);
  if (open === -1) throw new Error(`function ${name} has no body`);
  let depth = 0;
  for (let j = open; j < source.length; j++) {
    if (source[j] === "{") depth++;
    else if (source[j] === "}") {
      depth--;
      if (depth === 0) {
        const body = source.slice(open, j + 1);
        if (body.trim().length <= 2) throw new Error(`function ${name} has an empty body`);
        return body;
      }
    }
  }
  throw new Error(`function ${name} has an unbalanced body`);
}
