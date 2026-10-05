import { zipSync, strToU8 } from 'fflate';
import windows from './templates/setup.ps1?raw';
import posix from './templates/setup.sh?raw';
import launcher from './templates/setup.cmd?raw';
import {
  TEMPLATE_VERSION,
  renderConfig,
  renderAuth,
  validateMetadata,
} from './config';

const sha256 = async (text) =>
  Array.from(
    new Uint8Array(await crypto.subtle.digest('SHA-256', strToU8(text))),
    (byte) => byte.toString(16).padStart(2, '0'),
  ).join('');

export async function buildCodexSetupZip({
  metadata,
  target,
  model,
  rawKey,
  runCliTest = true,
}) {
  const checked = validateMetadata(metadata, metadata.token_id);
  const config = renderConfig(checked, { target, model });
  const auth = renderAuth(rawKey);
  const [configHash, authHash] = await Promise.all([
    sha256(config),
    sha256(auth),
  ]);
  const manifest = {
    schema_version: 1,
    template_version: TEMPLATE_VERSION,
    target,
    model,
    base_url: checked.base_url,
    run_cli_test: !!runCliTest,
    files: { 'config.toml': configHash, 'auth.json': authHash },
  };
  const settings =
    Object.entries({
      target,
      model,
      base_url: checked.base_url,
      run_cli_test: String(!!runCliTest),
      template_version: TEMPLATE_VERSION,
    })
      .map(([key, value]) => `${key}\t${value}`)
      .join('\n') + '\n';
  const entry =
    target === 'windows'
      ? 'setup.cmd'
      : target === 'macos'
        ? 'setup.command'
        : 'setup.sh';
  const instructions = `new-api · Codex 快速配置\n\n先完整解压。Windows 双击 setup.cmd；macOS 在终端运行 bash setup.command；Linux/WSL 运行 bash setup.sh。请在真正运行 Codex 的系统中操作。\n实际目标是 CODEX_HOME 或当前用户的 .codex。已有配置会先备份，经本机 REPLACE 确认后替换；旧 MCP、profiles、hooks、登录方式也会被替换。复杂配置可取消后手动合并。\n本包不安装 CLI、不创建或撤销令牌。请先安装 Codex CLI，再重开客户端。\n本机只有输入 FULL 才做在线检查，短 CLI 调用可能产生少量费用。仅本地检查不会调用模型。\n线路：${checked.base_url}\n模型：${model}\n\n只检查：Windows setup.ps1 -CheckOnly；POSIX bash ${entry} --check-only。\n恢复：Windows setup.ps1 -Restore；POSIX bash ${entry} --restore。\n仅本地：Windows setup.ps1 -LocalOnly；POSIX bash ${entry} --local-only。\nreport.html/report.json 仅保存在本机，不会上传网站；配置保存与调用验证是独立结果。\n\n本包 auth.json 含完整密钥，请勿转发、提交仓库或截图公开。下载包和历史备份均需妥善保管。\nhttps://developers.openai.com/codex/cli/\n`;
  const files = {
    'payload/config.toml': strToU8(config),
    'payload/auth.json': [strToU8(auth), { os: 3, attrs: 0o100600 << 16 }],
    'payload/manifest.json': strToU8(JSON.stringify(manifest, null, 2) + '\n'),
    'payload/settings.tsv': strToU8(settings),
    'payload/checksums.sha256': strToU8(
      `${configHash}  config.toml\n${authHash}  auth.json\n`,
    ),
    'README.txt': strToU8(instructions),
  };
  if (target === 'windows') {
    files['setup.cmd'] = strToU8(launcher.replace(/\r?\n/g, '\r\n'));
    files['setup.ps1'] = strToU8(
      '\uFEFF' + windows.replace(/^\uFEFF/, '').replace(/\r?\n/g, '\r\n'),
    );
  } else {
    files[entry] = [
      strToU8(posix.replace(/\r\n/g, '\n')),
      { os: 3, attrs: 0o100700 << 16 },
    ];
  }
  return {
    filename: `new-api-codex-${target}-v1.zip`,
    blob: new Blob([zipSync(files, { level: 6 })], { type: 'application/zip' }),
  };
}
