# 上架前 Defender 掃描（#22）：更新特徵碼後掃描發行檔（ZIP、setup.exe），並把 ZIP 解開逐檔再掃一次；
# 不隔離、不刪除，掃到任何威脅即失敗。以管理員執行；結果寫入 ResultDir\defender-result.json。
# 判斷結果只看報告的 passed：Defender 處置威脅時會終止命令列帶有該檔路徑的程序，被終止的程序結束碼是 0。
# 因此 -Path 請傳發行檔所在的資料夾（腳本自己列出其中的 *.zip、*.exe），不要把檔案路徑放在命令列上。
param([Parameter(Mandatory=$true)][string[]]$Path,[string]$ResultDir)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$identity=[Security.Principal.WindowsIdentity]::GetCurrent()
if(-not ([Security.Principal.WindowsPrincipal]::new($identity)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)){throw 'Run as administrator'}
if(-not $ResultDir){$ResultDir=Join-Path ([IO.Path]::GetTempPath()) ('0c-defender-'+[guid]::NewGuid().ToString('N'))}
if(Test-Path $ResultDir){throw 'ResultDir must be a new directory'}
New-Item -ItemType Directory $ResultDir | Out-Null
$resultFile=Join-Path $ResultDir 'defender-result.json'
# 第一個動作就寫一份失敗報告：之後任何時間被終止，留下的都是失敗。
@{passed=$false;error='Scan did not finish; the process may have been terminated by Defender remediation. Check the Defender event log.'}|ConvertTo-Json|Set-Content -LiteralPath $resultFile -Encoding utf8
# powershell -File 會把陣列參數傳成一個以逗號連接的字串。路徑在 try 內解析：檔案可能已被即時防護隔離，也要留下報告。
$targets=@($Path|ForEach-Object{$_ -split ','}|Where-Object{$_})
$mpcmd=Join-Path $env:ProgramFiles 'Windows Defender\MpCmdRun.exe'
if(-not (Test-Path $mpcmd)){throw "MpCmdRun not found: $mpcmd"}

# 特徵碼過期的掃描沒有意義：先更新，失敗就停止。
& $mpcmd -SignatureUpdate | Out-File -LiteralPath (Join-Path $ResultDir 'signature-update.log') -Encoding utf8
if($LASTEXITCODE -ne 0){throw "Signature update failed ($LASTEXITCODE)"}
$status=Get-MpComputerStatus
$pref=Get-MpPreference
if(-not $status.AntivirusEnabled -or -not $status.RealTimeProtectionEnabled){throw 'Defender antivirus and real-time protection must be enabled'}
if($pref.MAPSReporting -eq 0){throw 'Cloud-delivered protection (MAPS) must be enabled; !ml detections come from the cloud'}

$started=Get-Date
$report=[ordered]@{passed=$false;started=$started.ToString('o')
 defender=[ordered]@{engine=$status.AMEngineVersion;signature=$status.AntivirusSignatureVersion;signature_updated=$status.AntivirusSignatureLastUpdated.ToString('o')
  product=$status.AMProductVersion;maps_reporting=[int]$pref.MAPSReporting;submit_samples=[int]$pref.SubmitSamplesConsent}
 targets=@();extracted=$null;threats=@();error=''}
