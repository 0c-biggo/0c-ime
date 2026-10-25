# 明確的 Windows 測試機操作：暫時註冊指定套件，在互動桌面執行測試，最後還原。
# 不加入一般單元測試；不強制停止日常引擎、候選窗或應用程式。
param(
    [Parameter(Mandatory=$true)][string]$BundleDir,
    # explorer、winui（#105）不在預設組：explorer 在共用的 explorer.exe 開自己的視窗，winui 需要 Windows App SDK 版的「相片」。
    # onenote（#163）不在預設組：需要已安裝並開過一次的 OneNote 桌面版，測試在結果目錄建立自己的本機筆記本。
    # excel（#189）不在預設組：需要已安裝的 Excel 桌面版，測試以 COM 開自己的空白活頁簿、不存檔。
    # leftshift（#65）不在預設組：它自己以 0c-update set left-shift on／off -apply 重啟隔離引擎，需要 Edge 與 Chrome。
    # native32、wpf32（#112）：同一份原生 EDIT 與 WPF 測試改在 32 位元 PowerShell（SysWOW64）裡跑，宿主載入 x86\0c_tsf.dll。
    # webview2（#144）：WinForms 裡的 WebView2（新版 Teams 的視窗結構）；第一次執行從 NuGet 下載固定版本的 WebView2 SDK，
    # 離線或沒有 WebView2 執行階段時標為 skipped（列在結果裡，不算失敗）。
    [ValidateSet('wpf','native','wpf32','native32','browser','webview2','notepad','terminal','candidate','search','word','excel','onenote','private','leftshift','explorer','winui')][string[]]$Suite=@('wpf','native','wpf32','native32','browser','webview2','notepad','terminal','candidate','search','private'),
    [string]$ResultDir
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
# 測試安裝不建立自動更新排程（否則會留下 0c-update-<SID> 排程與 Zuin\bin）。
$env:ZC_NO_SCHEDULE='1'
$identity=[Security.Principal.WindowsIdentity]::GetCurrent()
$principal=[Security.Principal.WindowsPrincipal]::new($identity)
if(-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Run as the logged-in desktop user with administrator rights'}
$bundle=(Resolve-Path -LiteralPath $BundleDir).Path
$updater=Join-Path $bundle '0c-update.exe'
& $updater verify -bundle $bundle
if($LASTEXITCODE -ne 0){throw 'Bundle verification failed'}
$release=Get-Content (Join-Path $bundle 'release.json') -Raw -Encoding UTF8|ConvertFrom-Json
if($release.os -ne 'windows' -or $release.arch -ne 'amd64' -or $release.channel -notin @('internal-preview','public-beta')){throw 'Desktop acceptance requires a Windows x64 preview or public-beta bundle'}
$root=(Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
if(-not $ResultDir){$ResultDir=Join-Path $root ('build\windows\validation\acceptance-'+(Get-Date -Format yyyyMMdd-HHmmss)+'-'+[guid]::NewGuid().ToString('N').Substring(0,8))}
if(Test-Path -LiteralPath $ResultDir){throw 'ResultDir must be a new directory'}
$mutex=[Threading.Mutex]::new($false,('Global\0c-desktop-acceptance-'+$identity.User.Value))
$locked=$false
$server=$null;$taskName=$null;$installed=$null;$snapshotTaken=$false
$base=Join-Path $env:LOCALAPPDATA 'Zuin'
$themeKey='HKCU:\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize'
$themeSaved=$false
$receiptFile=Join-Path $base 'install-receipt.json'
$reg='Registry::HKEY_CLASSES_ROOT\CLSID\{138C35D0-7F08-4CEF-BA90-C34C16301528}\InprocServer32'
$report=[ordered]@{passed=$false;version=$release.version;suites=@();error='';restored=$false;restored32=$false;user_preserved=$false;legacy_data_preserved=$false;receipt_restored=$false}
function RegisteredDLL {if(Test-Path $reg){return (Get-Item $reg).GetValue('')};return ''}
# 32 位元應用程式讀的登錄視圖（HKCR\WOW6432Node\CLSID，#112）。
function RegisteredDLL32 {
    $key=[Microsoft.Win32.RegistryKey]::OpenBaseKey('ClassesRoot','Registry32').OpenSubKey('CLSID\{138C35D0-7F08-4CEF-BA90-C34C16301528}\InprocServer32')
    if($key){try{return [string]$key.GetValue('')}finally{$key.Close()}};return ''
}
function HashOrAbsent([string]$path){if(Test-Path -LiteralPath $path){return (Get-FileHash -LiteralPath $path).Hash};return 'absent'}
function Regsvr([string]$dll,[bool]$remove){
    $args='/s "'+$dll+'"';if($remove){$args='/u '+$args}
    $p=Start-Process "$env:WINDIR\System32\regsvr32.exe" -ArgumentList $args -PassThru
    $handle=$p.Handle;$p.WaitForExit()
    if($p.ExitCode -ne 0){throw "Registration restore failed ($($p.ExitCode))"}
}
function StopTestProcesses {
    if($script:taskName -and (Get-ScheduledTask -TaskName $script:taskName -ErrorAction SilentlyContinue)){
        Stop-ScheduledTask -TaskName $script:taskName
        Unregister-ScheduledTask -TaskName $script:taskName -Confirm:$false
    }
    $script:taskName=$null
    if($script:server -and -not $script:server.HasExited){Stop-Process -Id $script:server.Id}
    $script:server=$null
    if($script:installed){Get-Process 0c-ui -ErrorAction SilentlyContinue|Where-Object Path -eq "$script:installed\0c-ui.exe"|Stop-Process}
}
try {
    try {$locked=$mutex.WaitOne(0)} catch [Threading.AbandonedMutexException] {$locked=$true}
    if(-not $locked){throw 'Another desktop acceptance run is active'}
    if(Get-Process 0c-server,0c-ui,zuin-server,zuin-ui -ErrorAction SilentlyContinue){throw 'Finish daily input and close the existing engine/candidate UI before acceptance'}
    New-Item -ItemType Directory -Path $ResultDir | Out-Null
    $ResultDir=(Resolve-Path -LiteralPath $ResultDir).Path
    $report.result_dir=$ResultDir
    $original=RegisteredDLL
    $original32=RegisteredDLL32
    $fixedDir=Join-Path $base 'tsf'
    $fixedExisted=Test-Path -LiteralPath $fixedDir
    $hadReceipt=Test-Path -LiteralPath $receiptFile
    if($hadReceipt){Copy-Item -LiteralPath $receiptFile -Destination "$ResultDir\original-receipt.json"}
    $receiptBefore=HashOrAbsent $receiptFile
    $userBefore=HashOrAbsent "$base\user.json"
    $dataBefore=HashOrAbsent "$base\data\manifest.json"
    $report.original_registration=$original
    $report.original_registration32=$original32
    if($Suite -contains 'candidate'){
        $themeKeyExisted=Test-Path $themeKey
        $themeSettings=Get-ItemProperty $themeKey -ErrorAction SilentlyContinue
        $themeValue=if($themeSettings){$themeSettings.PSObject.Properties['AppsUseLightTheme']}else{$null}
        $themeHadValue=$null -ne $themeValue
        if($themeHadValue){$themeOriginal=$themeValue.Value;$themeKind=(Get-Item $themeKey).GetValueKind('AppsUseLightTheme')}
        @{had_value=$themeHadValue;value=$themeOriginal;kind=[string]$themeKind;key_existed=$themeKeyExisted}|ConvertTo-Json|Set-Content "$ResultDir\original-theme.json" -Encoding UTF8
        $themeSaved=$true
    }
    $snapshotTaken=$true
    & $updater install -bundle $bundle -channel $release.channel
    if($LASTEXITCODE -ne 0){throw 'Test installation failed'}
    $receipt=Get-Content $receiptFile -Raw|ConvertFrom-Json
    $installed=Join-Path $base $receipt.directory
    # 0.1.0-dev.62 起註冊固定路徑 Zuin\tsf\0c_tsf.dll（#121），內容與版本目錄相同；更舊的版本註冊版本目錄。
    $fixed=Join-Path $fixedDir '0c_tsf.dll'
    $useFixed=(Test-Path -LiteralPath $fixed) -and (RegisteredDLL) -eq $fixed
    $expectedDLL=if($useFixed){$fixed}else{Join-Path $installed '0c_tsf.dll'}
    if((RegisteredDLL) -ne $expectedDLL){throw 'Registration does not match test bundle'}
    if($useFixed -and (Get-FileHash $fixed).Hash -ne (Get-FileHash (Join-Path $installed '0c_tsf.dll')).Hash){throw 'Fixed DLL differs from the installed version'}
    $report.fixed_path=$useFixed
    $expectedDLL32=if($useFixed){Join-Path $fixedDir 'x86\0c_tsf.dll'}else{Join-Path $installed 'x86\0c_tsf.dll'}
    if(Test-Path -LiteralPath $expectedDLL32){
        if((RegisteredDLL32) -ne $expectedDLL32){throw "32-bit registration does not match test bundle: $(RegisteredDLL32)"}
    } elseif($Suite -match '32$'){throw 'Bundle has no 32-bit DLL (x86\0c_tsf.dll)'}
    $scripts=@{wpf='smoke';native='native';wpf32='smoke';native32='native';browser='browser';webview2='webview2';notepad='notepad';terminal='terminal';candidate='candidate';search='search';word='word';excel='excel';onenote='onenote';private='private';leftshift='leftshift';explorer='explorer';winui='winui'}
    $outputs=@{wpf='desktop';native='native';wpf32='desktop';native32='native';browser='browser';webview2='webview2';notepad='notepad';terminal='terminal';candidate='candidate';search='search';word='word';excel='excel';onenote='onenote';private='private';leftshift='leftshift';explorer='explorer';winui='winui'}
    foreach($name in ($Suite|Select-Object -Unique)){
        $directory=Join-Path $ResultDir $name
        New-Item -ItemType Directory $directory|Out-Null
        $resultFile=Join-Path $directory ($outputs[$name]+'-result.json')
        try {
            # 每個套件都有新的學習資料，避免前一套選字結果影響下一套。
            # 記事本與 Terminal 的快速連打斷言不含標點（補標點會把「你好你好」補成「你好，你好」），這兩套關閉補標點。
            if($name -in @('notepad','terminal')){'{"punctuation":false}'|Set-Content (Join-Path $directory 'settings.json') -Encoding ASCII}
            # search 比對同引擎的參考 session 與 SearchHost 的數字選字（#214）。
            # 只驗外殼：兩邊都用統計排序、全新詞庫、無前文與補標點；Esc 用來證明候選已收起。
            if($name -eq 'search'){'{"selection":"statistical","punctuation":false,"document_context":false,"esc_clear":true}'|Set-Content (Join-Path $directory 'settings.json') -Encoding ASCII}
            $args='-data "'+$installed+'\data" -user "'+$directory+'\test-user.json" -idle-exit 300s'
            $server=Start-Process "$installed\0c-server.exe" -ArgumentList $args -PassThru -NoNewWindow -RedirectStandardOutput "$directory\server.out" -RedirectStandardError "$directory\server.err"
            Start-Sleep -Seconds 1
            if($server.HasExited){throw 'Isolated test engine failed to start'}
            $script=Join-Path $PSScriptRoot ($scripts[$name]+'-test.ps1')
            $host32=$name -match '32$'
            $dll=if($host32){$expectedDLL32}else{$expectedDLL}
            $shell=if($host32){"$env:WINDIR\SysWOW64\WindowsPowerShell\v1.0\powershell.exe"}else{"$env:WINDIR\System32\WindowsPowerShell\v1.0\powershell.exe"}
            $args='-NoProfile -STA -ExecutionPolicy Bypass -File "'+$script+'" -ResultDir "'+$directory+'" -ExpectedDLL "'+$dll+'"'
            $taskName='0c-Acceptance-'+[guid]::NewGuid().ToString('N')
            $action=New-ScheduledTaskAction -Execute $shell -Argument $args
            $taskPrincipal=New-ScheduledTaskPrincipal -UserId $identity.Name -LogonType Interactive -RunLevel Highest
            # 無痕組依序開 Chrome 無痕、訪客、Firefox 私密與一般視窗，需要較長時間。
            $limit=if($name -in @('private','leftshift')){8}else{4}
            $settings=New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::FromMinutes($limit))
            Register-ScheduledTask -TaskName $taskName -Action $action -Principal $taskPrincipal -Settings $settings|Out-Null
            Start-ScheduledTask -TaskName $taskName
            $deadline=(Get-Date).AddSeconds($limit*60-60)
            do {
                Start-Sleep -Milliseconds 300
                $taskState=(Get-ScheduledTask -TaskName $taskName).State
                $info=Get-ScheduledTaskInfo -TaskName $taskName
                if($taskState -ne 'Running' -and $info.LastRunTime.Year -gt 2000){break}
            }while((Get-Date) -lt $deadline)
            if($taskState -eq 'Running'){throw "$name timed out; inspect its test window before rerunning"}
            if(-not(Test-Path $resultFile)){throw "$name did not produce a report (task result $($info.LastTaskResult))"}
            $result=Get-Content $resultFile -Raw -Encoding UTF8|ConvertFrom-Json
            $skipped=if($result.PSObject.Properties['skipped']){[string]$result.skipped}else{''}
            $report.suites+=@{name=$name;passed=[bool]$result.passed;skipped=$skipped;checks=@($result.checks).Count;task_result=$info.LastTaskResult}
            if(-not $result.passed -or $info.LastTaskResult -ne 0){throw "$name failed: $($result.error)"}
            if($skipped){Write-Host "$name skipped: $skipped"}else{Write-Host "$name passed ($(@($result.checks).Count) checks)"}
        } finally {StopTestProcesses}
    }
    $report.passed=$true
} catch {$report.error=$_.Exception.Message} finally {
    try {
        StopTestProcesses
        if($themeSaved){
            if($themeHadValue){Set-ItemProperty $themeKey AppsUseLightTheme -Type $themeKind -Value $themeOriginal}
            else {Remove-ItemProperty $themeKey AppsUseLightTheme -ErrorAction SilentlyContinue}
            $themeSettings=Get-ItemProperty $themeKey -ErrorAction SilentlyContinue
            $themeNow=if($themeSettings){$themeSettings.PSObject.Properties['AppsUseLightTheme']}else{$null}
            $report.theme_restored=if($themeHadValue){$themeNow.Value -eq $themeOriginal -and (Get-Item $themeKey).GetValueKind('AppsUseLightTheme') -eq $themeKind}else{$null -eq $themeNow}
            if(-not $themeKeyExisted -and (Test-Path $themeKey) -and (Get-Item $themeKey).ValueCount -eq 0 -and (Get-Item $themeKey).SubKeyCount -eq 0){Remove-Item $themeKey}
            if(-not $report.theme_restored){$report.passed=$false;$report.error+='; theme restoration failed'}
        }
        if($snapshotTaken){
            $current=RegisteredDLL
            if($current -and $current -ne $original){Regsvr $current $true}
            if($original -and (RegisteredDLL) -ne $original){Regsvr $original $false}
            if($hadReceipt){Copy-Item "$ResultDir\original-receipt.json" $receiptFile -Force}
            elseif(Test-Path $receiptFile){Remove-Item $receiptFile}
            $report.restored=(RegisteredDLL) -eq $original
            $report.restored32=(RegisteredDLL32) -eq $original32
            if(-not $fixedExisted -and (Test-Path -LiteralPath $fixedDir)){
                # 測試前沒有固定路徑：移除；仍被應用程式載入的檔案刪不掉時列出，不算還原失敗。
                Remove-Item -LiteralPath $fixedDir -Recurse -Force -ErrorAction SilentlyContinue
                $report.fixed_left=@(Get-ChildItem -LiteralPath $fixedDir -Recurse -File -ErrorAction SilentlyContinue|ForEach-Object FullName)
            }
            $report.receipt_restored=(HashOrAbsent $receiptFile) -eq $receiptBefore
            $report.user_preserved=(HashOrAbsent "$base\user.json") -eq $userBefore
            $report.legacy_data_preserved=(HashOrAbsent "$base\data\manifest.json") -eq $dataBefore
            if(-not($report.restored -and $report.restored32 -and $report.receipt_restored -and $report.user_preserved -and $report.legacy_data_preserved)){throw 'Restoration verification failed'}
        }
    } catch {$report.passed=$false;$report.error+='; cleanup: '+$_.Exception.Message}
    if($locked){$mutex.ReleaseMutex()}
    $mutex.Dispose()
    if(Test-Path -LiteralPath $ResultDir){$report|ConvertTo-Json -Depth 6|Set-Content "$ResultDir\acceptance-result.json" -Encoding UTF8}
}
$report|ConvertTo-Json -Depth 6
if(-not $report.passed){throw "Acceptance failed: $($report.error)"}
