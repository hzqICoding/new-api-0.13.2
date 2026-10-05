import assert from 'node:assert/strict';
import { createHash, webcrypto } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import { unzipSync, strFromU8 } from 'fflate';
import {
  normalizeApiKey,
  normalizeEndpoint,
  renderConfig,
  renderAuth,
  validateMetadata,
} from '../src/lib/codex-setup/config.js';
import {
  createSetupEpoch,
  isSetupTicketCurrent,
  normalizeTokenIds,
} from '../src/lib/codex-setup/session.js';

globalThis.crypto ||= webcrypto;
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const fakeKey = 'sk-newapi_FAKE_ONLY_12345678901234567890';
const metadata = {
  client: 'codex',
  schema_version: 1,
  template_version: 'newapi-codex-v1',
  token_id: 27,
  configurable: true,
  base_url: 'https://gateway.example.test/gateway/v1',
  default_model: 'gpt-5.6-sol',
  models: [
    {
      id: 'gpt-5.6-sol',
      reasoning_efforts: ['low'],
      default_reasoning_effort: 'low',
    },
  ],
  supported_targets: ['windows', 'macos', 'linux', 'wsl'],
  notices: [],
};
let count = 0;
function test(name, run) {
  run();
  count += 1;
  console.log(`PASS ${name}`);
}
test('prefix normalization is idempotent and rejects masked/injected values', () => {
  assert.equal(normalizeApiKey(fakeKey), fakeKey);
  assert.equal(normalizeApiKey(fakeKey.slice(3)), fakeKey);
  for (const value of [
    '',
    'sk-********',
    'sk-sk-' + fakeKey,
    fakeKey + '\n',
    'x\n$(touch pwn)',
    'x"key',
  ])
    assert.throws(() => normalizeApiKey(value));
});
test('administrator endpoint supports HTTPS host/port/subpath/IPv6', () => {
  for (const url of [
    metadata.base_url,
    'https://api.example.test:8443/v1',
    'https://[::1]:8443/v1',
  ])
    assert.equal(normalizeEndpoint(url), url);
  assert.equal(
    normalizeEndpoint('', 'https://browser.example.test'),
    'https://browser.example.test/v1',
  );
});
test('endpoint rejects controls, shell/TOML syntax, userinfo, query, fragments and dot paths', () => {
  for (const url of [
    'http://api.test/v1',
    'https://u:p@api.test/v1',
    'https://api.test/v1?a=1',
    'https://api.test/v1#x',
    'https://api.test/../v1',
    'https://api.test/$x/v1',
    'https://api.test/"x/v1',
    'https://api.test/$(id)/v1',
    'https://api.test\\evil/v1',
    'https://api.test:99999/v1',
    'https://api.test/v1\n',
    'https://api.test/v1\t',
  ])
    assert.throws(() => normalizeEndpoint(url), url);
  assert.throws(() => normalizeEndpoint('', 'http://browser.test'));
  assert.throws(() => normalizeEndpoint('', 'https://browser.test/path'));
});
test('metadata is bound to exact token/template and effective model intersection', () => {
  assert.equal(validateMetadata(metadata, 27).models.length, 1);
  assert.throws(() => validateMetadata(metadata, 28));
  assert.throws(() =>
    validateMetadata({ ...metadata, configurable: false }, 27),
  );
  assert.throws(() =>
    validateMetadata({ ...metadata, template_version: 'unknown' }, 27),
  );
  assert.throws(() =>
    validateMetadata(
      { ...metadata, models: [{ id: 'evil', reasoning_efforts: ['low'] }] },
      27,
    ),
  );
  assert.throws(() =>
    renderConfig(metadata, { target: 'linux', model: 'gpt-6-astra' }),
  );
  assert.equal(
    validateMetadata({ ...metadata, default_model: 'unknown' }, 27)
      .default_model,
    null,
  );
});
test('TOML/auth are separate with exact provider and raw key round-trip', () => {
  const config = renderConfig(metadata, {
    target: 'linux',
    model: 'gpt-5.6-sol',
  });
  assert.match(config, /model_provider = "new-api"/);
  assert.match(config, /\[model_providers.new-api\]/);
  assert.ok(!config.includes(fakeKey));
  assert.equal(JSON.parse(renderAuth(fakeKey)).OPENAI_API_KEY, fakeKey);
});
test('token IDs never inferred from array order or names; epoch cancels delayed A-B-A work', () => {
  assert.deepEqual(normalizeTokenIds([27, 28, 27, '29', -1, null]), [27, 28]);
  const guard = createSetupEpoch();
  const a = guard.begin();
  const b = guard.begin();
  const a2 = guard.begin();
  assert.equal(guard.current(a), false);
  assert.equal(guard.current(b), false);
  assert.equal(guard.current(a2), true);
  assert.equal(a.signal.aborted, true);
  guard.invalidate();
  assert.equal(guard.current(a2), false);
});

