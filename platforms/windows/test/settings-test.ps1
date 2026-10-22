# 設定程式（#47）驗收：以 EXE 安裝程式的靜默模式安裝新版，驗證收據、開始功能表的「靈犀注音設定」、
# 0c-update open-settings、捷徑、TSF 輸入指示器選單的「設定…」、檔案被改時的拒絕，再解除安裝並還原。
# 測試機必須已登入互動工作階段，並以同一帳號的系統管理員 PowerShell 執行；已有 EXE 安裝、收據或引擎指標時拒絕執行。
# 給 -BaselineSetup（沒有設定程式的舊版安裝程式）時，先驗證升級與回復：舊版升級後出現開始功能表項目，
# 回復到舊版後項目移除（舊版沒有設定程式），再重新安裝新版繼續下面的驗證。
# 結果與視窗截圖（只擷取設定程式自己的視窗）放在 ResultDir。
param([Parameter(Mandatory=$true)][string]$Setup, [string]$BaselineSetup, [string]$ResultDir)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$Setup = (Resolve-Path -LiteralPath $Setup).Path
if ($BaselineSetup) { $BaselineSetup = (Resolve-Path -LiteralPath $BaselineSetup).Path }
if (-not $ResultDir) { $ResultDir = Join-Path ([IO.Path]::GetTempPath()) ('0c-settings-test-' + (Get-Date -Format 'yyyyMMdd-HHmmss')) }
if (Test-Path $ResultDir) { throw 'ResultDir must be a new directory' }
New-Item -ItemType Directory $ResultDir | Out-Null
$base = "$env:LOCALAPPDATA\Zuin"
$reg = 'Registry::HKEY_CLASSES_ROOT\CLSID\{138C35D0-7F08-4CEF-BA90-C34C16301528}\InprocServer32'
$arp = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\0c-Zhuyin'
$folder = "$env:APPDATA\Microsoft\Windows\Start Menu\Programs\靈犀注音"
$lnk = "$folder\靈犀注音設定（0c）.lnk"
$report = [ordered]@{ passed = $false; checks = @(); error = ''; restored = $false; version = ''; menu = @() }
function Check([bool]$Ok, [string]$Name, [string]$Detail = '') { if (-not $Ok) { throw "$Name failed $Detail" }; $report.checks += $Name; Write-Host "ok: $Name" }
function Run([string]$exe, [string]$arguments) { $p = Start-Process $exe -ArgumentList $arguments -PassThru; $null = $p.Handle; $p.WaitForExit(); if ($p.ExitCode -ne 0) { throw "$exe exited $($p.ExitCode)" } }
# 0c-update 輸出 UTF-8；以 .NET 行程接輸出並指定編碼，不依賴主控台的預設編碼（否則中文變亂碼、比對落空）。
function RunCapture([string]$exe, [string]$arguments) {
    $psi = New-Object Diagnostics.ProcessStartInfo $exe, $arguments
    $psi.UseShellExecute = $false; $psi.CreateNoWindow = $true
    $psi.RedirectStandardOutput = $true; $psi.RedirectStandardError = $true
    $psi.StandardOutputEncoding = [Text.Encoding]::UTF8; $psi.StandardErrorEncoding = [Text.Encoding]::UTF8
    $p = [Diagnostics.Process]::Start($psi)
    $text = $p.StandardOutput.ReadToEnd() + $p.StandardError.ReadToEnd()
    $p.WaitForExit()
    return @{ code = $p.ExitCode; text = $text }
}
function HashOrAbsent([string]$p) { if (Test-Path $p) { return (Get-FileHash $p).Hash }; return 'absent' }
function Entries { @(Get-ChildItem $base -Force -Recurse -Depth 2 -ErrorAction SilentlyContinue | ForEach-Object { $_.FullName }) }

