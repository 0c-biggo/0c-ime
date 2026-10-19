# EXE 升級／解除安裝驗收；測試機必須已登入，並以同一帳號管理員執行。
param([Parameter(Mandatory=$true)][string]$Setup,[Parameter(Mandatory=$true)][string]$BaselineSetup,[string]$ResultDir)
$ErrorActionPreference='Stop'
$Setup=(Resolve-Path -LiteralPath $Setup).Path
$BaselineSetup=(Resolve-Path -LiteralPath $BaselineSetup).Path
if(-not $ResultDir){$ResultDir=Join-Path ([IO.Path]::GetTempPath()) ('0c-installer-test-'+[guid]::NewGuid().ToString('N'))}
if(Test-Path $ResultDir){throw 'ResultDir must be a new directory'}
$ProgressPreference='SilentlyContinue'
$result=$ResultDir
New-Item -ItemType Directory $result | Out-Null
$base="$env:LOCALAPPDATA\Zuin"
$reg='Registry::HKEY_CLASSES_ROOT\CLSID\{138C35D0-7F08-4CEF-BA90-C34C16301528}\InprocServer32'
$arp='HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\0c-Zhuyin'
if(Test-Path $arp){throw 'existing EXE install must be preserved; refusing test'}
if(Test-Path "$base\install-receipt.json"){throw 'existing receipt must be preserved; refusing test'}
$identity=[Security.Principal.WindowsIdentity]::GetCurrent()
$mutex=[Threading.Mutex]::new($false,('Global\0c-desktop-acceptance-'+$identity.User.Value))
$locked=$false;$snapshotTaken=$false
$initialUI=@(Get-Process 0c-ui -ErrorAction SilentlyContinue|Select-Object -ExpandProperty Id)
function HashOrAbsent([string]$p){if(Test-Path $p){return (Get-FileHash $p).Hash};return 'absent'}