function Scan([string]$item){
 # ScanType 3＝自訂路徑；DisableRemediation：只回報，不隔離或刪除，方便保留證據與送分析。
 $out=& $mpcmd -Scan -ScanType 3 -File $item -DisableRemediation 2>&1|Out-String
 $code=$LASTEXITCODE
 if($code -notin 0,2){throw "MpCmdRun scan failed ($code) on $item`n$out"}
 return [ordered]@{exit_code=$code;threat=($code -eq 2)}
}
$extractDir=Join-Path $env:TEMP ('0c'+[guid]::NewGuid().ToString('N').Substring(0,6))
function Save{$report|ConvertTo-Json -Depth 6|Set-Content -LiteralPath $resultFile -Encoding utf8}
# Defender 處置威脅時會一併終止讀過該檔的程序（含本腳本與呼叫者）。先寫一份「中斷」報告，被終止時留下的就是失敗。
$report.error='Scan did not finish; the process may have been terminated by Defender remediation. Check the Defender event log.'
Save
try {
 $targets=@($targets|ForEach-Object{
  if(-not (Test-Path -LiteralPath $_)){throw "Missing file (possibly quarantined by real-time protection): $_"}
  $item=Get-Item -LiteralPath $_
  if($item.PSIsContainer){Get-ChildItem -LiteralPath $item.FullName -File|Where-Object{$_.Extension -in '.zip','.exe'}|ForEach-Object{$_.FullName}}
  else{$item.FullName}})
 if(-not $targets){throw 'No release files (*.zip, *.exe) to scan'}
 foreach($t in $targets){
  # 本程序先不讀檔：由 MpCmdRun（Defender 自己的程序）先掃，乾淨才計算雜湊。
  $entry=[ordered]@{path=$t;size=(Get-Item -LiteralPath $t).Length}
  $report.targets+=$entry
  $scan=Scan $t
  $entry.exit_code=$scan.exit_code
  $entry.threat=$scan.threat
  if(-not $scan.threat){$entry.sha256=(Get-FileHash -LiteralPath $t).Hash.ToLowerInvariant()}
  Save
 }
 # 發行檔本身已被判定時不再解壓。送分析用的雜湊見打包時產生的 .sha256（本程序讀取被判定的檔案會被終止）。
 if(@($report.targets|Where-Object{$_.threat}).Count){throw 'Threat found in a release file'}
 # 壓縮檔內的 PE 另外逐一掃描並記錄簽章狀態：使用者實際執行的是解開後的檔案。
 # 以子程序 tar.exe 解壓：即時防護攔下解出的檔案時只終止子程序；tar 也不受 260 字元路徑限制。
 $zips=@($targets|Where-Object{$_ -like '*.zip'})
 if($zips){
  $files=@()
  $tar=Join-Path $env:SystemRoot 'System32\tar.exe'
  for($i=0;$i -lt $zips.Count;$i++){
   $dest=Join-Path $extractDir $i
   New-Item -ItemType Directory $dest -Force|Out-Null
   $out=& $tar -xf $zips[$i] -C $dest 2>&1|Out-String
   if($LASTEXITCODE -ne 0){throw "Extraction failed ($LASTEXITCODE) on $($zips[$i]); a file may have been blocked by real-time protection`n$out"}
  }
  $scan=Scan $extractDir
  $report.extracted=[ordered]@{exit_code=$scan.exit_code;threat=$scan.threat}
  Save
  if($scan.threat){throw 'Threat found in extracted files'}
  # -Include 搭配 -LiteralPath 不會生效，改以副檔名篩選。
  foreach($pe in Get-ChildItem -LiteralPath $extractDir -Recurse -File|Where-Object{$_.Extension -in '.exe','.dll','.pyd'}){
   $sig=Get-AuthenticodeSignature -LiteralPath $pe.FullName
   $files+=[ordered]@{path=$pe.FullName.Substring($extractDir.Length+1);status=[string]$sig.Status
    signer=if($sig.SignerCertificate){$sig.SignerCertificate.Subject}else{''}}
  }
  $ours=@('0c_tsf.dll','0c-ui.exe','0c-server.exe','0c-update.exe')
  $report.extracted.pe_files=$files.Count
  $report.extracted.signed=@($files|Where-Object{$_.status -eq 'Valid'}).Count
  $report.extracted.ours=@($files|Where-Object{$ours -contains (Split-Path $_.path -Leaf)})
  $report.extracted.unsigned_third_party=@($files|Where-Object{$_.status -ne 'Valid' -and $ours -notcontains (Split-Path $_.path -Leaf)}|ForEach-Object{$_.path})
 }
 $report.error=''
 $report.passed=$true
} catch {
 $report.error="$($_.Exception.Message) (line $($_.InvocationInfo.ScriptLineNumber))"
} finally {
 # 掃描期間（含解壓時的即時防護）偵測到的項目都算失敗，不只看 MpCmdRun 的結束碼。讀事件紀錄而非
 # Get-MpThreatDetection：同一威脅以前偵測過時，InitialDetectionTime 仍是第一次的時間，會漏掉這次。
 # 1006／1116：發現惡意程式；1015：可疑行為。
 Start-Sleep -Seconds 5   # 偵測事件非同步寫入，立即查詢會漏掉剛發生的偵測
 $events=@(Get-WinEvent -FilterHashtable @{LogName='Microsoft-Windows-Windows Defender/Operational';Id=1006,1015,1116;StartTime=$started} -ErrorAction SilentlyContinue)
 $report.threats=@($events|ForEach-Object{
  $m=$_.Message
  [ordered]@{event_id=$_.Id;time=$_.TimeCreated.ToString('o')
   threat=if($m -match '(?m)^\s*Name:\s*(.+?)\s*$'){$Matches[1]}else{''}
   path=if($m -match '(?m)^\s*Path:\s*(.+?)\s*$'){$Matches[1]}else{''}}})
 $hit=@($report.targets|Where-Object{$_.threat}).Count -gt 0 -or ($report.extracted -and $report.extracted.threat) -or $report.threats.Count -gt 0
 if($hit){$report.passed=$false;$report.error=(($report.error,'Defender reported a threat; do not publish. Keep the files and submit them for analysis (docs/releasing.md).')|Where-Object{$_}) -join ' / '}
 if(Test-Path $extractDir){Remove-Item -LiteralPath $extractDir -Recurse -Force -ErrorAction SilentlyContinue}
 Save
}
$report|ConvertTo-Json -Depth 6
if(-not $report.passed){throw "Defender scan failed: $($report.error)"}
