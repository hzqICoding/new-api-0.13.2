# Fixed template: payload is data, never PowerShell code. Compatible with PS 5.1.
[CmdletBinding()]
param([switch]$Restore, [switch]$CheckOnly, [switch]$LocalOnly)
Set-StrictMode -Version 2
$ErrorActionPreference = 'Stop'
# A PS7 parent may export its module path to PS5.1; load this runtime's built-in ACL module explicitly.
Import-Module (Join-Path $PSHOME 'Modules/Microsoft.PowerShell.Security/Microsoft.PowerShell.Security.psd1') -ErrorAction Stop
$script:checks = New-Object System.Collections.ArrayList
$script:applied = $false
$script:lockHandle = $null
$script:backup = $null
$script:transactionStarted = $false
$script:reportDir = $null
$script:lastCheck = [DateTime]::UtcNow
$checkNames=@{environment='运行环境';directory='配置目录';config='配置文件';permissions='访问权限';auth='认证文件';backup='备份';codex_runtime='Codex CLI';tls='HTTPS 连接';authentication='渠道鉴权';models='模型清单';cli_inference='短 CLI 调用';restore='恢复备份'}
$statusNames=@{passed='通过';failed='失败';warning='警告';skipped='跳过'}
$utf8 = New-Object System.Text.UTF8Encoding($false)
if(-not ('CarpoolSetupAcl' -as [type])){Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class CarpoolSetupAcl {
    [DllImport("advapi32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
    public static extern bool SetFileSecurity(string path, uint information, byte[] descriptor);
}
'@
}
function Apply-Acl($path,$acl) {
    [uint32]$flags=4
    if($acl.AreAccessRulesProtected){$flags=$flags -bor [uint32]2147483648}else{$flags=$flags -bor [uint32]536870912}
    if(-not [CarpoolSetupAcl]::SetFileSecurity($path,$flags,$acl.GetSecurityDescriptorBinaryForm())){throw 'Cannot apply file access permissions.'}
}
function Test-Endpoint($value) {
    if($value -isnot [string] -or $value.Length -gt 1024 -or $value -match '[^\x21-\x7e]' -or $value -cnotmatch '^https://(?:[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?|\[[0-9A-Fa-f:]+\])(?::[0-9]{1,5})?(?:/[A-Za-z0-9._~-]+)*/v1$' -or $value -match '/\.\.?(/|$)'){return $false}
    $parsed=$null
    return [Uri]::TryCreate($value,[UriKind]::Absolute,[ref]$parsed) -and $parsed.Scheme -ceq 'https' -and -not $parsed.UserInfo -and -not $parsed.Query -and -not $parsed.Fragment -and $parsed.Port -gt 0 -and $parsed.Port -le 65535
}
function Result($id, $status, $message, $action = '') {
    $name=if($checkNames.ContainsKey($id)){$checkNames[$id]}else{$id}
    if(-not $action -and $status -in @('failed','warning')){$action='请按本项说明处理后，重新运行仅检查模式；再次选择 FULL 可能产生用量。'}
    $now=[DateTime]::UtcNow
    [void]$script:checks.Add([ordered]@{id=$id;name=$name;status=$status;duration_ms=[int]($now-$script:lastCheck).TotalMilliseconds;message=$message;reason_code=$id+'_'+$status;action=$action})
    $script:lastCheck=$now
    Write-Host ('[{0}] {1}: {2}' -f $statusNames[$status],$name,$message)
}
function Assert-Path($path) {
    if ($path -match '[\x00-\x1f\x7f]' -or $path -notmatch '^[A-Za-z]:[\\/]') { throw 'Unsafe or non-local absolute path.' }
    $cursor = [IO.Path]::GetFullPath($path)
    while ($cursor) {
        if (Test-Path -LiteralPath $cursor) {
            $entry = Get-Item -LiteralPath $cursor -Force
            if (($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'Symbolic links/junctions are not supported.' }
            if ($cursor -ne [IO.Path]::GetFullPath($path) -and -not $entry.PSIsContainer) { throw 'A parent path is not a directory.' }
        }
        $parent = [IO.Directory]::GetParent($cursor)
        if ($null -eq $parent) { break }; $cursor = $parent.FullName
    }
}
function Private-Acl($path) {
    if(Test-Private $path){return}
    $item = Get-Item -LiteralPath $path -Force
    $acl = Get-Acl -LiteralPath $path
    $acl.SetAccessRuleProtection($true,$false)
    foreach($existingRule in @($acl.Access)){[void]$acl.RemoveAccessRuleSpecific($existingRule)}
    foreach ($identity in @($script:sid, (New-Object Security.Principal.SecurityIdentifier('S-1-5-18')))) {
        $rule = if ($item.PSIsContainer) { New-Object Security.AccessControl.FileSystemAccessRule($identity,'FullControl','ContainerInherit,ObjectInherit','None','Allow') } else { New-Object Security.AccessControl.FileSystemAccessRule($identity,'FullControl','Allow') }
        $acl.AddAccessRule($rule)
    }
    Apply-Acl $path $acl
}
function Test-Private($path) {
    Assert-Path $path
    $acl = Get-Acl -LiteralPath $path
    foreach ($rule in $acl.Access) {
        if ($rule.AccessControlType -eq 'Allow' -and $rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value -notin @($script:sid.Value,'S-1-5-18','S-1-5-32-544')) { return $false }
    }
    return $true
}
function Assert-DirectoryWriters($path) {
    $cursor=$path
    while($cursor){
        if(Test-Path -LiteralPath $cursor){
            $acl=Get-Acl -LiteralPath $cursor
            $allowedWriters=@($script:sid.Value,'S-1-5-18','S-1-5-32-544')
            if($acl.GetOwner([Security.Principal.SecurityIdentifier]).Value -eq $script:sid.Value){$allowedWriters+='S-1-3-4'}
            $writeMask=[Security.AccessControl.FileSystemRights]'DeleteSubdirectoriesAndFiles,ChangePermissions,TakeOwnership'
            if($cursor -eq $path){$writeMask=$writeMask -bor [Security.AccessControl.FileSystemRights]'Write,Delete'}
            foreach($rule in $acl.Access){
                if($rule.AccessControlType -eq 'Allow' -and ($rule.PropagationFlags -band [Security.AccessControl.PropagationFlags]::InheritOnly) -eq 0 -and ($rule.FileSystemRights -band $writeMask) -ne 0 -and $rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value -notin $allowedWriters){throw 'A target directory or parent permits writes by another identity.'}
            }
        }
        $parent=[IO.Directory]::GetParent($cursor);if($null -eq $parent){break};$cursor=$parent.FullName
    }
}
function Write-Private($path, $text) {
    Assert-Path $path
    [IO.File]::WriteAllText($path,$text,$utf8)
    Private-Acl $path
}
function Hash($path) {
    $stream=[IO.File]::OpenRead($path); $algorithm=[Security.Cryptography.SHA256]::Create()
    try{return [BitConverter]::ToString($algorithm.ComputeHash($stream)).Replace('-','').ToLowerInvariant()}finally{$stream.Dispose();$algorithm.Dispose()}
}
function File-Check($path) {
    Assert-Path $path
    if (Test-Path -LiteralPath $path) {
        $item = Get-Item -LiteralPath $path -Force
        if ($item.PSIsContainer -or $item.Length -gt 1048576) { throw 'Expected an ordinary file of at most 1 MiB.' }
    }
}
function Move-Replace($source,$destination) {
    File-Check $destination
    if (Test-Path -LiteralPath $destination) { Private-Acl $destination; [IO.File]::Replace($source,$destination,[NullString]::Value) } else { [IO.File]::Move($source,$destination) }
    if(-not(Test-Private $destination)){throw 'Replaced file has unsafe ACL.'}
}
function Write-Journal($backupName,$state) {
    $temp = Join-Path $target ('.carpool-journal-'+[guid]::NewGuid().ToString('N')+'.tmp')
    Write-Private $temp (@{backup=$backupName;state=$state;schema_version=1}|ConvertTo-Json -Compress)
    Move-Replace $temp $journalPath
}
function Load-Backup($name) {
    if ($name -notmatch '^carpool-setup-[0-9]{14}-[a-f0-9]{32}$') { throw 'Invalid backup reference.' }
    $path = Join-Path (Join-Path $target 'backups') $name
    Assert-Path $path
    File-Check (Join-Path $path 'state.json')
    $state = Get-Content -LiteralPath (Join-Path $path 'state.json') -Raw | ConvertFrom-Json
    if ($state.schema_version -ne 1) { throw 'Unsupported backup state.' }
    foreach ($name in @('config.toml','auth.json')) {
        $entry = $state.files.$name
        if ($entry.existed -isnot [bool]) { throw 'Invalid backup existence marker.' }
        if ($entry.existed) {
            File-Check (Join-Path $path $name)
            if ((Hash (Join-Path $path $name)) -ne $entry.sha256) { throw 'Backup integrity check failed.' }
        }
    }
    return @{path=$path;state=$state}
}
function Recover($name) {
    $saved = Load-Backup $name
    foreach ($name in @('config.toml','auth.json')) {
        $dest = Join-Path $target $name
        File-Check $dest
        $entry = $saved.state.files.$name
        if ($entry.existed) {
            $tmp = Join-Path $target ('.carpool-restore-'+[guid]::NewGuid().ToString('N')+'.tmp')
            Copy-Item -LiteralPath (Join-Path $saved.path $name) -Destination $tmp
            Private-Acl $tmp
            Move-Replace $tmp $dest
            $acl = Get-Acl -LiteralPath $dest
            $acl.SetSecurityDescriptorSddlForm($entry.sddl,[Security.AccessControl.AccessControlSections]::Access)
            Apply-Acl $dest $acl
        } elseif (Test-Path -LiteralPath $dest) { Remove-Item -LiteralPath $dest -Force }
    }
}
function Run-CLI($arguments,$working,$cliHome,$timeout=90) {
    $psi = New-Object Diagnostics.ProcessStartInfo
    $psi.FileName = (Get-Process -Id $PID).Path
    $psi.UseShellExecute = $false; $psi.CreateNoWindow=$true
    $psi.RedirectStandardOutput=$true; $psi.RedirectStandardError=$true
    $psi.WorkingDirectory=$working
    foreach ($name in @($psi.EnvironmentVariables.Keys)) {
        if ($name -match '^(OPENAI|AZURE_OPENAI|CODEX_API|CODEX_AUTH|CHATGPT|ANTHROPIC|CLAUDE|GEMINI|GOOGLE_API)') { $psi.EnvironmentVariables.Remove($name) }
    }
    $psi.EnvironmentVariables['CODEX_HOME']=$cliHome
    $psi.EnvironmentVariables['CARPOOL_CLI_PATH']=$script:cli.Source
    $psi.EnvironmentVariables['CARPOOL_CLI_ARGS']=($arguments|ConvertTo-Json -Compress)
    $code = '$a=ConvertFrom-Json $env:CARPOOL_CLI_ARGS; & $env:CARPOOL_CLI_PATH @a; exit $LASTEXITCODE'
    $psi.Arguments='-NoLogo -NoProfile -NonInteractive -EncodedCommand '+[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($code))
    $p=New-Object Diagnostics.Process; $p.StartInfo=$psi
    [void]$p.Start()
    $err=$p.StandardError.ReadToEndAsync()
    $line=$p.StandardOutput.ReadLineAsync(); $output=New-Object Text.StringBuilder
    $timer=[Diagnostics.Stopwatch]::StartNew(); $stopCode=0; $ended=$false
    while (-not $ended) {
        if($timer.Elapsed.TotalSeconds -gt $timeout){$stopCode=-1;break}
        if($line.IsCompleted){
            if($null -eq $line.Result){$ended=$true;break}
            [void]$output.AppendLine($line.Result)
            if($line.Result -match '"type"\s*:\s*"(command_execution|mcp_tool_call|web_search|file_change|tool_call)"'){$stopCode=-2;break}
            if($output.Length -gt 1048576){$stopCode=-3;break}
            $line=$p.StandardOutput.ReadLineAsync()
        }else{Start-Sleep -Milliseconds 30}
    }
    if($stopCode -ne 0 -or -not $p.WaitForExit([Math]::Max(1,($timeout*1000)-[int]$timer.ElapsedMilliseconds))){
        if($stopCode -eq 0){$stopCode=-1}
        & "$env:SystemRoot\System32\taskkill.exe" /PID $p.Id /T /F 2>&1 | Out-Null
        $p.Dispose(); return @{code=$stopCode;output='';timeout=($stopCode -eq -1)}
    }
    $p.WaitForExit(); $null=$err.Result; $exitCode=$p.ExitCode; $p.Dispose()
    return @{code=$exitCode;output=$output.ToString();timeout=$false}
}
function Doctor {
    $script:lastCheck=[DateTime]::UtcNow
    Result 'environment' 'passed' ('Windows '+[Environment]::OSVersion.Version+' / '+([IntPtr]::Size*8)+' 位 / PowerShell '+$PSVersionTable.PSVersion)
    Result 'directory' 'passed' '已确认实际配置目录，路径中没有符号链接。'
    foreach ($name in @('config.toml','auth.json')) {
        if (-not (Test-Path -LiteralPath (Join-Path $target $name))) { Result $name 'failed' '配置文件缺失。' '请运行配置入口安装配置。'; return }
        File-Check (Join-Path $target $name)
    }
    $same=$true
    foreach ($name in @('config.toml','auth.json')) { if ((Hash (Join-Path $target $name)) -ne $manifest.files.$name) { $same=$false } }
    Result 'config' $(if($same){'passed'}else{'warning'}) $(if($same){'配置文件与本次下载包一致。'}else{'现有文件与本包不同，已跳过联网检查，以免测试到其他配置。'})
    $private=(Test-Private (Join-Path $target 'auth.json')) -and (Test-Private (Join-Path $target 'config.toml'))
    Result 'permissions' $(if($private){'passed'}else{'failed'}) $(if($private){'配置文件访问权限已限制。'}else{'配置文件允许其他身份访问，请收紧权限。'})
    try {
        $auth=Get-Content -LiteralPath (Join-Path $target 'auth.json') -Raw|ConvertFrom-Json
        $key=$auth.OPENAI_API_KEY
        if ($key -isnot [string] -or $key -notmatch '^[\x21-\x7e]{1,4096}$') { throw 'Invalid credential.' }
        Result 'auth' 'passed' 'API 密钥格式已读取，实际密钥不会显示。'
    } catch { Result 'auth' 'failed' '无法读取有效 API 密钥。'; return }
    Result 'backup' $(if($script:backup){'passed'}else{'skipped'}) $(if($script:backup){'已备份原始内容、存在状态和访问权限。'}else{'本次无需新建备份。'})
    $script:cli=Get-Command codex -ErrorAction SilentlyContinue
    if ($script:cli -and $script:cli.CommandType -notin @('Application','ExternalScript')) { $script:cli=$null }
    $isolated=Join-Path ([IO.Path]::GetTempPath()) ('carpool-doctor-'+[guid]::NewGuid().ToString('N'))
    Assert-Path $isolated
    New-Item -ItemType Directory -Path $isolated|Out-Null; Private-Acl $isolated
    $testHome=Join-Path $isolated 'home'; $work=Join-Path $isolated 'work'
    New-Item -ItemType Directory -Path $testHome,$work|Out-Null
    try {
        foreach ($name in @('config.toml','auth.json')) { Copy-Item -LiteralPath (Join-Path $target $name) -Destination (Join-Path $testHome $name); Private-Acl (Join-Path $testHome $name) }
        $cliReady=$false
        if ($script:cli) {
            $version=Run-CLI @('--version') $work $testHome 5
            $cliReady=$version.code -eq 0
            $versionLabel='版本格式未识别'
            if($version.output -match '(?m)^codex-cli\s+([0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}(?:[-+][a-zA-Z0-9.-]{1,40})?)\s*$'){$versionLabel=$Matches[1]}
            Result 'codex_runtime' $(if($cliReady){'passed'}else{'warning'}) $(if($cliReady){'Codex CLI 可以正常启动。版本：'+$versionLabel}else{'Codex CLI 启动失败或超过 5 秒。'})
            Write-Host ('CLI 路径：'+$script:cli.Source)
        } else { Result 'codex_runtime' 'warning' '未在 PATH 找到 Codex CLI；配置文件仍已保存。' '请按官方指引安装 Codex CLI。' }
        if (-not $script:full -or -not $same -or -not $private) {
            foreach ($id in @('tls','authentication','models','cli_inference')) { Result $id 'skipped' '未选择完整检查，或本地前置检查未通过。' }; return
        }
        [Net.ServicePointManager]::SecurityProtocol=[Net.SecurityProtocolType]::Tls12
        $authRejected=$false
        try {
            $request=[Net.HttpWebRequest]::Create($manifest.base_url+'/models'); $request.AllowAutoRedirect=$false; $request.Timeout=15000; $request.ReadWriteTimeout=15000
            $request.Headers['Authorization']='Bearer '+$key
            try { $response=$request.GetResponse() } catch [Net.WebException] { if ($_.Exception.Response) { $response=$_.Exception.Response } else { throw } }
            $status=[int]$response.StatusCode
            Result 'tls' 'passed' 'HTTPS 证书校验成功；不会跟随重定向。'
            if ($status -eq 200) {
                $reader=New-Object IO.StreamReader($response.GetResponseStream()); $buffer=New-Object char[] 262144; $count=$reader.ReadBlock($buffer,0,$buffer.Length); $body=-join $buffer[0..([Math]::Max(0,$count-1))]; $reader.Dispose()
                try { $models=$body|ConvertFrom-Json; if($null -eq $models.data -or $models.data -isnot [array]){throw 'Unexpected model response.'}; Result 'authentication' 'passed' '模型接口接受了密钥并返回有效模型清单。'; $found=@($models.data|Where-Object {$_.id -eq $manifest.model}).Count -gt 0; Result 'models' $(if($found){'passed'}else{'warning'}) $(if($found){'模型清单包含所选模型。'}else{'清单未列出所选模型，仍可根据实际 CLI 测试判断。'}) } catch { Result 'authentication' 'warning' 'HTTP 200 响应不是有效模型数据，尚未验证鉴权。'; Result 'models' 'warning' '模型接口未返回受支持的模型清单。' }
            } else {
                $authRejected=$status -in @(401,403)
                Result 'authentication' $(if($status -in @(401,403)){'failed'}else{'warning'}) ('Models endpoint returned HTTP '+$status+'. No redirect was followed.') '请检查密钥、座位状态和所选线路。'
                Result 'models' 'warning' '模型清单暂不可用，请参考实际 CLI 测试结果。'
            }; $response.Close()
        } catch { Result 'tls' 'failed' 'HTTPS 请求失败或超时。' '请检查网络、代理和证书信任。'; Result 'authentication' 'skipped' '尚未取得有效鉴权响应。'; Result 'models' 'skipped' '尚未取得模型清单。' }
        if($authRejected){Result 'cli_inference' 'skipped' '渠道已明确拒绝鉴权，跳过实际模型调用。';return}
        if (-not $cliReady -or -not $manifest.run_cli_test) { Result 'cli_inference' 'skipped' 'CLI 不可用，或本包未启用可选 CLI 测试。'; return }
        $help=Run-CLI @('exec','--help') $work $testHome 5
        foreach ($flag in @('--ephemeral','--skip-git-repo-check','--json','--sandbox','--output-last-message','--cd','--config')) {
            if ($help.code -ne 0 -or -not $help.output.Contains($flag)) { Result 'cli_inference' 'skipped' '当前 CLI 缺少必要的安全参数，请更新 CLI。'; return }
        }
        $reply=Join-Path $isolated 'reply.txt'
        $r=Run-CLI @('exec','--ephemeral','--skip-git-repo-check','--json','--sandbox','read-only','-c','approval_policy="never"','-c','web_search="disabled"','-c','project_doc_max_bytes=0','-C',$work,'-o',$reply,'Do not use tools. Reply only CODEX_SETUP_OK.') $work $testHome 90
        if ($r.timeout) { Result 'cli_inference' 'failed' 'CLI 测试超过 90 秒，仅终止了本次测试进程树。' }
        elseif ($r.code -eq -2) { Result 'cli_inference' 'failed' '检测到不符合预期的工具调用，已终止测试进程树。' }
        elseif ($r.code -ne 0 -or -not (Test-Path -LiteralPath $reply) -or -not ([IO.File]::ReadAllText($reply).Trim())) { Result 'cli_inference' 'failed' 'CLI 未正常完成，或没有返回最终回复。' '请检查模型权限、额度和网络；本工具不会自动重试。' }
        elseif ([IO.File]::ReadAllText($reply).Trim() -ceq 'CODEX_SETUP_OK') { Result 'cli_inference' 'passed' '隔离 CLI 已返回 CODEX_SETUP_OK。客户端可能有内部重试，实际用量和费用未知。' }
        else { Result 'cli_inference' 'warning' 'CLI 调用成功，但回复格式不同；不会保留回复内容。' }
    } finally { if (Test-Path -LiteralPath $isolated) { Assert-Path $isolated; Remove-Item -LiteralPath $isolated -Recurse -Force } }
}
function Save-Report {
    if (-not $script:reportDir) { return }
    $readiness='not_verified'
    if (@($script:checks|Where-Object {$_.status -in @('failed','warning')}).Count) { $readiness='attention' }
    elseif (@($script:checks|Where-Object {$_.id -eq 'cli_inference' -and $_.status -eq 'passed'}).Count) { $readiness='verified' }
    $report=[ordered]@{schema_version=1;template_version='newapi-codex-v1';generated_at=[DateTime]::UtcNow.ToString('o');mode=$(if($Restore){'restore'}elseif($CheckOnly){'check_only'}else{'configure_and_check'});configuration_applied=$script:applied;readiness=$readiness;checks=@($script:checks)}
    Write-Private (Join-Path $script:reportDir 'report.json') ($report|ConvertTo-Json -Depth 8)
    $html='<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>Codex 本机检查报告</title><style>body{font:16px sans-serif;max-width:900px;margin:40px auto;padding:16px;background:#f7f9fc;color:#182230}ul{padding:0;list-style:none}li{background:white;border:1px solid #ddd;border-left:5px solid #98a2b3;padding:16px;margin:12px 0;border-radius:8px}.passed{border-left-color:#12b76a}.failed{border-left-color:#f04438}.warning{border-left-color:#f79009}small{color:#667085}</style><h1>Codex 本机检查报告</h1><p>报告仅保存在本机，不含密钥或个人目录。结果：'+$readiness+'</p><ul>'
    foreach($check in $script:checks) { $html+='<li class="'+$check.status+'"><strong>'+[Net.WebUtility]::HtmlEncode($statusNames[$check.status]+' · '+$check.name)+'</strong><p>'+[Net.WebUtility]::HtmlEncode($check.message)+'</p><small>'+[Net.WebUtility]::HtmlEncode($check.action)+' · '+$check.duration_ms+' ms</small></li>' }
    Write-Private (Join-Path $script:reportDir 'report.html') ($html+'</ul>')
    Write-Host ('本机报告：'+$script:reportDir)
}
$exitCode=0
try {
    if ($env:OS -ne 'Windows_NT') { throw 'Use setup.sh on POSIX systems.' }
    if ($Restore -and $CheckOnly) { throw 'Restore and CheckOnly cannot be combined.' }
    $identity=[Security.Principal.WindowsIdentity]::GetCurrent(); $script:sid=$identity.User
    if ((New-Object Security.Principal.WindowsPrincipal($identity)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Run as the ordinary Codex user, without administrator elevation.' }
    $target=if ([string]::IsNullOrEmpty($env:CODEX_HOME)) { Join-Path ([Environment]::GetFolderPath('UserProfile')) '.codex' } else { $env:CODEX_HOME }
    Assert-Path $target; $target=[IO.Path]::GetFullPath($target)
    Assert-DirectoryWriters $target
    Write-Host ('Codex 实际配置目录：'+$target)
    foreach ($name in @('config.toml','auth.json','.carpool-setup.lock','.carpool-setup-journal.json')) { File-Check (Join-Path $target $name) }
    if ((Test-Path -LiteralPath $target) -and -not (Get-Item -LiteralPath $target).PSIsContainer) { throw 'CODEX_HOME is not a directory.' }
    if ($CheckOnly -and -not (Test-Path -LiteralPath $target)) { throw 'Configuration directory does not exist.' }
    $script:full=$false
    if (-not $LocalOnly -and -not $Restore) { $script:full=(Read-Host '输入 FULL 进行完整联网检查（短 CLI 测试可能产生少量费用）；直接回车仅做本地检查') -ceq 'FULL' }
    $payload=Join-Path $PSScriptRoot 'payload'
    if (-not $Restore) {
        foreach($name in @('manifest.json','config.toml','auth.json')) { File-Check (Join-Path $payload $name) }
        $manifest=Get-Content -LiteralPath (Join-Path $payload 'manifest.json') -Raw|ConvertFrom-Json
        if ($manifest.schema_version -ne 1 -or $manifest.template_version -cne 'newapi-codex-v1' -or $manifest.target -cne 'windows' -or $manifest.model -cnotin @('gpt-5.6-sol','gpt-6-sol','gpt-6-astra','gpt-6-luna','gpt-6.1-sol') -or -not (Test-Endpoint $manifest.base_url) -or $manifest.run_cli_test -isnot [bool]) { throw 'Unsupported package settings.' }
        foreach($name in @('config.toml','auth.json')) { if ($manifest.files.$name -notmatch '^[a-f0-9]{64}$' -or (Hash (Join-Path $payload $name)) -cne $manifest.files.$name) { throw 'Payload checksum mismatch.' } }
    }
    if (-not (Test-Path -LiteralPath $target)) { New-Item -ItemType Directory -Path $target|Out-Null; Private-Acl $target }
    $lockPath=Join-Path $target '.carpool-setup.lock'
    $script:lockHandle=[IO.File]::Open($lockPath,[IO.FileMode]::OpenOrCreate,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None)
    Private-Acl $lockPath
    $journalPath=Join-Path $target '.carpool-setup-journal.json'
    if (Test-Path -LiteralPath $journalPath) {
        $journal=Get-Content -LiteralPath $journalPath -Raw|ConvertFrom-Json
        if ($journal.state -ne 'committed' -and -not $Restore) { throw 'Unfinished journal found. Run setup.ps1 -Restore before continuing.' }
    }
    if ($Restore) {
        $backups=Join-Path $target 'backups'; Assert-Path $backups
        $names=@(Get-ChildItem -LiteralPath $backups -Directory|Where-Object {$_.Name -match '^carpool-setup-[0-9]{14}-[a-f0-9]{32}$'}|Sort-Object Name -Descending)
        if (-not $names.Count) { throw 'No backup found.' }
        foreach($item in $names) { Write-Host $item.Name }
        $chosen=Read-Host '粘贴要恢复的备份名称（回车取消）'
        if (-not $chosen) { Write-Host '已取消。'; exit 0 }
        $null=Load-Backup $chosen
        if ((Read-Host '恢复将覆盖当前 config/auth（包括后续手动修改）。确认请输入 RESTORE') -cne 'RESTORE') { Write-Host '已取消。'; exit 0 }
        Write-Journal $chosen 'restoring'
        Recover $chosen
        if (Test-Path -LiteralPath $journalPath) { Remove-Item -LiteralPath $journalPath -Force }
        Result 'restore' 'passed' '已恢复原文件内容、是否存在的状态和访问权限。'
    } elseif (-not $CheckOnly) {
        $same=$true; $existing=$false
        foreach($name in @('config.toml','auth.json')) { $dest=Join-Path $target $name; if(Test-Path -LiteralPath $dest) {$existing=$true; if((Hash $dest) -cne $manifest.files.$name){$same=$false}}else{$same=$false} }
        if ($same) { foreach($name in @('config.toml','auth.json')) { Private-Acl (Join-Path $target $name) }; $script:applied=$true; Write-Host '已是当前配置，本次未重复创建备份。' }
        else {
            if ($existing -and (Read-Host '将先备份，再替换 config/auth，现有 MCP、hooks、profiles 和登录设置将被替换。确认请输入 REPLACE') -cne 'REPLACE') { Write-Host '已取消，原配置未变更。'; exit 0 }
            $backups=Join-Path $target 'backups'; Assert-Path $backups
            if(-not(Test-Path -LiteralPath $backups)){New-Item -ItemType Directory -Path $backups|Out-Null;Private-Acl $backups}
            if(-not(Test-Private $backups)){throw 'Backup directory is accessible to other identities.'}
            $backupName='carpool-setup-'+[DateTime]::UtcNow.ToString('yyyyMMddHHmmss')+'-'+[guid]::NewGuid().ToString('N')
            $script:backup=Join-Path $backups $backupName;New-Item -ItemType Directory -Path $script:backup|Out-Null;Private-Acl $script:backup
            $states=@{}
            foreach($name in @('config.toml','auth.json')) {
                $dest=Join-Path $target $name; $exists=Test-Path -LiteralPath $dest
                $states[$name]=@{existed=[bool]$exists;sha256=$null;sddl=$null;new_sha256=$manifest.files.$name}
                if($exists){$states[$name].sha256=Hash $dest;$states[$name].sddl=(Get-Acl -LiteralPath $dest).Sddl;Copy-Item -LiteralPath $dest -Destination (Join-Path $script:backup $name);Private-Acl (Join-Path $script:backup $name)}
            }
            Write-Private (Join-Path $script:backup 'state.json') (@{schema_version=1;files=$states}|ConvertTo-Json -Depth 6)
            $null=Load-Backup $backupName
            Write-Journal $backupName 'prepared';$script:transactionStarted=$true
            foreach($name in @('config.toml','auth.json')) {
                $tmp=Join-Path $target ('.carpool-install-'+[guid]::NewGuid().ToString('N')+'.tmp')
                Copy-Item -LiteralPath (Join-Path $payload $name) -Destination $tmp;Private-Acl $tmp
                Move-Replace $tmp (Join-Path $target $name)
                if((Hash (Join-Path $target $name)) -cne $manifest.files.$name){throw 'Post-write checksum mismatch.'}
                Write-Journal $backupName ('replaced-'+$name)
            }
            Write-Journal $backupName 'committed';$script:transactionStarted=$false;$script:applied=$true
            Write-Host ('备份位置：'+$script:backup)
        }
    }
    $script:reportDir=Join-Path $target ('carpool-report-'+[DateTime]::UtcNow.ToString('yyyyMMddHHmmss')+'-'+[guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $script:reportDir|Out-Null;Private-Acl $script:reportDir
    if(-not $Restore){Doctor}
    Save-Report
    if(@($script:checks|Where-Object {$_.status -eq 'failed'}).Count){$exitCode=2}
    Write-Host '配置后请重新打开 Codex。下载包和备份均包含密钥，请妥善保管。'
} catch {
    $exitCode=1
    if($script:transactionStarted -and $script:backup){
        try{Recover ([IO.Path]::GetFileName($script:backup));Remove-Item -LiteralPath $journalPath -Force;Write-Host '[失败] 安装失败，已恢复原文件。'}
        catch{Write-Host ('[FAILED] Recovery incomplete; keep backup and journal. 备份位置：'+$script:backup)}
    } else { Write-Host '[失败] 配置已停止，本次未完成。' }
    # Exceptions may embed credential-bearing paths/responses; only static messages are reported.
    if($_.Exception.Message -match '^[A-Za-z][A-Za-z0-9 ./;(),:-]{1,180}$'){Write-Host $_.Exception.Message}
} finally { if($script:lockHandle){$script:lockHandle.Dispose()} }
exit $exitCode
