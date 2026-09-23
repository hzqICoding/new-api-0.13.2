param(
  [Parameter(Mandatory = $true)]
  [string]$InputArchive,

  [Parameter(Mandatory = $true)]
  [string]$OutputArchive,

  [string]$ExpectedArchiveSha256 = '360D9002DFEC6318A3074165E57BD01EBCD87D1943C39689666CE10AD1920558'
)

$ErrorActionPreference = 'Stop'

$inputPath = (Resolve-Path -LiteralPath $InputArchive).Path
$outputPath = [IO.Path]::GetFullPath($OutputArchive)

if ($inputPath -eq $outputPath) {
  throw 'OutputArchive must differ from InputArchive.'
}

$actualArchiveSha256 = (Get-FileHash -LiteralPath $inputPath -Algorithm SHA256).Hash
if ($ExpectedArchiveSha256 -and $actualArchiveSha256 -ne $ExpectedArchiveSha256.ToUpperInvariant()) {
  throw "Input archive SHA256 mismatch. Expected $ExpectedArchiveSha256, got $actualArchiveSha256."
}

$workRoot = Join-Path ([IO.Path]::GetTempPath()) ("luna-pricing-frontend-" + [guid]::NewGuid().ToString('N'))
$verifyRoot = Join-Path ([IO.Path]::GetTempPath()) ("luna-pricing-verify-" + [guid]::NewGuid().ToString('N'))