test('identity changed after reveal during ZIP generation blocks save before React props update', () => {
  const guard = createSetupEpoch();
  const ticket = { ...guard.begin(), tokenId: 27, userIdentity: 'owner-A' };
  const staleReactProps = {
    visible: true,
    tokenId: 27,
    userIdentity: 'owner-A',
  };
  let liveIdentity = 'owner-A';
  const externalGuard = () => liveIdentity === ticket.userIdentity;
  assert.equal(
    isSetupTicketCurrent(guard, ticket, staleReactProps, externalGuard),
    true,
  );
  // API/reveal has completed; an import/digest is still pending when logout or
  // a same-tab account switch occurs. Props and the polling tick are unchanged.
  liveIdentity = 'owner-B';
  assert.equal(guard.current(ticket), true);
  assert.equal(
    isSetupTicketCurrent(guard, ticket, staleReactProps, externalGuard),
    false,
  );
  assert.equal(
    isSetupTicketCurrent(guard, ticket, staleReactProps, () => {
      throw new Error('session unavailable');
    }),
    false,
  );
});

const rawPlugin = {
  name: 'fixed-script-raw',
  setup(builder) {
    builder.onResolve({ filter: /\?raw$/ }, (args) => ({
      path: path.resolve(args.resolveDir, args.path.slice(0, -4)),
      namespace: 'raw',
    }));
    builder.onLoad({ filter: /.*/, namespace: 'raw' }, async (args) => ({
      contents: await readFile(args.path, 'utf8'),
      loader: 'text',
    }));
  },
};
const compiled = await build({
  absWorkingDir: root,
  entryPoints: ['src/lib/codex-setup/bundle.js'],
  bundle: true,
  format: 'esm',
  platform: 'browser',
  write: false,
  plugins: [rawPlugin],
});
const generated = await import(
  `data:text/javascript;base64,${Buffer.from(compiled.outputFiles[0].text).toString('base64')}`
);
for (const target of metadata.supported_targets) {
  const { blob, filename } = await generated.buildCodexSetupZip({
    metadata,
    target,
    model: 'gpt-5.6-sol',
    rawKey: fakeKey,
    runCliTest: true,
  });
  const files = unzipSync(new Uint8Array(await blob.arrayBuffer()));
  const text = (name) => strFromU8(files[name]);
  const manifest = JSON.parse(text('payload/manifest.json'));
  assert.equal(manifest.template_version, 'newapi-codex-v1');
  assert.equal(manifest.base_url, metadata.base_url);
  for (const name of ['config.toml', 'auth.json'])
    assert.equal(
      createHash('sha256')
        .update(files['payload/' + name])
        .digest('hex'),
      manifest.files[name],
    );
  assert.equal(JSON.parse(text('payload/auth.json')).OPENAI_API_KEY, fakeKey);
  assert.equal(files['payload/config.toml'][0] === 0xef, false);
  assert.equal(files['payload/auth.json'][0] === 0xef, false);
  assert.match(
    text('payload/settings.tsv'),
    /template_version\tnewapi-codex-v1/,
  );
  assert.ok(!filename.includes(fakeKey));
  for (const [name, value] of Object.entries(files))
    if (name !== 'payload/auth.json')
      assert.ok(!strFromU8(value).includes(fakeKey), name);
  if (target === 'windows') {
    assert.deepEqual(
      Array.from(files['setup.ps1'].slice(0, 3)),
      [0xef, 0xbb, 0xbf],
    );
    assert.match(text('setup.ps1'), /function Test-Endpoint/);
    assert.match(text('setup.cmd'), /DisableDelayedExpansion/);
  } else {
    const script = text(target === 'macos' ? 'setup.command' : 'setup.sh');
    assert.ok(script.startsWith('#!/usr/bin/env bash\n'));
    assert.ok(!script.includes('api.dm.1aicode.space'));
    assert.match(script, /newapi-codex-v1/);
  }
  count += 1;
  console.log(
    `PASS ${target} ZIP: payload hashes, no plaintext leaks, fixed entry, platform encoding`,
  );
}
// JSX and import graph verification, without building/replacing the production site.
await build({
  absWorkingDir: root,
  entryPoints: [
    'src/components/table/tokens/CodexSetupHost.jsx',
    'src/components/table/tokens/index.jsx',
  ],
  bundle: true,
  format: 'esm',
  platform: 'browser',
  write: false,
  outdir: 'unused-verification-output',
  packages: 'external',
  plugins: [rawPlugin],
  loader: { '.svg': 'dataurl', '.png': 'dataurl' },
  logLevel: 'silent',
});
count += 1;
console.log('PASS JSX integration modules compile');
console.log(
  `Verified ${count} groups. Fake key only; no real network or CLI invocation.`,
);

