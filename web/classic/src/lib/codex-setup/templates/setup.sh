#!/usr/bin/env bash
# Bash 3.2+; no payload is ever sourced/evaluated. No root/sudo required.
set -euo pipefail
umask 077
mode=configure
local_only=false
full=false
applied=false
locked=false
transaction=false
backup_name=
scratch=
cli_pid=
report_dir=
failure=false
warning=false
verified=false
rows=()
html_rows=()
last_check=$SECONDS
files=(config.toml auth.json)
for arg in "$@"; do
  case "$arg" in
    --restore) [ "$mode" = configure ] || { echo 'Conflicting modes.'; exit 1; }; mode=restore ;;
    --check-only) [ "$mode" = configure ] || { echo 'Conflicting modes.'; exit 1; }; mode=check_only ;;
    --local-only) local_only=true ;;
    *) echo 'Usage: bash setup.sh [--restore | --check-only] [--local-only]'; exit 1 ;;
  esac
done
die() { printf '[FAILED] %s\n' "$1" >&2; exit 1; }
result() {
  # Callers only pass fixed messages/enumerations, never endpoint bodies or keys.
  local label status_label advice= duration
  case "$1" in environment) label=运行环境;;directory) label=配置目录;;config) label=配置文件;;permissions) label=访问权限;;auth) label=认证文件;;backup) label=备份;;codex_runtime) label='Codex CLI';;tls) label='HTTPS 连接';;authentication) label=渠道鉴权;;models) label=模型清单;;cli_inference) label='短 CLI 调用';;restore) label=恢复备份;;*) label=$1;;esac
  case "$2" in passed) status_label=通过;;failed) status_label=失败;;warning) status_label=警告;;skipped) status_label=跳过;;esac
  if [ "$2" = failed ] || [ "$2" = warning ]; then advice='请按本项说明处理后重新检查；再次选择 FULL 可能产生用量。'; fi
  duration=$(( (SECONDS-last_check)*1000 )); last_check=$SECONDS
  printf '[%s] %s: %s\n' "$status_label" "$label" "$3"
  rows+=("{\"id\":\"$1\",\"name\":\"$label\",\"status\":\"$2\",\"duration_ms\":$duration,\"message\":\"$3\",\"reason_code\":\"${1}_$2\",\"action\":\"$advice\"}")
  local escaped=$3
  escaped=${escaped//&/\&amp;}; escaped=${escaped//</\&lt;}; escaped=${escaped//>/\&gt;}
  html_rows+=("<li class=\"$2\"><strong>$status_label · $label</strong><p>$escaped</p><small>$advice · $duration ms</small></li>")
  [ "$2" != failed ] || failure=true
  [ "$2" != warning ] || warning=true
  return 0
}
safe_path() {
  local path=$1 cursor
  case "$path" in /*) ;; *) die 'Expected an absolute path.';; esac
  [[ "$path" != *[$'\001'-$'\037'$'\177']* ]] || die 'Control characters in paths are unsupported.'
  case "$path/" in */../*|*/./*|*//*) die 'Normalize dot/repeated-slash path components first.';; esac
  cursor=$path
  while [ "$cursor" != / ]; do
    [ ! -L "$cursor" ] || die 'Symbolic links in the target or its parents are unsupported.'
    cursor=${cursor%/*}; [ -n "$cursor" ] || cursor=/
  done
}
ordinary() { safe_path "$1"; [ ! -e "$1" ] || [ -f "$1" ] || die 'Expected an ordinary file.'; }
file_mode() { if stat -c %a "$1" >/dev/null 2>&1; then stat -c %a "$1"; else stat -f %Lp "$1"; fi; }
owner() { if stat -c %u "$1" >/dev/null 2>&1; then stat -c %u "$1"; else stat -f %u "$1"; fi; }
private() { local bits; bits=$(file_mode "$1"); [ "$(owner "$1")" = "$(id -u)" ] && (( (8#$bits & 077) == 0 )); }
hash() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d ' ' -f1; else shasum -a 256 "$1" | cut -d ' ' -f1; fi; }
confirm() { local answer; printf '%s\n> ' "$1"; IFS= read -r answer || answer=; answer=${answer%$'\r'}; [ "$answer" = "$2" ]; }
journal() {
  local stage=$1 tmp
  tmp=$(mktemp "$target/.carpool-journal.XXXXXXXX")
  printf '%s\n%s\n' "$backup_name" "$stage" > "$tmp"
  ordinary "$target/.carpool-setup-journal"
  mv -f "$tmp" "$target/.carpool-setup-journal"
}
validate_backup() {
  local chosen=$1 entry marker saved
  [[ "$chosen" =~ ^carpool-setup-[0-9]{14}-[a-zA-Z0-9]+$ ]] || die 'Invalid backup name.'
  saved="$target/backups/$chosen"
  safe_path "$saved"; [ -d "$saved" ] && private "$saved" || die 'Backup permissions are unsafe.'
  for entry in "${files[@]}"; do
    ordinary "$saved/$entry.state"; [ -f "$saved/$entry.state" ] || die 'Missing backup state.'
    marker=$(cat "$saved/$entry.state")
    case "$marker" in
      absent) ;;
      present)
        ordinary "$saved/$entry"; ordinary "$saved/$entry.sha256"; ordinary "$saved/$entry.mode"
        [ "$(hash "$saved/$entry")" = "$(cat "$saved/$entry.sha256")" ] || die 'Backup checksum mismatch.'
        [[ "$(cat "$saved/$entry.mode")" =~ ^[0-7]{3,4}$ ]] || die 'Invalid backup mode.' ;;
      *) die 'Invalid original existence state.' ;;
    esac
  done
}
recover() {
  local chosen=$1 entry tmp saved
  validate_backup "$chosen"
  saved="$target/backups/$chosen"
  for entry in "${files[@]}"; do
    ordinary "$target/$entry"
    if [ "$(cat "$saved/$entry.state")" = present ]; then
      tmp=$(mktemp "$target/.carpool-restore.XXXXXXXX")
      cp "$saved/$entry" "$tmp" && chmod 600 "$tmp" && mv -f "$tmp" "$target/$entry" || return 1
      chmod "$(cat "$saved/$entry.mode")" "$target/$entry" || return 1
    else
      rm -f -- "$target/$entry" || return 1
    fi
  done
}
cleanup() {
  local exit_status=$?
  trap - EXIT INT TERM
  if [ -n "$cli_pid" ]; then kill -TERM -- "-$cli_pid" 2>/dev/null || true; fi
  if $transaction; then
    if (recover "$backup_name"); then
      rm -f -- "$target/.carpool-setup-journal"
      printf '[失败] 安装中断，已恢复原文件。\n' >&2
    else
      printf '[FAILED] Recovery incomplete; retain backup and journal: %s\n' "$target/backups/$backup_name" >&2
    fi
    exit_status=1
  fi
  if [ -n "$scratch" ] && [ -d "$scratch" ] && [ ! -L "$scratch" ]; then
    # This random private directory is created by this invocation only.
    case "$scratch" in "${TMPDIR:-/tmp}"/carpool-doctor.*) rm -rf -- "$scratch" || printf '[warning] 临时凭证清理失败：%s\n' "$scratch" >&2;; esac
  fi
  if $locked; then rm -f -- "$target/.carpool-setup.lock/pid"; rmdir "$target/.carpool-setup.lock" 2>/dev/null || true; fi
  exit "$exit_status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
[ "$(id -u)" != 0 ] && [ -z "${SUDO_USER:-}" ] || die 'Run as the ordinary Codex user without root/sudo.'
command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1 || die 'Install sha256sum or shasum before configuring.'
[ -n "${HOME:-}" ] || die 'HOME is not set.'
target=${CODEX_HOME:-"$HOME/.codex"}
target=${target%/}
safe_path "$target"
[ "$target" != / ] && [ -n "$target" ] || die 'Root is not a supported CODEX_HOME.'
ancestor=$target
while [ "$ancestor" != / ]; do
  if [ -d "$ancestor" ]; then
    ancestor_mode=$(file_mode "$ancestor")
    if (( (8#$ancestor_mode & 022) != 0 && (8#$ancestor_mode & 01000) == 0 )); then die 'A parent directory is writable by other users without sticky protection.'; fi
  fi
  ancestor=${ancestor%/*}; [ -n "$ancestor" ] || ancestor=/
done
printf 'Codex 实际配置目录：%s\n' "$target"
if [ -e "$target" ]; then
  [ -d "$target" ] && [ -w "$target" ] && [ "$(owner "$target")" = "$(id -u)" ] || die 'Target must be a writable directory owned by the current user.'
  bits=$(file_mode "$target"); (( (8#$bits & 022) == 0 )) || die 'Target directory is writable by another identity.'
fi
for entry in "${files[@]}" .carpool-setup-journal; do ordinary "$target/$entry"; done
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
payload="$script_dir/payload"
if [ "$mode" != restore ]; then
  for entry in config.toml auth.json settings.tsv checksums.sha256 manifest.json; do
    ordinary "$payload/$entry"; [ -f "$payload/$entry" ] || die 'Package payload is incomplete.'
    [ "$(wc -c < "$payload/$entry")" -le 1048576 ] || die 'Oversized payload.'
  done
  target_os= model= base_url= run_cli_test= template_version=
  setting_count=0
  while IFS=$'\t' read -r setting value extra || [ -n "${setting:-}" ]; do
    [ -z "${extra:-}" ] || die 'Invalid settings columns.'
    case "$setting" in
      target) [ -z "$target_os" ] || die 'Duplicate target.'; target_os=$value;;
      model) [ -z "$model" ] || die 'Duplicate model.'; model=$value;;
      base_url) [ -z "$base_url" ] || die 'Duplicate endpoint.'; base_url=$value;;
      run_cli_test) [ -z "$run_cli_test" ] || die 'Duplicate CLI option.'; run_cli_test=$value;;
      template_version) [ -z "$template_version" ] || die 'Duplicate template.'; template_version=$value;;
      *) die 'Unknown package setting.';;
    esac
    setting_count=$((setting_count+1))
  done < "$payload/settings.tsv"
  [ "$setting_count" = 5 ] && [ "$template_version" = newapi-codex-v1 ] || die 'Unsupported settings version.'
  case "$target_os" in macos|linux|wsl) ;; *) die 'Use the package for the actual CLI operating system.';; esac
  kernel=$(uname -s)
  case "$target_os:$kernel" in
    macos:Darwin|linux:Linux) ;;
    wsl:Linux) grep -qi microsoft /proc/sys/kernel/osrelease || die 'The WSL package must run inside WSL.';;
    *) die 'Package target differs from the current operating system.';;
  esac
  case "$model" in gpt-5.6-sol|gpt-6-sol|gpt-6-astra|gpt-6-luna|gpt-6.1-sol) ;; *) die 'Unsupported model.';; esac
  [[ "$base_url" =~ ^https://([A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?|\[[0-9A-Fa-f:]+\])(:[0-9]{1,5})?(/[A-Za-z0-9._~-]+)*/v1$ ]] || die '配置地址必须是无认证信息、查询或片段的 HTTPS 地址。'
  [[ "$base_url" != *[$'\001'-$'\040'$'\177']* ]] || die '配置地址不允许空白或控制字符。'
  [ "${#base_url}" -le 1024 ] || die '配置地址过长。'
  case "$base_url/" in */./*|*/../*) die '配置地址不允许点路径。';; esac
  authority=${base_url#https://}; authority=${authority%%/*}
  if [[ "$authority" =~ :([0-9]{1,5})$ ]]; then port=${BASH_REMATCH[1]}; (( 10#$port > 0 && 10#$port <= 65535 )) || die '配置地址端口无效。'; fi
  case "$run_cli_test" in true|false) ;; *) die 'Invalid CLI option.';; esac
  checksum_count=0
  config_hash= auth_hash=
  while read -r checksum filename extra || [ -n "${checksum:-}" ]; do
    [[ "$checksum" =~ ^[a-f0-9]{64}$ ]] && [ -z "${extra:-}" ] || die 'Invalid checksum entry.'
    case "$filename" in
      config.toml) [ -z "$config_hash" ] || die 'Duplicate checksum.'; config_hash=$checksum;;
      auth.json) [ -z "$auth_hash" ] || die 'Duplicate checksum.'; auth_hash=$checksum;;
      *) die 'Unexpected checksum filename.';;
    esac
    [ "$(hash "$payload/$filename")" = "$checksum" ] || die 'Payload checksum mismatch.'
    checksum_count=$((checksum_count+1))
  done < "$payload/checksums.sha256"
  [ "$checksum_count" = 2 ] || die 'Two checksums are required.'
fi
if ! $local_only && [ "$mode" != restore ]; then
  if confirm '输入 FULL 进行完整联网检查（短 CLI 测试可能产生少量费用）；直接回车仅做本地检查.' FULL; then full=true; fi
fi
if [ "$mode" = check_only ]; then [ -d "$target" ] || die 'Configuration directory does not exist.'; fi
if [ ! -d "$target" ]; then mkdir -p -- "$target"; chmod 700 "$target"; fi
safe_path "$target/.carpool-setup.lock"
if ! mkdir -- "$target/.carpool-setup.lock" 2>/dev/null; then
  die 'Configuration lock exists. If an earlier process crashed, verify its recorded pid is no longer running, then remove only the lock directory and run --restore.'
fi
locked=true
printf '%s\n' "$$" > "$target/.carpool-setup.lock/pid"
if [ -f "$target/.carpool-setup-journal" ]; then
  journal_state=$(sed -n '2p' "$target/.carpool-setup-journal")
  [ "$journal_state" = committed ] || [ "$mode" = restore ] || die 'Unfinished journal found. Run setup.sh --restore first.'
fi
if [ "$mode" = restore ]; then
  safe_path "$target/backups"
  printf '可用备份：\n'
  for saved in "$target"/backups/carpool-setup-*; do [ ! -d "$saved" ] || printf '%s\n' "${saved##*/}"; done
  printf '粘贴要恢复的备份名称（回车取消）：'; IFS= read -r chosen || chosen=
  chosen=${chosen%$'\r'}
  [ -n "$chosen" ] || { echo '已取消。'; exit 0; }
  validate_backup "$chosen"
  confirm '恢复将覆盖当前 config/auth（包括后续手动修改）。确认请输入 RESTORE。' RESTORE || { echo '已取消。'; exit 0; }
  backup_name=$chosen
  journal restoring
  recover "$chosen" || die 'Recovery incomplete. Retain backup and journal.'
  rm -f -- "$target/.carpool-setup-journal"
  result restore passed '已恢复原文件内容、是否存在的状态和权限。'
