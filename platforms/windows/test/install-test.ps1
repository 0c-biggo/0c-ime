# 明確的 Windows 測試機驗收：暫時變更 TSF 註冊，finally 還原原安裝。
# 不作為一般單元測試執行；請先完成組字。
param(
    [Parameter(Mandatory=$true)][string]$BundleDir,
    [Parameter(Mandatory=$true)][string]$UpgradeArchive,
    [Parameter(Mandatory=$true)][string]$ResultDir
)
$ErrorActionPreference = "Stop"
$bundle = (Resolve-Path -LiteralPath $BundleDir).Path
$archive = (Resolve-Path -LiteralPath $UpgradeArchive).Path
$updater = Join-Path $bundle "0c-update.exe"
$base = Join-Path $env:LOCALAPPDATA "Zuin"
$receiptFile = Join-Path $base "install-receipt.json"
$reg = 'Registry::HKEY_CLASSES_ROOT\CLSID\{138C35D0-7F08-4CEF-BA90-C34C16301528}\InprocServer32'
function RegisteredDLL {
    if (Test-Path $reg) { return (Get-Item $reg).GetValue('') }
    return ""
}
function FileHashOrAbsent([string]$Path) {
    if (Test-Path -LiteralPath $Path) { return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash }
    return "absent"
}
function Assert([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}
function InvokeUpdate([string[]]$Arguments) {
    & $updater @Arguments
    if ($LASTEXITCODE -ne 0) { throw "updater failed: $Arguments ($LASTEXITCODE)" }
}
function Receipt { return Get-Content -LiteralPath $receiptFile -Raw | ConvertFrom-Json }
function AssertRegisteredReceipt {
    $r = Receipt
    # 0.1.0-dev.15 以前的版本目錄使用 0c_tsf.dll。
    $dll = if ($r.files.PSObject.Properties.Name -contains '0c_tsf.dll') { '0c_tsf.dll' } else { 'zuin_tsf.dll' }
    $expected = Join-Path (Join-Path $base $r.directory) $dll
    # 0.1.0-dev.62 起註冊固定路徑 Zuin\tsf\0c_tsf.dll（#121），內容必須與收據版本目錄的 DLL 相同。
    $fixed = Join-Path $base 'tsf\0c_tsf.dll'
    if ((RegisteredDLL) -eq $fixed) {
        Assert ((Get-FileHash $fixed).Hash -eq (Get-FileHash $expected).Hash) "fixed DLL does not match receipt"
        return
    }
    Assert ((RegisteredDLL) -eq $expected) "registration does not match receipt"
}
function Regsvr([string]$DLL, [bool]$Remove) {
    $args = @('/s'); if ($Remove) { $args += '/u' }; $args += ('"' + $DLL + '"')
    $proc = Start-Process -FilePath "$env:SystemRoot\System32\regsvr32.exe" -ArgumentList $args -PassThru
    $proc.WaitForExit()
    Assert ($proc.ExitCode -eq 0) "restore regsvr32 failed: $($proc.ExitCode)"
}
if (Test-Path -LiteralPath $ResultDir) { throw "ResultDir must be a new directory" }
New-Item -ItemType Directory $ResultDir | Out-Null
$original = RegisteredDLL
$hadReceipt = Test-Path -LiteralPath $receiptFile
if ($hadReceipt) { Copy-Item -LiteralPath $receiptFile -Destination (Join-Path $ResultDir "original-receipt.json") }
$userFile = Join-Path $base "user.json"
$userBefore = FileHashOrAbsent $userFile
$dataFile = Join-Path $base "data\manifest.json"
$dataBefore = FileHashOrAbsent $dataFile
$first = Get-Content -LiteralPath (Join-Path $bundle "release.json") -Raw -Encoding UTF8 | ConvertFrom-Json
$report = [ordered]@{ stages = @(); original_registration = $original; restored = $false; user_preserved = $false; legacy_data_preserved = $false }
try {
    InvokeUpdate @('verify','-bundle',$bundle)
    InvokeUpdate @('install','-bundle',$bundle,'-channel',$first.channel)
    AssertRegisteredReceipt
    Assert ((Receipt).version -eq $first.version) "first version differs"
    $report.stages += 'install_or_legacy_migration'
    $hash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    InvokeUpdate @('apply','-archive',$archive,'-sha256',$hash,'-channel',$first.channel)
    AssertRegisteredReceipt
    Assert ((Receipt).version -ne $first.version) "upgrade must use a different version"
    $report.stages += 'archive_upgrade'
    InvokeUpdate @('rollback')
    AssertRegisteredReceipt
    Assert ((Receipt).version -eq $first.version) "rollback did not restore first version"
    $report.stages += 'rollback'
    InvokeUpdate @('uninstall')
    Assert ((RegisteredDLL) -eq '') "uninstall left COM registration"
    Assert (-not (Receipt).active) "uninstall receipt is still active"
    $report.stages += 'uninstall'
    InvokeUpdate @('rollback')
    AssertRegisteredReceipt
    Assert ((Receipt).active) "uninstall rollback did not activate installation"
    $report.stages += 'restore_after_uninstall'
} finally {
    $current = RegisteredDLL
    if ($current -and $current -ne $original) { Regsvr $current $true }
    if ($original -and (RegisteredDLL) -ne $original) { Regsvr $original $false }
    Assert ((RegisteredDLL) -eq $original) "original COM registration was not restored"
    if ($hadReceipt) {
        Copy-Item -LiteralPath (Join-Path $ResultDir "original-receipt.json") -Destination $receiptFile -Force
    } elseif (Test-Path -LiteralPath $receiptFile) {
        Remove-Item -LiteralPath $receiptFile
    }
    $report.restored = $true
    $report.user_preserved = (FileHashOrAbsent $userFile) -eq $userBefore
    $report.legacy_data_preserved = (FileHashOrAbsent $dataFile) -eq $dataBefore
    $report | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $ResultDir "result.json") -Encoding UTF8
    Assert $report.user_preserved "personal dictionary changed"
    Assert $report.legacy_data_preserved "legacy dictionary manifest changed"
}
Write-Host "PASS: migration, archive upgrade, rollback, uninstall; original registration and personal data preserved."
