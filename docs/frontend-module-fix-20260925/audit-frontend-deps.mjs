#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';

const STATIC_EDGE_TYPES = new Set([
  'static-import',
  're-export',
  'html-script',
  'html-style',
  'css-import',
  'css-url',
]);

function usage(message) {
  if (message) console.error(`error: ${message}\n`);
  console.error(
    'usage: node audit-frontend-deps.mjs <dist-dir> [--entry <relative-path>]... [--json]\n' +
      'example: node audit-frontend-deps.mjs web/classic/dist --entry index.html --json',
  );
  process.exit(message ? 2 : 0);
}

const argv = process.argv.slice(2);
if (argv.length === 0 || argv.includes('--help') || argv.includes('-h')) usage();

const rootArg = argv.shift();
const entryArgs = [];
let jsonOnly = false;
while (argv.length > 0) {
  const arg = argv.shift();
  if (arg === '--entry') {
    if (argv.length === 0) usage('--entry requires a path');
    entryArgs.push(argv.shift());
  } else if (arg === '--json') {
    jsonOnly = true;
  } else {
    usage(`unknown argument: ${arg}`);
  }
}

const root = path.resolve(rootArg);
if (!fs.existsSync(root) || !fs.statSync(root).isDirectory()) {
  usage(`dist directory does not exist: ${root}`);
}

const toPosix = (value) => value.split(path.sep).join('/');
const rel = (value) => toPosix(path.relative(root, value));

function walk(dir) {
  const files = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) files.push(...walk(full));
    else if (entry.isFile()) files.push(full);
  }
  return files;
}

