import React, { useCallback, useEffect, useRef, useState } from 'react';
import {
  Banner,
  Button,
  Checkbox,
  Modal,
  Select,
  Space,
  Spin,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import {
  normalizeApiKey,
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

const TARGET_LABELS = {
  windows: 'Windows',
  macos: 'macOS',
  linux: 'Linux',
  wsl: 'WSL',
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
  const [runCliTest, setRunCliTest] = useState(true);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [downloaded, setDownloaded] = useState(false);
  const [manual, setManual] = useState(false);
  const [expired, setExpired] = useState(false);
  const [revision, setRevision] = useState(0);
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
    setManual(false);
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
    if (busyRef.current || !tokenId || !metadata || !model || expired) return;
    busyRef.current = true;
    setBusy(true);
    setError('');
    setDownloaded(false);
    const ticket = { ...epoch.current.begin(), tokenId, userIdentity };
    const selected = { tokenId, target, model, runCliTest, userIdentity };
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
      if (checked.base_url !== metadata.base_url)
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
      preview = renderConfig(metadata, { target, model });
    } catch {
      /* selection not ready */
    }
  }
  const enabled = !!metadata && !!model && !expired && !loading && !busy;
  const entry =
    target === 'windows'
      ? 'setup.cmd'
      : target === 'macos'
        ? 'setup.command'
        : 'setup.sh';
  return (
    <Modal
      visible={visible}
      title={t('Codex 快速配置')}
      footer={null}
      onCancel={close}
      closeOnEsc
      width={640}
      style={{ maxWidth: 'calc(100vw - 24px)' }}
      bodyStyle={{ maxHeight: '75vh', overflowY: 'auto' }}
    >
      <Space vertical align='start' spacing='medium' style={{ width: '100%' }}>
        <Typography.Text type='tertiary'>
          {t(
            '下载配置包，在真正运行 Codex 的电脑上解压执行。网站不会读取你的电脑环境。',
          )}
        </Typography.Text>
        {ids.length > 1 && (
          <Select
            value={tokenId}
            placeholder={t('请选择本次创建的令牌')}
            optionList={ids.map((id) => ({
              value: id,
              label: `${t('令牌')} #${id}`,
            }))}
            style={{ width: '100%' }}
            onChange={(id) => {
              clearExports();
              setTokenId(id);
            }}
          />
        )}
        {tokenId && (
          <Typography.Text>
            {t('当前令牌')} #{tokenId}
          </Typography.Text>
        )}
        {error && <Banner type='danger' description={error} closeIcon={null} />}
        {expired && (
          <Banner
            type='warning'
            description={t('向导已超时，临时下载引用已清理，请重新加载。')}
            closeIcon={null}
          />
        )}
        {(error || expired) && (
          <Button
            onClick={() => {
              clearExports();
              setRevision((value) => value + 1);
            }}
          >
            {t('重新加载')}
          </Button>
        )}
        {loading && <Spin tip={t('读取当前令牌的配置能力')} />}
        {metadata && (
          <>
            <Typography.Text strong>{t('运行环境')}</Typography.Text>
            <Select
              value={target}
              optionList={metadata.supported_targets.map((value) => ({
                value,
                label: TARGET_LABELS[value],
              }))}
              style={{ width: '100%' }}
              onChange={(value) => {
                clearExports();
                setTarget(value);
              }}
            />
            <Typography.Text type='tertiary'>
              {t(
                'WSL 请手动选择，并在对应发行版终端运行；手机仅用于下载到目标电脑。',
              )}
            </Typography.Text>
            <Typography.Text strong>{t('模型与线路')}</Typography.Text>
            <Select
              value={model || undefined}
              placeholder={t('请选择此令牌可用的模型')}
              optionList={metadata.models.map((item) => ({
                value: item.id,
                label: item.id,
              }))}
              style={{ width: '100%' }}
              onChange={(value) => {
                clearExports();
                setModel(value);
              }}
            />
            <Typography.Text copyable>{metadata.base_url}</Typography.Text>
            {(metadata.notices || []).map((notice, index) => (
              <Banner
                key={index}
                type='warning'
                description={t(notice)}
                closeIcon={null}
              />
            ))}
            <Checkbox
              checked={runCliTest}
              onChange={(event) => {
                clearExports();
                setRunCliTest(event.target.checked);
              }}
            >
              {t(
                '包含可选短 CLI 测试；本机选择 FULL 后才运行，可能产生少量用量。',
              )}
            </Checkbox>
            <Banner
              type='warning'
              description={t(
                '下载包含完整密钥，请勿转发。脚本会先备份，再由你确认替换现有连接、认证及 MCP 等配置。',
              )}
              closeIcon={null}
            />
            <Space wrap>
              <Button
                theme='solid'
                disabled={!enabled}
                loading={busy}
                onClick={() => exportFiles('zip')}
              >
                {t('下载配置包')}
              </Button>
              <Button
                disabled={busy}
                onClick={() => setManual((value) => !value)}
              >
                {t('手动配置')}
              </Button>
            </Space>
            {manual && (
              <>
                <Typography.Text>
                  {t(
                    '保存到实际 CODEX_HOME，未设置时为当前用户的 .codex。已有复杂配置请自行合并。预览不包含完整密钥。',
                  )}
                </Typography.Text>
                <pre
                  style={{
                    width: '100%',
                    overflowX: 'auto',
                    fontSize: 12,
                    padding: 12,
                    background: 'var(--semi-color-fill-0)',
                  }}
                >
                  {preview}
                  {
                    '\n# auth.json\n{ "OPENAI_API_KEY": "sk-********", "auth_mode": "apikey" }'
                  }
                </pre>
                <Space wrap>
                  <Button
                    disabled={!enabled}
                    onClick={() => exportFiles('config')}
                  >
                    {t('下载 config.toml')}
                  </Button>
                  <Button
                    disabled={!enabled}
                    onClick={() => exportFiles('auth')}
                  >
                    {t('下载 auth.json')}
                  </Button>
                </Space>
              </>
            )}
          </>
        )}
        {downloaded && (
          <Banner
            type='success'
            description={
              <div>
                {downloaded === 'zip' ? (
                  <>
                    {t(
                      '文件下载已开始，尚未配置你的电脑。请完整解压，在目标电脑运行：',
                    )}
                    <code>
                      {target === 'windows' ? entry : `bash ${entry}`}
                    </code>
                  </>
                ) : (
                  t(
                    '单文件下载已开始。请备份旧配置，再把 config.toml 和 auth.json 放入实际配置目录；网站尚未验证本机配置。',
                  )
                )}
                <p>
                  {t(
                    '运行后逐项查看本机报告。配置保存与 CLI 调用通过是独立结果；完成后请重新打开 Codex。',
                  )}
                </p>
              </div>
            }
            closeIcon={null}
          />
        )}
        <Button onClick={close}>{t('关闭')}</Button>
      </Space>
    </Modal>
  );
}
