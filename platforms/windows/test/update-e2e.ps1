# 更新 E2E（docs/update-e2e.md）core 案例：以使用者方式安裝 A，讓 A 自己建立工作排程器工作，
# 發布相容核心更新 B；使用中持續打字（不得切換），之後真實閒置，等排程發現、準備、交接並核對 B，再打字比對。
# 測試機必須已登入，並以同一帳號的系統管理員 PowerShell 執行（A 的首次安裝需要註冊 TSF）；
# 更新本身由排程以一般權限（LeastPrivilege）執行。A、B 須為 --preview --e2etest 套件 ZIP。
# 原註冊、原程式與個人檔案先備份，結束時還原並核對。查詢週期縮短為 2 分鐘（報告標示）；閒置門檻為真實 5 分鐘。
param([Parameter(Mandatory=$true)][string]$ArchiveA, [Parameter(Mandatory=$true)][string]$ArchiveB,
      [Parameter(Mandatory=$true)][string]$Tools, [string]$ResultDir, [string]$Updater,
      [ValidateSet('core','fail','shell','online','menu','button')][string]$Case = 'core', [string]$ArchiveF)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$ArchiveA = (Resolve-Path -LiteralPath $ArchiveA).Path; $ArchiveB = (Resolve-Path -LiteralPath $ArchiveB).Path; $Tools = (Resolve-Path -LiteralPath $Tools).Path
if (-not $ResultDir) { $ResultDir = Join-Path ([IO.Path]::GetTempPath()) ('0c-update-e2e-' + (Get-Date -Format 'yyyyMMdd-HHmmss')) }
if (Test-Path $ResultDir) { throw 'ResultDir must be a new directory' }
New-Item -ItemType Directory $ResultDir | Out-Null
$base = "$env:LOCALAPPDATA\Zuin"
$stable = "$base\bin\0c-update.exe"
$reg = 'Registry::HKEY_CLASSES_ROOT\CLSID\{138C35D0-7F08-4CEF-BA90-C34C16301528}\InprocServer32'
$sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value.ToLower()
$pipe = "0c-$sid"
$feedRoot = Join-Path $ResultDir 'feed'
$port = 8443
$feedUrl = "https://127.0.0.1:$port/feeds/e2e.json"
$t0 = Get-Date
$report = [ordered]@{ case = $Case; passed = $false; checks = @(); timeline = @(); error = ''; restored = $false
    account = "$env:USERDOMAIN\$env:USERNAME (administrator; scheduled update runs LeastPrivilege)"
    timing = [ordered]@{ feed_check = '2m (shortened, e2etest build)'; idle_needed = '5m (real)'; schedule_every = '1m (e2etest build)' } }
