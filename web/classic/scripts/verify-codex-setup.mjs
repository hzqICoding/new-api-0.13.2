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
  requireSetupSuccess,
  setupError,
  setupErrorMessage,
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

const displayedError = (action, fallback) => {
  let thrown = false;
  let failure;
  try {
    action();
  } catch (error) {
    thrown = true;
    failure = error;
  }
  assert.ok(thrown, 'expected a setup error');
  return setupErrorMessage(failure, fallback);
};

test('known metadata reason codes retain distinct safe actionable messages on load and export refresh', () => {
  const expected = {
    token_disabled: /未处于启用状态/,
    token_expired: /已到期.*有效期/,
    token_exhausted: /额度已耗尽/,
    user_disabled: /账号已停用/,
    invalid_server_address: /服务地址配置无效/,
    group_unavailable: /分组暂不可用/,
    group_forbidden: /分组不在当前账号可用范围内.*编辑令牌.*恢复对应订阅/,
    group_retired: /分组已停用或删除/,
    no_compatible_model: /没有本向导支持的 Codex 模型/,
  };
  for (const [code, pattern] of Object.entries(expected)) {
    const data = {
      ...metadata,
      configurable: false,
      reason_code: code,
      reason: `<script>${fakeKey}</script>`,
    };
    for (const fallback of ['metadata_unavailable', 'export_failed']) {
      const message = displayedError(
        () =>
          validateMetadata(requireSetupSuccess({ success: true, data }), 27),
        fallback,
      );
      assert.match(message, pattern, code);
      assert.ok(!message.includes(fakeKey) && !message.includes('<script>'));
    }
  }
  const knownEnvelope = displayedError(() =>
    requireSetupSuccess({
      success: false,
      reason_code: 'group_forbidden',
      message: fakeKey,
    }),
  );
  assert.match(knownEnvelope, expected.group_forbidden);
  const unknown = displayedError(() =>
    validateMetadata(
      {
        ...metadata,
        configurable: false,
        reason_code: fakeKey,
        reason: fakeKey,
      },
      27,
    ),
  );
  assert.match(unknown, /暂时无法读取/);
  assert.ok(!unknown.includes(fakeKey));
});

test('HTTP, network and timeout failures are distinguished without trusting Axios or response text', () => {
  const expected = {
    401: /登录已过期/,
    403: /无权读取/,
    404: /不存在/,
    429: /限流/,
    500: /服务器暂时异常/,
    503: /服务器暂时异常/,
  };
  for (const [status, pattern] of Object.entries(expected)) {
    const error = {
      isAxiosError: true,
      message: fakeKey,
      response: {
        status: Number(status),
        data: { message: fakeKey, reason: fakeKey },
      },
      config: { headers: { Authorization: fakeKey } },
    };
    for (const fallback of ['metadata_unavailable', 'export_failed']) {
      const message = setupErrorMessage(error, fallback);
      assert.match(message, pattern);
      assert.ok(!message.includes(fakeKey));
    }
  }
  assert.match(
    setupErrorMessage({ code: 'ERR_NETWORK', message: fakeKey }),
    /网络连接失败/,
  );
  assert.match(
    setupErrorMessage({ code: 'ECONNABORTED', message: fakeKey }),
    /请求超时/,
  );
  assert.match(
    setupErrorMessage({ isAxiosError: true, request: {}, message: fakeKey }),
    /网络连接失败/,
  );
  assert.match(
    setupErrorMessage(new Error(fakeKey), 'export_failed'),
    /文件导出未完成/,
  );
  assert.match(
    displayedError(() =>
      requireSetupSuccess(
        { success: false, message: fakeKey },
        'reveal_failed',
      ),
    ),
    /无法读取.*完整密钥/,
  );
  assert.ok(
    !setupErrorMessage({
      name: 'SafeSetupError',
      code: fakeKey,
      message: fakeKey,
    }).includes(fakeKey),
  );
});

test('local schema, URL, selection and credential failures survive safe UI formatting', () => {
  assert.match(
    displayedError(() =>
      validateMetadata({ ...metadata, template_version: 'unknown' }, 27),
    ),
    /模板版本不兼容/,
  );
  assert.match(
    displayedError(() => validateMetadata(metadata, 28)),
    /令牌与当前选择不一致/,
  );
  assert.match(
    displayedError(() =>
      validateMetadata({ ...metadata, supported_targets: null }, 27),
    ),
    /结构不完整/,
  );
  assert.match(
    displayedError(() => normalizeEndpoint('', 'invalid-origin')),
    /当前页面不是可用的 HTTPS/,
  );
  assert.match(
    displayedError(() => normalizeEndpoint('https://api.test:99999/v1')),
    /服务地址配置无效/,
  );
  assert.match(
    displayedError(() =>
      renderConfig(metadata, { target: 'linux', model: 'not-supported' }),
    ),
    /所选系统或模型已不可用/,
  );
  assert.match(
    displayedError(() => normalizeApiKey(fakeKey + '\n')),
    /未取得有效的完整密钥/,
  );
  const error = setupError('endpoint_changed');
  error.message = fakeKey;
  assert.match(setupErrorMessage(error, 'export_failed'), /接入地址已变化/);
  error.code = '__proto__';
  assert.equal(typeof setupErrorMessage(error), 'string');
  assert.ok(!setupErrorMessage(error).includes(fakeKey));
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