else
  same=true; existing=false
  for entry in "${files[@]}"; do
    if [ -f "$target/$entry" ]; then existing=true; cmp -s "$target/$entry" "$payload/$entry" || same=false; else same=false; fi
  done
  if [ "$mode" = configure ]; then
    if $same; then
      chmod 600 "$target/config.toml" "$target/auth.json"
      applied=true
      echo '已是当前配置，本次未重复创建备份。'
    else
      if $existing; then confirm '将先备份，再替换 config/auth，现有 MCP、hooks、profiles 和登录设置将被替换。确认请输入 REPLACE.' REPLACE || { echo '已取消，原配置未变更。'; exit 0; }; fi
      safe_path "$target/backups"
      if [ ! -e "$target/backups" ]; then mkdir "$target/backups"; chmod 700 "$target/backups"; fi
      [ -d "$target/backups" ] && private "$target/backups" || die 'Backup directory permissions are unsafe.'
      saved=$(mktemp -d "$target/backups/carpool-setup-$(date -u +%Y%m%d%H%M%S)-XXXXXXXX")
      backup_name=${saved##*/}
      for entry in "${files[@]}"; do
        if [ -f "$target/$entry" ]; then
          printf 'present\n' > "$saved/$entry.state"
          cp "$target/$entry" "$saved/$entry"; chmod 600 "$saved/$entry"
          hash "$target/$entry" > "$saved/$entry.sha256"
          file_mode "$target/$entry" > "$saved/$entry.mode"
        else printf 'absent\n' > "$saved/$entry.state"; fi
        hash "$payload/$entry" > "$saved/$entry.new-sha256"
      done
      validate_backup "$backup_name"
      journal prepared; transaction=true
      for entry in "${files[@]}"; do
        tmp=$(mktemp "$target/.carpool-install.XXXXXXXX")
        cp "$payload/$entry" "$tmp"; chmod 600 "$tmp"
        ordinary "$target/$entry"; mv -f "$tmp" "$target/$entry"
        [ "$(hash "$target/$entry")" = "$(hash "$payload/$entry")" ] || die 'Post-write checksum mismatch.'
        journal "replaced-$entry"
      done
      journal committed; transaction=false; applied=true; same=true
      printf '备份位置：%s\n' "$saved"
    fi
  fi
  architecture=$(uname -m)
  case "$architecture" in x86_64|arm64|aarch64|i686) ;; *) architecture=unknown;; esac
  result environment passed "POSIX Bash 环境正常；$target_os / $architecture / Bash ${BASH_VERSINFO[0]}.${BASH_VERSINFO[1]}。"
  result directory passed '配置目录可写，路径中没有符号链接。'
  if $same; then result config passed '配置文件与本次下载包一致。'; else result config warning '现有文件与本包不同，将跳过联网检查。'; fi
  private_files=true
  for entry in "${files[@]}"; do if [ ! -f "$target/$entry" ] || ! private "$target/$entry"; then private_files=false; fi; done
  if $private_files; then result permissions passed '配置文件仅当前用户可访问。'; else result permissions failed '配置文件缺失或可被其他用户访问。'; fi
  if [ -n "$backup_name" ]; then result backup passed '已备份原始内容、存在状态和权限。'; else result backup skipped '本次无需新建备份。'; fi
  auth_valid=false
  if command -v jq >/dev/null 2>&1; then
    if jq -e '(.OPENAI_API_KEY|type == "string") and (.OPENAI_API_KEY|test("^[!-~]{1,4096}$"))' "$target/auth.json" >/dev/null 2>&1; then auth_valid=true; result auth passed 'API 密钥格式已读取，实际密钥不会显示。'; else result auth failed '密钥 JSON 格式不正确。'; fi
  else result auth skipped '未安装 jq，已跳过 JSON 解析和带密钥的 HTTP 检查。'; fi
  scratch=$(mktemp -d "${TMPDIR:-/tmp}/carpool-doctor.XXXXXXXX")
  mkdir "$scratch/home" "$scratch/work"
  if $private_files; then cp "$target/config.toml" "$target/auth.json" "$scratch/home/"; chmod 600 "$scratch/home/"*; fi
  # Job control places each test in its own process group. Never kill by executable name.
  set -m
  run_cli() {
    local timeout_seconds=$1 name start status=0 event
    shift
    (
      for name in $(compgen -e); do
        case "$name" in OPENAI*|AZURE_OPENAI*|CODEX_API*|CODEX_AUTH*|CHATGPT*|ANTHROPIC*|CLAUDE*|GEMINI*|GOOGLE_API*) unset "$name";; esac
      done
      export CODEX_HOME="$scratch/home"
      cd "$scratch/work"
      exec codex "$@"
    ) > "$scratch/stdout" 2> "$scratch/stderr" &
    cli_pid=$!
    start=$SECONDS
    while kill -0 "$cli_pid" 2>/dev/null; do
      if (( SECONDS-start >= timeout_seconds )); then status=124; break; fi
      if grep -Eq '"type"[[:space:]]*:[[:space:]]*"(command_execution|mcp_tool_call|web_search|file_change|tool_call)"' "$scratch/stdout"; then status=125; break; fi
      sleep 0.2
    done
    if [ "$status" != 0 ]; then kill -TERM -- "-$cli_pid" 2>/dev/null || true; sleep 0.2; kill -KILL -- "-$cli_pid" 2>/dev/null || true; fi
    wait "$cli_pid" 2>/dev/null || { event=$?; [ "$status" != 0 ] || status=$event; }
    if [ "$status" = 0 ] && grep -Eq '"type"[[:space:]]*:[[:space:]]*"(command_execution|mcp_tool_call|web_search|file_change|tool_call)"' "$scratch/stdout"; then status=125; fi
    cli_pid=
    return "$status"
  }
  cli_ready=false
  if command -v codex >/dev/null 2>&1; then
    if run_cli 5 --version; then
      cli_ready=true
      version_label=$(sed -nE 's/^codex-cli[[:space:]]+([0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}([-+][a-zA-Z0-9.-]{1,40})?)[[:space:]]*$/\1/p' "$scratch/stdout" | head -n 1)
      [ -n "$version_label" ] || version_label=未识别
      result codex_runtime passed "Codex CLI 可以正常启动；版本：$version_label。"
      printf 'CLI 路径：%s\n' "$(command -v codex)"
    else result codex_runtime warning 'Codex CLI 启动失败或超过 5 秒。'; fi
  else result codex_runtime warning '未找到 Codex CLI，请安装或检查 PATH；配置文件仍已保存。'; fi
  if $full && $same && $private_files; then
    auth_rejected=false
    if command -v curl >/dev/null 2>&1; then
      if curl --disable --silent --show-error --proto '=https' --max-time 15 --max-redirs 0 --output /dev/null "$base_url/models" 2>/dev/null; then result tls passed 'HTTPS 证书校验成功；不会跟随重定向。'; else result tls failed 'HTTPS 失败或超时，请检查网络、代理和证书。'; fi
      if $auth_valid; then
        # jq emits curl config on stdin; the secret never enters argv or environment.
        http_status=$(jq -r '"header = " + ("Authorization: Bearer " + .OPENAI_API_KEY|@json)' "$target/auth.json" | curl --disable --config - --silent --proto '=https' --max-time 15 --max-redirs 0 --max-filesize 262144 --output "$scratch/models.json" --write-out '%{http_code}' "$base_url/models" 2>/dev/null) || http_status=000
        case "$http_status" in
          200)
            if jq -e '.data|type == "array"' "$scratch/models.json" >/dev/null 2>&1; then
              result authentication passed '模型接口接受了密钥并返回有效模型清单。'
              if jq -e --arg model "$model" '.data|any(.id == $model)' "$scratch/models.json" >/dev/null 2>&1; then result models passed '模型清单包含所选模型。'; else result models warning '清单未列出所选模型，仍可根据实际 CLI 测试判断。'; fi
            else result authentication warning 'HTTP 200 响应不是有效模型数据，尚未验证鉴权。'; result models warning '模型响应格式不受支持。'; fi;;
          401|403) auth_rejected=true; result authentication failed '密钥无效或当前座位无访问权限。'; result models skipped '未取得有效鉴权的模型清单。';;
          *) result authentication warning '模型接口不可用或返回重定向；未跟随重定向。'; result models warning '模型清单暂不可用，请参考实际 CLI 测试结果。';;
        esac
      else result authentication skipped '本项需要 JSON 解析器和有效密钥。'; result models skipped '未请求需要鉴权的模型清单。'; fi
    else result tls skipped '未找到 curl，已跳过对应检查。'; result authentication skipped '未找到 curl，已跳过对应检查。'; result models skipped '未找到 curl，已跳过对应检查。'; fi
    if $auth_rejected; then result cli_inference skipped '渠道已明确拒绝鉴权，跳过实际模型调用。'
    elif $cli_ready && [ "$run_cli_test" = true ]; then
      supported=true
      if run_cli 5 exec --help; then
        for flag in --ephemeral --skip-git-repo-check --json --sandbox --output-last-message --cd --config; do grep -q -- "$flag" "$scratch/stdout" || supported=false; done
      else supported=false; fi
      if $supported; then
        cli_status=0
        run_cli 90 exec --ephemeral --skip-git-repo-check --json --sandbox read-only -c 'approval_policy="never"' -c 'web_search="disabled"' -c 'project_doc_max_bytes=0' -C "$scratch/work" -o "$scratch/reply" 'Do not use tools. Reply only CODEX_SETUP_OK.' || cli_status=$?
        case "$cli_status" in
          0)
            if [ ! -s "$scratch/reply" ]; then result cli_inference failed 'CLI 已退出，但没有最终回复。'
            elif [ "$(cat "$scratch/reply")" = CODEX_SETUP_OK ]; then verified=true; result cli_inference passed '隔离 CLI 已返回 CODEX_SETUP_OK。内部重试、实际用量和费用未知。'
            else result cli_inference warning 'CLI 调用成功，但回复格式不同；不会保留回复内容。'; fi;;
          124) result cli_inference failed 'CLI 测试超过 90 秒，仅终止了本次测试进程组。';;
          125) result cli_inference failed '检测到不符合预期的工具调用，已终止测试。';;
          *) result cli_inference failed 'CLI 未正常完成，请检查模型权限、额度和网络。本工具不会自动重试。';;
        esac
      else result cli_inference skipped '当前 CLI 缺少必要的安全参数，请更新 CLI。'; fi
    else result cli_inference skipped 'CLI 不可用，或本包未启用可选 CLI 测试。'; fi
  else
    for entry in tls authentication models cli_inference; do result "$entry" skipped '未选择完整检查，或本地前置检查未通过。'; done
  fi
  set +m