function decodeQuoted(raw, quote) {
  try {
    if (quote === '"') return JSON.parse(`"${raw.replace(/"/g, '\\"')}"`);
  } catch {}
  return raw
    .replace(/\\(['"\\])/g, '$1')
    .replace(/\\n/g, '\n')
    .replace(/\\r/g, '\r')
    .replace(/\\t/g, '\t');
}

function resolveSpecifier(fromFile, specifier) {
  const clean = specifier.replace(/[?#].*$/, '');
  if (!clean || /^(?:[a-z][a-z\d+.-]*:|#)/i.test(clean)) return null;

  let resolved;
  if (clean.startsWith('/')) resolved = path.join(root, clean.replace(/^\/+/, ''));
  else if (clean.startsWith('./') || clean.startsWith('../')) {
    resolved = path.resolve(path.dirname(fromFile), clean);
  } else if (clean.startsWith('assets/')) {
    resolved = path.join(root, clean);
  } else {
    return null;
  }

  return path.normalize(resolved);
}

function lineAt(source, index) {
  let line = 1;
  for (let i = 0; i < index; i += 1) if (source.charCodeAt(i) === 10) line += 1;
  return line;
}

function quotedValues(source) {
  const values = [];
  const regex = /(["'])((?:\\.|(?!\1)[^\\\r\n])*)\1/g;
  let match;
  while ((match = regex.exec(source))) {
    values.push({ value: decodeQuoted(match[2], match[1]), index: match.index });
  }
  return values;
}

function commaNames(raw, exportedSide = false) {
  return raw
    .split(',')
    .map((part) => part.trim().replace(/^type\s+/, ''))
    .filter(Boolean)
    .map((part) => {
      const pieces = part.split(/\s+as\s+/);
      return exportedSide ? pieces.at(-1).trim() : pieces[0].trim();
    })
    .filter((name) => /^[A-Za-z_$][\w$]*$/.test(name));
}

function scanModuleContracts(source) {
  const imports = [];
  const directExports = new Set();
  const starExports = [];
  const namedReExports = [];
  let match;

  const namedImport = /\bimport\s*\{([^}]*)\}\s*from\s*(["'])((?:\\.|(?!\2)[^\\\r\n])*)\2/g;
  while ((match = namedImport.exec(source))) {
    imports.push({ specifier: decodeQuoted(match[3], match[2]), names: commaNames(match[1]), index: match.index });
  }
  const defaultImport = /\bimport\s+([A-Za-z_$][\w$]*)\s*(?:,\s*(?:\{[^}]*\}|\*\s+as\s+[A-Za-z_$][\w$]*))?\s+from\s*(["'])((?:\\.|(?!\2)[^\\\r\n])*)\2/g;
  while ((match = defaultImport.exec(source))) {
    imports.push({ specifier: decodeQuoted(match[3], match[2]), names: ['default'], index: match.index });
  }

  const exportList = /\bexport\s*\{([^}]*)\}(?:\s*from\s*(["'])((?:\\.|(?!\2)[^\\\r\n])*)\2)?/g;
  while ((match = exportList.exec(source))) {
    const names = commaNames(match[1], true);
    if (match[3]) namedReExports.push({ specifier: decodeQuoted(match[3], match[2]), names });
    else for (const name of names) directExports.add(name);
  }
  const starExport = /\bexport\s*\*\s*from\s*(["'])((?:\\.|(?!\1)[^\\\r\n])*)\1/g;
  while ((match = starExport.exec(source))) starExports.push(decodeQuoted(match[2], match[1]));
  if (/\bexport\s+default\b/.test(source)) directExports.add('default');
  const declarationExport = /\bexport\s+(?:const|let|var|function|class)\s+([A-Za-z_$][\w$]*)/g;
  while ((match = declarationExport.exec(source))) directExports.add(match[1]);

  return { imports, directExports: [...directExports], starExports, namedReExports };
}

function scanJs(file, source) {
  const refs = [];
  const addMatches = (type, regex, valueGroup = 2) => {
    let match;
    while ((match = regex.exec(source))) {
      refs.push({ type, specifier: decodeQuoted(match[valueGroup], match[1]), index: match.index });
    }
  };

  addMatches(
    'static-import',
    /\bimport\s*(?!\s*\()(?:[^;"']*?\bfrom\s*)?(["'])((?:\\.|(?!\1)[^\\\r\n])*)\1/g,
  );
  addMatches(
    're-export',
    /\bexport\s+(?:\*|\{[^}]*\})\s+from\s*(["'])((?:\\.|(?!\1)[^\\\r\n])*)\1/g,
  );
  addMatches(
    'dynamic-import',
    /\bimport\s*\(\s*(["'])((?:\\.|(?!\1)[^\\\r\n])*)\1\s*\)/g,
  );

  const mapMatch = /m\.f\s*\|\|\s*\(m\.f\s*=\s*\[([\s\S]*?)\]\)/.exec(source);
  const viteMap = mapMatch ? quotedValues(mapMatch[1]).map((item) => item.value) : [];
  const vitePreloadCalls = [];
  const preloadCall = /import\s*\(\s*(["'])((?:\\.|(?!\1)[^\\\r\n])*)\1\s*\)\s*,\s*__vite__mapDeps\s*\(\s*\[([\d,\s]+)\]\s*\)/g;
  let preloadMatch;
  while ((preloadMatch = preloadCall.exec(source))) {
    const target = decodeQuoted(preloadMatch[2], preloadMatch[1]);
    const indexes = preloadMatch[3]
      .split(',')
      .map((value) => Number(value.trim()))
      .filter(Number.isInteger);
    const dependencies = indexes.map((index) => viteMap[index] ?? `<invalid-map-index:${index}>`);
    vitePreloadCalls.push({ target, indexes, dependencies, index: preloadMatch.index });
  }

  return { refs, viteMap, vitePreloadCalls, contracts: scanModuleContracts(source) };
}

function scanHtml(source) {
  const refs = [];
  const tagRegex = /<(script|link)\b([^>]*)>/gi;
  let tag;
  while ((tag = tagRegex.exec(source))) {
    const attrs = tag[2];
    const url = /\b(?:src|href)\s*=\s*(["'])((?:\\.|(?!\1)[^\\\r\n])*)\1/i.exec(attrs);
    if (!url) continue;
    const isStyle = tag[1].toLowerCase() === 'link' && /\brel\s*=\s*["']stylesheet["']/i.test(attrs);
    const type = tag[1].toLowerCase() === 'script' ? 'html-script' : isStyle ? 'html-style' : 'html-asset';
    refs.push({ type, specifier: decodeQuoted(url[2], url[1]), index: tag.index });
  }
  return { refs, viteMap: [], vitePreloadCalls: [], contracts: null };
}

function scanCss(source) {
  const refs = [];
  let match;
  const importRegex = /@import\s+(?:url\(\s*)?(["'])((?:\\.|(?!\1)[^\\\r\n])*)\1\s*\)?/gi;
  while ((match = importRegex.exec(source))) {
    refs.push({ type: 'css-import', specifier: decodeQuoted(match[2], match[1]), index: match.index });
  }
  const urlRegex = /url\(\s*(?:(['"])((?:\\.|(?!\1)[^\\\r\n])*)\1|([^)'"\s][^)]*?))\s*\)/gi;
  while ((match = urlRegex.exec(source))) {
    refs.push({ type: 'css-url', specifier: match[2] ? decodeQuoted(match[2], match[1]) : match[3].trim(), index: match.index });
  }
  return { refs, viteMap: [], vitePreloadCalls: [], contracts: null };
}

const allFiles = walk(root);
const fileSet = new Set(allFiles.map((file) => path.normalize(file)));
const scanned = new Map();
const references = [];
const vitePreloadCalls = [];

for (const file of allFiles) {
  const extension = path.extname(file).toLowerCase();
  if (!['.js', '.mjs', '.html', '.css'].includes(extension)) continue;
  const source = fs.readFileSync(file, 'utf8');
  const result = extension === '.html' ? scanHtml(source) : extension === '.css' ? scanCss(source) : scanJs(file, source);
  scanned.set(file, result);

  for (const item of result.refs) {
    const resolved = resolveSpecifier(file, item.specifier);
    if (!resolved) continue;
    references.push({
      from: rel(file),
      type: item.type,
      specifier: item.specifier,
      to: rel(resolved),
      exists: fileSet.has(resolved),
      line: lineAt(source, item.index),
    });
  }

  for (const specifier of result.viteMap) {
    const resolved = resolveSpecifier(file, specifier);
    if (!resolved) continue;
    references.push({
      from: rel(file),
      type: 'vite-map-entry',
      specifier,
      to: rel(resolved),
      exists: fileSet.has(resolved),
      line: 1,
    });
  }

  for (const call of result.vitePreloadCalls) {
    vitePreloadCalls.push({
      from: rel(file),
      target: call.target,
      targetExists: (() => {
        const resolved = resolveSpecifier(file, call.target);
        return resolved ? fileSet.has(resolved) : null;
      })(),
      line: lineAt(source, call.index),
      dependencies: call.dependencies.map((specifier, index) => {
        const resolved = specifier.startsWith('<invalid-') ? null : resolveSpecifier(file, specifier);
        return {
          mapIndex: call.indexes[index],
          specifier,
          to: resolved ? rel(resolved) : null,
          exists: resolved ? fileSet.has(resolved) : false,
        };
      }),
    });
  }
}

function resolveEntry(arg) {
  const direct = path.normalize(path.resolve(root, arg));
  if (fileSet.has(direct)) return direct;
  const matches = allFiles.filter((file) => path.basename(file) === arg);
  if (matches.length === 1) return matches[0];
  if (matches.length > 1) usage(`entry basename is ambiguous: ${arg}`);
  usage(`entry not found: ${arg}`);
}

const entries = (entryArgs.length > 0 ? entryArgs : ['index.html']).map(resolveEntry);
const staticAdjacency = new Map();
for (const reference of references) {
  if (!STATIC_EDGE_TYPES.has(reference.type)) continue;
  const from = path.normalize(path.join(root, reference.from));
  const to = path.normalize(path.join(root, reference.to));
  if (!staticAdjacency.has(from)) staticAdjacency.set(from, []);
  staticAdjacency.get(from).push({ to, reference });
}

function staticClosure(entry) {
  const visited = new Set();
  const stack = [entry];
  const missing = [];
  while (stack.length > 0) {
    const current = stack.pop();
    if (visited.has(current)) continue;
    visited.add(current);
    for (const edge of staticAdjacency.get(current) ?? []) {
      if (!edge.reference.exists) missing.push(edge.reference);
      else if (!visited.has(edge.to)) stack.push(edge.to);
    }
  }
  return { files: [...visited].map(rel).sort(), missing };
}

function findCycles(nodes) {
  const allowed = new Set(nodes.map((node) => path.normalize(path.join(root, node))));
  const state = new Map();
  const stack = [];
  const cycles = [];
  const seen = new Set();

  function visit(node) {
    state.set(node, 1);
    stack.push(node);
    for (const edge of staticAdjacency.get(node) ?? []) {
      if (!edge.reference.exists || !allowed.has(edge.to)) continue;
      if (!state.has(edge.to)) visit(edge.to);
      else if (state.get(edge.to) === 1) {
        const start = stack.indexOf(edge.to);
        const cycle = [...stack.slice(start), edge.to].map(rel);
        const key = [...new Set(cycle.slice(0, -1))].sort().join('|');
        if (!seen.has(key)) {
          seen.add(key);
          cycles.push(cycle);
        }
      }
    }
    stack.pop();
    state.set(node, 2);
  }

  for (const node of allowed) if (!state.has(node)) visit(node);
  return cycles;
}

const closures = Object.fromEntries(
  entries.map((entry) => {
    const closure = staticClosure(entry);
    return [rel(entry), { ...closure, cycles: findCycles(closure.files) }];
  }),
);

const missingReferences = references
  .filter((item) => !item.exists)
  .sort((a, b) => a.to.localeCompare(b.to) || a.from.localeCompare(b.from));

const exportSurfaces = new Map();
for (const [file, result] of scanned) {
  if (result.contracts) exportSurfaces.set(file, new Set(result.contracts.directExports));
}
let exportsChanged = true;
while (exportsChanged) {
  exportsChanged = false;
  for (const [file, result] of scanned) {
    if (!result.contracts) continue;
    const surface = exportSurfaces.get(file);
    for (const specifier of result.contracts.starExports) {
      const target = resolveSpecifier(file, specifier);
      for (const name of exportSurfaces.get(target) ?? []) {
        if (name === 'default' || surface.has(name)) continue;
        surface.add(name);
        exportsChanged = true;
      }
    }
    for (const item of result.contracts.namedReExports) {
      const target = resolveSpecifier(file, item.specifier);
      const targetSurface = exportSurfaces.get(target);
      if (!targetSurface) continue;
      for (const name of item.names) {
        if (!targetSurface.has(name) || surface.has(name)) continue;
        surface.add(name);
        exportsChanged = true;
      }
    }
  }
}

const unresolvedNamedImports = [];
for (const [file, result] of scanned) {
  if (!result.contracts) continue;
  for (const item of result.contracts.imports) {
    const target = resolveSpecifier(file, item.specifier);
    if (!target || !fileSet.has(target) || !exportSurfaces.has(target)) continue;
    const surface = exportSurfaces.get(target);
    const missingNames = item.names.filter((name) => !surface.has(name));
    if (missingNames.length > 0) {
      unresolvedNamedImports.push({
        from: rel(file),
        to: rel(target),
        specifier: item.specifier,
        missingNames,
      });
    }
  }
}
const report = {
  root,
  generatedAt: new Date().toISOString(),
  counts: {
    files: allFiles.length,
    scannedTextAssets: scanned.size,
    localReferences: references.length,
    missingReferences: missingReferences.length,
    missingTargets: new Set(missingReferences.map((item) => item.to)).size,
    vitePreloadCalls: vitePreloadCalls.length,
    unresolvedNamedImports: unresolvedNamedImports.length,
  },
  missingReferences,
  vitePreloadCalls,
  unresolvedNamedImports,
  closures,
};

if (jsonOnly) {
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
} else {
  console.log(`root: ${root}`);
  console.log(`files: ${report.counts.files}; local refs: ${report.counts.localReferences}`);
  console.log(`missing refs: ${report.counts.missingReferences}; missing targets: ${report.counts.missingTargets}`);
  console.log(`unresolved named imports: ${report.counts.unresolvedNamedImports}`);
  for (const [entry, closure] of Object.entries(closures)) {
    console.log(`entry ${entry}: ${closure.files.length} static files, ${closure.missing.length} missing refs, ${closure.cycles.length} cycles`);
    for (const item of closure.missing) console.log(`  MISSING ${item.from} -> ${item.to} (${item.type})`);
  }
  if (missingReferences.length > 0) {
    console.log('all missing local references:');
    for (const item of missingReferences) {
      console.log(`  ${item.to} <- ${item.from}:${item.line} [${item.type}] ${item.specifier}`);
    }
  }
}
