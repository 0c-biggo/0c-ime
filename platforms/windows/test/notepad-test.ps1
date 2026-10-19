param([Parameter(Mandatory=$true)][string]$ResultDir,[Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();error='';dll=''}
$own=$null
$originalLocalAppData=$env:LOCALAPPDATA
try {
 if(Get-Process notepad -ErrorAction SilentlyContinue){throw 'Existing Notepad is open; refusing to use user tabs'}
 Add-Type -AssemblyName System.Windows.Forms,UIAutomationClient,UIAutomationTypes
 Add-Type @'
using System; using System.Runtime.InteropServices;
public static class NoteProbe {
 [DllImport("user32.dll")] public static extern IntPtr GetKeyboardLayout(uint thread);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hwnd, out uint pid);
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern IntPtr FindWindow(string cls,string title);
 [DllImport("user32.dll")] public static extern uint GetDpiForWindow(IntPtr hwnd);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hwnd);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hwnd,uint msg,IntPtr wp,IntPtr lp);
 [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr hwnd,uint msg,IntPtr wp,IntPtr lp);
 public static void Shift(bool down){keybd_event(0xA1,0x36,down?0u:2u,UIntPtr.Zero);}
 [DllImport("user32.dll")] static extern void keybd_event(byte key,byte scan,uint flags,UIntPtr extra);
 public static void Type(string s){foreach(char c in s){byte k=(byte)char.ToUpperInvariant(c);keybd_event(k,0,0,UIntPtr.Zero);System.Threading.Thread.Sleep(30);keybd_event(k,0,2,UIntPtr.Zero);}}
}
'@
 $env:LOCALAPPDATA=$ResultDir
 New-Item -ItemType Directory -Force "$ResultDir\Zuin" | Out-Null
 New-Item -ItemType File -Force "$ResultDir\Zuin\tsf-debug" | Out-Null
 $stem='0c-notepad-'+[guid]::NewGuid().ToString('N')
 $file=Join-Path $ResultDir ($stem+'.txt')
 [IO.File]::WriteAllText($file,'',[Text.UTF8Encoding]::new($true))
 $launcher=Start-Process notepad.exe -ArgumentList ('"'+$file+'"') -PassThru
 $deadline=(Get-Date).AddSeconds(20)
 do {
  Start-Sleep -Milliseconds 200
  $own=Get-Process notepad -ErrorAction SilentlyContinue | Where-Object MainWindowTitle -like "*$stem*" | Select-Object -First 1
 }while(-not $own -and (Get-Date) -lt $deadline)
 if(-not $own){throw 'own Notepad window missing'}
 [NoteProbe]::SetForegroundWindow($own.MainWindowHandle)|Out-Null
 function Pump([int]$ms){$w=[Diagnostics.Stopwatch]::StartNew();while($w.ElapsedMilliseconds -lt $ms){[Windows.Forms.Application]::DoEvents();Start-Sleep -Milliseconds 10}}
 function Send([string]$keys){
  if([NoteProbe]::GetForegroundWindow() -ne $own.MainWindowHandle){throw 'Notepad test window lost focus'}
  [Windows.Forms.SendKeys]::SendWait($keys);Pump 1000
 }

 function Latin([string]$text){if([NoteProbe]::GetForegroundWindow() -ne $own.MainWindowHandle){throw 'Notepad test window lost focus'};foreach($c in $text.ToCharArray()){[NoteProbe]::Type([string]$c);Pump 100};Pump 500}
 function Shift([bool]$down){if([NoteProbe]::GetForegroundWindow() -ne $own.MainWindowHandle){throw 'Notepad test window lost focus'};[NoteProbe]::Shift($down);$script:shiftDown=$down;Pump 150}
 function SaveExpect([string]$text,[string]$name){
  Send '^s'
  $deadline=(Get-Date).AddSeconds(3)
  while([IO.File]::ReadAllText($file) -ne $text -and (Get-Date) -lt $deadline){Pump 100}
  if([IO.File]::ReadAllText($file) -ne $text){throw "$name differs (test file was not saved as expected)"};$report.checks+=$name
 }

 function OwnEvents { return @(Get-Content "$ResultDir\Zuin\tsf.log" | Where-Object {$_ -match " pid=$($own.Id) "}) }

 Pump 700
 $root=[Windows.Automation.AutomationElement]::FromHandle($own.MainWindowHandle)
 $condition=New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::IsTextPatternAvailableProperty,$true)
 $editors=$root.FindAll([Windows.Automation.TreeScope]::Descendants,$condition)
 $editor=$editors | Where-Object {-not $_.Current.IsOffscreen -and $_.Current.ClassName -eq "RichEditD2DPT"} | Select-Object -First 1
 if(-not $editor){throw 'Notepad editor not found'}
 Pump 600
 $own.Refresh()
 $report.pid=$own.Id
 $report.path=$own.Path
 $report.dll=($own.Modules | Where-Object ModuleName -eq '0c_tsf.dll' | Select-Object -First 1).FileName
 if($report.dll -ne $ExpectedDLL){throw "Notepad has not activated the expected input method"}
 $report.editor=$editor.Current.ClassName
 $report.app_version=$own.MainModule.FileVersionInfo.FileVersion
 [uint32]$ownerPid=0
 $tid=[NoteProbe]::GetWindowThreadProcessId($own.MainWindowHandle,[ref]$ownerPid)
 $report.layout=[NoteProbe]::GetKeyboardLayout($tid).ToInt64().ToString('X')
 if([NoteProbe]::GetForegroundWindow() -ne $own.MainWindowHandle){throw 'Notepad not focused'}
 Latin 'su3cl3'
 Send '{ENTER}';SaveExpect '你好' 'notepad_nihao_commit_saved'
 Send '^a';Send '{BACKSPACE}'
 Latin 'su3';Send '{ESC}';SaveExpect '' 'notepad_cancel_saved'
 Latin 'su3';Send '{DOWN}'
 $deadline=(Get-Date).AddSeconds(3)
 do {Pump 100; $candidate=[NoteProbe]::FindWindow('ZuinCandidateWindow','Zuin')}while(($candidate -eq [IntPtr]::Zero -or -not [NoteProbe]::IsWindowVisible($candidate)) -and (Get-Date) -lt $deadline)
 if($candidate -eq [IntPtr]::Zero -or -not [NoteProbe]::IsWindowVisible($candidate)){throw 'Notepad candidate window missing'}
 $dpi=[NoteProbe]::GetDpiForWindow($candidate)
 $x=[int](20*$dpi/96);$y=[int](70*$dpi/96) # 第一列是小字標題（dev.54 起）：pad 8＋標題 20＋一列 28 後是第二個候選
 [NoteProbe]::PostMessage($candidate,0x202,[IntPtr]::Zero,[IntPtr](($y -shl 16) -bor $x))|Out-Null;Pump 500
 Send '{ENTER}';SaveExpect '妳' 'notepad_mouse_candidate_saved'
 Send '^a';Send '{BACKSPACE}'
 Shift $true;Shift $false
 Latin 'abc';SaveExpect 'abc' 'notepad_right_shift_english'
 Shift $true;Shift $false
 Send '^a';Send '{BACKSPACE}';Latin 'cl3';Send '{ENTER}';SaveExpect '好' 'notepad_right_shift_zhuyin'
 Send '^a';Send '{BACKSPACE}'
 $editorHwnd=[IntPtr]$editor.Current.NativeWindowHandle
 [NoteProbe]::SendMessage($editorHwnd,0xCC,[IntPtr]42,[IntPtr]::Zero)|Out-Null;Pump 200
 if([NoteProbe]::SendMessage($editorHwnd,0xD2,[IntPtr]::Zero,[IntPtr]::Zero).ToInt64() -ne 42){throw 'test password mask was not set'}
 if(-not (OwnEvents | Where-Object {$_ -match 'debug snapshot'})){throw 'isolated debug logging was not active'}
 $before=(OwnEvents).Count
 Latin 'su3cl3'
 $newEvents=@(OwnEvents | Select-Object -Skip $before)
 if($newEvents -match 'vk=|snapshot|open: session='){throw 'masked RichEdit input reached key logging or engine'}
 $report.checks+='notepad_mask_no_key_logging_or_session'
 [NoteProbe]::SendMessage($editorHwnd,0xCC,[IntPtr]::Zero,[IntPtr]::Zero)|Out-Null;Pump 200
 SaveExpect 'su3cl3' 'notepad_mask_passthrough'
 Send '^a';Send '{BACKSPACE}';Latin 'cl3';Send '{ENTER}';SaveExpect '好' 'notepad_after_mask'
 Send '^a';Send '{BACKSPACE}'
 # 每鍵保留 30ms 的按下時間，以真實鍵盤事件測試連續高速輸入（約 33 鍵／秒）。
 if([NoteProbe]::GetForegroundWindow() -ne $own.MainWindowHandle){throw 'Notepad test window lost focus'}
 [NoteProbe]::Type('su3cl3su3cl3');Pump 1000
 Send '{ENTER}';SaveExpect '你好你好' 'notepad_fast_typing_commit'
 $report.dll=($own.Modules | Where-Object ModuleName -eq '0c_tsf.dll' | Select-Object -First 1).FileName
 if($report.dll -ne $ExpectedDLL){throw 'unexpected Notepad DLL'}
 $report.checks+='notepad_new_dll_loaded'
 $report.passed=$true
} catch {$report.error=$_.Exception.ToString()} finally {
 if($script:shiftDown){[NoteProbe]::Shift($false)}
 if($editorHwnd){[NoteProbe]::SendMessage($editorHwnd,0xCC,[IntPtr]::Zero,[IntPtr]::Zero)|Out-Null}
 if($own){
  $own.CloseMainWindow()|Out-Null
 }
 $env:LOCALAPPDATA=$originalLocalAppData
 $report|ConvertTo-Json -Depth 5|Set-Content "$ResultDir\notepad-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