fi
report_dir=$(mktemp -d "$target/carpool-report-$(date -u +%Y%m%d%H%M%S)-XXXXXXXX")
readiness=not_verified
if $verified; then readiness=verified; fi
if $failure || $warning; then readiness=attention; fi
{
  printf '{"schema_version":1,"template_version":"newapi-codex-v1","generated_at":"%s","mode":"%s","configuration_applied":%s,"readiness":"%s","checks":[' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$mode" "$applied" "$readiness"
  separator=
  for row in "${rows[@]}"; do printf '%s%s' "$separator" "$row"; separator=,; done
  printf ']}\n'
} > "$report_dir/report.json"
{
  printf '<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>Codex 本机检查报告</title><style>body{font:16px sans-serif;max-width:900px;margin:40px auto;padding:16px;background:#f7f9fc;color:#182230}ul{padding:0;list-style:none}li{background:white;border:1px solid #ddd;border-left:5px solid #98a2b3;padding:16px;margin:12px 0;border-radius:8px}.passed{border-left-color:#12b76a}.failed{border-left-color:#f04438}.warning{border-left-color:#f79009}small{color:#667085}</style><h1>Codex 本机检查报告</h1><p>报告仅保存在本机，不含密钥或个人目录。</p><ul>'
  for row in "${html_rows[@]}"; do printf '%s' "$row"; done
  printf '</ul></html>'
} > "$report_dir/report.html"
printf '本机报告：%s\n请重新打开 Codex。下载包和备份均包含密钥，请妥善保管。\n' "$report_dir"
if $failure; then exit 2; fi
