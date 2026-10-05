import { createRequire } from 'node:module';
import { readFileSync, mkdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const require = createRequire(path.resolve(here, '../../web/classic/package.json'));
const { build } = require('esbuild');
const outdir = path.resolve(process.argv[2] || 'E:/tmp/newapi-codex-setup-20261005/addon');
mkdirSync(outdir, { recursive: true });
const namespace = process.argv[3] || 'codex-setup-20261005';
if (!/^[a-z0-9-]+$/.test(namespace)) throw new Error('Invalid asset namespace');
const ns = `/assets/${namespace}`;
const core = `${ns}/react-core-D-iPSUlg.js`;
const shims = {
  react: `import {r as React} from '${core}'; export default React; export const {Children,Component,PureComponent,Fragment,StrictMode,Suspense,Profiler,createElement,cloneElement,isValidElement,createContext,createRef,forwardRef,lazy,memo,useCallback,useContext,useDebugValue,useDeferredValue,useEffect,useId,useImperativeHandle,useInsertionEffect,useLayoutEffect,useMemo,useReducer,useRef,useState,useSyncExternalStore,useTransition,startTransition,version,__SECRET_INTERNALS_DO_NOT_USE_OR_YOU_WILL_BE_FIRED}=React;`,
  'react-dom': `import {a as dom} from '${core}'; export default dom; export const {createPortal,createRoot,findDOMNode,flushSync,hydrate,hydrateRoot,render,unmountComponentAtNode,unstable_batchedUpdates,unstable_renderSubtreeIntoContainer,version,__SECRET_INTERNALS_DO_NOT_USE_OR_YOU_WILL_BE_FIRED}=dom;`,
  'react-dom/client': `export {createRoot,hydrateRoot} from 'react-dom';`,
  'react/jsx-runtime': `import React from 'react'; export const Fragment=React.Fragment; export const jsx=(type,props,key)=>React.createElement(type,key===undefined?props:{...props,key}); export const jsxs=jsx;`,
  'react-i18next': `export {u as useTranslation} from '${ns}/i18n-RrXBN1CV.js';`,
  '@newapi/production-api': `export {v as API} from '${ns}/index-luna-0216aa8f9328.js';`,
};
await build({
  entryPoints: { 'codex-setup-entry': path.join(here, 'production-entry.jsx') },
  outdir, bundle: true, splitting: true, format: 'esm', platform: 'browser', target: 'es2020',
  minify: true, metafile: true, chunkNames: 'codex-setup-[hash]',
  define: { 'process.env.NODE_ENV': '"production"' },
  // Production already has the complete Semi CSS. Do not replace global styles.
  loader: { '.css': 'empty', '.scss': 'empty' },
  plugins: [{ name: 'reuse-production-runtime', setup(api) {
    api.onResolve({ filter: /^\/assets\// }, (args) => ({ path: args.path, external: true }));
    api.onResolve({ filter: /^(react|react-dom|react-i18next|@newapi\/production-api)(\/.*)?$/ }, (args) => {
      if (shims[args.path]) return { path: args.path, namespace: 'production-shim' };
    });
    api.onLoad({ filter: /.*/, namespace: 'production-shim' }, (args) => ({ contents: shims[args.path], loader: 'js', resolveDir: here }));
    api.onResolve({ filter: /\?raw$/ }, (args) => ({ path: path.resolve(args.resolveDir, args.path.slice(0, -4)), namespace: 'raw-template' }));
    api.onLoad({ filter: /.*/, namespace: 'raw-template' }, (args) => ({ contents: `export default ${JSON.stringify(readFileSync(args.path, 'utf8'))}`, loader: 'js' }));
  }}],
}).then(async (result) => {
  const { writeFileSync } = await import('node:fs');
  writeFileSync(path.join(outdir, 'build-meta.json'), JSON.stringify(result.metafile, null, 2));
  console.log('Production addon built:', Object.keys(result.metafile.outputs).map((name) => path.basename(name)).join(', '));
});
