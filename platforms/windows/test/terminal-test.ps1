# Windows Terminal 驗收：專用視窗內只讀取文字，不執行輸入內容。
param([Parameter(Mandatory=$true)][string]$ResultDir, [Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();error='';terminal='';dll=''}
$own=$null
$originalLocalAppData=$env:LOCALAPPDATA
try {
    if(Get-Process WindowsTerminal -ErrorAction SilentlyContinue){throw 'Existing Terminal is open; refusing to use user tabs'}
    Add-Type -AssemblyName System.Windows.Forms
    Add-Type @'
using System; using System.Runtime.InteropServices; using System.Threading;
public static class TerminalProbe {
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr hwnd);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern IntPtr FindWindow(string cls,string title);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hwnd);
 [DllImport("user32.dll")] public static extern uint GetDpiForWindow(IntPtr hwnd);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hwnd,uint msg,IntPtr wp,IntPtr lp);
 [DllImport("user32.dll")] static extern void keybd_event(byte key,byte scan,uint flags,UIntPtr extra);
 public static void Key(byte key,byte scan=0){keybd_event(key,scan,0,UIntPtr.Zero);Thread.Sleep(30);keybd_event(key,scan,2,UIntPtr.Zero);}
 public static void Type(string text){foreach(char c in text)Key((byte)char.ToUpperInvariant(c));}
}
'@
    $result=(Resolve-Path -LiteralPath $ResultDir).Path
    $wt=Join-Path $originalLocalAppData 'Microsoft\WindowsApps\wt.exe'
    if(-not(Test-Path $wt)){throw 'Windows Terminal is not installed'}
    $env:LOCALAPPDATA=$result
    New-Item -ItemType Directory -Force "$result\Zuin" | Out-Null
    # 每次測試獨立紀錄，不對其他程序開啟鍵碼紀錄。
    New-Item -ItemType File "$result\Zuin\tsf-debug" | Out-Null
    @'