try {
  New-Item -ItemType Directory -Path $workRoot | Out-Null
  New-Item -ItemType Directory -Path $verifyRoot | Out-Null

  & tar -xzf $inputPath -C $workRoot
  if ($LASTEXITCODE -ne 0) {
    throw "Failed to extract input archive (tar exit code $LASTEXITCODE)."
  }

  $distRoot = Join-Path $workRoot 'web/classic/dist'
  $indexPath = Join-Path $distRoot 'index.html'
  if (-not (Test-Path -LiteralPath $indexPath -PathType Leaf)) {
    throw 'Expected web/classic/dist/index.html was not found.'
  }

  $beforeFiles = @(Get-ChildItem -LiteralPath $workRoot -Recurse -File)
  if ($beforeFiles.Count -ne 357) {
    throw "Expected 357 frontend files, found $($beforeFiles.Count)."
  }

  $beforeHashes = @{}
  foreach ($file in $beforeFiles) {
    $relative = [IO.Path]::GetRelativePath($workRoot, $file.FullName).Replace('\', '/')
    $beforeHashes[$relative] = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
  }

  $utf8NoBom = [Text.UTF8Encoding]::new($false)
  $indexText = [IO.File]::ReadAllText($indexPath)
  $mainMatch = [regex]::Match(
    $indexText,
    '<script type="module" crossorigin src="/assets/(?<name>index-[^"]+\.js)"></script>'
  )
  if (-not $mainMatch.Success) {
    throw 'Unable to locate the classic frontend main bundle in index.html.'
  }

  $oldMainName = $mainMatch.Groups['name'].Value
  $oldMainRelative = "web/classic/dist/assets/$oldMainName"
  $oldMainPath = Join-Path $distRoot "assets/$oldMainName"
  if (-not (Test-Path -LiteralPath $oldMainPath -PathType Leaf)) {
    throw "Referenced main bundle was not found: $oldMainRelative"
  }

  $oldSnippet = 'qZ=({record:e,selectedGroup:t,groupRatio:r,tokenUnit:n,displayPrice:i,currency:o,quotaDisplayType:s="USD",precision:l=4})=>{let c=t,u=r[t];if(t==="all"||u===void 0){let f=Number.POSITIVE_INFINITY;Array.isArray(e.enable_groups)&&e.enable_groups.length>0&&e.enable_groups.forEach(d=>{const h=r[d];h!==void 0&&h<f&&(f=h,c=d,u=h)}),u===void 0&&(u=1)}if(e.billing_mode'
  $newSnippet = 'qZ=({record:e,selectedGroup:t,groupRatio:r,tokenUnit:n,displayPrice:i,currency:o,quotaDisplayType:s="USD",precision:l=4})=>{let c=t,u=r[t];if(t==="all"||u===void 0){let f=Number.POSITIVE_INFINITY;Array.isArray(e.enable_groups)&&e.enable_groups.length>0&&e.enable_groups.forEach(d=>{const h=r[d];h!==void 0&&h<f&&(f=h,c=d,u=h)}),u===void 0&&(u=1)}Number.isFinite(e.group_ratio_override)&&(u=e.group_ratio_override);if(e.billing_mode'

  $mainText = [IO.File]::ReadAllText($oldMainPath)
  $matchCount = ([regex]::Matches($mainText, [regex]::Escape($oldSnippet))).Count
  if ($matchCount -ne 1) {
    throw "Expected exactly one pricing function match, found $matchCount. Refusing to patch."
  }
  if ($mainText.Contains('group_ratio_override')) {
    throw 'Main bundle already contains group_ratio_override. Refusing to patch twice.'
  }

  $patchedMainText = $mainText.Replace($oldSnippet, $newSnippet)
  $patchedBytes = $utf8NoBom.GetBytes($patchedMainText)
  $patchedSha256 = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($patchedBytes))
  $newMainName = "index-luna-$($patchedSha256.Substring(0, 12).ToLowerInvariant()).js"
  $newMainRelative = "web/classic/dist/assets/$newMainName"
  $newMainPath = Join-Path $distRoot "assets/$newMainName"

  [IO.File]::WriteAllBytes($newMainPath, $patchedBytes)
  Remove-Item -LiteralPath $oldMainPath

  $oldScriptReference = "src=`"/assets/$oldMainName`""
  $newScriptReference = "src=`"/assets/$newMainName`""
  if (([regex]::Matches($indexText, [regex]::Escape($oldScriptReference))).Count -ne 1) {
    throw 'Expected exactly one main bundle reference in index.html.'
  }
  $patchedIndexText = $indexText.Replace($oldScriptReference, $newScriptReference)
  [IO.File]::WriteAllText($indexPath, $patchedIndexText, $utf8NoBom)

  if (([regex]::Matches($patchedMainText, [regex]::Escape($newSnippet))).Count -ne 1) {
    throw 'Patched bundle does not contain exactly one patched pricing function.'
  }
  if ($patchedMainText.Contains($oldSnippet)) {
    throw 'Old pricing function boundary is still present after patching.'
  }

  $nodeCommand = Get-Command node -ErrorAction SilentlyContinue
  if ($nodeCommand) {
    $syntaxCheckPath = Join-Path $workRoot '__luna-pricing-syntax-check.mjs'
    Copy-Item -LiteralPath $newMainPath -Destination $syntaxCheckPath
    try {
      & $nodeCommand.Source --check $syntaxCheckPath
      if ($LASTEXITCODE -ne 0) {
        throw "Patched bundle failed node --check (exit code $LASTEXITCODE)."
      }
    } finally {
      Remove-Item -LiteralPath $syntaxCheckPath -ErrorAction SilentlyContinue
    }
  }

  $afterFiles = @(Get-ChildItem -LiteralPath $workRoot -Recurse -File)
  if ($afterFiles.Count -ne $beforeFiles.Count) {
    throw "File count changed from $($beforeFiles.Count) to $($afterFiles.Count)."
  }

  $allowedChanges = @(
    'web/classic/dist/index.html',
    $oldMainRelative,
    $newMainRelative
  )
  foreach ($file in $afterFiles) {
    $relative = [IO.Path]::GetRelativePath($workRoot, $file.FullName).Replace('\', '/')
    if ($allowedChanges -contains $relative) {
      continue
    }
    $afterHash = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
    if (-not $beforeHashes.ContainsKey($relative) -or $beforeHashes[$relative] -ne $afterHash) {
      throw "Unexpected file change detected: $relative"
    }
  }

  $outputParent = Split-Path -Parent $outputPath
  if ($outputParent) {
    New-Item -ItemType Directory -Force -Path $outputParent | Out-Null
  }
  if (Test-Path -LiteralPath $outputPath) {
    Remove-Item -LiteralPath $outputPath
  }

  & tar -czf $outputPath -C $workRoot web
  if ($LASTEXITCODE -ne 0) {
    throw "Failed to create output archive (tar exit code $LASTEXITCODE)."
  }

  & tar -xzf $outputPath -C $verifyRoot
  if ($LASTEXITCODE -ne 0) {
    throw "Failed to re-extract output archive (tar exit code $LASTEXITCODE)."
  }

  $verifiedFiles = @(Get-ChildItem -LiteralPath $verifyRoot -Recurse -File)
  if ($verifiedFiles.Count -ne 357) {
    throw "Output archive verification found $($verifiedFiles.Count) files instead of 357."
  }
  $verifiedMainPath = Join-Path $verifyRoot $newMainRelative
  $verifiedIndexPath = Join-Path $verifyRoot 'web/classic/dist/index.html'
  if (-not (Test-Path -LiteralPath $verifiedMainPath -PathType Leaf)) {
    throw "Output archive is missing $newMainRelative."
  }
  if (-not ([IO.File]::ReadAllText($verifiedIndexPath).Contains($newScriptReference))) {
    throw 'Output archive index.html does not reference the patched main bundle.'
  }
  if (([regex]::Matches([IO.File]::ReadAllText($verifiedMainPath), [regex]::Escape($newSnippet))).Count -ne 1) {
    throw 'Output archive bundle verification failed.'
  }

  $outputSha256 = (Get-FileHash -LiteralPath $outputPath -Algorithm SHA256).Hash
  [pscustomobject]@{
    InputArchive = $inputPath
    InputSha256 = $actualArchiveSha256
    OutputArchive = $outputPath
    OutputSha256 = $outputSha256
    FileCount = $verifiedFiles.Count
    OldMainBundle = $oldMainRelative
    NewMainBundle = $newMainRelative
    NewMainBundleSha256 = $patchedSha256
    NodeSyntaxChecked = [bool]$nodeCommand
  } | Format-List
} finally {
  if (Test-Path -LiteralPath $workRoot) {
    Remove-Item -LiteralPath $workRoot -Recurse -Force
  }
  if (Test-Path -LiteralPath $verifyRoot) {
    Remove-Item -LiteralPath $verifyRoot -Recurse -Force
  }
}
