// A small reader for an element's own stylesheet (a Lit CSSResult's text), so a
// floor can name the selector and the declaration instead of scanning the source
// for a string that might sit on a different rule. It handles plain rules and
// one level of at-rule nesting (@container), which is all these sheets use.

const stripComments = (text) => text.replace(/\/\*[\s\S]*?\*\//g, '');

function parseDecls(body) {
  const decls = {};
  for (const part of body.split(';')) {
    const at = part.indexOf(':');
    if (at < 0) continue;
    decls[part.slice(0, at).trim()] = part.slice(at + 1).trim();
  }
  return decls;
}

/** Every rule as { query, selectors, decls }; query is null outside an at-rule. */
export function parseRules(cssText) {
  const text = stripComments(cssText);
  const rules = [];
  const walk = (chunk, query) => {
    let i = 0;
    while (i < chunk.length) {
      const open = chunk.indexOf('{', i);
      if (open < 0) break;
      const head = chunk.slice(i, open).trim();
      let depth = 1;
      let j = open + 1;
      while (j < chunk.length && depth > 0) {
        if (chunk[j] === '{') depth += 1;
        else if (chunk[j] === '}') depth -= 1;
        j += 1;
      }
      const body = chunk.slice(open + 1, j - 1);
      if (head.startsWith('@')) walk(body, head);
      else rules.push({ query, selectors: head.split(',').map((s) => s.trim()), decls: parseDecls(body) });
      i = j;
    }
  };
  walk(text, null);
  return rules;
}

/** The declarations that apply to one selector, merged in source order, for
 * rules outside any at-rule (query null) or inside the named query. */
export function declsFor(rules, selector, query = null) {
  const merged = {};
  for (const rule of rules) {
    if (rule.query === query && rule.selectors.includes(selector)) Object.assign(merged, rule.decls);
  }
  return merged;
}

/** A px declaration as a number, or null when it is not a plain px length. */
export const px = (value) => {
  const m = /^(\d+(?:\.\d+)?)px$/.exec(value ?? '');
  return m ? Number(m[1]) : null;
};
