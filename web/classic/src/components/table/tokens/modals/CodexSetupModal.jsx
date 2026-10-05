import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Modal, Typography } from '@douyinfe/semi-ui';
import {
  AlertCircle,
  ArrowLeft,
  ArrowRight,
  Check,
  ChevronDown,
  Circle,
  Download,
  FileArchive,
  Folder,
  KeyRound,
  Laptop,
  Link2,
  LoaderCircle,
  Monitor,
  RefreshCw,
  Shield,
  SquareTerminal,
  Terminal,
  Zap,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  normalizeApiKey,
  getSetupEndpoints,
  selectSetupEndpoint,
  renderAuth,
  renderConfig,
  requireSetupSuccess,
  setupError,
  setupErrorMessage,
  suggestTarget,
  validateMetadata,
} from '../../../../lib/codex-setup/config';
import {
  createSetupEpoch,
  isSetupTicketCurrent,
  INVALIDATE_EVENT,
  normalizeTokenIds,
} from '../../../../lib/codex-setup/session';
import './CodexSetupModal.css';

const TARGET_LABELS = {
  windows: 'Windows',
  macos: 'macOS',
  linux: 'Linux',
  wsl: 'WSL',
};
const TARGET_ICONS = {
  windows: Monitor,
  macos: Laptop,
  linux: Terminal,
  wsl: SquareTerminal,
};
const TARGET_HINTS = {
  windows: '原生桌面环境',
  macos: 'Intel / Apple 芯片',
  linux: 'Linux 发行版',
  wsl: 'Windows 子系统',
};
const identity = (value) => value;

