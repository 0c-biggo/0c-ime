# 原生 EDIT 欄位驗收，必須由隔離引擎與暫時註冊的外層驗收腳本啟動。
param([Parameter(Mandatory=$true)][string]$ResultDir, [Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();failures=@();error='';dll=''}
$form=$null
try {
 Add-Type -AssemblyName System.Windows.Forms
 Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class NativeProbe {
 [ComImport, Guid("71C6E74C-0F28-11D8-A82A-00065B84435C"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
 interface IProfiles { [PreserveSig] int ActivateProfile(uint type, ushort language, ref Guid clsid, ref Guid profile, IntPtr hkl, uint flags); }
 [DllImport("msctf.dll")] public static extern int SetInputScope(IntPtr hwnd, int scope);
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [StructLayout(LayoutKind.Sequential)] struct MOUSEINPUT { public int dx, dy; public uint mouseData, dwFlags, time; public IntPtr extra; }
 [StructLayout(LayoutKind.Sequential)] struct KEYBDINPUT { public ushort vk, scan; public uint flags, time; public IntPtr extra; }
 [StructLayout(LayoutKind.Explicit)] struct INPUTUNION { [FieldOffset(0)] public MOUSEINPUT mi; [FieldOffset(0)] public KEYBDINPUT ki; }
 [StructLayout(LayoutKind.Sequential)] struct INPUT { public uint type; public INPUTUNION u; }
 [DllImport("user32.dll", SetLastError=true)] static extern uint SendInput(uint n, INPUT[] inputs, int size);
 // 只按一下 Ctrl（按下、放開）：SendKeys 沒有「單按修飾鍵」的寫法，所以用 SendInput（#66）。
 public static void LoneCtrl() {
  foreach(uint flags in new uint[]{0, 2}) {
   INPUT[] i=new INPUT[1]; i[0].type=1; i[0].u.ki.vk=0x11; i[0].u.ki.flags=flags;
   if(SendInput(1,i,Marshal.SizeOf(typeof(INPUT)))!=1) throw new System.ComponentModel.Win32Exception();
   System.Threading.Thread.Sleep(60);
  }
 }
 public static void Activate() {
  object obj=Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("33C53A50-F456-4884-B049-85FD643ECFED")));
  try { Guid c=new Guid("138C35D0-7F08-4CEF-BA90-C34C16301528"), p=new Guid("EF62F743-9855-4C94-A658-1F0F98B7B840");
   Marshal.ThrowExceptionForHR(((IProfiles)obj).ActivateProfile(1,0x404,ref c,ref p,IntPtr.Zero,0x10000000));
  } finally { Marshal.FinalReleaseComObject(obj); }
 }
}
'@
 $savedLocalAppData=$env:LOCALAPPDATA
 $env:LOCALAPPDATA=$ResultDir
 New-Item -ItemType Directory -Force "$ResultDir\Zuin" | Out-Null
 New-Item -ItemType File -Force "$ResultDir\Zuin\tsf-debug" | Out-Null
 $form=New-Object Windows.Forms.Form
 $form.Text='0c native EDIT acceptance'; $form.Width=600; $form.Height=200; $form.TopMost=$true
 $button=New-Object Windows.Forms.Button
 $button.Text='OK'; $button.Dock='Bottom'; $form.Controls.Add($button)
 $box=New-Object Windows.Forms.TextBox
 $box.Multiline=$true; $box.Dock='Fill'; $form.Controls.Add($box); $box.BringToFront()
 # #66：表單先看到按鍵，記下 Ctrl+S、F5 的觸發次數與當下文字框的內容（重排的快捷鍵必須排在送出的字之後，而且只觸發一次）。
 $hits=@{S=@();F5=@()}
 $form.KeyPreview=$true
 $form.Add_KeyDown({param($sender,$e)
  if($e.Control -and -not $e.Alt -and $e.KeyCode -eq 'S'){$hits.S+=,$box.Text}
  elseif(-not $e.Control -and -not $e.Alt -and $e.KeyCode -eq 'F5'){$hits.F5+=,$box.Text}
 }.GetNewClosure())
 $form.Show();$form.Activate();$box.Focus() | Out-Null
 function Pump([int]$ms) { $w=[Diagnostics.Stopwatch]::StartNew();while($w.ElapsedMilliseconds -lt $ms){[Windows.Forms.Application]::DoEvents();Start-Sleep -Milliseconds 10} }
 function Send([string]$keys) { if([NativeProbe]::GetForegroundWindow() -ne $form.Handle){throw 'test window lost focus'};[Windows.Forms.SendKeys]::SendWait($keys);Pump 350 }
 function Expect([string]$text,[string]$name){if($box.Text -ne $text){throw "$name expected [$text] got [$($box.Text)]"};$report.checks+=$name}
 Pump 500;[NativeProbe]::Activate();Pump 500
 Send 'su3cl3{ENTER}';Expect '你好' 'native_edit_nihao'
 $box.Clear();Pump 200
 # IS_PRIVATE（61）：Chrome 無痕／訪客、Firefox 私密視窗的欄位。照常組字，但提交後外殼不讀前文（#68、#70）。
 [Runtime.InteropServices.Marshal]::ThrowExceptionForHR([NativeProbe]::SetInputScope($box.Handle,61))
 Pump 150
 $logBefore = (Get-Content "$ResultDir\Zuin\tsf.log" -Raw).Length
 Send 'su3cl3{ENTER}';Expect '你好' 'scope_61_private_composes'
 Send 'su3{ENTER}';Expect '你好你' 'scope_61_private_continues'
 $newLog = (Get-Content "$ResultDir\Zuin\tsf.log" -Raw).Substring($logBefore)
 if ($newLog -match 'document context confirmed') { throw "private field read preceding text" }
 $report.checks += 'scope_61_private_no_document_context'
 $box.Clear();Pump 150
 foreach($scope in @(63,64,65,66,31)) {
  [Runtime.InteropServices.Marshal]::ThrowExceptionForHR([NativeProbe]::SetInputScope($box.Handle,$scope))
  Pump 150
  $logBefore = (Get-Content "$ResultDir\Zuin\tsf.log" -Raw).Length
  Send 'su3cl3';Expect 'su3cl3' "scope_${scope}_passthrough"
  $newLog = (Get-Content "$ResultDir\Zuin\tsf.log" -Raw).Substring($logBefore)
  if ($newLog -match 'vk=|snapshot|open: session=') { throw "sensitive field reached key logging or engine" }
  $report.checks += "scope_${scope}_no_key_logging_or_session"
  $box.Clear();Pump 150
 }
 [Runtime.InteropServices.Marshal]::ThrowExceptionForHR([NativeProbe]::SetInputScope($box.Handle,0));Pump 200
 Send 'su3{ENTER}';Expect '你' 'native_after_private'
 # 組字中焦點移到沒有文字的按鈕：組字照舊送出到原欄位（#189 只在宿主把組字搬到新的焦點文件時才繼續組字）。
 # 回到原欄位按 Esc：組字還在的話會被取消，已送出就不受影響。
 $box.Clear();Pump 150
 Send 'su3'
 $button.Focus() | Out-Null;Pump 500
 $box.Focus() | Out-Null;Pump 300
 Send '{ESC}';Expect '你' 'native_focus_change_commits_composition'
 # #66：IMM32 相容層宿主（原生 EDIT）組字中的快捷鍵與單按 Ctrl。每項只記錄結果，全部跑完才判定，一項紅不遮住其他項。
 function Check([bool]$ok,[string]$name,[string]$detail){if($ok){$report.checks+=$name}else{$report.failures+="$name : $detail"}}
 function Reset-Hits{$hits.S=@();$hits.F5=@();Send '{ESC}';$box.Clear();Pump 200}
 # 組字中按 Ctrl+S／F5：處理函式只觸發一次，當下文字框已有剛打的字（重排在送出的字之後；不重排會缺字，重排卻沒吃鍵會觸發兩次）
 foreach($case in @(@{name='ctrl_s';keys='^s';list='S'},@{name='f5';keys='{F5}';list='F5'})) {
  Reset-Hits
  Send 'su3';Send $case.keys
  $got=@($hits[$case.list])
  Check ($got.Count -eq 1) "native_$($case.name)_fires_once" "count=$($got.Count)"
  Check ($got.Count -ge 1 -and $got[0] -eq '你') "native_$($case.name)_text_before_key" "text at handler=[$($got -join '|')]"
  Check ($box.Text -eq '你') "native_$($case.name)_final_text" "final=[$($box.Text)]"
 }
 # 單按 Ctrl 不送出組字：Esc 取消後文字框不該多出字
 Reset-Hits
 Send 'su3'
 if([NativeProbe]::GetForegroundWindow() -ne $form.Handle){throw 'test window lost focus'}
 [NativeProbe]::LoneCtrl();Pump 350
 Send '{ESC}'
 Check ($box.Text -eq '') 'native_lone_ctrl_keeps_composition' "text after Esc=[$($box.Text)]"
 # Ctrl＋, 進組字區：Esc 取消後沒有多出字
 Reset-Hits
 Send 'su3';Send '^,';Send '{ESC}'
 Check ($box.Text -eq '') 'native_ctrl_comma_stays_in_composition' "text after Esc=[$($box.Text)]"
 # Ctrl＋, 後接著打注音：全形逗號落在兩段字之間，證明進了同一段組字而不是被忽略
 Reset-Hits
 Send 'su3';Send '^,';Send 'cl3';Send '{ENTER}'
 Check ($box.Text -eq '你，好') 'native_ctrl_comma_between_words' "text=[$($box.Text)]"
 $box.Clear();Pump 150
 $report.dll=((Get-Process -Id $PID).Modules | Where-Object ModuleName -eq '0c_tsf.dll' | Select-Object -First 1).FileName
 if($report.dll -ne $ExpectedDLL){throw 'wrong native host DLL'}
 if($report.failures.Count -gt 0){throw ('#66 檢查未通過:'+[Environment]::NewLine+($report.failures -join [Environment]::NewLine))}
 $report.passed=$true
} catch {$report.error=$_.Exception.ToString()} finally {
 if($form){[NativeProbe]::SetInputScope($box.Handle,0) | Out-Null;$form.Close()}
 if($savedLocalAppData){$env:LOCALAPPDATA=$savedLocalAppData}
 $report | ConvertTo-Json -Depth 4 | Set-Content "$ResultDir\native-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
