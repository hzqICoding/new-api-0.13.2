#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';

function fail(message) {
  console.error(`error: ${message}`);
  process.exit(2);
}

const [distArg, ...entryArgs] = process.argv.slice(2);
if (!distArg || entryArgs.length === 0) {
  fail('usage: node --experimental-vm-modules audit-vm-link.mjs <dist-dir> <entry>...');
}
if (typeof vm.SourceTextModule !== 'function') {
  fail('vm.SourceTextModule unavailable; run node with --experimental-vm-modules');
}

const root = path.resolve(distArg);
const context = vm.createContext({});
const modules = new Map();

function insideRoot(file) {
  const relative = path.relative(root, file);
  return relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative);
}

function resolveLocal(specifier, importer) {
  const clean = specifier.replace(/[?#].*$/, '');
  let target;
  if (clean.startsWith('./') || clean.startsWith('../')) {
    target = path.resolve(path.dirname(importer), clean);
  } else if (clean.startsWith('/')) {
    target = path.resolve(root, clean.replace(/^\/+/, ''));
  } else {
    throw new Error(`unsupported non-local static import ${JSON.stringify(specifier)} from ${importer}`);
  }
  if (!insideRoot(target)) throw new Error(`import escapes dist root: ${specifier} from ${importer}`);
  if (!fs.existsSync(target)) throw new Error(`missing module: ${target} imported from ${importer}`);
  if (!/\.(?:m?js)$/i.test(target)) throw new Error(`non-JavaScript static import: ${target}`);
  return target;
}

function getModule(file) {
  const normalized = path.normalize(file);
  if (modules.has(normalized)) return modules.get(normalized);
  const source = fs.readFileSync(normalized, 'utf8');
  const module = new vm.SourceTextModule(source, {
    context,
    identifier: normalized,
    initializeImportMeta(meta) {
      meta.url = new URL(`file:///${normalized.replace(/\\/g, '/')}`).href;
    },
    importModuleDynamically(specifier, referencingModule) {
      return getModule(resolveLocal(specifier, referencingModule.identifier));
    },
  });
  modules.set(normalized, module);
  return module;
}

for (const entryArg of entryArgs) {
  const entry = path.resolve(root, entryArg);
  if (!insideRoot(entry) || !fs.existsSync(entry)) fail(`entry not found under dist: ${entryArg}`);
  const before = modules.size;
  const module = getModule(entry);
  await module.link((specifier, referencingModule) =>
    getModule(resolveLocal(specifier, referencingModule.identifier)),
  );
  const linked = modules.size - before;
  console.log(`LINK_OK ${entryArg} modules=${linked} status=${module.status}`);
}

console.log(`TOTAL_MODULES=${modules.size}`);
