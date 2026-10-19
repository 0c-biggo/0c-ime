# Windows #33 分階段證據工具。只能在專用測試帳號執行；不送鍵、不修改 UAC、不替代工作列操作。
# record：更新前留下版本／權限／工作階段；cancel：UAC 取消或不同帳號後核對未變；
# core：核對標準使用者 OS 排程的核心更新；relogin：核對真實重登後的外殼／引擎／唯一 writer。
param(
    [Parameter(Mandatory=$true)][ValidateSet('record','cancel','pending','core','relogin')][string]$Phase,
    [Parameter(Mandatory=$true)][string]$ResultDir,
    [Parameter(Mandatory=$true)][string]$ExpectedUser,
    [string]$ExpectedVersion,
    [string]$ExpectedDLL,
    [string]$ExpectedBundle,
    [string]$TypingEvidence,
    [string]$ExpectedText,
    [switch]$RequireStandard,
    [switch]$RequireLearningUnchanged
)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
$identity=[Security.Principal.WindowsIdentity]::GetCurrent()
if ($identity.Name -ne $ExpectedUser) { throw 'Wrong test account; refusing to inspect another user' }
$base=Join-Path $env:LOCALAPPDATA 'Zuin'
$taskName='0c-update-'+$identity.User.Value.ToLower()
$reg='Registry::HKEY_CLASSES_ROOT\CLSID\{138C35D0-7F08-4CEF-BA90-C34C16301528}\InprocServer32'
function HashOrAbsent([string]$Path) {
    if (Test-Path -LiteralPath $Path) { return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLower() }
    return 'absent'
}
function ReadExact($Stream,[int]$Length) {
    $buf=New-Object byte[] $Length
    $offset=0
    while ($offset -lt $Length) {
        $n=$Stream.Read($buf,$offset,$Length-$offset)
        if ($n -le 0) { throw 'Truncated engine response' }
        $offset+=$n
    }
    return ,$buf
}
function Health {
    $c=New-Object IO.Pipes.NamedPipeClientStream('.',('0c-'+$identity.User.Value.ToLower()),[IO.Pipes.PipeDirection]::InOut)
    try {
        $c.Connect(2000)
        $c.ReadMode=[IO.Pipes.PipeTransmissionMode]::Byte
        $bytes=[Text.Encoding]::UTF8.GetBytes('{"op":"health"}')
        $c.Write([BitConverter]::GetBytes([uint32]$bytes.Length),0,4)
        $c.Write($bytes,0,$bytes.Length); $c.Flush()
        $length=[BitConverter]::ToUInt32((ReadExact $c 4),0)
        if ($length -gt 1048576) { throw 'Oversized engine response' }
        return ([Text.Encoding]::UTF8.GetString((ReadExact $c $length)) | ConvertFrom-Json).info
    } finally { $c.Dispose() }
}
function Snapshot {
    $accountSID=$identity.User.Value
    $member=@(Get-LocalGroupMember (Get-LocalGroup -SID 'S-1-5-32-544').Name | Where-Object { $_.SID.Value -eq $accountSID }).Count -gt 0
    $elevated=(New-Object Security.Principal.WindowsPrincipal($identity)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    if ($RequireStandard -and ($member -or $elevated)) { throw 'Standard-user evidence requires no Administrators membership and no elevated token' }
    $h=Health
    $writers=@(Get-CimInstance Win32_Process -Filter "Name='0c-server.exe' OR Name='zuin-server.exe'" | ForEach-Object {
        $owner=Invoke-CimMethod -InputObject $_ -MethodName GetOwnerSid
        if ($owner.ReturnValue -eq 0 -and $owner.Sid -eq $accountSID) {
            @{ pid=$_.ProcessId; executable=$_.ExecutablePath; executable_sha256=(HashOrAbsent $_.ExecutablePath); command_line=$_.CommandLine; session=$_.SessionId }
        }
    })
    $dll=if (Test-Path $reg) { (Get-Item $reg).GetValue('') } else { '' }
    $loaded=@(Get-Process | Where-Object { $_.SessionId -eq [Diagnostics.Process]::GetCurrentProcess().SessionId } | ForEach-Object {
        $hostProcess=$_
        try { foreach($module in $hostProcess.Modules) {
            if ($module.ModuleName -in '0c_tsf.dll','zuin_tsf.dll') {
                @{ pid=$hostProcess.Id; host=$hostProcess.ProcessName; dll=$module.FileName; sha256=(HashOrAbsent $module.FileName) }
            }
        } } catch { }
    })
    $task=Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
    $taskInfo=if ($task) { $task | Get-ScheduledTaskInfo } else { $null }
    $state=Get-Content "$base\update\state.json" -Raw -Encoding UTF8 | ConvertFrom-Json
    $receipt=Get-Content "$base\install-receipt.json" -Raw -Encoding UTF8 | ConvertFrom-Json
    $session=[Diagnostics.Process]::GetCurrentProcess().SessionId
    $explorer=Get-Process explorer -ErrorAction SilentlyContinue | Where-Object { $_.SessionId -eq $session } | Select-Object -First 1
    [ordered]@{
        utc=[DateTime]::UtcNow.ToString('o'); user=$identity.Name; sid=$accountSID; administrator_member=$member; elevated=$elevated
        session=$session; explorer_pid=if($explorer){$explorer.Id}else{$null}; explorer_start=if($explorer){$explorer.StartTime.ToUniversalTime().ToString('o')}else{$null}
        health=$h; writers=$writers; registration=$dll; loaded=$loaded; receipt=$receipt; state=$state
        receipt_sha256=(HashOrAbsent "$base\install-receipt.json"); pointer_sha256=(HashOrAbsent "$base\engine.json")
        store_sha256=(HashOrAbsent "$base\user.json"); settings_sha256=(HashOrAbsent "$base\settings.json")
        task=if($task){@{name=$task.TaskName;user=$task.Principal.UserId;level=$task.Principal.RunLevel.ToString();last_run=$taskInfo.LastRunTime.ToUniversalTime().ToString('o');last_result=$taskInfo.LastTaskResult}}else{$null}
    }
}
if ($Phase -eq 'record') {
    if (Test-Path -LiteralPath $ResultDir) { throw 'ResultDir must be new; previous evidence must be kept' }
    New-Item -ItemType Directory $ResultDir | Out-Null
    $s=Snapshot
    $s | ConvertTo-Json -Depth 12 | Set-Content "$ResultDir\baseline.json" -Encoding UTF8
    Write-Output 'Baseline recorded; perform actual desktop/UAC operations before verification.'
    exit 0
}
$before=Get-Content "$ResultDir\baseline.json" -Raw -Encoding UTF8 | ConvertFrom-Json
if ($before.sid -ne $identity.User.Value) { throw 'Baseline belongs to another user' }
$out=Join-Path $ResultDir ($Phase+'-'+(Get-Date -Format 'yyyyMMdd-HHmmss-fff')+'.json')
$result=[ordered]@{ phase=$Phase; passed=$false; scope='runtime assertions only; actual taskbar/UAC actions need separate evidence'; checks=@(); snapshot=$null; error='' }
function Check([bool]$Ok,[string]$Name) {
    $result.checks+=@{ name=$Name;passed=$Ok }
    if (-not $Ok) { throw "Check failed: $Name" }
}
try {
    $s=Snapshot; $result.snapshot=$s
    Check ($s.writers.Count -eq 1 -and $s.writers[0].pid -eq [int]$s.health.pid) 'one_writer_for_test_user'
    if ($Phase -eq 'cancel') {
        foreach($field in 'registration','receipt_sha256','pointer_sha256','settings_sha256') { Check ($s[$field] -eq $before.$field) ('unchanged_'+$field) }
        Check ($s.health.version -eq $before.health.version) 'old_engine_still_running'
        Check ($s.state.stage -eq 'prepared' -and $s.state.blocked.reason -eq 'needs_consent') 'pending_consent_preserved'
        Check ($s.state.target.version -eq $before.state.target.version) 'pending_target_preserved'
    } elseif ($Phase -eq 'pending') {
        if (-not $ExpectedVersion) { throw 'ExpectedVersion required' }
        Check ($s.session -eq $before.session -and $s.explorer_pid -eq $before.explorer_pid) 'same_desktop_login'
        Check ($s.receipt.version -eq $ExpectedVersion) 'shell_installed'
        Check ($s.state.stage -eq 'pending' -and $s.state.target.version -eq $ExpectedVersion -and $s.state.blocked.reason -eq 'needs_relogin') 'relogin_pending_preserved'
        Check (@($s.state.history | Where-Object { $_.kind -eq 'active' -and $_.version -eq $ExpectedVersion }).Count -eq 0) 'no_premature_activation'
    } else {
        if (-not $ExpectedVersion) { throw 'ExpectedVersion required' }
        Check ($s.health.version -eq $ExpectedVersion) 'expected_runtime_version'
        Check (@($s.state.history | Where-Object { $_.kind -eq 'active' -and $_.version -eq $ExpectedVersion }).Count -gt 0 -and ($s.state.stage -in 'active','idle' -or ($s.state.target -and $s.state.target.version -ne $ExpectedVersion))) 'activation_confirmed_in_history'
        if ($Phase -eq 'core') {
            Check (-not $s.administrator_member -and -not $s.elevated) 'true_standard_user'
            Check ($s.health.version -ne $before.health.version -and $s.health.pid -ne $before.health.pid) 'new_engine_version_and_writer'
            Check ($s.task -and $s.task.level -eq 'Limited' -and $s.task.last_result -eq 0) 'user_task_completed_without_elevation'
            Check ([DateTime]$s.task.last_run -gt [DateTime]$before.utc) 'os_task_ran_after_baseline'
            Check (@($s.state.history | Where-Object { $_.kind -eq 'found' -and $_.version -eq $ExpectedVersion }).Count -gt 0) 'target_discovered'
            Check ($s.registration -eq $before.registration) 'core_keeps_shell_registration'
        } else {
            if (-not $ExpectedDLL) { throw 'ExpectedDLL required' }
            Check ($s.explorer_start -and $before.explorer_start -and [DateTime]$s.explorer_start -gt [DateTime]$before.explorer_start) 'new_explorer_after_baseline'
            $events=@(Get-WinEvent -FilterHashtable @{LogName='Microsoft-Windows-TerminalServices-LocalSessionManager/Operational';ID=21,23;StartTime=[DateTime]$before.utc} | ForEach-Object {
                $xml=[xml]$_.ToXml()
                $data=$xml.Event.UserData.EventXML
                if ($data.User -eq $ExpectedUser) { @{id=$_.Id;session=[int]$data.SessionID;utc=$_.TimeCreated.ToUniversalTime().ToString('o')} }
            })
            $result.logon_events=$events
            $logoff=$events | Where-Object { $_.id -eq 23 -and $_.session -eq $before.session } | Sort-Object utc | Select-Object -First 1
            $logon=$events | Where-Object { $_.id -eq 21 -and $_.session -eq $s.session -and $logoff -and [DateTime]$_.utc -gt [DateTime]$logoff.utc } | Sort-Object utc | Select-Object -First 1
            Check ($logoff -and $logon) 'os_records_real_signout_then_logon'
            Check ($s.receipt.version -eq $ExpectedVersion) 'shell_receipt_matches'
            Check ($s.registration -eq $ExpectedDLL) 'registration_matches_expected_dll'
            Check ($s.loaded.Count -gt 0 -and @($s.loaded | Where-Object { $_.dll -ne $ExpectedDLL }).Count -eq 0) 'all_observed_hosts_load_expected_dll'
        }
    }
    if ($RequireLearningUnchanged) {
        Check ($before.store_sha256 -ne 'absent' -and $s.store_sha256 -eq $before.store_sha256) 'persisted_learning_unchanged'
    }
    if ($Phase -eq 'cancel') {
        Check ($s.health.executable_sha256 -eq $before.health.executable_sha256) 'same_running_executable'
        Check ($s.health.data_sha256 -eq $before.health.data_sha256) 'same_runtime_dictionary'
    } elseif ($Phase -ne 'pending') {
        $engineDir=Split-Path $s.writers[0].executable
        if ($Phase -eq 'core') {
            if (-not $ExpectedBundle) { throw 'ExpectedBundle required for core; core-only directories do not contain release.json' }
            & (Join-Path $ExpectedBundle '0c-update.exe') verify -bundle $ExpectedBundle | Out-Null
            Check ($LASTEXITCODE -eq 0) 'reference_bundle_verified'
            $releasePath=Join-Path $ExpectedBundle 'release.json'
        } else { $releasePath=Join-Path $engineDir 'release.json' }
        $release=Get-Content $releasePath -Raw -Encoding UTF8 | ConvertFrom-Json
        Check ($release.version -eq $s.health.version) 'runtime_release_matches_version'
        Check ($s.writers[0].executable_sha256 -eq $release.files.'0c-server.exe') 'running_executable_matches_bundle'
        Check ($s.health.data_sha256 -eq (HashOrAbsent (Join-Path $engineDir 'data\manifest.json'))) 'runtime_dictionary_matches_manifest'
        foreach($file in $release.files.PSObject.Properties | Where-Object { $_.Name -like 'data/*' -or $_.Name -like 'neural/*' }) {
            Check ((HashOrAbsent (Join-Path $engineDir $file.Name.Replace('/','\'))) -eq $file.Value) ('matched_'+$file.Name)
        }
    }
    if ($TypingEvidence) {
        if (-not $ExpectedText) { throw 'ExpectedText required with TypingEvidence' }
        $typed=Get-Content $TypingEvidence -Raw -Encoding UTF8 | ConvertFrom-Json
        $focus=if ($null -ne $typed.foreground) { $typed.foreground -eq $true -and $typed.input_complete -eq $true -and $typed.passed -eq $true } else { $typed.focus -eq $true }
        Check ($focus -and -not $typed.error -and $typed.text -ceq $ExpectedText -and $typed.session -eq $s.session) 'actual_input_in_test_session'
        Check ((Get-Item $TypingEvidence).LastWriteTimeUtc -gt ([DateTimeOffset]::Parse($before.utc).UtcDateTime)) 'typing_evidence_after_baseline'
    } elseif ($Phase -notin 'cancel','pending') { throw 'TypingEvidence required; health alone is not desktop input' }
    $result.passed=$true
} catch { $result.error=$_.Exception.Message } finally {
    $result | ConvertTo-Json -Depth 14 | Set-Content $out -Encoding UTF8
}
Write-Output $out
if (-not $result.passed) { throw $result.error }