$report=[ordered]@{passed=$false;schedule=$false;sha256=(Get-FileHash $Setup).Hash;upgrade=$false;ui_retired=$false;uninstall=$false;restored=$false;user_preserved=$false;receipt_restored=$false;error=''}
function Run([string]$exe,[string]$arguments){$p=Start-Process $exe -ArgumentList $arguments -PassThru;$handle=$p.Handle;$p.WaitForExit();if($p.ExitCode -ne 0){throw "installer process exited $($p.ExitCode)"}}
$task='0c-Installer-'+[guid]::NewGuid().ToString('N')
try {
 try {$locked=$mutex.WaitOne(0)} catch [Threading.AbandonedMutexException] {$locked=$true}
 if(-not $locked){throw 'Another desktop acceptance run is active'}
 if((Test-Path $arp) -or (Test-Path "$base\install-receipt.json")){throw 'Existing EXE installation or receipt must be preserved'}
 if(Get-Process 0c-server,0c-ui,zuin-server,zuin-ui -ErrorAction SilentlyContinue){throw 'Close daily engine/candidate UI before installer acceptance'}
 $original=if(Test-Path $reg){(Get-Item $reg).GetValue('')}else{''}
 # 升級會清除不再是回復目標的 zuin-* 程式；原註冊所在的程式先備份，結束時補回再恢復註冊。
 $originalDir=if($original){Split-Path $original}else{''}
 $savedPrograms=Join-Path $result 'original-programs'
 if($originalDir){
  New-Item -ItemType Directory $savedPrograms | Out-Null
  if($originalDir -eq $base){foreach($n in 'zuin_tsf.dll','zuin-server.exe','zuin-ui.exe','0c_tsf.dll','0c-server.exe','0c-ui.exe'){if(Test-Path "$base\$n"){Copy-Item "$base\$n" $savedPrograms}}}
  else{Copy-Item -LiteralPath $originalDir -Destination "$savedPrograms\directory" -Recurse}
 }
 $before=HashOrAbsent "$base\user.json"
 $snapshotTaken=$true
 Run $BaselineSetup '/S'
 $baselineVersion=(Get-ItemProperty $arp).DisplayVersion
 $report.baseline_version=$baselineVersion
 $baselineDirectory=(Get-Content "$base\install-receipt.json" -Raw|ConvertFrom-Json).directory
 # 升級要讓基準版的候選窗程式結束（#159 的全螢幕檢查在 0c-ui，舊的留著要重新登入才生效）：
 # 像 DLL 一樣在 console 工作階段以一般權限啟動基準版的 0c-ui，升級後確認它已結束。
 $baselineUIPath=[IO.Path]::GetFullPath((Join-Path (Join-Path $base $baselineDirectory) '0c-ui.exe'))
 $uiTask='0c-InstallerUI-'+[guid]::NewGuid().ToString('N')
 Register-ScheduledTask -TaskName $uiTask -Action (New-ScheduledTaskAction -Execute cmd.exe -Argument ('/c start "" "'+$baselineUIPath+'"')) -Principal (New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Limited) | Out-Null
 Start-ScheduledTask $uiTask
 $deadline=(Get-Date).AddSeconds(15)
 while(-not(Get-Process 0c-ui -ErrorAction SilentlyContinue|Where-Object {$_.Path -eq $baselineUIPath}) -and (Get-Date) -lt $deadline){Start-Sleep -Milliseconds 200}
 Unregister-ScheduledTask $uiTask -Confirm:$false
 $baselineUI=@(Get-Process 0c-ui -ErrorAction SilentlyContinue|Where-Object {$_.Path -eq $baselineUIPath})
 if($baselineUI.Count -ne 1){throw 'baseline 0c-ui did not start in the console session'}
 $args='-NoProfile -STA -ExecutionPolicy Bypass -File "'+$PSScriptRoot+'\setup-gui-test.ps1" -Setup "'+$Setup+'" -ResultDir "'+$result+'"'
 $action=New-ScheduledTaskAction -Execute powershell.exe -Argument $args
 $principal=New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Highest
 Register-ScheduledTask -TaskName $task -Action $action -Principal $principal | Out-Null
 Start-ScheduledTask $task
 $deadline=(Get-Date).AddSeconds(100)
 while(-not(Test-Path "$result\setup-gui.json") -and (Get-Date) -lt $deadline){Start-Sleep -Milliseconds 500}
 $gui=Get-Content "$result\setup-gui.json" -Raw | ConvertFrom-Json
 if(-not $gui.passed){throw $gui.error}
 $app=Get-ItemProperty $arp
 $receipt=Get-Content "$base\install-receipt.json" -Raw|ConvertFrom-Json
 if($app.DisplayVersion -ne $receipt.version -or $app.DisplayVersion -eq $baselineVersion){throw 'upgraded app entry mismatch'}
 $report.version=$app.DisplayVersion
 $installedDirectory=$receipt.directory
 $tasks=@(Get-ScheduledTask | Where-Object { $_.TaskName -like '0c-update-*' })
 if($tasks.Count -ne 1 -or $tasks[0].Principal.RunLevel -ne 'Limited'){throw 'upgrade did not create exactly one non-elevated update task'}
 if(-not(Test-Path "$base\bin\0c-update.exe")){throw 'upgrade did not install the stable updater'}
 $report.schedule=$true
 $report.upgrade=$true
 if(-not $baselineUI[0].WaitForExit(15000)){throw 'upgrade left the baseline 0c-ui running'}
 $report.ui_retired=$true
 Run ($app.UninstallString.Trim('"')) '/S'
 $deadline=(Get-Date).AddSeconds(15)
 while((Test-Path $arp) -and (Get-Date) -lt $deadline){Start-Sleep -Milliseconds 200}
 if((Test-Path $arp) -or (Test-Path $reg)){throw 'uninstall left registration'}
 if(@(Get-ScheduledTask | Where-Object { $_.TaskName -like '0c-update-*' }).Count -ne 0){throw 'uninstall left the update task'}
 if(Test-Path 'Registry::HKEY_CURRENT_USER\Software\Classes\zc-update'){throw 'uninstall left the notification protocol'}
 $report.uninstall=$true
 $report.passed=$true
} catch {$report.error=$_.Exception.Message} finally {
 try {
 if($snapshotTaken){
 if(Get-ScheduledTask $task -ErrorAction SilentlyContinue){Stop-ScheduledTask $task;Unregister-ScheduledTask $task -Confirm:$false}
 if(Test-Path $arp){$app=Get-ItemProperty $arp;Run ($app.UninstallString.Trim('"')) '/S';Start-Sleep -Seconds 2}
 if(Test-Path $reg){$current=(Get-Item $reg).GetValue('');if($current -ne $original){Run "$env:WINDIR\System32\regsvr32.exe" ('/u /s "'+$current+'"')}}
 if($originalDir -eq $base){foreach($saved in Get-ChildItem $savedPrograms -File){if((HashOrAbsent "$base\$($saved.Name)") -ne (HashOrAbsent $saved.FullName)){Copy-Item $saved.FullName "$base\$($saved.Name)" -Force}}}
 elseif($originalDir -and -not(Test-Path $original)){if(Test-Path $originalDir){Remove-Item $originalDir -Recurse -Force};Copy-Item "$savedPrograms\directory" $originalDir -Recurse}
 if($original){Run "$env:WINDIR\System32\regsvr32.exe" ('/s "'+$original+'"')}
 if(Test-Path "$base\install-receipt.json"){Remove-Item "$base\install-receipt.json"}
 $current=if(Test-Path $reg){(Get-Item $reg).GetValue('')}else{''}
 $report.restored=$current -eq $original
 $report.user_preserved=(HashOrAbsent "$base\user.json") -eq $before
 foreach($directory in @($baselineDirectory,$installedDirectory)){
  if($directory){
   foreach($ui in '0c-ui','zuin-ui'){
    $testUI=Join-Path (Join-Path $base $directory) "$ui.exe"
    Get-Process $ui -ErrorAction SilentlyContinue|Where-Object {$_.Id -notin $initialUI -and $_.Path -eq $testUI}|Stop-Process
   }
  }
 }
 $report.receipt_restored=-not(Test-Path "$base\install-receipt.json")
 if(-not $report.restored -or -not $report.user_preserved -or -not $report.receipt_restored){throw 'restoration verification failed'}
 }
 } catch {$report.passed=$false;$report.error+='; cleanup: '+$_.Exception.Message} finally {
 if($locked){$mutex.ReleaseMutex()};$mutex.Dispose()
 $report|ConvertTo-Json|Set-Content "$result\result.json" -Encoding UTF8
 }
}
$report|ConvertTo-Json
if(-not $report.passed){throw "Installer acceptance failed: $($report.error)"}
