# 原生 EDIT 欄位驗收，必須由隔離引擎與暫時註冊的外層驗收腳本啟動。
param([Parameter(Mandatory=$true)][string]$ResultDir, [Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();error='';dll=''}
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
 $box=New-Object Windows.Forms.TextBox
 $box.Multiline=$true; $box.Dock='Fill'; $form.Controls.Add($box)
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
 $report.dll=((Get-Process -Id $PID).Modules | Where-Object ModuleName -eq '0c_tsf.dll' | Select-Object -First 1).FileName
 if($report.dll -ne $ExpectedDLL){throw 'wrong native host DLL'}
 $report.passed=$true
} catch {$report.error=$_.Exception.ToString()} finally {
 if($form){[NativeProbe]::SetInputScope($box.Handle,0) | Out-Null;$form.Close()}
 if($savedLocalAppData){$env:LOCALAPPDATA=$savedLocalAppData}
 $report | ConvertTo-Json -Depth 4 | Set-Content "$ResultDir\native-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