// Standalone integration surface: request is an axios-compatible instance. No token
// cache or TokensPage context is consumed. Only IDs enter component state.
export default function CodexSetupModal({
  visible,
  tokenIds = [],
  initialTokenId,
  userIdentity,
  isSessionCurrent,
  onClose,
  request,
  t: translate,
}) {
  const { t: translated } = useTranslation();
  const t = translate || translated || identity;
  const ids = normalizeTokenIds(tokenIds);
  const idsKey = ids.join(',');
  const [tokenId, setTokenId] = useState(null);
  const [target, setTarget] = useState(suggestTarget);
  const [model, setModel] = useState('');
  const [metadata, setMetadata] = useState(null);
  const [endpointId, setEndpointId] = useState(undefined);
  const [runCliTest, setRunCliTest] = useState(true);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [downloaded, setDownloaded] = useState(false);
  const [manual, setManual] = useState(false);
  const [expired, setExpired] = useState(false);
  const [revision, setRevision] = useState(0);
  const [step, setStep] = useState(1);
  const epoch = useRef(createSetupEpoch());
  const busyRef = useRef(false);
  const urls = useRef(new Map());
  const active = useRef({ visible, tokenId, userIdentity });
  active.current = { visible, tokenId, userIdentity };
  const current = (ticket) =>
    isSetupTicketCurrent(
      epoch.current,
      ticket,
      active.current,
      isSessionCurrent,
    );
  const clearExports = useCallback(() => {
    epoch.current.invalidate();
    busyRef.current = false;
    setBusy(false);
    setDownloaded(false);
    for (const [url, timer] of urls.current) {
      clearTimeout(timer);
      URL.revokeObjectURL(url);
    }
    urls.current.clear();
  }, []);
  const close = useCallback(() => {
    clearExports();
    setMetadata(null);
    setModel('');
    setError('');
    onClose?.();
  }, [clearExports, onClose]);

  useEffect(() => {
    clearExports();
    setMetadata(null);
    setModel('');
    setError('');
    setExpired(false);
    setEndpointId(undefined);
    setManual(false);
    setStep(1);
    setLoading(false);
    // A batch requires an explicit choice; never infer list order or a token name.
    setTokenId(
      ids.includes(initialTokenId)
        ? initialTokenId
        : ids.length === 1
          ? ids[0]
          : null,
    );
    if (!visible) return undefined;
    const timer = setTimeout(() => {
      clearExports();
      setMetadata(null);
      setExpired(true);
      setLoading(false);
    }, 180000);
    return () => {
      clearTimeout(timer);
      clearExports();
    };
  }, [visible, idsKey, initialTokenId, userIdentity, revision, clearExports]);

  useEffect(() => {
    if (!visible || !tokenId || expired || !request) return undefined;
    const ticket = { ...epoch.current.begin(), tokenId, userIdentity };
    setLoading(true);
    setMetadata(null);
    setModel('');
    setError('');
    request
      .get(`/api/token/${tokenId}/codex-config`, {
        signal: ticket.signal,
        disableDuplicate: true,
        skipErrorHandler: true,
        timeout: 15000,
        headers: { 'Cache-Control': 'no-store' },
      })
      .then(({ data: response }) => {
        if (!current(ticket)) return;
        const checked = validateMetadata(
          requireSetupSuccess(response),
          tokenId,
          window.location.origin,
        );
        setMetadata(checked);
        setEndpointId(getSetupEndpoints(checked)[0].id);
        setModel(checked.default_model || '');
        setTarget((current) =>
          checked.supported_targets.includes(current)
            ? current
            : checked.supported_targets[0],
        );
      })
      .catch((error) => {
        if (current(ticket)) setError(t(setupErrorMessage(error)));
      })
      .finally(() => {
        if (current(ticket)) setLoading(false);
      });
    return () => epoch.current.invalidate();
  }, [visible, tokenId, userIdentity, expired, revision, request, t]);

  useEffect(() => {
    const invalidated = (event) => {
      if (
        !active.current.visible ||
        !event.detail?.tokenIds?.includes(active.current.tokenId)
      )
        return;
      clearExports();
      setMetadata(null);
      setLoading(false);
      setStep(1);
      setError(t('令牌已变更，请关闭并重新打开配置向导。'));
    };
    window.addEventListener(INVALIDATE_EVENT, invalidated);
    const accountChanged = (event) => {
      if (event.key === 'user') close();
    };
    window.addEventListener('storage', accountChanged);
    return () => {
      window.removeEventListener(INVALIDATE_EVENT, invalidated);
      window.removeEventListener('storage', accountChanged);
    };
  }, [clearExports, close, t]);

  const save = (blob, filename, ticket) => {
    if (!current(ticket)) return;
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = filename;
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    urls.current.set(
      url,
      setTimeout(() => {
        URL.revokeObjectURL(url);
        urls.current.delete(url);
      }, 15000),
    );
  };
  const exportFiles = async (kind) => {
    if (
      busyRef.current ||
      !tokenId ||
      !metadata ||
      !model ||
      expired ||
      step !== 2
    )
      return;
    busyRef.current = true;
    setBusy(true);
    setError('');
    setDownloaded(false);
    const ticket = { ...epoch.current.begin(), tokenId, userIdentity };
    const selected = {
      tokenId,
      target,
      model,
      endpointId,
      runCliTest,
      userIdentity,
    };
    let rawKey = '';
    try {
      const response = await request.get(
        `/api/token/${selected.tokenId}/codex-config`,
        {
          signal: ticket.signal,
          disableDuplicate: true,
          skipErrorHandler: true,
          timeout: 15000,
          headers: { 'Cache-Control': 'no-store' },
        },
      );
      if (!current(ticket)) return;
      const checked = validateMetadata(
        requireSetupSuccess(response.data),
        selected.tokenId,
        window.location.origin,
      );
      if (
        checked.base_url !== metadata.base_url ||
        selectSetupEndpoint(checked, selected.endpointId).base_url !==
          selectSetupEndpoint(metadata, selected.endpointId).base_url
      )
        throw setupError('endpoint_changed');
      const config = renderConfig(checked, selected);
      // Always obtain the real key at the actual export boundary; never use resolvedTokenKeys.
      const reveal = await request.post(
        `/api/token/${selected.tokenId}/key`,
        {},
        {
          signal: ticket.signal,
          skipErrorHandler: true,
          timeout: 15000,
          headers: { 'Cache-Control': 'no-store' },
        },
      );
      if (!current(ticket)) return;
      const revealed = requireSetupSuccess(reveal.data, 'reveal_failed');
      rawKey = normalizeApiKey(revealed?.key);
      revealed.key = '';
      if (kind === 'zip') {
        const { buildCodexSetupZip } = await import(
          '../../../../lib/codex-setup/bundle'
        );
        const bundle = await buildCodexSetupZip({
          metadata: checked,
          ...selected,
          rawKey,
        });
        if (!current(ticket)) return;
        save(bundle.blob, bundle.filename, ticket);
      } else if (kind === 'config') {
        save(
          new Blob([config], { type: 'text/plain;charset=utf-8' }),
          'config.toml',
          ticket,
        );
      } else {
        save(
          new Blob([renderAuth(rawKey)], { type: 'application/json' }),
          'auth.json',
          ticket,
        );
      }
      if (current(ticket)) setDownloaded(kind);
    } catch (error) {
      if (current(ticket))
        setError(t(setupErrorMessage(error, 'export_failed')));
    } finally {
      rawKey = '';
      if (current(ticket)) {
        busyRef.current = false;
        setBusy(false);
      }
    }
  };

  let preview = '';
  if (metadata && model) {
    try {
      preview = renderConfig(metadata, { target, model, endpointId });
    } catch {
      /* selection not ready */
    }
  }
  const enabled =
    !!metadata && !!preview && !expired && !loading && !busy && !error;
  const endpoints = metadata ? getSetupEndpoints(metadata) : [];
  const selectedEndpoint =
    endpoints.find((item) => item.id === endpointId) || endpoints[0];
  const entry =
    target === 'windows'
      ? 'setup.cmd'
      : target === 'macos'
        ? 'setup.command'
        : 'setup.sh';
  const command = target === 'windows' ? entry : `bash ./${entry}`;
  const directory =
    target === 'windows'
      ? '%USERPROFILE%\\.codex'
      : target === 'wsl'
        ? t('WSL 当前用户的 ~/.codex')
        : '~/.codex';
  const changeStep = (next) => {
    clearExports();
    setManual(false);
    setStep(next);
  };
  const reload = () => {
    clearExports();
    setRevision((value) => value + 1);
  };

  return (
    <Modal
      className='newapi-codex-setup'
      visible={visible}
      title={
        <div className='ncs-header'>
          <span className='ncs-brand-icon'>
            <Zap size={21} strokeWidth={1.9} aria-hidden='true' />
          </span>
          <div className='ncs-header-copy'>
            <div className='ncs-title-line'>
              <span className='ncs-title'>{t('快速配置 Codex')}</span>
              {tokenId && (
                <span className='ncs-token-badge'>
                  <KeyRound size={11} aria-hidden='true' />#{tokenId}
                </span>
              )}
            </div>
            <p className='ncs-header-subtitle'>
              {t('选好环境，把配置带到你的电脑。')}
            </p>
          </div>
        </div>
      }
      footer={
        <div className='ncs-footer'>
          <span className='ncs-footer-note'>
            <Shield size={13} aria-hidden='true' />
            {step === 1
              ? t('使用现有令牌，不会额外创建')
              : t('接下来，在你的电脑完成')}
          </span>
          <div className='ncs-footer-actions'>
            <button
              type='button'
              className='ncs-button ncs-button-secondary'
              onClick={() => (step === 1 ? close() : changeStep(1))}
            >
              {step === 1 ? (
                t('稍后配置')
              ) : (
                <>
                  <ArrowLeft size={14} aria-hidden='true' />
                  {t('上一步')}
                </>
              )}
            </button>
            {step === 1 ? (
              <button
                type='button'
                className='ncs-button ncs-button-primary'
                disabled={!enabled}
                onClick={() => changeStep(2)}
              >
                {t('下一步')}
                <ArrowRight size={14} aria-hidden='true' />
              </button>
            ) : (
              <button
                type='button'
                className='ncs-button ncs-button-primary'
                onClick={close}
              >
                {t('完成，关闭')}
              </button>
            )}
          </div>
        </div>
      }
      onCancel={close}
      closeOnEsc
      width={700}
      style={{ maxWidth: 'calc(100vw - 24px)' }}
      bodyStyle={{
        padding: 0,
        maxHeight: 'min(70vh, 680px)',
        overflowY: 'auto',
      }}
    >
      <div className='ncs-body' aria-busy={loading || busy}>
        <ol className='ncs-steps' aria-label={t('配置步骤')}>
          <li
            className={step === 1 ? 'is-current' : 'is-complete'}
            aria-current={step === 1 ? 'step' : undefined}
          >
            <span className='ncs-step-number'>
              {step === 2 ? <Check size={13} aria-hidden='true' /> : '1'}
            </span>
            <span>{t('选择环境')}</span>
          </li>
          <li
            className={step === 2 ? 'is-current' : ''}
            aria-current={step === 2 ? 'step' : undefined}
          >
            <span className='ncs-step-number'>2</span>
            <span>{t('下载并运行')}</span>
          </li>
          <li className='ncs-step-local'>
            <span className='ncs-step-number'>3</span>
            <span>
              {t('本机检查')}
              <small>{t('在电脑上')}</small>
            </span>
          </li>
        </ol>

        {ids.length > 1 && (
          <label className='ncs-token-select'>
            <span className='ncs-field-label'>{t('本次创建的令牌')}</span>
            <span className='ncs-select-wrap'>
              <select
                value={tokenId || ''}
                aria-label={t('请选择本次创建的令牌')}
                onChange={(event) => {
                  clearExports();
                  setStep(1);
                  setManual(false);
                  setMetadata(null);
                  setModel('');
                  setLoading(false);
                  setTokenId(Number(event.target.value) || null);
                }}
              >
                <option value='' disabled>
                  {t('请选择要配置的令牌')}
                </option>
                {ids.map((id) => (
                  <option key={id} value={id}>
                    {t('令牌')} #{id}
                  </option>
                ))}
              </select>
              <ChevronDown size={14} aria-hidden='true' />
            </span>
          </label>
        )}

        {(error || expired) && (
          <div
            className={`ncs-alert ${expired ? 'ncs-alert-warning' : 'ncs-alert-error'}`}
            role='alert'
          >
            <AlertCircle size={17} aria-hidden='true' />
            <div className='ncs-alert-content'>
              <strong>
                {expired ? t('向导已超时') : t('暂时无法继续配置')}
              </strong>
              <p>
                {expired
                  ? t('临时下载引用已清理。重新加载后，可继续为当前令牌配置。')
                  : error}
              </p>
              <button
                type='button'
                className='ncs-text-button'
                onClick={reload}
              >
                <RefreshCw size={13} aria-hidden='true' />
                {t('重新加载')}
              </button>
            </div>
          </div>
        )}

        {loading && !expired && (
          <div className='ncs-loading' role='status'>
            <span className='ncs-loading-icon'>
              <LoaderCircle size={22} className='ncs-spin' aria-hidden='true' />
            </span>
            <strong>{t('正在读取配置选项')}</strong>
            <p>{t('确认当前令牌可用的模型与接入地址…')}</p>
            <div className='ncs-loading-lines' aria-hidden='true'>
              <span />
              <span />
            </div>
          </div>
        )}
        {!tokenId && !loading && !expired && !error && (
          <div className='ncs-empty'>
            <KeyRound size={26} aria-hidden='true' />
            <strong>{t('先选择一枚令牌')}</strong>
            <p>{t('每份配置都与所选令牌绑定，不会默认使用列表中的第一枚。')}</p>
          </div>
        )}

        {metadata && !expired && !loading && step === 1 && (
          <div className='ncs-step-content'>
            <section aria-labelledby='ncs-environment-title'>
              <div className='ncs-section-heading'>
                <h3 id='ncs-environment-title'>
                  {t('Codex 运行在哪个系统？')}
                </h3>
                <p>{t('选择真正运行 Codex 的电脑环境，可与当前设备不同。')}</p>
              </div>
              <div
                className='ncs-system-grid'
                role='group'
                aria-label={t('运行系统')}
              >
                {Object.keys(TARGET_LABELS).map((os) => {
                  const Icon = TARGET_ICONS[os];
                  return (
                    <button
                      key={os}
                      type='button'
                      className='ncs-system-card'
                      aria-pressed={target === os}
                      disabled={!metadata.supported_targets.includes(os)}
                      onClick={() => {
                        clearExports();
                        setTarget(os);
                      }}
                    >
                      <span className='ncs-system-check' aria-hidden='true'>
                        {target === os && <Check size={10} strokeWidth={3} />}
                      </span>
                      <Icon size={25} strokeWidth={1.5} aria-hidden='true' />
                      <strong>{TARGET_LABELS[os]}</strong>
                      <small>{t(TARGET_HINTS[os])}</small>
                    </button>
                  );
                })}
              </div>
              <p className='ncs-subtle-note'>
                {target === 'wsl'
                  ? t(
                      '将在 WSL 的 Linux 用户目录配置，请在对应发行版的终端运行。',
                    )
                  : t('手机可以下载配置包，但需要在目标电脑上解压运行。')}
              </p>
            </section>

            <div className='ncs-field-grid'>
              <label className='ncs-field'>
                <span className='ncs-field-label'>{t('使用模型')}</span>
                <span className='ncs-select-wrap'>
                  <select
                    value={model}
                    aria-label={t('使用模型')}
                    onChange={(event) => {
                      clearExports();
                      setModel(event.target.value);
                    }}
                  >
                    <option value='' disabled>
                      {t('请选择可用模型')}
                    </option>
                    {metadata.models.map((item) => (
                      <option key={item.id} value={item.id}>
                        {item.id}
                      </option>
                    ))}
                  </select>
                  <ChevronDown size={14} aria-hidden='true' />
                </span>
                <small>{t('仅显示当前令牌可用的兼容模型')}</small>
              </label>
              <div className='ncs-field'>
                <label
                  className='ncs-field-label'
                  htmlFor='ncs-endpoint-select'
                >
                  <Link2 size={13} aria-hidden='true' />
                  {t('请求线路')}
                </label>
                <span className='ncs-select-wrap'>
                  <select
                    id='ncs-endpoint-select'
                    value={endpointId || ''}
                    onChange={(event) => {
                      clearExports();
                      setEndpointId(event.target.value);
                    }}
                  >
                    {endpoints.map((item) => (
                      <option key={item.id} value={item.id}>
                        {t(item.label)}
                      </option>
                    ))}
                  </select>
                  <ChevronDown size={14} aria-hidden='true' />
                </span>
                <div className='ncs-endpoint'>
                  <Typography.Text copyable>
                    {selectedEndpoint?.base_url}
                  </Typography.Text>
                </div>
              </div>
            </div>

            <div className='ncs-directory'>
              <Folder size={17} aria-hidden='true' />
              <div>
                <div className='ncs-directory-line'>
                  <strong>{t('默认配置目录')}</strong>
                  <code>{directory}</code>
                </div>
                <p>
                  {t(
                    '设置了 CODEX_HOME 时，以本机检测的实际目录为准。已有文件先备份，再确认替换。',
                  )}
                </p>
              </div>
            </div>

            <label className='ncs-test-option'>
              <span className='ncs-test-icon'>
                <SquareTerminal size={20} aria-hidden='true' />
              </span>
              <span className='ncs-test-copy'>
                <strong>
                  {t('Codex CLI 测试')}
                  <span className='ncs-mini-badge'>{t('可选')}</span>
                </strong>
                <small>
                  {t(
                    '仅在本机输入 FULL 后发起短调用，可能产生少量用量。不勾选也可完成配置与本地检查。',
                  )}
                </small>
              </span>
              <input
                type='checkbox'
                checked={runCliTest}
                onChange={(event) => {
                  clearExports();
                  setRunCliTest(event.target.checked);
                }}
              />
            </label>
          </div>
        )}

        {metadata && !expired && !loading && step === 2 && (
          <div className='ncs-step-content'>
            <div className='ncs-endpoint'>
              <span>{t(selectedEndpoint?.label)} · </span>
              <Typography.Text copyable>
                {selectedEndpoint?.base_url}
              </Typography.Text>
            </div>
            <div className='ncs-section-heading'>
              <h3>{t('下载后，在电脑上完成配置')}</h3>
              <p>
                {t(
                  '无需手动填写密钥。运行脚本前，你仍可以确认目录和替换设置。',
                )}
              </p>
            </div>
            <div className='ncs-package'>
              <div className='ncs-package-top'>
                <span className='ncs-package-icon'>
                  <FileArchive size={26} strokeWidth={1.5} aria-hidden='true' />
                </span>
                <div className='ncs-package-copy'>
                  <strong>new-api-codex-{target}-v1.zip</strong>
                  <p>{t('配置脚本')} · config.toml · auth.json · README</p>
                </div>
                <span className='ncs-platform-badge'>
                  {TARGET_LABELS[target]}
                </span>
              </div>
              <div className='ncs-package-bottom'>
                <span className='ncs-package-model'>
                  {model}
                  <small>{t('使用当前令牌')}</small>
                </span>
                <button
                  type='button'
                  className='ncs-button ncs-button-primary ncs-download-button'
                  disabled={!enabled}
                  onClick={() => exportFiles('zip')}
                >
                  {busy ? (
                    <LoaderCircle
                      size={16}
                      className='ncs-spin'
                      aria-hidden='true'
                    />
                  ) : (
                    <Download size={16} aria-hidden='true' />
                  )}
                  {busy
                    ? t('正在生成…')
                    : downloaded === 'zip'
                      ? t('重新下载配置包')
                      : t('下载配置包')}
                </button>
              </div>
            </div>

            <ol className='ncs-instructions'>
              <li>
                <span className='ncs-instruction-number'>1</span>
                <div>
                  <strong>{t('完整解压配置包')}</strong>
                  <p>
                    {t('放到目标电脑的任意文件夹，不要在 ZIP 预览中直接运行。')}
                  </p>
                </div>
              </li>
              <li>
                <span className='ncs-instruction-number'>2</span>
                <div>
                  <strong>
                    {target === 'windows'
                      ? t('双击启动配置脚本')
                      : target === 'wsl'
                        ? t('进入目标 WSL 发行版终端')
                        : t('在解压目录打开终端')}
                  </strong>
                  <code className='ncs-command'>
                    <Terminal size={13} aria-hidden='true' />
                    {command}
                  </code>
                  {target === 'macos' && (
                    <p>{t('若双击被系统拦截，可使用上面的本地终端命令。')}</p>
                  )}
                </div>
              </li>
              <li>
                <span className='ncs-instruction-number'>3</span>
                <div>
                  <strong>{t('确认目录，选择检查范围')}</strong>
                  <p>
                    {t(
                      '已有配置会先备份，输入 REPLACE 才替换。MCP、profiles 和原登录设置也会被替换，复杂配置可取消后手动合并。',
                    )}
                  </p>
                </div>
              </li>
              <li>
                <span className='ncs-instruction-number'>4</span>
                <div>
                  <strong>{t('查看本机报告，再重新打开 Codex')}</strong>
                  <p>
                    {t(
                      '配置保存与实际调用通过会分别显示。未安装 CLI 时，脚本会给出相应提示。',
                    )}
                  </p>
                </div>
              </li>
            </ol>

            <div className='ncs-privacy-note'>
              <Shield size={15} aria-hidden='true' />
              <p>
                {t(
                  '配置包包含完整密钥，请勿转发。网页不会执行脚本，也不会自动接收你的本机检查结果。',
                )}
              </p>
            </div>

            <div className='ncs-manual'>
              <button
                type='button'
                className='ncs-disclosure'
                aria-expanded={manual}
                aria-controls='ncs-manual-content'
                disabled={busy}
                onClick={() => setManual((value) => !value)}
              >
                <span>
                  {t('想自己管理配置？')}
                  <strong>{t('手动配置')}</strong>
                </span>
                <ChevronDown
                  size={15}
                  className={manual ? 'is-open' : ''}
                  aria-hidden='true'
                />
              </button>
              {manual && (
                <div id='ncs-manual-content' className='ncs-manual-content'>
                  <p>
                    {t(
                      '将两份文件放入实际 CODEX_HOME；未设置时使用上述默认目录。请自行合并已有高级配置。下方仅为脱敏预览。',
                    )}
                  </p>
                  <pre>
                    {preview}
                    {
                      '\n# auth.json\n{ "OPENAI_API_KEY": "sk-********", "auth_mode": "apikey" }'
                    }
                  </pre>
                  <div className='ncs-manual-actions'>
                    <button
                      type='button'
                      className='ncs-button ncs-button-secondary'
                      disabled={!enabled}
                      aria-label={t('下载 config.toml')}
                      onClick={() => exportFiles('config')}
                    >
                      <Download size={13} aria-hidden='true' />
                      config.toml
                    </button>
                    <button
                      type='button'
                      className='ncs-button ncs-button-secondary'
                      disabled={!enabled}
                      aria-label={t('下载 auth.json')}
                      onClick={() => exportFiles('auth')}
                    >
                      <Download size={13} aria-hidden='true' />
                      auth.json
                    </button>
                  </div>
                </div>
              )}
            </div>

            <details className='ncs-local-checks'>
              <summary>
                <span>
                  <Monitor size={14} aria-hidden='true' />
                  {t('本机将检查哪些项目？')}
                </span>
                <span className='ncs-pending-badge'>{t('待本机运行')}</span>
              </summary>
              <ul>
                {[
                  '运行环境与 Codex CLI',
                  '配置目录与文件',
                  '备份与访问权限',
                  'HTTPS 线路与密钥认证',
                  '模型权限与可选 CLI 调用',
                  '本地脱敏检查报告',
                ].map((item) => (
                  <li key={item}>
                    <Circle size={10} aria-hidden='true' />
                    {t(item)}
                  </li>
                ))}
              </ul>
              <p>
                {t(
                  '这里仅说明检查项目，并非检测结果。运行后，每项分别显示通过、失败、警告或跳过，报告只保存在本机。',
                )}
              </p>
            </details>
          </div>
        )}

        {metadata && !expired && !loading && metadata.notices?.length > 0 && (
          <div className='ncs-notices'>
            {metadata.notices.map((notice, index) => (
              <p key={index}>
                <AlertCircle size={12} aria-hidden='true' />
                {t(notice)}
              </p>
            ))}
          </div>
        )}
        {downloaded && (
          <div className='ncs-download-notice' role='status'>
            <Download size={16} aria-hidden='true' />
            <div>
              <strong>{t('下载已发起')}</strong>
              <p>
                {downloaded === 'zip'
                  ? t(
                      '请到目标电脑解压运行。当前仅完成下载，尚未验证本机配置或实际调用。',
                    )
                  : t(
                      '请备份旧配置，再将两份文件放入实际配置目录。当前尚未验证本机配置。',
                    )}
              </p>
            </div>
          </div>
        )}
      </div>
    </Modal>
  );
}