# 在互動工作階段執行一段 PowerShell（開視窗、截圖都要在使用者看得到的桌面）。
function Interactive([string]$Script, [int]$Seconds = 40) {
    $done = Join-Path $ResultDir ('done-' + [guid]::NewGuid().ToString('N'))
    $enc = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($Script + "; Set-Content -LiteralPath '$done' ok"))
    $name = '0c-SettingsTest-' + [guid]::NewGuid().ToString('N')
    Register-ScheduledTask -TaskName $name -Action (New-ScheduledTaskAction -Execute powershell.exe -Argument "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -EncodedCommand $enc") -Principal (New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Limited) -Force | Out-Null
    Start-ScheduledTask $name
    $deadline = (Get-Date).AddSeconds($Seconds); while (-not (Test-Path $done) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 300 }
    Unregister-ScheduledTask $name -Confirm:$false
}
function SettingsProcesses { @(Get-Process -Name '0c-settings' -ErrorAction SilentlyContinue) }
# 強制結束設定程式時，它開的 0c-update vocab watch 在 Windows 上不會跟著結束（父行程編號不變，只靠寫入失敗離開），
# 會鎖住版本目錄的 0c-update.exe，讓解除安裝與清理刪不掉；一併結束。
function StopSettings {
    SettingsProcesses | Stop-Process -Force -ErrorAction SilentlyContinue
    Get-CimInstance Win32_Process -Filter "Name='0c-update.exe'" -ErrorAction SilentlyContinue | Where-Object { $_.CommandLine -match 'vocab watch' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
    Start-Sleep -Milliseconds 800
}
function WaitSettings([int]$Seconds = 20) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) { if (@(SettingsProcesses).Count -gt 0) { return $true }; Start-Sleep -Milliseconds 400 }
    return $false
}
function Shot([string]$Name) {
    $png = Join-Path $ResultDir $Name
    Interactive ("& '$PSScriptRoot\window-shot.ps1' -Out '$png' -WaitSeconds 20") 60
    $text = Get-Content "$png.txt" -Raw -Encoding UTF8 -ErrorAction SilentlyContinue
    $report.screenshots += @{ name = $Name; info = $text }
    return $text
}
# 輸入指示器選單探針（menu-probe.ps1）：互動工作階段中，經 TSF 呼叫本輸入法的按鈕與選單。
function MenuProbe([string]$Select = '') {
    $out = Join-Path $ResultDir ('menu-' + [guid]::NewGuid().ToString('N') + '.json')
    $a = '-NoProfile -STA -ExecutionPolicy Bypass -File "' + $PSScriptRoot + '\menu-probe.ps1" -Out "' + $out + '"'
    if ($Select) { $a += ' -Select "' + $Select + '"' }
    $name = '0c-SettingsTest-Menu-' + [guid]::NewGuid().ToString('N')
    Register-ScheduledTask -TaskName $name -Action (New-ScheduledTaskAction -Execute powershell.exe -Argument $a) -Principal (New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Limited) -Force | Out-Null
    Start-ScheduledTask $name
    $deadline = (Get-Date).AddSeconds(60)
    while (-not (Test-Path $out) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 500 }
    Unregister-ScheduledTask $name -Confirm:$false
    Start-Sleep -Milliseconds 300
    $r = Get-Content $out -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($r.error) { throw "menu probe: $($r.error)" }
    return $r
}
$report.screenshots = @()