const bash = [
  'E:/SoftWare/Git/bin/bash.exe',
  'C:/Program Files/Git/bin/bash.exe',
  '/bin/bash',
].find(existsSync);
if (bash) {
  const source = await readFile(
    path.join(root, 'src/lib/codex-setup/templates/setup.sh'),
    'utf8',
  );
  const start = source.indexOf('  [[ "$base_url"');
  const end = source.indexOf('  case "$run_cli_test"', start);
  assert.ok(start > 0 && end > start);
  const predicate =
    'set -e; die(){ exit 2; }; base_url=$SETUP_URL_TEST;\n' +
    source.slice(start, end);
  const valid = [
    'https://api.example.test/v1',
    'https://api.example.test:8443/proxy/v1',
    'https://[::1]:8443/v1',
  ];
  const invalid = [
    'http://api.example.test/v1',
    'https://u:p@api.example.test/v1',
    'https://api.example.test/v1?q=1',
    'https://api.example.test/v1#x',
    'https://api.example.test/../v1',
    'https://api.example.test/$(whoami)/v1',
    'https://api.example.test/$name/v1',
    'https://api.example.test/"x/v1',
    'https://api.example.test:99999/v1',
    'https://api.example.test:0/v1',
    'https://api.example.test/v1\n',
  ];
  for (const url of valid)
    assert.equal(
      spawnSync(
        bash,
        ['--noprofile', '--norc', '-c', predicate, 'predicate-test'],
        { encoding: 'utf8', env: { ...process.env, SETUP_URL_TEST: url } },
      ).status,
      0,
      'valid POSIX URL',
    );
  for (const url of invalid)
    assert.notEqual(
      spawnSync(
        bash,
        ['--noprofile', '--norc', '-c', predicate, 'predicate-test'],
        { encoding: 'utf8', env: { ...process.env, SETUP_URL_TEST: url } },
      ).status,
      0,
      'invalid POSIX URL: ' + JSON.stringify(url),
    );
  console.log(
    'PASS Bash URL predicate: 14 cases; no setup, network or real CLI executed.',
  );
} else {
  console.log('SKIPPED Bash URL predicate: bash unavailable.');
}
