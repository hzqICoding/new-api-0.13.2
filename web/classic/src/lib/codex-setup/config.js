// new-api Codex export: metadata and credentials are data, never executable code.
export const TEMPLATE_VERSION = 'newapi-codex-v1';
export const MODELS = [
  'gpt-5.6-sol',
  'gpt-6-sol',
  'gpt-6-astra',
  'gpt-6-luna',
  'gpt-6.1-sol',
];
export const TARGETS = ['windows', 'macos', 'linux', 'wsl'];
const ENDPOINT =
  /^https:\/\/(?:[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?|\[[0-9A-Fa-f:]+\])(?::[0-9]{1,5})?(?:\/[A-Za-z0-9._~-]+)*\/v1$/;

export function normalizeEndpoint(value, origin) {
  if (
    value === '' &&
    (typeof origin !== 'string' ||
      new URL(origin).origin !== origin ||
      !origin.startsWith('https://'))
  ) {
    throw new Error('当前页面不是可用的 HTTPS 服务地址，请联系管理员。');
  }
  const candidate = value === '' ? `${origin || ''}/v1` : value;
  if (
    typeof candidate !== 'string' ||
    candidate.length > 1024 ||
    /[^\x21-\x7e]/.test(candidate) ||
    !ENDPOINT.test(candidate) ||
    /\/\.\.?(\/|$)/.test(candidate)
  ) {
    throw new Error('管理员的服务地址配置无效，必须使用 HTTPS 地址。');
  }
  const url = new URL(candidate);
  if (
    url.protocol !== 'https:' ||
    !url.hostname ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    (url.port && Number(url.port) === 0)
  ) {
    throw new Error('管理员的服务地址配置无效，必须使用 HTTPS 地址。');
  }
  return url.href;
}

export function normalizeApiKey(raw) {
  if (typeof raw !== 'string') throw new Error('未取得有效完整密钥，请重试。');
  const key = raw.startsWith('sk-') ? raw : `sk-${raw}`;
  if (
    /[^\x21-\x7e]/.test(key) ||
    !/^sk-[A-Za-z0-9_-]{16,253}$/.test(key) ||
    key.startsWith('sk-sk-')
  )
    throw new Error('未取得有效完整密钥，请重试。');
  return key;
}

export function validateMetadata(metadata, tokenId, origin) {
  if (
    metadata?.schema_version !== 1 ||
    metadata?.client !== 'codex' ||
    metadata?.template_version !== TEMPLATE_VERSION ||
    metadata?.token_id !== tokenId
  ) {
    throw new Error('配置模板或令牌归属不匹配，请刷新页面重试。');
  }
  if (!metadata.configurable)
    throw new Error(
      '当前令牌暂不能配置 Codex，请检查状态、模型权限或服务地址。',
    );
  const base_url = normalizeEndpoint(metadata.base_url, origin);
  const models = (Array.isArray(metadata.models) ? metadata.models : []).filter(
    (item) =>
      MODELS.includes(item.id) &&
      Array.isArray(item.reasoning_efforts) &&
      item.reasoning_efforts.includes('low'),
  );
  if (!models.length) throw new Error('当前令牌没有已验证的 Codex 模型。');
  const supported_targets = (metadata.supported_targets || []).filter(
    (target) => TARGETS.includes(target),
  );
  if (!supported_targets.length)
    throw new Error('当前配置没有可用的运行系统。');
  return {
    ...metadata,
    base_url,
    models,
    supported_targets,
    default_model: models.some((m) => m.id === metadata.default_model)
      ? metadata.default_model
      : null,
  };
}

export function renderConfig(metadata, { target, model }) {
  if (
    !TARGETS.includes(target) ||
    !metadata.supported_targets.includes(target) ||
    !MODELS.includes(model) ||
    !metadata.models.some(
      (item) => item.id === model && item.reasoning_efforts.includes('low'),
    )
  ) {
    throw new Error('请选择当前令牌支持的运行系统和模型。');
  }
  const endpoint = normalizeEndpoint(metadata.base_url);
  return [
    `# ${TEMPLATE_VERSION}`,
    'model_provider = "new-api"',
    `model = "${model}"`,
    'model_reasoning_effort = "low"',
    'approval_policy = "on-request"',
    'sandbox_mode = "workspace-write"',
    'cli_auth_credentials_store = "file"',
    'forced_login_method = "api"',
    '',
    '[model_providers.new-api]',
    'name = "new-api"',
    `base_url = "${endpoint}"`,
    'wire_api = "responses"',
    'requires_openai_auth = true',
    '',
  ].join('\n');
}

export function renderAuth(rawKey) {
  return (
    JSON.stringify(
      { OPENAI_API_KEY: normalizeApiKey(rawKey), auth_mode: 'apikey' },
      null,
      2,
    ) + '\n'
  );
}

export function suggestTarget() {
  const source =
    typeof navigator === 'undefined'
      ? ''
      : `${navigator.userAgentData?.platform || ''} ${navigator.platform || ''}`;
  return /Win/i.test(source)
    ? 'windows'
    : /Mac/i.test(source)
      ? 'macos'
      : 'linux';
}