# 前置：不碰既有安裝。
if (Test-Path $arp) { throw 'existing EXE install must be preserved; refusing test' }
if (Test-Path "$base\install-receipt.json") { throw 'existing receipt must be preserved; refusing test' }
if (Test-Path "$base\engine.json") { throw 'existing engine pointer must be preserved; refusing test' }
if (Test-Path $lnk) { throw 'existing settings shortcut must be preserved; refusing test' }
if (Get-Process 0c-server, 0c-ui, zuin-server, zuin-ui, 0c-settings -ErrorAction SilentlyContinue) { throw 'Close the engine, candidate UI and settings program before this test' }
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$mutex = [Threading.Mutex]::new($false, ('Global\0c-desktop-acceptance-' + $identity.User.Value))
$locked = $false; $snapshot = $false
$original = if (Test-Path $reg) { (Get-Item $reg).GetValue('') } else { '' }
if ($original) { throw "an input method is already registered ($original); refusing test" }
try {
    try { $locked = $mutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $locked = $true }
    if (-not $locked) { throw 'Another desktop acceptance run is active' }
    $entriesBefore = Entries
    $userBefore = HashOrAbsent "$base\user.json"
    $folderExisted = Test-Path $folder
    # 更新狀態檔在測試前就存在（以前的測試留下的）：安裝會動到它，結束時逐位元組還原。
    $stateFile = "$base\update\state.json"
    $stateBefore = if (Test-Path $stateFile) { [IO.File]::ReadAllBytes($stateFile) } else { $null }
    if ($null -ne $stateBefore) { [IO.File]::WriteAllBytes((Join-Path $ResultDir 'state.json.before'), $stateBefore) } # 萬一還原中斷，原內容還在
    # 登入啟動引擎的工作（0c-engine-<SID>）由安裝建立、解除安裝刪除；結束時只清本次新增的，原有的不動。
    $engineTasksBefore = @(Get-ScheduledTask -ErrorAction SilentlyContinue | Where-Object { $_.TaskName -like '0c-engine-*' } | ForEach-Object { $_.TaskName })
    $snapshot = $true

    if ($BaselineSetup) {
        Run $BaselineSetup '/S'
        $old = Get-Content "$base\install-receipt.json" -Raw | ConvertFrom-Json
        $report.baseline_version = $old.version
        Check (-not $old.files.'0c-settings.exe') 'the baseline release has no settings program'
        Check (-not (Test-Path $lnk)) 'the baseline release has no settings shortcut'
        Run $Setup '/S'
        Check (Test-Path $lnk) 'upgrading from the baseline creates the Start Menu shortcut'
        $new = Get-Content "$base\install-receipt.json" -Raw | ConvertFrom-Json
        Check ($new.version -ne $old.version -and $new.files.'0c-settings.exe') 'the upgrade installed the settings program'
        $run = RunCapture "$base\bin\0c-update.exe" 'rollback'
        Check ($run.code -eq 0) 'rollback to the baseline succeeds' $run.text
        $back = Get-Content "$base\install-receipt.json" -Raw | ConvertFrom-Json
        Check ($back.version -eq $old.version -and $back.active) 'the baseline is the active release again'
        Check (-not (Test-Path $lnk)) 'rolling back to a release without a settings program removes the shortcut'
        Check (-not (Test-Path "$base\settings-entry.json")) 'the marker file is removed with it'
    }
    Run $Setup '/S'
    $receipt = Get-Content "$base\install-receipt.json" -Raw | ConvertFrom-Json
    $report.version = $receipt.version
    $program = [IO.Path]::GetFullPath("$base\$($receipt.directory)\0c-settings.exe")
    $stable = "$base\bin\0c-update.exe"
    Check ($receipt.files.'0c-settings.exe' -match '^[0-9a-f]{64}$') 'receipt records the settings program hash'
    Check ((Get-FileHash $program).Hash.ToLower() -eq $receipt.files.'0c-settings.exe') 'installed settings program matches the receipt'
    $sig = Get-AuthenticodeSignature $program
    $report.signature = [string]$sig.Status
    Check (Test-Path $stable) 'stable updater installed'

    # 開始功能表項目：由安裝後的 0c-update 建立。
    Check (Test-Path $lnk) 'Start Menu shortcut created by the install'
    $link = (New-Object -ComObject WScript.Shell).CreateShortcut($lnk)
    $report.shortcut = [ordered]@{ target = $link.TargetPath; arguments = $link.Arguments; icon = $link.IconLocation; dir = $link.WorkingDirectory }
    Check ($link.TargetPath -ieq "$env:SystemRoot\System32\conhost.exe") 'shortcut runs conhost' $link.TargetPath
    Check ($link.Arguments -ieq ('--headless "' + $stable + '" open-settings')) 'shortcut runs the stable updater hidden' $link.Arguments
    Check (Test-Path ($link.IconLocation -replace ',\d+$', '')) 'shortcut icon file exists'
    Check (Test-Path "$base\settings-entry.json") 'marker file written'

    # 1) 命令列：0c-update open-settings（在互動工作階段執行，視窗才看得到）。
    Interactive "& '$stable' open-settings"
    Check (WaitSettings) 'open-settings starts the settings program'
    $shot = Shot 'windows-open-settings.png'
    Check ($shot -match 'found=true') 'settings window found' $shot
    $proc = SettingsProcesses | Select-Object -First 1
    Check ($proc.Path -ieq $program) 'the program started is the installed one' $proc.Path
    # 第二次執行：只有一個行程（單一實例）。
    Interactive "& '$stable' open-settings"
    Start-Sleep -Seconds 3
    Check (@(SettingsProcesses).Count -eq 1) 'a second launch does not start another process'
    # 1b) 在視窗裡改一個設定（UI Automation 按「選字」頁的開關）：設定檔更新，再改回來。
    $settingsFile = "$base\settings.json"
    $toggleOut = Join-Path $ResultDir 'toggle-1.txt'
    Interactive ("& '$PSScriptRoot\settings-ui-toggle.ps1' -Page '選字' -Switch '依前文選字' -Out '$toggleOut'") 120
    $toggleResult = Get-Content $toggleOut -Raw -Encoding UTF8 -ErrorAction SilentlyContinue
    Check ($toggleResult -match 'toggled=true') 'the switch in the settings window can be pressed' $toggleResult
    $changed = Get-Content $settingsFile -Raw -Encoding UTF8 | ConvertFrom-Json
    Check ($changed.document_context -eq $false -and $changed.schema -eq 1) 'pressing the switch wrote settings.json through the contract' ($changed | ConvertTo-Json -Compress)
    Shot 'windows-after-toggle.png' | Out-Null
    $toggleOut = Join-Path $ResultDir 'toggle-2.txt'
    Interactive ("& '$PSScriptRoot\settings-ui-toggle.ps1' -Page '選字' -Switch '依前文選字' -Out '$toggleOut'") 120
    $restored = Get-Content $settingsFile -Raw -Encoding UTF8 | ConvertFrom-Json
    Check ($restored.document_context -ne $false) 'pressing it again restores the setting' ($restored | ConvertTo-Json -Compress)
    StopSettings

    # 2) 開始功能表的捷徑（以 conhost --headless 執行，不閃出主控台視窗）。
    Interactive "Start-Process -FilePath '$lnk'"
    Check (WaitSettings) 'the shortcut starts the settings program'
    $shot = Shot 'windows-shortcut.png'
    Check ($shot -match 'found=true') 'settings window found via the shortcut' $shot
    Check (@(Get-Process -Name conhost -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowHandle -ne [IntPtr]::Zero -and $_.StartTime -gt (Get-Date).AddMinutes(-1) }).Count -eq 0) 'no console window was shown'
    StopSettings

    # 3) TSF 輸入指示器選單：第一項「設定…」，選了之後經 0c-ui 開啟設定程式。
    $m = MenuProbe
    $labels = @($m.menu | ForEach-Object { ($_ -split '\|', 3)[2] })
    $report.menu = $labels
    Check ($labels[0] -eq '設定…') 'the input method menu starts with Settings' ($labels -join ' / ')
    $m = MenuProbe -Select '設定…'
    Check ($m.selected -like '*設定…*') 'Settings was selected in the menu'
    Check (WaitSettings 25) 'selecting Settings in the input method menu starts the settings program'
    $shot = Shot 'windows-ime-menu.png'
    Check ($shot -match 'found=true') 'settings window found via the input method menu' $shot
    StopSettings

    # 3b) 「我的詞彙…」（#125）：0.2.0 起一律是第二項，選了開到「我的詞彙」頁，
    #     設定程式已開在別頁時再選一次也切過去（單一實例）。「我加的詞」分頁只在詞彙頁顯示時才在無障礙樹裡。
    $report.menu_vocab = $labels
    Check ($labels[0] -eq '設定…' -and $labels[1] -eq '我的詞彙…') 'the vocab entry follows Settings' ($labels -join ' / ')
    $m = MenuProbe -Select '我的詞彙…'
    Check ($m.selected -like '*我的詞彙…*') 'the vocab entry was selected'
    Check (WaitSettings 25) 'selecting the vocab entry starts the settings program'
    $pageOut = Join-Path $ResultDir 'page-1.txt'
    Interactive ("& '$PSScriptRoot\settings-page-probe.ps1' -Expect '我加的詞' -Out '$pageOut'") 90
    $page = Get-Content $pageOut -Raw -Encoding UTF8 -ErrorAction SilentlyContinue
    Check ($page -match 'found=true') 'the vocab entry opens the vocab page' $page
    Shot 'windows-ime-menu-vocab.png' | Out-Null
    $pageOut = Join-Path $ResultDir 'page-2.txt'
    Interactive ("& '$PSScriptRoot\settings-page-probe.ps1' -Nav '選字' -Expect '依前文選字' -Out '$pageOut'") 90
    $page = Get-Content $pageOut -Raw -Encoding UTF8 -ErrorAction SilentlyContinue
    Check ($page -match 'found=true') 'the settings window moved to another page' $page
    $m = MenuProbe -Select '我的詞彙…'
    Start-Sleep -Seconds 3
    Check (@(SettingsProcesses).Count -eq 1) 'selecting the vocab entry again keeps one settings process'
    $pageOut = Join-Path $ResultDir 'page-3.txt'
    Interactive ("& '$PSScriptRoot\settings-page-probe.ps1' -Expect '我加的詞' -Out '$pageOut'") 90
    $page = Get-Content $pageOut -Raw -Encoding UTF8 -ErrorAction SilentlyContinue
    Check ($page -match 'found=true') 'the running settings program switches to the vocab page' $page
    Shot 'windows-ime-menu-vocab-switch.png' | Out-Null
    StopSettings

    # 4) 檔案被改一個位元組：拒絕啟動並說明（命令列，不需要圖形）。
    $stream = [IO.File]::Open($program, 'Open', 'ReadWrite', 'None'); $length = $stream.Length
    $stream.Seek(0, 'End') | Out-Null; $stream.WriteByte(0x20); $stream.Dispose()
    $run = RunCapture $stable 'open-settings'
    $message = $run.text; $exit = $run.code
    $stream = [IO.File]::Open($program, 'Open', 'ReadWrite', 'None'); $stream.SetLength($length); $stream.Dispose()
    $report.tamper_message = $message.Trim()
    Check ($exit -ne 0 -and $message -match '雜湊與記錄不符') 'a modified settings program is refused with a reason' $message
    Check ((Get-FileHash $program).Hash.ToLower() -eq $receipt.files.'0c-settings.exe') 'the settings program was restored byte for byte'
    Check (@(SettingsProcesses).Count -eq 0) 'the refused program did not start'

    # 5) 解除安裝：捷徑與標記檔一併移除。
    Run ((Get-ItemProperty $arp).UninstallString.Trim('"')) '/S'
    $deadline = (Get-Date).AddSeconds(15); while ((Test-Path $arp) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 200 }
    Check (-not (Test-Path $arp)) 'uninstall removed the app entry'
    Check (-not (Test-Path $lnk)) 'uninstall removed the settings shortcut'
    Check (-not (Test-Path "$base\settings-entry.json")) 'uninstall removed the marker file'
    $report.passed = $true
} catch { $report.error = $_.Exception.Message } finally {
    try {
        if ($snapshot) {
            StopSettings
            Get-Process 0c-ui, 0c-server -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
            if (Test-Path $arp) {
                $report.cleanup_uninstall = 'ran'
                Run ((Get-ItemProperty $arp).UninstallString.Trim('"')) '/S'
                # NSIS 的解除安裝程式把自己複製到暫存目錄再執行，/S 的行程結束時實際工作還在進行；等登錄項目消失再清理。
                $deadline = (Get-Date).AddSeconds(30); while ((Test-Path $arp) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 300 }
                Start-Sleep -Seconds 2
            }
            if (Test-Path $reg) { $current = (Get-Item $reg).GetValue(''); if ($current) { Run "$env:WINDIR\System32\regsvr32.exe" ('/u /s "' + $current + '"') } }
            Get-ScheduledTask -ErrorAction SilentlyContinue | Where-Object { $_.TaskName -like '0c-update-*' -or $_.TaskName -like '0c-SettingsTest-*' } | ForEach-Object { Unregister-ScheduledTask $_.TaskName -Confirm:$false }
            $report.engine_tasks_removed = @(Get-ScheduledTask -ErrorAction SilentlyContinue | Where-Object { $_.TaskName -like '0c-engine-*' -and $engineTasksBefore -notcontains $_.TaskName } | ForEach-Object { Unregister-ScheduledTask $_.TaskName -Confirm:$false; $_.TaskName })
            if (Test-Path $lnk) { Remove-Item $lnk -Force }
            if (-not $folderExisted -and (Test-Path $folder)) { Remove-Item $folder -Recurse -Force }
            # 這一輪新增的檔案（版本目錄、備份、穩定副本…）一併移除；log 與測試前就存在的不動。
            $keep = @{}; foreach ($e in $entriesBefore) { $keep[$e] = $true }
            Entries | Where-Object { -not $keep[$_] -and $_ -notlike '*\tsf.log' } | Sort-Object Length -Descending | ForEach-Object { if (Test-Path $_) { Remove-Item $_ -Recurse -Force -ErrorAction SilentlyContinue } }
            if ($null -ne $stateBefore) { [IO.File]::WriteAllBytes($stateFile, $stateBefore) }
            $report.state_preserved = if ($null -ne $stateBefore) { ([BitConverter]::ToString([IO.File]::ReadAllBytes($stateFile)) -eq [BitConverter]::ToString($stateBefore)) } else { -not (Test-Path $stateFile) }
            $report.user_preserved = ((HashOrAbsent "$base\user.json") -eq $userBefore)
            $left = @(Entries | Where-Object { -not $keep[$_] -and $_ -notlike '*\tsf.log' })
            $report.leftover = $left
            $report.restored = ($left.Count -eq 0 -and -not (Test-Path $reg) -and -not (Test-Path $arp) -and -not (Test-Path $lnk) -and $report.user_preserved -and $report.state_preserved)
        }
    } catch { $report.error += ' restore: ' + $_.Exception.Message }
    if ($locked) { $mutex.ReleaseMutex() }
    $report | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $ResultDir 'settings-test.json') -Encoding UTF8
}
if (-not $report.passed -or -not $report.restored) { Write-Error ($report.error + ' restored=' + $report.restored); exit 1 }
# 預期失敗的 CLI 檢查會留下 LASTEXITCODE=1；成功時明確回傳零給呼叫者。
exit 0
