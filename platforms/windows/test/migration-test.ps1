# 明確的 Windows 測試機驗收：zuin-* 舊版（0.1.0-dev.15 以前）升級到 0c-* 新版，以及回復到舊版。
# 暫時變更 TSF 註冊，finally 還原原安裝；不讀寫日常個人詞庫。請先完成組字，以系統管理員 PowerShell 執行。
# 被測行為會刪除不再是目前版本或回復目標的 zuin-* 程式；原註冊所在的程式先備份，結束時補回。
# 其他舊測試留下的 zuin-* 版本目錄會依此行為被清除。
param(
    [Parameter(Mandatory=$true)][string]$LegacyBundleDir,
    [Parameter(Mandatory=$true)][string]$BundleDir,
    [Parameter(Mandatory=$true)][string]$ResultDir
)
$ErrorActionPreference = "Stop"
$legacyBundle = (Resolve-Path -LiteralPath $LegacyBundleDir).Path
$bundle = (Resolve-Path -LiteralPath $BundleDir).Path
$legacyUpdater = Join-Path $legacyBundle "0c-update.exe"
$updater = Join-Path $bundle "0c-update.exe"
$base = Join-Path $env:LOCALAPPDATA "Zuin"
$receiptFile = Join-Path $base "install-receipt.json"
$clsid = '{138C35D0-7F08-4CEF-BA90-C34C16301528}'
$reg = "Registry::HKEY_CLASSES_ROOT\CLSID\$clsid\InprocServer32"
$tip = "Registry::HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\CTF\TIP\$clsid"
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
function Invoke([string]$Tool, [string[]]$Arguments) {
    & $Tool @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Tool failed: $Arguments ($LASTEXITCODE)" }
}
function Receipt { return Get-Content -LiteralPath $receiptFile -Raw | ConvertFrom-Json }
function Release([string]$Dir) { return Get-Content -LiteralPath (Join-Path $Dir "release.json") -Raw -Encoding UTF8 | ConvertFrom-Json }
function InstalledDir { return Join-Path $base (Receipt).directory }
# 註冊必須指向收據目錄中該世代的 DLL；CLSID 與 TIP profile 不因改名而變。
function AssertActive([string]$Version, [string]$DLL) {
    $r = Receipt
    Assert ($r.active -and $r.version -eq $Version) "receipt is not $Version"
    $fixed = Join-Path $base 'tsf\0c_tsf.dll'  # 0.1.0-dev.62 起註冊固定路徑（#121），內容與版本目錄相同
    if ((RegisteredDLL) -eq $fixed) {
        Assert ((Get-FileHash $fixed).Hash -eq (Get-FileHash (Join-Path (InstalledDir) $DLL)).Hash) "fixed DLL is not $DLL of $Version"
    } else {
        Assert ((RegisteredDLL) -eq (Join-Path (InstalledDir) $DLL)) "registration is not $DLL of $Version"
    }
    Assert (Test-Path $tip) "TSF profile registration missing; users would have to re-add the input method"
}
function Regsvr([string]$DLL, [bool]$Remove) {
    $args = @('/s'); if ($Remove) { $args += '/u' }; $args += ('"' + $DLL + '"')
    $proc = Start-Process -FilePath "$env:SystemRoot\System32\regsvr32.exe" -ArgumentList $args -PassThru
    $proc.WaitForExit()
    Assert ($proc.ExitCode -eq 0) "restore regsvr32 failed: $($proc.ExitCode)"
}
function PipeReady([string]$Pipe) {
    $client = [IO.Pipes.NamedPipeClientStream]::new('.', $Pipe.Substring(9), [IO.Pipes.PipeDirection]::InOut)
    try { $client.Connect(1000); return $true } catch [TimeoutException] { return $false } finally { $client.Dispose() }
}

