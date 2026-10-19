# Native Windows settings update journey. Run only on an uninstalled test machine.
# Uses a private profile, real signed-fixture HTTPS feed and production dev.48/dev.49 archives.
param([string]$Settings,[string]$Updater,[string]$Tools,[string]$ArchiveA,[string]$ArchiveB,[string]$ResultDir,[switch]$Worker)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$reg='Registry::HKEY_CLASSES_ROOT\CLSID\{138C35D0-7F08-4CEF-BA90-C34C16301528}\InprocServer32'
$repo=Split-Path (Split-Path (Split-Path $PSScriptRoot))
function Check($ok,$name){if(-not $ok){throw $name}; $script:checks+=@($name)}
function Capture([string]$arguments){
 $psi=New-Object Diagnostics.ProcessStartInfo $Updater,$arguments
 $psi.UseShellExecute=$false; $psi.CreateNoWindow=$true; $psi.RedirectStandardOutput=$true; $psi.RedirectStandardError=$true
 $psi.StandardOutputEncoding=[Text.Encoding]::UTF8; $psi.StandardErrorEncoding=[Text.Encoding]::UTF8
 $p=[Diagnostics.Process]::Start($psi); $out=$p.StandardOutput.ReadToEnd(); $err=$p.StandardError.ReadToEnd(); $p.WaitForExit()
 [IO.File]::AppendAllText("$ResultDir\commands.log","$arguments`n$out`n$err`n",[Text.Encoding]::UTF8)
 return @{code=$p.ExitCode;text=$out;error=$err}
}
function Status { $r=Capture 'status -json'; if($r.code -ne 0){throw $r.error}; return ($r.text|ConvertFrom-Json) }
function WaitFor([scriptblock]$test,[int]$seconds=90){$end=(Get-Date).AddSeconds($seconds);while((Get-Date)-lt $end){if(& $test){return $true};Start-Sleep -Milliseconds 250};return $false}
$checks=@(); $report=[ordered]@{passed=$false;restored=$false;checks=@();error='';mode='private profile; e2etest updater; admin GUI; real archives'}
$oldLocal=$env:LOCALAPPDATA; $oldRoaming=$env:APPDATA
$env:LOCALAPPDATA="$ResultDir\profile\Local"; $env:APPDATA="$ResultDir\profile\Roaming"
$env:ZC_NO_SCHEDULE='1'; $env:ZC_UPDATER=$Updater
$base="$env:LOCALAPPDATA\Zuin"
if($Worker){
 Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes
 $app=$null;$background=$null;$engine=$null
 function Window { $app.Refresh(); return [Windows.Automation.AutomationElement]::FromHandle($app.MainWindowHandle) }
 function FindButton($name){$w=Window; foreach($e in $w.FindAll([Windows.Automation.TreeScope]::Descendants,(New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::NameProperty,$name)))){if($e.Current.ControlType-eq[Windows.Automation.ControlType]::Button){return $e}}; return $null}
 # The WebView may refresh between finding a button and invoking its pattern.
 # Retry a stale UI Automation element; finish and download remain target-bound.
 function Click($name){
  Check (WaitFor {
   try {
    $button=FindButton $name
    if($null-eq$button-or-not$button.Current.IsEnabled){return $false}
    $button.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    return $true
   }catch{return $false}
  } 30) ("clicked enabled button "+$name)
 }
 function Shot($name){& "$PSScriptRoot\window-shot.ps1" -Out "$ResultDir\$name.png" -WaitSeconds 20}
 try{
  $sha=(Get-FileHash $ArchiveA).Hash.ToLower()
  $r=Capture ('apply -archive "'+$ArchiveA+'" -sha256 '+$sha)
  Check ($r.code-eq0) 'baseline dev.48 installed in private profile'
  $receipt=Get-Content "$base\install-receipt.json" -Raw|ConvertFrom-Json
  Check ($receipt.version-eq'0.1.0-dev.48') 'baseline version is dev.48'
  $directory=Join-Path $base $receipt.directory
  $engine=Start-Process "$directory\0c-server.exe" -ArgumentList ('-data "'+$directory+'\data" -user "'+$base+'\user.json"') -PassThru -WindowStyle Hidden
  Check (WaitFor { (Status).runtime.version-eq'0.1.0-dev.48' } 60) 'baseline engine dev.48 running'
  $state=@{schema=1;install_id='settings-update-native';mode='ask';stage='idle';schedule_removed=$true;feed='https://localhost:8447/feeds/update.json'}
  New-Item -ItemType Directory "$base\update" -Force|Out-Null
  # PowerShell writes a BOM; the updater's JSON parser requires plain UTF-8.
  [IO.File]::WriteAllText("$base\update\state.json",($state|ConvertTo-Json),(New-Object Text.UTF8Encoding($false)))
  $app=Start-Process $Settings -ArgumentList '--page update' -PassThru
  Check (WaitFor {$app.Refresh();$app.MainWindowHandle-ne[IntPtr]::Zero} 30) 'settings window opens'
  $null=(Window).FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition)
  Start-Sleep 3
  Click '檢查更新'
  Check (WaitFor {(Status).update.target.version-eq'0.1.0-dev.49'} 40) 'checking finds dev.49'
  Check (WaitFor {$null-ne(FindButton '立即下載')} 20) 'manual download action is available'
  Shot 'found'
  Click '立即下載'
  Check (WaitFor {$s=Status; $s.update.operation.interactive-eq$true-and$s.update.operation.received-gt0-and$s.update.operation.received-lt$s.update.operation.total} 30) 'GUI starts an immediate download without waiting for the scheduler'
  Shot 'manual-downloading'
  Check (WaitFor {(Status).update.stage-eq'prepared'} 120) 'immediate download verifies the archive'
  Check (WaitFor {$null-ne(FindButton '立即安裝')} 20) 'immediate download exposes installation action'
  Check (WaitFor {-not(Test-Path "$base\update\coordinator.lock")} 20) 'immediate download releases coordinator before next scenario'
  Remove-Item "$base\update\downloads" -Recurse -Force
  Remove-Item "$base\update\download-request.json" -Force
  [IO.File]::WriteAllText("$base\update\state.json",($state|ConvertTo-Json),(New-Object Text.UTF8Encoding($false)))
  Click '檢查更新'
  Check (WaitFor {(Status).update.stage-eq'found'} 40) 'next scenario starts with an undownloaded target'
  $background=Start-Process $Updater -ArgumentList 'auto' -PassThru -WindowStyle Hidden -RedirectStandardOutput "$ResultDir\background.out" -RedirectStandardError "$ResultDir\background.err"
  $null=$background.Handle # Retain the native handle so Windows PowerShell 5.1 reports ExitCode after exit.
  Check (WaitFor {$s=Status; $s.update.operation.received-gt 0 -and $s.update.operation.received-lt $s.update.operation.total} 40) 'real download bytes are visible before completion'
  $first=(Status).update.operation
  Check (WaitFor {$s=Status; $s.update.operation.received-gt $first.received} 15) 'reported received bytes increase'
  Check (WaitFor {$null-ne(FindButton '立即加速下載')} 20) 'background download can be accelerated from the GUI'
  $bars=(Window).FindAll([Windows.Automation.TreeScope]::Descendants,(New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::ControlTypeProperty,[Windows.Automation.ControlType]::ProgressBar)))
  Check ($bars.Count-gt0) 'native WebView2 exposes a progress bar'
  Shot 'downloading'
  Click '立即加速下載'
  Check (WaitFor {(Status).update.operation.interactive-eq$true} 15) 'existing download switches to interactive speed'
  Check (WaitFor {(Status).update.stage-eq'prepared'} 120) 'download completes and verifies before installation'
  $background.WaitForExit();Check ($background.ExitCode-eq0) 'background coordinator exits successfully'
  Check (WaitFor {$null-ne(FindButton '立即安裝')} 20) 'manual install action is available'
  Shot 'prepared'
  Click '立即安裝'
  Check (WaitFor {(Status).installed-eq'0.1.0-dev.49'} 120) 'GUI manual installation writes dev.49 receipt'
  Check (WaitFor {(Status).update.stage-eq'pending'} 30) 'Windows update waits for login activation'
  Start-Sleep 3
  $names=@((Window).FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition)|ForEach-Object{$_.Current.Name})
  Check (-not ($names-match'無法.*JSON|不是 JSON|發生未知')) 'installer diagnostics do not break the JSON contract'
  Click '總覽';Start-Sleep 2
  $s=Status
  Check ($s.runtime.version-eq'0.1.0-dev.48'-and$s.installed-eq'0.1.0-dev.49') 'installed and running versions remain distinct before relogin'
  $names=@((Window).FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition)|ForEach-Object{$_.Current.Name})
  Check (($names-match'已安裝版本').Count-gt0-and($names-match'登出再登入').Count-gt0) 'overview explains installed version and relogin'
  Check (-not ($names-match'有新版 0.1.0-dev.49').Count) 'installed pending version is not described as a new available download'
  Shot 'installed-pending'
  $report.passed=$true
 }catch{$report.error=$_.Exception.Message; if($app){try{$report.names=@((Window).FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition)|ForEach-Object{$_.Current.Name});Shot 'failure'}catch{}}}finally{
  Get-Process 0c-ui,0c-server -ErrorAction SilentlyContinue | Where-Object { $_.Path -like "$base\*" } | Stop-Process -Force -ErrorAction SilentlyContinue
  foreach($p in @($app,$background,$engine)){if($null-ne$p){try{$p.Refresh();if(-not$p.HasExited){Stop-Process -Id $p.Id -Force}}catch{}}}
  $report.checks=$checks;$report|ConvertTo-Json -Depth 8|Set-Content "$ResultDir\worker.json" -Encoding UTF8
 }
 exit $(if($report.passed){0}else{1})
}
# Controller refuses existing desktop installations and serializes with other acceptance tests.
if((Test-Path $reg)-or(Test-Path "$oldLocal\Zuin\install-receipt.json")-or(Get-Process 0c-server,0c-settings,0c-ui -ErrorAction SilentlyContinue)){throw 'requires an uninstalled test machine with no input-method processes'}
if(Test-Path $ResultDir){throw 'ResultDir must be new'}
New-Item -ItemType Directory "$ResultDir\feed\feeds","$ResultDir\feed\releases" -Force|Out-Null
$mutex=[Threading.Mutex]::new($false,('Global\0c-desktop-acceptance-'+[Security.Principal.WindowsIdentity]::GetCurrent().User.Value));$locked=$false;$feed=$null
$task='0c-SettingsUpdateTest-'+[guid]::NewGuid().ToString('N')
try{
 $locked=$mutex.WaitOne(0);if(-not$locked){throw 'another acceptance test is active'}
 Copy-Item $ArchiveB "$ResultDir\feed\releases\update.zip"
 & "$Tools\0c-release.exe" feed -key "$repo\testdata\e2e\release-test.key" -serial 1 -valid 48h -kind shell -out "$ResultDir\feed\feeds\update.json" -asset "$ResultDir\feed\releases\update.zip=https://localhost:8447/releases/update.zip"
 if($LASTEXITCODE-ne0){throw 'fixture signing failed'}
 $feed=Start-Process "$Tools\0c-e2e-feed.exe" -ArgumentList ('-addr 127.0.0.1:8447 -root "'+$ResultDir+'\feed" -cert "'+$repo+'\testdata\e2e\feed-server.pem" -key "'+$repo+'\testdata\e2e\feed-server.key"') -PassThru -WindowStyle Hidden -RedirectStandardError "$ResultDir\feed.log"
 Start-Sleep 2
 & curl.exe -fsS --ssl-no-revoke --cacert "$repo\testdata\e2e\test-ca.pem" 'https://localhost:8447/_control/fault?kind=slow&match=update.zip&delay=15ms'|Out-Null
 if($LASTEXITCODE-ne0){throw 'fixture control failed'}
 $arguments='-NoProfile -STA -ExecutionPolicy Bypass -File "'+$PSCommandPath+'" -Worker -Settings "'+$Settings+'" -Updater "'+$Updater+'" -Tools "'+$Tools+'" -ArchiveA "'+$ArchiveA+'" -ArchiveB "'+$ArchiveB+'" -ResultDir "'+$ResultDir+'"'
 Register-ScheduledTask -TaskName $task -Action (New-ScheduledTaskAction -Execute powershell.exe -Argument $arguments) -Principal (New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Highest) -Force|Out-Null
 Start-ScheduledTask $task
 Check (WaitFor {Test-Path "$ResultDir\worker.json"} 360) 'interactive worker returns'
 $workerResult=Get-Content "$ResultDir\worker.json" -Raw -Encoding UTF8|ConvertFrom-Json
 Check $workerResult.passed ('GUI journey: '+$workerResult.error)
 & curl.exe -fsS --ssl-no-revoke --cacert "$repo\testdata\e2e\test-ca.pem" 'https://localhost:8447/_control/hits' > "$ResultDir\hits.json"
 $hits=Get-Content "$ResultDir\hits.json" -Raw|ConvertFrom-Json
 Check ($hits.'/releases/update.zip'-eq2) 'immediate and background downloads each use one request; installation reuses verified cache'
 $report.passed=$true;$report.checks=$checks+$workerResult.checks
}catch{$report.error=$_.Exception.Message}finally{
 Unregister-ScheduledTask $task -Confirm:$false -ErrorAction SilentlyContinue
 if($feed){Stop-Process -Id $feed.Id -Force -ErrorAction SilentlyContinue}
 if(Test-Path "$base\install-receipt.json"){$r=Capture 'uninstall';if($r.code-ne0){$report.error+=' cleanup: '+$r.error}}
 $report.restored=(-not(Test-Path $reg)-and-not(Test-Path "$oldLocal\Zuin\install-receipt.json"))
 $env:LOCALAPPDATA=$oldLocal;$env:APPDATA=$oldRoaming
 if($locked){$mutex.ReleaseMutex()};$mutex.Dispose()
 $report|ConvertTo-Json -Depth 8|Set-Content "$ResultDir\result.json" -Encoding UTF8
}
if(-not$report.passed-or-not$report.restored){Write-Error ($report.error+' restored='+$report.restored);exit 1}
exit 0
