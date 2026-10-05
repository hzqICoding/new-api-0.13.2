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
const CF_ENDPOINT = 'https://api.1aicode.space/v1';
const DIRECT_ENDPOINT = 'https://api.dm.1aicode.space/v1';
const ENDPOINT =
  /^https:\/\/(?:[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?|\[[0-9A-Fa-f:]+\])(?::[0-9]{1,5})?(?:\/[A-Za-z0-9._~-]+)*\/v1$/;

// Only local, reviewed messages may reach the UI. Never display a server reason,
// Axios message, response body, URL parse exception, or credential value.
const SAFE_MESSAGES = Object.freeze({
  token_disabled:
    '当前令牌未处于启用状态。请在令牌列表检查并启用；若已到期或额度耗尽，请先处理对应限制。',
  token_expired: '此令牌已到期。请编辑令牌调整有效期后，重新打开配置向导。',
  token_exhausted:
    '此令牌的可用额度已耗尽。请检查令牌额度设置并补足可用额度后重试。',
  user_disabled: '当前账号已停用。请联系管理员恢复账号后重试。',
  invalid_server_address:
    '管理员的服务地址配置无效。请联系管理员设置规范的 HTTPS 接入地址后重试。',
  group_unavailable:
    '此令牌的分组暂不可用，或不在当前账号可用范围内。请编辑令牌选择可用分组，或联系管理员核对分组配置与授权。',
  group_forbidden:
    '此令牌指定的分组不在当前账号可用范围内。请编辑令牌选择可用分组，或恢复对应订阅后重试。',
  group_retired:
    '此令牌指定的分组已停用或删除。请编辑令牌选择仍然可用的分组，或联系管理员核对分组配置。',
  no_compatible_model:
    '此令牌的有效分组和模型限制下，没有本向导支持的 Codex 模型。请检查令牌模型限制，或联系管理员配置兼容模型。',
  unsupported_template:
    '配置模板版本不兼容。请刷新页面后重试；仍然失败请联系管理员更新配置模块。',
  token_mismatch:
    '配置返回的令牌与当前选择不一致。请关闭向导，从正确令牌的操作入口重新打开。',
  metadata_invalid:
    '配置接口返回的结构不完整。请刷新页面后重试；仍然失败请联系管理员。',
  invalid_origin:
    '当前页面不是可用的 HTTPS 服务地址。请使用本站 HTTPS 页面，或联系管理员设置服务地址。',
  no_supported_target:
    '当前配置没有本向导支持的运行系统。请刷新页面，或联系管理员更新配置模块。',
  invalid_selection:
    '所选系统或模型已不可用。请重新加载配置，选择当前令牌支持的运行系统和模型。',
  invalid_endpoint:
    '所选线路已不可用。请重新加载配置并选择当前站点提供的线路。',
  invalid_key: '未取得有效的完整密钥。请重新加载向导后重试；不要使用掩码密钥。',
  endpoint_changed:
    '管理员接入地址已变化。请重新加载配置，核对新线路后再下载。',
  metadata_unavailable:
    '暂时无法读取此令牌的配置能力。请重新加载；仍然失败请联系管理员。',
  reveal_failed:
    '暂时无法读取此令牌的完整密钥。请刷新令牌列表并确认权限后重试。',
  export_failed:
    '配置文件导出未完成。请重新加载后重试；不会为此新建或轮换令牌。',
  http_400: '配置请求参数无效。请关闭并重新打开配置向导后重试。',
  http_401: '登录已过期。请重新登录后打开配置向导。',
  http_403:
    '当前账号无权读取此配置或密钥。请确认登录账号，或联系管理员核对访问权限。',
  http_404:
    '配置接口或令牌不存在，或令牌不属于当前账号。请刷新令牌列表；仍然失败请联系管理员。',
  http_429: '操作过于频繁，已触发限流。请稍后重试，不要重复创建令牌。',
  http_5xx:
    '服务器暂时异常，无法完成配置请求。请稍后重试；持续失败请联系管理员。',
  network_error: '网络连接失败。请检查网络或代理后重试。',
  request_timeout: '配置请求超时。请检查网络，稍后重新加载。',
  request_cancelled: '本次请求已取消。需要时请重新打开配置向导。',
});

const SERVER_REASON_CODES = new Set([
  'token_disabled',
  'token_expired',
  'token_exhausted',
  'user_disabled',
  'invalid_server_address',
  'group_unavailable',
  'group_forbidden',
  'group_retired',
  'no_compatible_model',
]);

class SafeSetupError extends Error {
  constructor(code) {
    const safeCode =
      typeof code === 'string' && Object.hasOwn(SAFE_MESSAGES, code)
        ? code
        : 'metadata_unavailable';
    super(SAFE_MESSAGES[safeCode]);
    this.name = 'SafeSetupError';
    this.code = safeCode;
  }
}

export const setupError = (code) => new SafeSetupError(code);
const serverReasonError = (code, fallback = 'metadata_unavailable') =>
  setupError(SERVER_REASON_CODES.has(code) ? code : fallback);

export function requireSetupSuccess(
  response,
  fallback = 'metadata_unavailable',
) {
  if (response?.success !== true)
    throw serverReasonError(
      response?.reason_code ?? response?.data?.reason_code,
      fallback,
    );
  return response.data;
}

export function setupErrorMessage(error, fallback = 'metadata_unavailable') {
  // Read code from our own typed error, never its mutable .message property.
  if (error instanceof SafeSetupError)
    return typeof error.code === 'string' &&
      Object.hasOwn(SAFE_MESSAGES, error.code)
      ? SAFE_MESSAGES[error.code]
      : SAFE_MESSAGES.metadata_unavailable;
  const status = error?.response?.status;
  if ([400, 401, 403, 404, 429].includes(status))
    return SAFE_MESSAGES[`http_${status}`];
  if (Number.isInteger(status) && status >= 500 && status <= 599)
    return SAFE_MESSAGES.http_5xx;
  if (['ECONNABORTED', 'ETIMEDOUT'].includes(error?.code))
    return SAFE_MESSAGES.request_timeout;
  if (error?.code === 'ERR_CANCELED') return SAFE_MESSAGES.request_cancelled;
  if (
    ['ERR_NETWORK', 'ENOTFOUND', 'ECONNREFUSED', 'ECONNRESET'].includes(
      error?.code,
    ) ||
    (error?.isAxiosError === true && !error?.response)
  )
    return SAFE_MESSAGES.network_error;
  return Object.hasOwn(SAFE_MESSAGES, fallback)
    ? SAFE_MESSAGES[fallback]
    : SAFE_MESSAGES.metadata_unavailable;
}

export function normalizeEndpoint(value, origin) {
  if (value === '') {
    try {
      if (
        typeof origin !== 'string' ||
        new URL(origin).origin !== origin ||
        !origin.startsWith('https://')
      )
        throw setupError('invalid_origin');
    } catch {
      throw setupError('invalid_origin');
    }
  }
  const candidate = value === '' ? `${origin || ''}/v1` : value;
  if (
    typeof candidate !== 'string' ||
    candidate.length > 1024 ||
    /[^\x21-\x7e]/.test(candidate) ||
    !ENDPOINT.test(candidate) ||
    /\/\.\.?(\/|$)/.test(candidate)
  ) {
    throw setupError('invalid_server_address');
  }
  let url;
  try {
    url = new URL(candidate);
  } catch {
    throw setupError('invalid_server_address');
  }
  if (
    url.protocol !== 'https:' ||
    !url.hostname ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    (url.port && Number(url.port) === 0)
  ) {
    throw setupError('invalid_server_address');
  }
  return url.href;
}

export function normalizeApiKey(raw) {
  if (typeof raw !== 'string') throw setupError('invalid_key');
  const key = raw.startsWith('sk-') ? raw : `sk-${raw}`;
  if (
    /[^\x21-\x7e]/.test(key) ||
    !/^sk-[A-Za-z0-9_-]{16,253}$/.test(key) ||
    key.startsWith('sk-sk-')
  )
    throw setupError('invalid_key');
  return key;
}

export function validateMetadata(metadata, tokenId, origin) {
  if (
    metadata?.schema_version !== 1 ||
    metadata?.client !== 'codex' ||
    metadata?.template_version !== TEMPLATE_VERSION
  ) {
    throw setupError('unsupported_template');
  }
  if (metadata.token_id !== tokenId) throw setupError('token_mismatch');
  if (metadata.configurable === false)
    throw serverReasonError(metadata.reason_code);
  if (
    metadata.configurable !== true ||
    !Array.isArray(metadata.models) ||
    !Array.isArray(metadata.supported_targets)
  )
    throw setupError('metadata_invalid');
  const base_url = normalizeEndpoint(metadata.base_url, origin);
  const models = (Array.isArray(metadata.models) ? metadata.models : []).filter(
    (item) =>
      MODELS.includes(item?.id) &&
      Array.isArray(item.reasoning_efforts) &&
      item.reasoning_efforts.includes('low'),
  );
  if (!models.length) throw setupError('no_compatible_model');
  const supported_targets = (metadata.supported_targets || []).filter(
    (target) => TARGETS.includes(target),
  );
  if (!supported_targets.length) throw setupError('no_supported_target');
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

// These are two operator-approved entrances to this deployment. Never offer
// this site's alternate host for a different administrator-configured gateway.
export function getSetupEndpoints(metadata) {
  const base_url = normalizeEndpoint(metadata.base_url);
  if (base_url === CF_ENDPOINT) {
    return [
      { id: 'cf', label: 'CF 线路（默认）', base_url: CF_ENDPOINT },
      { id: 'direct', label: '直连线路', base_url: DIRECT_ENDPOINT },
    ];
  }
  return [{ id: 'default', label: '站点默认线路', base_url }];
}

export function selectSetupEndpoint(metadata, endpointId) {
  const endpoints = getSetupEndpoints(metadata);
  const selected = endpoints.find(
    (item) => item.id === (endpointId ?? endpoints[0].id),
  );
  if (!selected) throw setupError('invalid_endpoint');
  return selected;
}

export function renderConfig(metadata, { target, model, endpointId }) {
  if (
    !Array.isArray(metadata?.supported_targets) ||
    !Array.isArray(metadata?.models)
  )
    throw setupError('metadata_invalid');
  if (
    !TARGETS.includes(target) ||
    !metadata.supported_targets.includes(target) ||
    !MODELS.includes(model) ||
    !metadata.models.some(
      (item) =>
        item?.id === model &&
        Array.isArray(item.reasoning_efforts) &&
        item.reasoning_efforts.includes('low'),
    )
  ) {
    throw setupError('invalid_selection');
  }
  const endpoint = selectSetupEndpoint(metadata, endpointId).base_url;
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