if (Test-Path -LiteralPath $ResultDir) { throw "ResultDir must be a new directory" }
New-Item -ItemType Directory $ResultDir | Out-Null
$old = Release $legacyBundle
$new = Release $bundle
Assert ($old.files.PSObject.Properties.Name -contains 'zuin_tsf.dll') "LegacyBundleDir must be a zuin-* release"
Assert ($new.files.PSObject.Properties.Name -contains '0c_tsf.dll') "BundleDir must be a 0c-* release"
if (Get-Process 0c-server,0c-ui,zuin-server,zuin-ui -ErrorAction SilentlyContinue) {
    throw 'Finish daily input and close the existing engine/candidate UI before migration acceptance'
}
$original = RegisteredDLL
$originalDir = if ($original) { Split-Path $original } else { '' }
$savedPrograms = Join-Path $ResultDir "original-programs"
if ($originalDir) {
    New-Item -ItemType Directory $savedPrograms | Out-Null
    if ($originalDir -eq $base) {
        foreach ($name in 'zuin_tsf.dll','zuin-server.exe','zuin-ui.exe','0c_tsf.dll','0c-server.exe','0c-ui.exe') {
            $file = Join-Path $base $name
            if (Test-Path -LiteralPath $file) { Copy-Item -LiteralPath $file -Destination $savedPrograms }
        }
    } else {
        Copy-Item -LiteralPath $originalDir -Destination (Join-Path $savedPrograms 'directory') -Recurse
    }
}
$hadReceipt = Test-Path -LiteralPath $receiptFile
if ($hadReceipt) { Copy-Item -LiteralPath $receiptFile -Destination (Join-Path $ResultDir "original-receipt.json") }
$userFile = Join-Path $base "user.json"
$userBefore = FileHashOrAbsent $userFile
$report = [ordered]@{ legacy = $old.version; new = $new.version; stages = @(); original_registration = $original; restored = $false; user_preserved = $false }
$server = $null
$temp = Join-Path $ResultDir "engine"
New-Item -ItemType Directory $temp | Out-Null
try {
    # 1. 以舊版自己的工具安裝舊版，模擬使用者現有的 zuin-* 安裝。
    Invoke $legacyUpdater @('install','-bundle',$legacyBundle,'-channel',$old.channel,'-allow-downgrade')
    AssertActive $old.version 'zuin_tsf.dll'
    $legacyDir = InstalledDir
    $report.stages += 'legacy_installed'

    # 2. 舊版 0c-update 不認得新檔名，必須在變更任何註冊前拒絕。
    # Windows PowerShell 5.1 在 Stop 模式會把原生程式的 stderr 當成例外；這裡預期它失敗。
    $ErrorActionPreference = "Continue"
    & $legacyUpdater install -bundle $bundle -channel $new.channel 2>&1 | Out-File (Join-Path $ResultDir "legacy-updater.log") -Encoding UTF8
    $rejected = $LASTEXITCODE -ne 0
    $ErrorActionPreference = "Stop"
    Assert $rejected "legacy updater accepted a 0c-* bundle"
    AssertActive $old.version 'zuin_tsf.dll'
    $report.stages += 'legacy_updater_rejects_safely'

    # 3. 新版 0c-update 升級：停用舊 DLL 註冊，舊版目錄保留為回復目標。
    Invoke $updater @('install','-bundle',$bundle,'-channel',$new.channel)
    AssertActive $new.version '0c_tsf.dll'
    foreach ($name in 'zuin_tsf.dll','zuin-server.exe','zuin-ui.exe') {
        Assert ((FileHashOrAbsent (Join-Path $legacyDir $name)) -eq $old.files.$name.ToUpperInvariant()) "rollback target changed: $name"
    }
    $report.stages += 'upgraded_from_legacy'

    # 4. 過渡期 IPC：新 server 同時服務新舊管道；只有舊管道被佔用時不另起寫入者。
    $pipe = "\\.\pipe\0c-migration-" + [guid]::NewGuid().ToString("N")
    $legacyPipe = "\\.\pipe\zuin-migration-" + [guid]::NewGuid().ToString("N")
    $engine = Join-Path (InstalledDir) '0c-server.exe'
    $arguments = @('-data', ('"' + (Join-Path (InstalledDir) 'data') + '"'), '-user', ('"' + (Join-Path $temp 'user.json') + '"'),
                   '-socket', $pipe, '-legacy-socket', $legacyPipe)
    $server = Start-Process -FilePath $engine -ArgumentList $arguments -PassThru -NoNewWindow `
        -RedirectStandardOutput (Join-Path $temp 'server.out') -RedirectStandardError (Join-Path $temp 'server.err')
    $ready = $false
    for ($i = 0; $i -lt 30 -and -not $ready; $i++) { $ready = (PipeReady $pipe) -and (PipeReady $legacyPipe) }
    Assert $ready "0c-server does not serve both pipe names"
    $second = Start-Process -FilePath $engine -ArgumentList $arguments -PassThru -NoNewWindow -Wait `
        -RedirectStandardOutput (Join-Path $temp 'second.out') -RedirectStandardError (Join-Path $temp 'second.err')
    Assert ($second.ExitCode -eq 0 -and -not $server.HasExited) "second engine did not defer to the running one"
    Stop-Process -Id $server.Id
    $server.WaitForExit()
    $legacyEngine = Start-Process -FilePath (Join-Path $legacyDir 'zuin-server.exe') -PassThru -NoNewWindow `
        -ArgumentList @('-data', ('"' + (Join-Path $legacyDir 'data') + '"'), '-user', ('"' + (Join-Path $temp 'legacy-user.json') + '"'), '-socket', $legacyPipe) `
        -RedirectStandardOutput (Join-Path $temp 'legacy.out') -RedirectStandardError (Join-Path $temp 'legacy.err')
    try {
        $ready = $false
        for ($i = 0; $i -lt 30 -and -not $ready; $i++) { $ready = PipeReady $legacyPipe }
        Assert $ready "legacy engine did not start"
        $third = Start-Process -FilePath $engine -ArgumentList $arguments -PassThru -NoNewWindow -Wait `
            -RedirectStandardOutput (Join-Path $temp 'third.out') -RedirectStandardError (Join-Path $temp 'third.err')
        Assert ($third.ExitCode -eq 0 -and -not (PipeReady $pipe)) "0c-server started beside a running zuin-server"
    } finally { Stop-Process -Id $legacyEngine.Id -ErrorAction SilentlyContinue }
    $report.stages += 'dual_pipe_single_writer'

    # 5. 回復到舊版，再回到新版。沒有日常個人詞庫時放一份合成詞庫，驗證 -restore-user；結束時刪除。
    $synthetic = $userBefore -eq 'absent'
    if ($synthetic) { Set-Content -LiteralPath $userFile -Value '{"Format":1,"Uni":{"migration-before":1}}' -Encoding ASCII -NoNewline }
    $userAtRollback = FileHashOrAbsent $userFile
    Invoke $updater @('rollback')
    AssertActive $old.version 'zuin_tsf.dll'
    Assert ((InstalledDir) -eq $legacyDir) "rollback used another directory"
    $report.stages += 'rolled_back_to_legacy'
    # 回到新版時一併走「還原個人詞庫」。日常詞庫在測試期間沒有引擎寫入，快照與目前內容相同，還原後雜湊不變。
    $snapshot = Join-Path (Join-Path $base (Receipt).previous) 'user.json'
    Assert ((FileHashOrAbsent $snapshot) -eq $userAtRollback) "personal dictionary snapshot missing from rollback backup"
    if ($synthetic) {
        Set-Content -LiteralPath $userFile -Value '{"Format":1,"Uni":{"written-after-rollback":1}}' -Encoding ASCII -NoNewline
        $userAfterRollback = FileHashOrAbsent $userFile
        # 引擎仍在執行時必須拒絕：它結束時會寫回記憶體中的詞庫。
        $guard = Start-Process -FilePath $engine -PassThru -NoNewWindow `
            -ArgumentList @('-data', ('"' + (Join-Path (Split-Path $engine) 'data') + '"'), '-socket', ("\\.\pipe\0c-guard-" + [guid]::NewGuid().ToString("N")), '-user', ('"' + (Join-Path $temp 'guard-user.json') + '"'), '-idle-exit', '60s') `
            -RedirectStandardOutput (Join-Path $temp 'guard.out') -RedirectStandardError (Join-Path $temp 'guard.err')
        try {
            $ErrorActionPreference = "Continue"
            & $updater rollback -restore-user 2>&1 | Out-File (Join-Path $ResultDir "restore-guard.log") -Encoding UTF8
            $refused = $LASTEXITCODE -ne 0
            $ErrorActionPreference = "Stop"
            Assert $refused "restore-user ran while an engine was running"
            AssertActive $old.version 'zuin_tsf.dll'
            Assert ((FileHashOrAbsent $userFile) -eq $userAfterRollback) "refused restore changed the dictionary"
        } finally { Stop-Process -Id $guard.Id -ErrorAction SilentlyContinue; $guard.WaitForExit() }
        Invoke $updater @('rollback','-restore-user')
        Assert ((FileHashOrAbsent $userFile) -eq $userAtRollback) "restore-user did not bring back the dictionary saved before rollback"
        $kept = Join-Path (Join-Path $base (Receipt).previous) 'user.json'
        Assert ((FileHashOrAbsent $kept) -eq $userAfterRollback) "replaced dictionary was not kept for undo"
        $report.stages += 'restore_user_guard_and_undo_copy'
    } else {
        Invoke $updater @('rollback','-restore-user')
        Assert ((FileHashOrAbsent $userFile) -eq $userBefore) "restore-user changed an unchanged dictionary"
    }
    $report.stages += 'returned_to_new_with_user_restore'
    AssertActive $new.version '0c_tsf.dll'
} finally {
    if ($null -ne $server -and -not $server.HasExited) { Stop-Process -Id $server.Id -ErrorAction SilentlyContinue }
    $current = RegisteredDLL
    if ($current -and $current -ne $original) { Regsvr $current $true }
    # 補回被清理的原安裝程式，再恢復原註冊。
    if ($originalDir -eq $base) {
        foreach ($saved in Get-ChildItem -LiteralPath $savedPrograms -File) {
            $file = Join-Path $base $saved.Name
            if ((FileHashOrAbsent $file) -ne (FileHashOrAbsent $saved.FullName)) { Copy-Item -LiteralPath $saved.FullName -Destination $file -Force }
        }
    } elseif ($originalDir -and -not (Test-Path -LiteralPath $original)) {
        if (Test-Path -LiteralPath $originalDir) { Remove-Item -LiteralPath $originalDir -Recurse -Force }
        Copy-Item -LiteralPath (Join-Path $savedPrograms 'directory') -Destination $originalDir -Recurse
    }
    if ($original -and (RegisteredDLL) -ne $original) { Regsvr $original $false }
    Assert ((RegisteredDLL) -eq $original) "original COM registration was not restored"
    if ($hadReceipt) {
        Copy-Item -LiteralPath (Join-Path $ResultDir "original-receipt.json") -Destination $receiptFile -Force
    } elseif (Test-Path -LiteralPath $receiptFile) {
        Remove-Item -LiteralPath $receiptFile
    }
    if ($userBefore -eq 'absent' -and (Test-Path -LiteralPath $userFile)) { Remove-Item -LiteralPath $userFile }
    $report.restored = $true
    $report.user_preserved = (FileHashOrAbsent $userFile) -eq $userBefore
    $report | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $ResultDir "result.json") -Encoding UTF8
    Assert $report.user_preserved "personal dictionary changed"
}
Write-Host "PASS: zuin-* → 0c-* upgrade, dual-pipe transition, rollback to zuin-* and back; original registration and personal data preserved."
