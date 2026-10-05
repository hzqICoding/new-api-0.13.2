# Parse trusted fixed templates and execute only the pure URL predicate. No setup,
# user's Codex directory, network request, or real CLI is ever executed here.
$ErrorActionPreference = 'Stop'
$source = Join-Path $PSScriptRoot '../src/lib/codex-setup/templates/setup.ps1'
$tokens=$null; $parseErrors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile((Resolve-Path $source),[ref]$tokens,[ref]$parseErrors)
if($parseErrors.Count){throw 'PowerShell template syntax failed.'}
$function=$ast.Find({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Test-Endpoint'},$true)
if(-not $function){throw 'URL predicate was not found.'}
. ([scriptblock]::Create($function.Extent.Text))
$valid=@('https://api.example.test/v1','https://api.example.test:8443/proxy/v1','https://[::1]:8443/v1')
$invalid=@('http://api.example.test/v1','https://u:p@api.example.test/v1','https://api.example.test/v1?q=1','https://api.example.test/v1#x','https://api.example.test/../v1','https://api.example.test/$(whoami)/v1','https://api.example.test/$name/v1','https://api.example.test/"x/v1','https://api.example.test:99999/v1','https://api.example.test:0/v1',("https://api.example.test/v1"+[char]10))
foreach($url in $valid){if(-not(Test-Endpoint $url)){throw 'Valid HTTPS endpoint was rejected.'}}
foreach($url in $invalid){if(Test-Endpoint $url){throw 'Unsafe endpoint was accepted.'}}
Write-Output ('PASS PowerShell '+$PSVersionTable.PSVersion+': template syntax and 14 URL cases (predicate only).')