function Log([string]$Kind, $Data) {
    $e = [ordered]@{ t = [math]::Round(((Get-Date) - $t0).TotalSeconds, 1); at = (Get-Date -Format 'HH:mm:ss'); kind = $Kind; data = $Data }
    $report.timeline += $e; ($e | ConvertTo-Json -Compress -Depth 6) | Write-Host
}
function Check([bool]$Ok, [string]$Name, [string]$Detail = '') { if (-not $Ok) { throw "$Name failed $Detail" }; $report.checks += $Name; Log 'check' $Name }
function HashOrAbsent([string]$p) { if (Test-Path $p) { return (Get-FileHash $p).Hash }; return 'absent' }
function Health {
    try {
        $c = New-Object IO.Pipes.NamedPipeClientStream('.', $pipe, [IO.Pipes.PipeDirection]::InOut)
        $c.Connect(2000)
        $body = [Text.Encoding]::UTF8.GetBytes('{"op":"health"}')
        $c.Write([BitConverter]::GetBytes([uint32]$body.Length), 0, 4); $c.Write($body, 0, $body.Length); $c.Flush()
        $h = New-Object byte[] 4; [void]$c.Read($h, 0, 4); $n = [BitConverter]::ToUInt32($h, 0)
        $buf = New-Object byte[] $n; $got = 0; while ($got -lt $n) { $got += $c.Read($buf, $got, $n - $got) }
        $c.Dispose()
        return ([Text.Encoding]::UTF8.GetString($buf) | ConvertFrom-Json).info
    } catch { return $null }
}
function State { try { return (Get-Content "$base\update\state.json" -Raw -Encoding UTF8 | ConvertFrom-Json) } catch { return $null } }
$probeTask = '0c-UpdateE2E-Probe-' + [guid]::NewGuid().ToString('N')
function TypeKeys([string]$Keys, [string]$Expect) {
    for ($attempt = 1; $attempt -le 3; $attempt++) {
        $r = TypeOnce $Keys $Expect
        if ($r.error -notlike '*foreground*') { break }
        Log 'probe_retry' ([ordered]@{ attempt = $attempt; foreground = $r.foreground })
    }
    return ($r.text -eq $Expect)
}
function TypeOnce([string]$Keys, [string]$Expect) {
    $out = Join-Path $ResultDir ('probe-' + [guid]::NewGuid().ToString('N') + '.json')
    $args = '-NoProfile -STA -ExecutionPolicy Bypass -File "' + $PSScriptRoot + '\type-probe.ps1" -Keys "' + $Keys + '" -Out "' + $out + '"'
    $action = New-ScheduledTaskAction -Execute powershell.exe -Argument $args
    $principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Limited
    Register-ScheduledTask -TaskName $probeTask -Action $action -Principal $principal -Force | Out-Null
    Start-ScheduledTask $probeTask
    $deadline = (Get-Date).AddSeconds(60)
    while (-not (Test-Path $out) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 500 }
    Start-Sleep -Milliseconds 300
    $r = Get-Content $out -Raw -Encoding UTF8 | ConvertFrom-Json
    Log 'typed' ([ordered]@{ keys = $Keys; text = $r.text; expect = $Expect; error = $r.error; foreground = $r.foreground })
    return $r
}
# 在互動工作階段執行一段 PowerShell（截圖、關閉通知等使用者可見的操作）。
function Interactive([string]$Script, [int]$Seconds = 20) {
    $done = Join-Path $ResultDir ('done-' + [guid]::NewGuid().ToString('N'))
    $enc = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($Script + "; Set-Content -LiteralPath '$done' ok"))
    $name = '0c-UpdateE2E-UI-' + [guid]::NewGuid().ToString('N')
    Register-ScheduledTask -TaskName $name -Action (New-ScheduledTaskAction -Execute powershell.exe -Argument "-NoProfile -WindowStyle Hidden -EncodedCommand $enc") -Principal (New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Limited) -Force | Out-Null
    Start-ScheduledTask $name
    $deadline = (Get-Date).AddSeconds($Seconds); while (-not (Test-Path $done) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 300 }
    Unregister-ScheduledTask $name -Confirm:$false
}
function Screenshot([string]$Name) {
    $png = Join-Path $ResultDir $Name
    Interactive ('Add-Type -AssemblyName System.Windows.Forms,System.Drawing; $b=[Windows.Forms.Screen]::PrimaryScreen.Bounds; $m=New-Object Drawing.Bitmap $b.Width,$b.Height; $g=[Drawing.Graphics]::FromImage($m); $g.CopyFromScreen($b.Location,[Drawing.Point]::Empty,$b.Size); $m.Save(''' + $png + ''')')
    Log 'screenshot' $Name
}
# 像使用者一樣關掉已顯示的更新通知（閒置時 Windows 會把通知留在畫面上並持有前景）。
function DismissNotices {
    Interactive '[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null; foreach($a in ''Zuin.0c.Update'',''{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe''){ [Windows.UI.Notifications.ToastNotificationManager]::History.Clear($a) }'
    Log 'notices_dismissed' ''
}
# 輸入指示器選單探針（menu-probe.ps1）：互動工作階段中，經 TSF 呼叫本輸入法的按鈕與選單。
function MenuProbe([string]$Select = '', [switch]$Toggle, [switch]$ShiftToggle, [string]$TypeAfter = '', [string]$ToggleBackThen = '', [switch]$RightClick) {
    $out = Join-Path $ResultDir ('menu-' + [guid]::NewGuid().ToString('N') + '.json')
    $a = '-NoProfile -STA -ExecutionPolicy Bypass -File "' + $PSScriptRoot + '\menu-probe.ps1" -Out "' + $out + '"'
    if ($Select) { $a += ' -Select "' + $Select + '"' }
    if ($Toggle) { $a += ' -Toggle' }
    if ($ShiftToggle) { $a += ' -ShiftToggle' }
    if ($RightClick) { $a += ' -RightClick' }
    if ($ToggleBackThen) { $a += ' -ToggleBackThen "' + $ToggleBackThen + '"' }
    if ($TypeAfter) { $a += ' -TypeAfter "' + $TypeAfter + '"' }
    $name = '0c-UpdateE2E-Menu-' + [guid]::NewGuid().ToString('N')
    Register-ScheduledTask -TaskName $name -Action (New-ScheduledTaskAction -Execute powershell.exe -Argument $a) -Principal (New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Limited) -Force | Out-Null
    Start-ScheduledTask $name
    $deadline = (Get-Date).AddSeconds(60)
    while (-not (Test-Path $out) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 500 }
    Unregister-ScheduledTask $name -Confirm:$false
    Start-Sleep -Milliseconds 300
    $r = Get-Content $out -Raw -Encoding UTF8 | ConvertFrom-Json
    Log 'menu' $r
    if ($r.error) { throw "menu probe: $($r.error)" }
    return $r
}
function MenuLabels($r) { return @($r.menu | ForEach-Object { ($_ -split '\|', 3)[2] }) }
function Settings { try { return (Get-Content "$base\settings.json" -Raw -Encoding UTF8 | ConvertFrom-Json) } catch { return $null } }
function WaitFor([scriptblock]$Cond, [int]$Seconds, [string]$What) {
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) { try { if (& $Cond) { return } } catch {}; Start-Sleep 2 }
    throw "timed out waiting for $What"
}
function Publish([string]$Archive, [int]$Serial, [string]$Kind = 'core') {
    $dest = Join-Path "$feedRoot\releases" (Split-Path $Archive -Leaf)
    if (-not (Test-Path $dest)) { Copy-Item $Archive $dest }
    & "$Tools\0c-release.exe" feed -key "$Tools\release-test.key" -serial $Serial -valid 48h -kind $Kind -out "$feedRoot\feeds\e2e.json" -asset "$dest=https://127.0.0.1:$port/releases/$(Split-Path $dest -Leaf)"
    if ($LASTEXITCODE -ne 0) { throw 'feed signing failed' }
    $f = Get-Content "$feedRoot\feeds\e2e.json" -Raw -Encoding UTF8 | ConvertFrom-Json
    Log 'published' ([ordered]@{ version = $f.version; serial = $Serial; asset_kind = $Kind }); return $f
}

# 還原用快照：原註冊、原註冊所在的程式、個人檔與設定。
if (Test-Path "$base\install-receipt.json") { throw 'existing receipt must be preserved; refusing test' }
if (Test-Path "$base\engine.json") { throw 'existing engine pointer must be preserved; refusing test' }
if (Test-Path "$base\update") { throw 'existing update state must be preserved; refusing test' }
if (Get-Process 0c-server, zuin-server -ErrorAction SilentlyContinue) { throw 'Close the daily engine before update E2E (sign out or end it after finishing composition)' }
$original = if (Test-Path $reg) { (Get-Item $reg).GetValue('') } else { '' }
$saved = Join-Path $ResultDir 'saved'; New-Item -ItemType Directory $saved | Out-Null
$originalDir = if ($original) { Split-Path $original } else { '' }
if ($originalDir) {
    if ($originalDir -eq $base) { foreach ($n in 'zuin_tsf.dll','zuin-server.exe','zuin-ui.exe','0c_tsf.dll','0c-server.exe','0c-ui.exe') { if (Test-Path "$base\$n") { Copy-Item "$base\$n" $saved } } }
    else { Copy-Item -LiteralPath $originalDir -Destination "$saved\directory" -Recurse }
}
foreach ($n in 'user.json','settings.json') { if (Test-Path "$base\$n") { Copy-Item "$base\$n" "$saved\$n" } }
$userBefore = HashOrAbsent "$base\user.json"
$versionsBefore = @(Get-ChildItem "$base\versions" -Directory -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Name)
$feedProc = $null
try {
    New-Item -ItemType Directory "$feedRoot\feeds", "$feedRoot\releases" -Force | Out-Null
    DismissNotices  # 前一輪留在畫面上的通知
    $feedProc = Start-Process "$Tools\0c-e2e-feed.exe" -ArgumentList "-addr 127.0.0.1:$port -root `"$feedRoot`" -cert `"$Tools\feed-server.pem`" -key `"$Tools\feed-server.key`"" -PassThru -WindowStyle Hidden -RedirectStandardError (Join-Path $ResultDir 'feed.log')
    # 與 EXE 安裝精靈相同：用 A 自己的 0c-update.exe 以 apply 核對並安裝 A 的 ZIP。
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::OpenRead($ArchiveA)
    try { $entry = $zip.Entries | Where-Object { $_.FullName -like '*/0c-update.exe' } | Select-Object -First 1
          [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, (Join-Path $ResultDir '0c-update-A.exe')) } finally { $zip.Dispose() }
    $shaA = (Get-FileHash $ArchiveA -Algorithm SHA256).Hash.ToLower()
    if ($Case -eq 'online') {
        # 一行安裝（#19）的後半段：install.ps1 以 UAC 執行的就是這個命令（UAC 對話框本身無法自動化）。
        $fa = Publish $ArchiveA 1
        & (Join-Path $ResultDir '0c-update-A.exe') install -online -feed $feedUrl -channel internal-preview | Out-String | ForEach-Object { Log 'installed_a' $_.Trim() }
    } else {
        & (Join-Path $ResultDir '0c-update-A.exe') apply -archive $ArchiveA -sha256 $shaA -channel internal-preview | Out-String | ForEach-Object { Log 'installed_a' $_.Trim() }
    }
    if ($LASTEXITCODE -ne 0) { throw 'installing A failed' }
    $tasks = @(Get-ScheduledTask | Where-Object { $_.TaskName -like '0c-update-*' })
    Check ($tasks.Count -eq 1) 'a_created_exactly_one_task'
    Check ($tasks[0].Principal.RunLevel -eq 'Limited') 'task_runs_without_elevation'
    Check (Test-Path $stable) 'stable_updater_installed'
    if ($Updater) { Copy-Item $Updater "$stable.dev"; Move-Item -Force "$stable.dev" $stable; $report.updater_override = (Get-FileHash $stable).Hash }
    if ($Case -ne 'online') { $fa = Publish $ArchiveA 1 }
    if ($Case -in 'fail', 'shell', 'menu', 'button') { & $stable schedule install -feed $feedUrl -test-check 2m -test-idle 1m | Out-Null; $report.timing.idle_needed = '1m (shortened, e2etest build)' }
    else { & $stable schedule install -feed $feedUrl -test-check 2m | Out-Null }
    Check (TypeKeys 'su3cl3{ENTER}' '你好') 'a_typing'
    $h = Health; Log 'health' $h
    Check ($h -and $h.version -eq $fa.version) 'a_running'
    $aPid = $h.pid
    $deadline = (Get-Date).AddMinutes(4)
    while ((Get-Date) -lt $deadline -and -not ((State) -and ([string](State).last_success) -notlike '0001*')) { Start-Sleep 5 }
    Check ((State).stage -eq 'idle') 'a_feed_seen_idle'
    $seen = @{}
    function WaitStage([string[]]$Stages, [int]$Minutes) {
        $deadline = (Get-Date).AddMinutes($Minutes)
        while ((Get-Date) -lt $deadline) {
            $s = State; $key = "$($s.stage)/$($s.blocked.reason)"
            if ($s -and -not $seen[$key]) { $seen[$key] = $true; Log 'stage' ([ordered]@{ stage = $s.stage; blocked = $s.blocked; error = $s.last_error }) }
            if ($s.stage -in $Stages) { return $s }
            Start-Sleep 10
        }
        return (State)
    }
    if ($Case -eq 'menu') {
        # #13：輸入指示器按鈕與選單（經 TSF 呼叫，與系統介面相同的入口）。
        $r = MenuProbe
        Check ($r.clsid -eq '138c35d0-7f08-4cef-ba90-c34c16301528' -and $r.text -eq '中') 'indicator_button_is_ours'
        Check ((MenuLabels $r) -contains "靈犀注音 $($fa.version) · 已是最新") 'menu_shows_current_version'
        $r = MenuProbe -Select '提交時補標點'
        WaitFor { (Settings).punctuation -eq $false } 30 'punctuation off'
        Check $true 'menu_toggle_saves_setting'
        $r = MenuProbe -Select '下載後詢問'
        WaitFor { (State).mode -eq 'ask' } 30 'ask mode'
        Check $true 'menu_switches_mode'
        $r = MenuProbe -TypeAfter 'su3cl3{ENTER}'
        Check ($r.typed -eq '你好') 'menu_probe_types' "$($r.typed)"
        $r = MenuProbe -Toggle -TypeAfter 'abc' -ToggleBackThen 'su3cl3{ENTER}'
        Check ($r.text_after_toggle -eq '英' -and $r.typed -eq 'abc') 'left_click_toggles_english' "$($r.text_after_toggle) $($r.typed)"
        Check ($r.text_after_back -eq '中' -and $r.typed_after_back -eq 'abc你好') 'left_click_back_to_chinese' "$($r.text_after_back) $($r.typed_after_back)"
        # 右鍵選單：系統在使用者於輸入指示器按右鍵時呼叫 OnClick(右鍵)，由本輸入法彈出選單。
        $r = MenuProbe -RightClick
        # 選單項目與 InitMenu 同一來源（已於上方核對）；這裡核對彈出、截圖存證，並確認關閉後宿主正常。
        Check ((($r.popup) -join '|') -like '*found=True*') 'right_click_popup_menu' (($r.popup) -join '|')
        Check ($r.alive_after_popup -eq $true) 'host_alive_after_popup'
        $h = Health
        Check ($h -and $h.punctuation -eq 'false') 'engine_restarted_with_new_setting' "$($h.punctuation)"
        $fb = Publish $ArchiveB 2
        WaitStage @('prepared') 8 | Out-Null
        WaitFor { (State).blocked.reason -eq 'needs_consent' } 60 'needs_consent'
        Screenshot 'consent-toast.png'
        DismissNotices
        Check ((Health).version -eq $fa.version) 'dismissed_notice_is_not_consent'
        $label = "有新版本 $($fb.version) — 立即安裝"
        WaitFor { (MenuLabels (MenuProbe)) -contains $label } 90 'pending entry in menu'
        Check $true 'pending_update_findable_in_menu'
        $r = MenuProbe -Select '立即安裝'
        WaitFor { (State).stage -eq 'active' } 180 'install from menu'
        Check ((Health).version -eq $fb.version) 'menu_install_applies_b'
        $r = MenuProbe -Select '暫停自動更新'
        WaitFor { ([string](State).paused_until) -like '20*' } 30 'paused'
        Check $true 'menu_pauses_updates'
        Check (TypeKeys 'su3cl3{ENTER}' '你好') 'b_typing'
        $report.passed = $true
    } elseif ($Case -eq 'button') {
        # #13：toast「立即安裝」按鈕。按鈕以 zc-update: 協定啟動 0c-update；這裡以同一協定啟動（等同按下）。
        & $stable mode ask | Out-Null
        $fb = Publish $ArchiveB 2
        WaitFor { (State).blocked.reason -eq 'needs_consent' } 480 'needs_consent'
        Start-Sleep 3
        Screenshot 'consent-toast-button.png'
        $cmd = (Get-ItemProperty 'Registry::HKEY_CURRENT_USER\Software\Classes\zc-update\shell\open\command').'(default)'
        Check ($cmd -like '*conhost.exe*--headless*0c-update.exe*protocol*') 'protocol_registered' $cmd
        Check ((Health).version -eq $fa.version) 'nothing_installed_before_button'
        $target = (State).target
        $query = 'version=' + [uri]::EscapeDataString($target.version) + '&sha256=' + $target.sha256
        Interactive ("Start-Process 'zc-update:snooze?" + $query + "'")
        WaitFor { $null -ne (State).notice_snooze } 30 'persistent one-hour reminder'
        Check ((State).notice_snooze.sha256 -eq $target.sha256) 'snooze_bound_to_asset'
        Check ((Health).version -eq $fa.version) 'snooze_did_not_install'
        Interactive ("Start-Process 'zc-update:finish?" + $query + "'")
        WaitFor { (State).stage -eq 'active' } 180 'install from button'
        Check ((Health).version -eq $fb.version) 'button_install_applies_b'
        Check (@((State).history | Where-Object { $_.kind -eq 'active' }).Count -ge 1) 'activated'
        DismissNotices
        Check (TypeKeys 'su3cl3{ENTER}' '你好') 'b_typing'
        $report.passed = $true
    } elseif ($Case -eq 'online') {
        $receipt = Get-Content "$base\install-receipt.json" -Raw -Encoding UTF8 | ConvertFrom-Json
        Check ($receipt.version -eq $fa.version) 'installed_signed_version'
        $again = & (Join-Path $ResultDir '0c-update-A.exe') install -online -feed $feedUrl -channel internal-preview | Out-String
        Log 'rerun' $again.Trim()
        Check ($LASTEXITCODE -eq 0 -and $again -like '*不重新安裝*') 'rerun_is_safe'
        Check (@(Get-ScheduledTask | Where-Object { $_.TaskName -like '0c-update-*' }).Count -eq 1) 'still_one_task'
        $report.passed = $true
    } elseif ($Case -eq 'shell') {
        # E15／E16（可自動化部分）：外殼版需要系統管理員；排程以一般權限執行，只能提醒並等待確認。
        # UAC 安全桌面無法自動操作：以本測試的系統管理員工作階段執行 finish，代表使用者通過 UAC 之後。
        $fb = Publish $ArchiveB 2 'shell'
        $s = WaitStage @('prepared', 'pending', 'active') 8
        Start-Sleep 15
        $s = State
        Check ($s.blocked.reason -eq 'needs_consent') 'shell_needs_consent_without_elevation' ($s.blocked | ConvertTo-Json)
        Check (@($s.history | Where-Object { $_.kind -eq 'notified' }).Count -ge 1) 'consent_notice_submitted'
        Screenshot 'consent-notice.png'
        DismissNotices
        $h = Health
        Check ($h -and $h.version -eq $fa.version) 'nothing_installed_before_consent'
        $out = & $stable finish 2>&1 | Out-String
        Log 'finish' $out.Trim()
        $receipt = Get-Content "$base\install-receipt.json" -Raw -Encoding UTF8 | ConvertFrom-Json
        Check ($receipt.version -eq $fb.version) 'finish_installed_b'
        $dll = (Get-Item $reg).GetValue('')
        Check ($dll -like "*$($fb.version)*") 'registration_points_to_b' $dll
        Check ((State).stage -in 'pending', 'active') 'pending_until_relogin_or_confirmed'
        # 混合期間：已執行的引擎仍是 A；新開的應用程式載入 B 的 DLL。輸入必須照常。
        Check (TypeKeys 'su3cl3{ENTER}' '你好') 'typing_in_new_app_during_mixed_period'
        $report.manual = '真實登出再登入後核對 runtime 為 B（需要人工或另一個互動帳號）'
        $report.state = State
        $report.passed = $true
    } elseif ($Case -eq 'fail') {
        # E23：F 切換後健康檢查失敗，回復 A 並可輸入；F 不再自動重試；修正版 B 恢復更新。
        $ff = Publish (Resolve-Path -LiteralPath $ArchiveF).Path 2
        $s = WaitStage @('failed', 'active') 12
        Check ($s.stage -eq 'failed' -and $s.blocked.reason -eq 'health_failed') 'f_health_failed' ($s.blocked | ConvertTo-Json)
        Check ($ff.version -in @($s.failed)) 'f_suspended'
        $h = Health; Log 'health' $h
        Check ($h -and $h.version -eq $fa.version) 'a_restored_running'
        Check (-not (Test-Path "$base\engine.json")) 'pointer_reverted'
        Check (@($s.history | Where-Object { $_.kind -eq 'notified' }).Count -ge 1) 'failure_notice_submitted'
        Start-Sleep 3
        Screenshot 'failure-notice.png'
        DismissNotices
        Check (TypeKeys 'su3cl3{ENTER}' '你好') 'a_typing_after_revert'
        Start-Sleep 180
        $s = State
        Check ($s.stage -eq 'failed' -and @($s.history | Where-Object { $_.kind -eq 'switched' -and $_.version -eq $ff.version }).Count -eq 1) 'f_not_retried'
        $fb = Publish $ArchiveB 3
        $s = WaitStage @('active') 12
        Check ($s.stage -eq 'active') 'fixed_release_recovers'
        $h = Health
        Check ($h -and $h.version -eq $fb.version) 'b_running_after_recovery'
        Check (TypeKeys 'su3cl3{ENTER}' '你好') 'b_typing'
        $report.state = State
        $report.passed = $true
    } else {
    $fb = Publish $ArchiveB 2
    $busyUntil = (Get-Date).AddMinutes(8)
    while ((Get-Date) -lt $busyUntil) {
        if (-not (TypeKeys 'su3cl3{ENTER}' '你好')) { throw 'typing failed while an update was pending' }
        $s = State; if ($s -and -not $seen[$s.stage]) { $seen[$s.stage] = $true; Log 'stage' ([ordered]@{ stage = $s.stage; blocked = $s.blocked }) }
        $h = Health; if ($h -and $h.version -ne $fa.version) { throw "engine switched while user was typing: $($h.version)" }
        Start-Sleep 50
    }
    Check $true 'typing_while_update_pending'
    $s = State
    Check ($s.stage -eq 'prepared' -and $s.blocked.reason -eq 'waiting_idle') 'prepared_waits_while_typing' ($s | ConvertTo-Json -Depth 4)
    $userMid = HashOrAbsent "$base\user.json"
    $idleStart = Get-Date
    $deadline = $idleStart.AddMinutes(15)
    while ((Get-Date) -lt $deadline) {
        $s = State; if ($s -and -not $seen[$s.stage]) { $seen[$s.stage] = $true; Log 'stage' ([ordered]@{ stage = $s.stage; blocked = $s.blocked }) }
        if ($s.stage -in 'active', 'failed') { break }
        Start-Sleep 10
    }
    $report.switch_after_idle_seconds = [int]((Get-Date) - $idleStart).TotalSeconds
    Check ($s.stage -eq 'active') 'switched_to_b_without_user_action' ($s.blocked | ConvertTo-Json)
    $h = Health; Log 'health' $h
    Check ($h -and $h.version -eq $fb.version -and $h.pid -ne $aPid) 'b_running_new_writer'
    $pointer = Get-Content "$base\engine.json" -Raw -Encoding UTF8 | ConvertFrom-Json
    Check ($pointer.version -eq $fb.version) 'engine_pointer_b'
    Check ((HashOrAbsent "$base\user.json") -eq $userMid) 'learning_preserved_across_switch'
    Check (TypeKeys 'su3cl3{ENTER}' '你好') 'b_typing_after_switch'
    Check (TypeKeys 'cl3{ENTER}' '好') 'b_typing_again'
    $report.state = State
    $report.versions = [ordered]@{ a = $fa.version; b = $fb.version; b_sha256 = $fb.assets[0].sha256 }
    $report.passed = $true
    }
} catch { $report.error = $_.Exception.Message; Log 'error' $_.Exception.Message } finally {
    if (-not $report.state) { $report.state = State }
    try {
        $t = Get-ScheduledTask | Where-Object { $_.TaskName -like '0c-update-*' } | Select-Object -First 1
        if ($t) { $i = $t | Get-ScheduledTaskInfo; $report.task = [ordered]@{ last_run = "$($i.LastRunTime)"; last_result = $i.LastTaskResult; next_run = "$($i.NextRunTime)"; runs = $i.NumberOfMissedRuns } }
    } catch {}
    try {
        if (Get-ScheduledTask $probeTask -ErrorAction SilentlyContinue) { Unregister-ScheduledTask $probeTask -Confirm:$false }
        if (Test-Path $stable) { & $stable schedule remove | Out-Null }
        Get-ScheduledTask | Where-Object { $_.TaskName -like '0c-update-*' } | Unregister-ScheduledTask -Confirm:$false
        if ($feedProc) { Stop-Process -Id $feedProc.Id -ErrorAction SilentlyContinue }
        Get-Process 0c-server, 0c-ui, python -ErrorAction SilentlyContinue | Where-Object { $_.Path -like "$base\versions\*" } | Stop-Process -Force -ErrorAction SilentlyContinue
        Start-Sleep 2
        if (Test-Path $reg) { $current = (Get-Item $reg).GetValue(''); if ($current -ne $original) { & "$env:WINDIR\System32\regsvr32.exe" /u /s $current } }
        if ($originalDir -eq $base) { foreach ($f in Get-ChildItem $saved -File) { if ($f.Name -notin 'user.json','settings.json' -and (HashOrAbsent "$base\$($f.Name)") -ne (HashOrAbsent $f.FullName)) { Copy-Item $f.FullName "$base\$($f.Name)" -Force } } }
        elseif ($originalDir -and -not (Test-Path $original)) { if (Test-Path $originalDir) { Remove-Item $originalDir -Recurse -Force }; Copy-Item "$saved\directory" $originalDir -Recurse }
        if ($original) { & "$env:WINDIR\System32\regsvr32.exe" /s $original }
        foreach ($n in 'install-receipt.json', 'engine.json') { Remove-Item "$base\$n" -ErrorAction SilentlyContinue }
        foreach ($d in 'update', 'bin', 'receipts', 'backups') { if (Test-Path "$base\$d") { Remove-Item "$base\$d" -Recurse -Force -ErrorAction SilentlyContinue } }
        foreach ($d in Get-ChildItem "$base\versions" -Directory -ErrorAction SilentlyContinue) { if ($d.Name -notin $versionsBefore) { Remove-Item $d.FullName -Recurse -Force -ErrorAction SilentlyContinue } }
        foreach ($n in 'user.json', 'settings.json') { if (Test-Path "$saved\$n") { Copy-Item "$saved\$n" "$base\$n" -Force } else { Remove-Item "$base\$n" -ErrorAction SilentlyContinue } }
        $current = if (Test-Path $reg) { (Get-Item $reg).GetValue('') } else { '' }
        $report.restored = ($current -eq $original) -and ((HashOrAbsent "$base\user.json") -eq $userBefore)
        if (-not $report.restored) { throw 'restoration verification failed' }
    } catch { $report.passed = $false; $report.error += '; cleanup: ' + $_.Exception.Message }
    $report | ConvertTo-Json -Depth 8 | Set-Content (Join-Path $ResultDir 'report.json') -Encoding UTF8
}
Write-Host $ResultDir
if (-not $report.passed) { exit 1 }
