/** Check object keys before JSON.parse discards aliases; keep the native decoder. */
export function strictJson(text, { maxBytes = 1048576, maxDepth = 64 } = {}) {
  if (typeof text !== 'string' || new TextEncoder().encode(text).length > maxBytes) throw new Error('JSON exceeds limit');
  let at = 0;
  const space = () => { while (/^[\t\n\r ]$/.test(text[at] ?? '')) at++; };
  const string = () => {
    const start = at++;
    while (at < text.length) {
      if (text[at] === '\\') at += 2;
      else if (text[at++] === '"') return JSON.parse(text.slice(start, at));
    }
    throw new Error('Unterminated JSON string');
  };
  const value = depth => {
    if (depth > maxDepth) throw new Error('JSON nesting exceeds limit');
    space();
    if (text[at] === '{') {
      at++; space();
      const keys = new Set();
      if (text[at] !== '}') while (true) {
        if (text[at] !== '"') throw new Error('Invalid JSON key');
        const key = string();
        if (keys.has(key)) throw new Error('Duplicate JSON key');
        keys.add(key); space();
        if (text[at++] !== ':') throw new Error('Invalid JSON object');
        value(depth + 1); space();
        if (text[at] !== ',') break;
        at++; space();
      }
      if (text[at++] !== '}') throw new Error('Invalid JSON object');
    } else if (text[at] === '[') {
      at++; space();
      if (text[at] !== ']') while (true) {
        value(depth + 1); space();
        if (text[at] !== ',') break;
        at++;
      }
      if (text[at++] !== ']') throw new Error('Invalid JSON array');
    } else if (text[at] === '"') string();
    else {
      const start = at;
      while (at < text.length && !/[\t\n\r ,}\]]/.test(text[at])) at++;
      if (!/^(?:null|true|false|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)$/.test(text.slice(start, at))) throw new Error('Invalid JSON value');
    }
  };
  value(0); space();
  if (at !== text.length) throw new Error('Expected one JSON document');
  return JSON.parse(text);
}

export function exactFields(object, required, optional = []) {
  return object !== null && typeof object === 'object' && !Array.isArray(object) &&
    required.every(key => Object.hasOwn(object, key)) &&
    Object.keys(object).every(key => required.includes(key) || optional.includes(key));
}