param([string]$ResultDir)
$ErrorActionPreference='Stop'
[IO.File]::WriteAllText("$ResultDir\reader.pid",[string]$PID)
try {
    for($i=0;$i -lt 6;$i++) {
        [Console]::Write("0c test {0}> ",$i)
        [IO.File]::WriteAllText("$ResultDir\ready-$i",'ready')
        $line=[Console]::ReadLine()
        [IO.File]::WriteAllText("$ResultDir\line-$i.tmp",$line,[Text.UTF8Encoding]::new($true))
        [IO.File]::Move("$ResultDir\line-$i.tmp","$ResultDir\line-$i.txt")
    }
    # 控制程序驗完後才關閉，避免 DLL／視窗在驗收完成前消失。
    while(-not(Test-Path "$ResultDir\done")){Start-Sleep -Milliseconds 100}
} catch {
    [IO.File]::WriteAllText("$ResultDir\reader-error.txt",$_.Exception.ToString())
    exit 1
}
'@ | Set-Content "$result\reader.ps1" -Encoding UTF8
    $title='0c-terminal-'+[guid]::NewGuid().ToString('N')
    $arguments='-w new new-tab --title '+$title+' --suppressApplicationTitle -d "'+$result+'" powershell.exe -NoProfile -ExecutionPolicy Bypass -File "'+$result+'\reader.ps1" -ResultDir "'+$result+'"'
    Start-Process $wt -ArgumentList $arguments | Out-Null
    $deadline=(Get-Date).AddSeconds(25)
    do {
        Start-Sleep -Milliseconds 100
        $own=Get-Process WindowsTerminal -ErrorAction SilentlyContinue | Where-Object MainWindowTitle -like "*$title*" | Select-Object -First 1
    }while(-not $own -and (Get-Date) -lt $deadline)
    if(-not $own){throw 'own Terminal window missing'}
    $report.terminal=$own.MainModule.FileVersionInfo.ProductVersion
    $report.pid=$own.Id
    [TerminalProbe]::SetForegroundWindow($own.MainWindowHandle) | Out-Null
    function Pump([int]$ms){$w=[Diagnostics.Stopwatch]::StartNew();while($w.ElapsedMilliseconds -lt $ms){[Windows.Forms.Application]::DoEvents();Start-Sleep -Milliseconds 10}}
    function FocusGuard {if([TerminalProbe]::GetForegroundWindow() -ne $own.MainWindowHandle){throw 'Terminal test window lost focus'}}
    function Key([byte]$vk){FocusGuard;[TerminalProbe]::Key($vk);Pump 500}
    function SendText([string]$text){FocusGuard;[TerminalProbe]::Type($text);Pump 700}
    function Toggle {FocusGuard;[TerminalProbe]::Key(0xA1,0x36);Pump 500}
    function Ready([int]$index){
        $deadline=(Get-Date).AddSeconds(10)
        while(-not(Test-Path "$result\ready-$index") -and (Get-Date) -lt $deadline){Pump 100}
        if(-not(Test-Path "$result\ready-$index")){throw "reader not ready for line $index"}
        if(Test-Path "$result\line-$index.txt"){throw 'input reached reader before test'}
    }
    function ExpectLine([int]$index,[string]$expected,[string]$name){
        Key 13
        $path="$result\line-$index.txt";$deadline=(Get-Date).AddSeconds(5)
        while(-not(Test-Path $path) -and (Get-Date) -lt $deadline){Pump 100}
        if(-not(Test-Path $path)){throw "$name was not received by console reader"}
        if([IO.File]::ReadAllText($path) -cne $expected){throw "$name differs"}
        $report.checks+=$name
    }
    Ready 0;Pump 700
    SendText 'su3cl3';Key 13
    if(Test-Path "$result\line-0.txt"){throw 'composition Enter also submitted the console line'}
    $report.checks+='terminal_commit_does_not_submit_line'
    ExpectLine 0 '你好' 'terminal_nihao_commit'
    Ready 1;SendText 'su3';Key 27;ExpectLine 1 '' 'terminal_cancel'
    Ready 2;SendText 'su3';Key 40
    $candidate=[TerminalProbe]::FindWindow('ZuinCandidateWindow','Zuin')
    if($candidate -eq [IntPtr]::Zero -or -not[TerminalProbe]::IsWindowVisible($candidate)){throw 'Terminal candidate window missing'}
    $dpi=[TerminalProbe]::GetDpiForWindow($candidate)
    $x=[int](20*$dpi/96);$y=[int](70*$dpi/96) # 第一列是小字標題（dev.54 起）：pad 8＋標題 20＋一列 28 後是第二個候選
    [TerminalProbe]::PostMessage($candidate,0x202,[IntPtr]::Zero,[IntPtr](($y -shl 16) -bor $x))|Out-Null;Pump 700
    Key 13;ExpectLine 2 '妳' 'terminal_mouse_candidate'
    Ready 3;Toggle;SendText 'abc';ExpectLine 3 'abc' 'terminal_right_shift_english'
    Ready 4;Toggle;SendText 'cl3';Toggle;SendText 'abc';Key 13
    ExpectLine 4 '好abc' 'terminal_mixed_composition'
    Ready 5;Toggle;SendText 'su3cl3su3cl3';Key 13;ExpectLine 5 '你好你好' 'terminal_fast_typing'
    $own.Refresh()
    $report.dll=($own.Modules | Where-Object ModuleName -eq '0c_tsf.dll' | Select-Object -First 1).FileName
    if($report.dll -ne $ExpectedDLL){throw 'Terminal loaded an unexpected DLL'}
    $report.checks+='terminal_new_dll_loaded'
    $report.passed=$true
} catch {$report.error=$_.Exception.ToString()} finally {
    if($own){
        [IO.File]::WriteAllText("$ResultDir\done",'done')
        $own.CloseMainWindow()|Out-Null
        if(-not $own.WaitForExit(3000)){Stop-Process -Id $own.Id -ErrorAction SilentlyContinue}
    }
    $env:LOCALAPPDATA=$originalLocalAppData
    $report|ConvertTo-Json -Depth 5|Set-Content "$ResultDir\terminal-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
