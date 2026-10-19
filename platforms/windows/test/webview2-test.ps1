# WebView2 宿主（新版 Teams、新版 Outlook 等，#144）：測試行程的 WinForms 視窗裡放一個 WebView2。
# 最上層視窗屬於測試行程，有鍵盤焦點的子視窗與 TSF 在 msedgewebview2.exe——和 Teams 一樣跨行程。
# 核對組字、提交、取消、候選窗真的看得到且在輸入框附近、鍵盤與滑鼠選字；不碰日常瀏覽器與 WebView2 資料。
# WebView2 SDK（Core 與 Loader）從 NuGet 下載固定版本並核對 SHA-256，放在 build\windows\webview2-sdk。
# 離線、下載失敗或沒有 WebView2 執行階段時不算失敗：報告標 skipped 與原因，驗收總表照列（雜湊不符仍是失敗）。
param([Parameter(Mandatory=$true)][string]$ResultDir,[Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;skipped='';checks=@();error='';dll='';runtime='';focus_process='';candidate=''}
$form=$null
$sdkVersion='1.0.4129.50'
$sdkHash='D3934F482D484B89FB4825DF720C710664E1143A1E90F7B3A60794EF33F473D2'
$log=Join-Path $env:LOCALAPPDATA 'Zuin\tsf.log'
$logStart=if(Test-Path $log){(Get-Item $log).Length}else{0}
$focusPid=0
try {
 $root=(Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
 $sdk=Join-Path $root "build\windows\webview2-sdk\$sdkVersion"
 $needed=@('Microsoft.Web.WebView2.Core.dll','WebView2Loader.dll')
 if(@($needed|Where-Object {-not(Test-Path (Join-Path $sdk $_))}).Count -gt 0){
  New-Item -ItemType Directory -Force $sdk|Out-Null
  $package=Join-Path $sdk 'package.nupkg'
  [Net.ServicePointManager]::SecurityProtocol=[Net.SecurityProtocolType]::Tls12
  try {
   Invoke-WebRequest -UseBasicParsing "https://api.nuget.org/v3-flatcontainer/microsoft.web.webview2/$sdkVersion/microsoft.web.webview2.$sdkVersion.nupkg" -OutFile $package
  } catch {
   Remove-Item $package -ErrorAction SilentlyContinue
   $report.skipped="WebView2 SDK $sdkVersion could not be downloaded from NuGet: $($_.Exception.Message)"
   $report.passed=$true
   return
  }
  if((Get-FileHash $package -Algorithm SHA256).Hash -ne $sdkHash){Remove-Item $package;throw 'WebView2 SDK hash mismatch'}
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $zip=[IO.Compression.ZipFile]::OpenRead($package)
  try {
   foreach($entry in $zip.Entries){
    if($entry.FullName -in @('lib/net462/Microsoft.Web.WebView2.Core.dll','runtimes/win-x64/native/WebView2Loader.dll')){
     [IO.Compression.ZipFileExtensions]::ExtractToFile($entry,(Join-Path $sdk $entry.Name),$true)
    }
   }
  } finally {$zip.Dispose()}
 }
 $env:PATH="$sdk;$env:PATH"
 Add-Type -AssemblyName System.Windows.Forms,System.Drawing
 Add-Type -Path (Join-Path $sdk 'Microsoft.Web.WebView2.Core.dll')
 Add-Type @'
using System; using System.Text; using System.Runtime.InteropServices;
public static class WebViewProbe {
 [ComImport, Guid("71C6E74C-0F28-11D8-A82A-00065B84435C"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
 interface IProfiles { [PreserveSig] int ActivateProfile(uint type, ushort language, ref Guid clsid, ref Guid profile, IntPtr hkl, uint flags); }
 // 對工作階段切到靈犀注音（與 explorer／search 測試相同）。
 public static int UseZuin(){
  object o = Activator.CreateInstance(System.Type.GetTypeFromCLSID(new Guid("33C53A50-F456-4884-B049-85FD643ECFED")));
  try { Guid c=new Guid("138C35D0-7F08-4CEF-BA90-C34C16301528"), p=new Guid("EF62F743-9855-4C94-A658-1F0F98B7B840");
   return ((IProfiles)o).ActivateProfile(1,0x0404,ref c,ref p,IntPtr.Zero,0x20000000); } finally { Marshal.FinalReleaseComObject(o); }
 }
 [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
 [StructLayout(LayoutKind.Sequential)] public struct GUITHREADINFO { public int cbSize, flags; public IntPtr hwndActive, hwndFocus, hwndCapture, hwndMenuOwner, hwndMoveSize, hwndCaret; public RECT rcCaret; }
 public delegate bool EnumProc(IntPtr h, IntPtr l);
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("user32.dll")] public static extern bool BringWindowToTop(IntPtr h);
 [DllImport("user32.dll")] public static extern bool AttachThreadInput(uint a, uint b, bool attach);
 [DllImport("kernel32.dll")] public static extern uint GetCurrentThreadId();
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
 [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc p, IntPtr l);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetClassName(IntPtr h, StringBuilder s, int n);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
 [DllImport("user32.dll")] public static extern bool GetGUIThreadInfo(uint tid, ref GUITHREADINFO info);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint msg, IntPtr w, IntPtr l);
 [DllImport("user32.dll")] public static extern uint GetDpiForWindow(IntPtr h);
 [DllImport("user32.dll")] public static extern void keybd_event(byte k, byte s, uint f, UIntPtr e);
 public static string Class(IntPtr h){ var s=new StringBuilder(256); GetClassName(h,s,256); return s.ToString(); }
 public static uint Pid(IntPtr h){ uint p; GetWindowThreadProcessId(h, out p); return p; }
 // 看得到的候選窗：0c-ui 的 ZuinCandidateWindow，或宿主行程內的 0cCandidateInProcess。
 public static IntPtr Candidate(){
  IntPtr found=IntPtr.Zero;
  EnumWindows((h,l)=>{ string c=Class(h); RECT r; if(IsWindowVisible(h) && (c=="ZuinCandidateWindow" || c=="0cCandidateInProcess") && GetWindowRect(h, out r) && r.Right>r.Left+20){ found=h; return false; } return true; }, IntPtr.Zero);
  return found;
 }
 public static IntPtr Focus(IntPtr top){
  uint p; uint tid=GetWindowThreadProcessId(top, out p);
  var info=new GUITHREADINFO(); info.cbSize=Marshal.SizeOf(typeof(GUITHREADINFO));
  return GetGUIThreadInfo(tid, ref info) ? info.hwndFocus : IntPtr.Zero;
 }
 public static void Activate(IntPtr h){
  uint p; uint fg=GetWindowThreadProcessId(GetForegroundWindow(), out p); uint me=GetCurrentThreadId();
  if(fg!=me) AttachThreadInput(me, fg, true);
  BringWindowToTop(h); SetForegroundWindow(h);
  if(fg!=me) AttachThreadInput(me, fg, false);
 }
 public static void Type(string text){ foreach(char c in text){ byte k=(byte)char.ToUpperInvariant(c); keybd_event(k,0,0,UIntPtr.Zero); keybd_event(k,0,2,UIntPtr.Zero); System.Threading.Thread.Sleep(40);} }
 public static void Key(byte vk){ keybd_event(vk,0,0,UIntPtr.Zero); keybd_event(vk,0,2,UIntPtr.Zero); }
}
'@
 function Pump([int]$ms){$w=[Diagnostics.Stopwatch]::StartNew();while($w.ElapsedMilliseconds -lt $ms){[Windows.Forms.Application]::DoEvents();Start-Sleep -Milliseconds 10}}
 function Await($task,[string]$what){$w=[Diagnostics.Stopwatch]::StartNew();while(-not $task.IsCompleted -and $w.ElapsedMilliseconds -lt 20000){[Windows.Forms.Application]::DoEvents();Start-Sleep -Milliseconds 10};if(-not $task.IsCompleted){throw "$what timed out"};if($task.IsFaulted){throw "$what failed: $($task.Exception.InnerException.Message)"};return $task}
 function JS([string]$expression){$t=Await $script:core.ExecuteScriptAsync($expression) 'script';return ($t.Result|ConvertFrom-Json)}
 function Ensure-Front {if([WebViewProbe]::GetForegroundWindow() -ne $script:form.Handle){throw 'WebView2 test window lost focus'}}
 function SendText([string]$text){Ensure-Front;[WebViewProbe]::Type($text);Pump 600}
 function Key([byte]$vk){Ensure-Front;[WebViewProbe]::Key($vk);Pump 400}
 function Expect([string]$expected,[string]$name){$actual=JS 'document.getElementById("text").value';if($actual -ne $expected){throw "$name expected [$expected], got [$actual]"};$report.checks+=$name}
 function Clear-Text {JS 'document.getElementById("text").value="";document.getElementById("text").focus();true'|Out-Null;Pump 300}
 # 候選窗出現（版面晚到時 OnLayoutChange 補送，最多等 2 秒）。
 function Wait-Candidate {$w=[Diagnostics.Stopwatch]::StartNew();do{$h=[WebViewProbe]::Candidate();if($h -ne [IntPtr]::Zero){return $h};Pump 50}while($w.ElapsedMilliseconds -lt 2000);return [IntPtr]::Zero}
 function Shot([string]$name){
  $f=New-Object WebViewProbe+RECT;[WebViewProbe]::GetWindowRect($script:form.Handle,[ref]$f)|Out-Null
  $left=$f.Left;$top=$f.Top;$right=$f.Right;$bottom=$f.Bottom
  $c=[WebViewProbe]::Candidate()
  if($c -ne [IntPtr]::Zero){$r=New-Object WebViewProbe+RECT;[WebViewProbe]::GetWindowRect($c,[ref]$r)|Out-Null;$left=[Math]::Min($left,$r.Left);$top=[Math]::Min($top,$r.Top);$right=[Math]::Max($right,$r.Right);$bottom=[Math]::Max($bottom,$r.Bottom)}
  $screen=[Windows.Forms.SystemInformation]::VirtualScreen
  $left=[Math]::Max($left,$screen.Left);$top=[Math]::Max($top,$screen.Top);$right=[Math]::Min($right,$screen.Right);$bottom=[Math]::Min($bottom,$screen.Bottom)
  $bmp=New-Object Drawing.Bitmap ($right-$left),($bottom-$top)
  $g=[Drawing.Graphics]::FromImage($bmp);$g.CopyFromScreen($left,$top,0,0,$bmp.Size);$g.Dispose()
  $bmp.Save((Join-Path $ResultDir $name),[Drawing.Imaging.ImageFormat]::Png);$bmp.Dispose()
 }

 try {$report.runtime=[Microsoft.Web.WebView2.Core.CoreWebView2Environment]::GetAvailableBrowserVersionString()}
 catch [Microsoft.Web.WebView2.Core.WebView2RuntimeNotFoundException] {$report.skipped='WebView2 runtime is not installed';$report.passed=$true;return}
 $hr=[WebViewProbe]::UseZuin()
 if($hr -ne 0){throw ('ActivateProfile failed 0x{0:X8}' -f $hr)}
 # 直接用 Core API 建 controller（WinForms 控制項的 await 在 PowerShell 裡回不到 UI 執行緒）；
 # controller 是表單的子視窗，和宿主程式嵌入 WebView2 的方式相同。
 $script:form=New-Object Windows.Forms.Form
 $form.Text='0c WebView2 acceptance';$form.Width=900;$form.Height=520;$form.StartPosition='CenterScreen';$form.TopMost=$true
 $form.Show();Pump 300
 $environment=(Await ([Microsoft.Web.WebView2.Core.CoreWebView2Environment]::CreateAsync($null,(Join-Path $ResultDir 'webview2-data'),$null)) 'WebView2 environment').Result
 $script:controller=(Await $environment.CreateCoreWebView2ControllerAsync($form.Handle) 'WebView2 controller').Result
 $controller.Bounds=$form.ClientRectangle
 $controller.IsVisible=$true
 $script:core=$controller.CoreWebView2
 $core.NavigateToString('<html><head><meta charset="utf-8"></head><body style="margin:24px"><h2>0c WebView2 acceptance</h2><textarea id="text" style="font-size:24px;width:600px;height:120px"></textarea></body></html>')
 $w=[Diagnostics.Stopwatch]::StartNew()
 while($w.ElapsedMilliseconds -lt 15000){Pump 200;try{if((JS 'document.readyState==="complete" && !!document.getElementById("text")') -eq $true){break}}catch{}}
 [WebViewProbe]::Activate($form.Handle);Pump 300
 $controller.MoveFocus([Microsoft.Web.WebView2.Core.CoreWebView2MoveFocusReason]::Programmatic)
 JS 'document.getElementById("text").focus();true'|Out-Null;Pump 700
 Ensure-Front
 if((JS 'document.activeElement && document.activeElement.id') -ne 'text'){throw 'WebView2 textarea not focused'}
 # 和 Teams 相同的視窗結構：前景視窗屬於本行程，鍵盤焦點在 msedgewebview2.exe。
 $focus=[WebViewProbe]::Focus($form.Handle)
 $focusPid=[WebViewProbe]::Pid($focus)
 $focusProcess=Get-Process -Id $focusPid
 $report.focus_process=$focusProcess.ProcessName
 if($focusPid -eq $PID -or $focusProcess.ProcessName -ne 'msedgewebview2'){throw "keyboard focus is in $($focusProcess.ProcessName), not a separate msedgewebview2 process"}
 $report.checks+='webview2_focus_in_separate_process'

 SendText 'su3cl3';Expect '你好' 'webview2_compose_nihao'
 Key 13;Expect '你好' 'webview2_commit'
 Clear-Text
 SendText 'su3';Key 27;Expect '' 'webview2_cancel'
 # 修正前：0c-ui 只認前景視窗的行程，WebView2 的候選窗永遠不出現；GetTextExt 回 TS_E_NOLAYOUT 時也沒有退路。
 SendText 'su3';Key 40
 $candidate=Wait-Candidate
 Shot 'webview2-candidate.png'
 if($candidate -eq [IntPtr]::Zero){throw 'WebView2 candidate window not visible'}
 $report.candidate=[WebViewProbe]::Class($candidate)+':'+(Get-Process -Id ([WebViewProbe]::Pid($candidate))).ProcessName
 $report.checks+='webview2_candidate_visible'
 $c=New-Object WebViewProbe+RECT;[WebViewProbe]::GetWindowRect($candidate,[ref]$c)|Out-Null
 $f=New-Object WebViewProbe+RECT;[WebViewProbe]::GetWindowRect($form.Handle,[ref]$f)|Out-Null
 $report.candidate_rect=@($c.Left,$c.Top,$c.Right,$c.Bottom);$report.form_rect=@($f.Left,$f.Top,$f.Right,$f.Bottom)
 # 輸入框在視窗左上（約 24,70 起、600x120）：候選窗要在它附近，不是螢幕角落。
 if($c.Left -lt $f.Left -or $c.Left -gt $f.Left+700 -or $c.Top -lt $f.Top -or $c.Top -gt $f.Top+400){throw "candidate window is not near the text field: $($report.candidate_rect -join ',')"}
 $report.checks+='webview2_candidate_near_field'
 Key 13;Key 13;Expect '你' 'webview2_candidate_enter'
 Clear-Text
 # 滑鼠選字：修正前 DLL 也只認前景視窗的行程，點了沒反應。
 SendText 'su3';Key 40
 $candidate=Wait-Candidate
 if($candidate -eq [IntPtr]::Zero){throw 'WebView2 candidate window not visible for mouse selection'}
 $dpi=[WebViewProbe]::GetDpiForWindow($candidate)
 $x=[int](20*$dpi/96);$y=[int](70*$dpi/96) # 第一列是小字標題：pad 8＋標題 20＋一列 28 後是第二個候選
 [WebViewProbe]::PostMessage($candidate,0x202,[IntPtr]::Zero,[IntPtr](($y -shl 16) -bor $x))|Out-Null;Pump 700
 Key 13;Expect '妳' 'webview2_mouse_candidate'
 $focusProcess.Refresh()
 $report.dll=($focusProcess.Modules|Where-Object ModuleName -eq '0c_tsf.dll'|Select-Object -First 1).FileName
 if($report.dll -ne $ExpectedDLL){throw "WebView2 loaded an unexpected DLL: $($report.dll)"}
 $report.checks+='webview2_loaded_dll'
 $report.passed=$true
} catch {$report.error=$_.Exception.ToString()} finally {
 if($script:controller){try{$script:controller.Close()}catch{}}
 if($form){try{$form.Close();$form.Dispose()}catch{}}
 # 本行程（msedgewebview2）在 tsf.log 的紀錄：候選窗退路、OnLayoutChange 等（不含輸入文字）。
 if($focusPid -and (Test-Path $log)){
  try {
   $stream=[IO.File]::Open($log,'Open','Read','ReadWrite')
   try {$stream.Seek([Math]::Min($logStart,$stream.Length),'Begin')|Out-Null;$text=(New-Object IO.StreamReader($stream,[Text.Encoding]::UTF8)).ReadToEnd()} finally {$stream.Dispose()}
   $lines=@($text -split "`n"|Where-Object {$_ -match " pid=$focusPid "})
   $lines|Set-Content (Join-Path $ResultDir 'webview2-tsf.log') -Encoding UTF8
   $report.tsf_log_lines=$lines.Count
  } catch {}
 }
 $report|ConvertTo-Json -Depth 5|Set-Content "$ResultDir\webview2-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
