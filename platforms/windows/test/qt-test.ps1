# Qt 6 Widgets 程式（#367）：打字不能讓 Qt 開啟它的無障礙功能，中文照常打得出來。
# 只在測試機的互動工作階段執行（acceptance-test.ps1 -Suite qt）。Qt 收到任何一個 WM_GETOBJECT 就開啟整套無障礙功能、
# 到程式結束都不關（qwindowsuiaaccessibility.cpp）。UIAutomationCore.dll 是 qbittorrent.exe 的靜態匯入、一啟動就載入，
# 不能拿來判斷；改開 Qt 自己的視窗訊息紀錄（QT_QPA_PLATFORM=windows:verbose=2＋qt.qpa.events），數它收到幾個 WM_GETOBJECT（0x3D）。
# 本測試因此不用 UI Automation 找畫面或讀文字（那本身就會送 WM_GETOBJECT）：以 Ctrl＋F 移到搜尋框、以剪貼簿讀回文字。
# 只有讀 InputScope 回 E_FAIL 時欄位政策才會走到 MSAA：.14（Windows 11 26200）的系統相容層讀得到 InputScope（tsf.log 是 field=Open），
# 回報者的 26300 回 E_FAIL（field=Private）；所以在 .14 這組修正前後都會過，field_notes 記下這次走的是哪一條。
# 用 qBittorrent 5.3.1（靜態連結 Qt 6.11.3，回報者用的版本）：把安裝檔裡的 qbittorrent.exe 解到 build\windows\qt-app，
# 或以 -QtApp 指定；找不到時標為 skipped。設定放在結果目錄（--profile），不碰日常的 qBittorrent。
# -Ime microsoft 改用微軟注音打同樣的字，當對照（不經 acceptance-test，直接在互動工作階段執行）。
param(
 [Parameter(Mandatory=$true)][string]$ResultDir,
 [string]$ExpectedDLL='',
 [string]$QtApp='',
 [ValidateSet('0c','microsoft')][string]$Ime='0c'
)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();error='';ime=$Ime;dll='';app='';app_version='';getobject=[ordered]@{}}
$app=$null
$originalLocalAppData=$env:LOCALAPPDATA
try {
 if(-not $QtApp){$QtApp=Join-Path (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path 'build\windows\qt-app\qbittorrent.exe'}
 if(-not (Test-Path -LiteralPath $QtApp)){$report.skipped="Qt test program not found: $QtApp";$report.passed=$true;return}
 if($Ime -eq '0c' -and -not $ExpectedDLL){throw 'ExpectedDLL is required for the 0c run'}
 if(Get-Process qbittorrent -ErrorAction SilentlyContinue){throw 'Existing qBittorrent is running; refusing to use it'}
 Add-Type -AssemblyName System.Windows.Forms,System.Drawing
 Add-Type @'
using System; using System.Text; using System.Runtime.InteropServices;
public static class QtProbe {
 [ComImport, Guid("71C6E74C-0F28-11D8-A82A-00065B84435C"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
 interface IProfiles { [PreserveSig] int ActivateProfile(uint type, ushort language, ref Guid clsid, ref Guid profile, IntPtr hkl, uint flags); }
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("user32.dll")] public static extern bool BringWindowToTop(IntPtr h);
 [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int cmd);
 [DllImport("user32.dll")] public static extern bool AttachThreadInput(uint a, uint b, bool attach);
 [DllImport("kernel32.dll")] public static extern uint GetCurrentThreadId();
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetClassName(IntPtr h, StringBuilder s, int n);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
 [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
 [DllImport("user32.dll")] static extern void keybd_event(byte k, byte s, uint f, UIntPtr e);
 public static string Class(IntPtr h){ var s=new StringBuilder(256); GetClassName(h,s,256); return s.ToString(); }
 public static uint Pid(IntPtr h){ uint p; GetWindowThreadProcessId(h, out p); return p; }
 public static void Down(byte vk){ keybd_event(vk,0,0,UIntPtr.Zero); System.Threading.Thread.Sleep(30); }
 public static void Up(byte vk){ keybd_event(vk,0,2,UIntPtr.Zero); System.Threading.Thread.Sleep(30); }
 public static void Key(byte vk){ Down(vk); Up(vk); System.Threading.Thread.Sleep(150); }
 public static void Combo(byte mod, byte vk){ Down(mod); Key(vk); Up(mod); System.Threading.Thread.Sleep(300); }
 public static void Keys(string s){ foreach(char c in s){ Key((byte)char.ToUpperInvariant(c)); } }
 public static bool Foreground(IntPtr h){
  ShowWindow(h, 9);
  IntPtr fg=GetForegroundWindow(); uint p;
  uint fgTid=GetWindowThreadProcessId(fg, out p), me=GetCurrentThreadId();
  bool attached=fgTid!=0 && fgTid!=me && AttachThreadInput(me, fgTid, true);
  keybd_event(0x12,0,0,UIntPtr.Zero); keybd_event(0x12,0,2,UIntPtr.Zero);
  BringWindowToTop(h); SetForegroundWindow(h);
  if(attached) AttachThreadInput(me, fgTid, false);
  System.Threading.Thread.Sleep(400);
  return GetForegroundWindow()==h;
 }
 // 對工作階段切到靈犀注音或微軟注音（profile type 1、FORSESSION，與其他測試相同）。
 public static int Use(bool zuin){
  object o = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("33C53A50-F456-4884-B049-85FD643ECFED")));
  try {
   Guid c=zuin?new Guid("138C35D0-7F08-4CEF-BA90-C34C16301528"):new Guid("B115690A-EA02-48D5-A231-E3578D2FDF80");
   Guid p=zuin?new Guid("EF62F743-9855-4C94-A658-1F0F98B7B840"):new Guid("B2F9C502-1742-11D4-9790-0080C882687E");
   return ((IProfiles)o).ActivateProfile(1,0x0404,ref c,ref p,IntPtr.Zero,0x20000000);
  } finally { Marshal.FinalReleaseComObject(o); }
 }
}
'@
 function Pump([int]$ms){$w=[Diagnostics.Stopwatch]::StartNew();while($w.ElapsedMilliseconds -lt $ms){[Windows.Forms.Application]::DoEvents();Start-Sleep -Milliseconds 20}}
 function Check([bool]$ok,[string]$name,[string]$detail=''){if(-not $ok){throw "$name failed $detail"};$report.checks+=$name}
 # Qt 的訊息紀錄裡 WM_GETOBJECT 的筆數（Qt 不認得名字時寫 Unknown，兩種都以 msg=0x3d 比對）。
 function GetObjects {if(-not(Test-Path "$ResultDir\qt-events.log")){return -1};return @(Get-Content "$ResultDir\qt-events.log" -Encoding UTF8|Where-Object {$_ -match ' msg=0x3d '}).Count}
 # 只擷取這個視窗在螢幕上的範圍（不用 PrintWindow：它會送訊息給 Qt）。
 function Shot([IntPtr]$h,[string]$name){
  $r=New-Object QtProbe+RECT;[QtProbe]::GetWindowRect($h,[ref]$r)|Out-Null
  $bmp=New-Object Drawing.Bitmap ($r.R-$r.L),($r.B-$r.T);$g=[Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($r.L,$r.T,0,0,$bmp.Size);$g.Dispose();$bmp.Save("$ResultDir\$name.png");$bmp.Dispose()
 }
 function Guard([IntPtr]$h){if([QtProbe]::GetForegroundWindow() -ne $h){throw "Qt test window lost focus to $([QtProbe]::Class([QtProbe]::GetForegroundWindow()))"}}
 function ReadField([IntPtr]$h){
  [Windows.Forms.Clipboard]::Clear()
  Guard $h;[QtProbe]::Combo(0x11,0x41);[QtProbe]::Combo(0x11,0x43);Pump 300
  return [Windows.Forms.Clipboard]::GetText()
 }
 function OwnEvents {if(-not(Test-Path "$ResultDir\Zuin\tsf.log")){return @()};return @(Get-Content "$ResultDir\Zuin\tsf.log" -Encoding UTF8|Where-Object {$_ -match " pid=$($app.Id) "})}

 # tsf.log 寫進結果目錄（同 notepad 測試）；tsf-debug 多記欄位政策走的是哪一條。
 $env:LOCALAPPDATA=$ResultDir
 New-Item -ItemType Directory -Force "$ResultDir\Zuin","$ResultDir\qbt-profile" | Out-Null
 if($Ime -eq '0c'){New-Item -ItemType File -Force "$ResultDir\Zuin\tsf-debug" | Out-Null}
 $hr=[QtProbe]::Use($Ime -eq '0c')
 if($hr -ne 0){throw ('ActivateProfile failed 0x{0:X8}' -f $hr)}
 $report.app=$QtApp
 $report.app_version=(Get-Item -LiteralPath $QtApp).VersionInfo.ProductVersion
 $env:QT_QPA_PLATFORM='windows:verbose=2'
 $env:QT_LOGGING_RULES='qt.qpa.events.debug=true;qt.qpa.uiautomation.debug=true;qt.qpa.input.methods.debug=true'
 $env:QT_FORCE_STDERR_LOGGING='1'
 $env:QT_MESSAGE_PATTERN='%{time process} %{category} %{message}'
 $app=Start-Process -FilePath $QtApp -ArgumentList @('--profile="'+"$ResultDir\qbt-profile"+'"','--confirm-legal-notice','--no-splash') -PassThru -RedirectStandardError "$ResultDir\qt-events.log" -RedirectStandardOutput "$ResultDir\qt-stdout.log"
 foreach($name in 'QT_QPA_PLATFORM','QT_LOGGING_RULES','QT_FORCE_STDERR_LOGGING','QT_MESSAGE_PATTERN'){Remove-Item "Env:$name"}
 $deadline=(Get-Date).AddSeconds(30)
 do {Pump 300;$app.Refresh()}while($app.MainWindowHandle -eq [IntPtr]::Zero -and -not $app.HasExited -and (Get-Date) -lt $deadline)
 if($app.HasExited -or $app.MainWindowHandle -eq [IntPtr]::Zero){throw 'qBittorrent main window missing'}
 $main=$app.MainWindowHandle
 Pump 3000
 $report.window_class=[QtProbe]::Class($main)
 Check ($report.window_class -match '^Qt\d+.*QWindow') 'qt_window_class' $report.window_class
 Check ([QtProbe]::Foreground($main)) 'qt_window_focused' ([QtProbe]::Class([QtProbe]::GetForegroundWindow()))
 Pump 1000
 Check ((GetObjects) -ge 0) 'qt_event_log_written'
 $report.getobject.before_typing=GetObjects
 # 這一步之前 Qt 就收過 WM_GETOBJECT（例如有螢幕閱讀程式在跑），就分不出是不是輸入法開的。
 Check ($report.getobject.before_typing -eq 0) 'qt_accessibility_off_before_typing' $report.getobject.before_typing

 # 搜尋框（Ctrl＋F 從種子清單移到搜尋框）打「你好」。
 Guard $main;[QtProbe]::Combo(0x11,0x46);Pump 500
 Guard $main;[QtProbe]::Keys('su3cl3');Pump 800
 Guard $main;[QtProbe]::Key(0x0D);Pump 800
 Shot $main 'qt-search'
 $report.search_text=ReadField $main
 Check ($report.search_text -eq '你好') 'qt_search_box_commits_chinese' $report.search_text
 if($Ime -eq '0c'){
  $report.dll=($app.Modules|Where-Object ModuleName -eq '0c_tsf.dll'|Select-Object -First 1).FileName
  Check ($report.dll -eq $ExpectedDLL) 'qt_expected_dll_loaded' $report.dll
  Check ([bool](OwnEvents|Where-Object {$_ -match 'open: session'})) 'qt_engine_session_opened'
 }
 Pump 1500
 $report.getobject.after_search=GetObjects
 Check ($report.getobject.after_search -eq 0) 'qt_accessibility_still_off_after_typing' $report.getobject.after_search
 # 清掉搜尋框（免得留著篩選），焦點回到種子清單。
 Guard $main;[QtProbe]::Combo(0x11,0x41);[QtProbe]::Key(0x08);[QtProbe]::Combo(0x11,0x46);Pump 500

 # 不測 Qt 的密碼框：qBittorrent 唯一不必先勾選設定就打得開的密碼框是「鎖定 qBittorrent」的設定密碼對話框，
 # 它的 AutoExpandableDialog::getText 在 setEchoMode(Password) 之後又 setInputMethodHints(預設 ImhNone)，把 ImhHiddenText 清掉，
 # Qt 因此不停用輸入法，微軟注音也照常組字，不是輸入法分得出來的情況。一般的 Qt 密碼框（ImhHiddenText）由 Qt 以
 # ImmAssociateContext(NULL) 停用輸入法，TSF 的焦點文件變成 NULL，按鍵根本不經過輸入法（#367 以 PySide6 小程式在 .14 核對過）。
 $report.passed=$true
} catch {$report.error=$_.Exception.Message} finally {
 if($app){$report.field_notes=@(OwnEvents|Where-Object {$_ -match '文件：|欄位政策'}|ForEach-Object {($_ -split ' pid=\d+ ',2)[-1]})}
 if($app -and -not $app.HasExited){
  $app.CloseMainWindow()|Out-Null
  if(-not $app.WaitForExit(15000)){Stop-Process -Id $app.Id -Force}
 }
 $env:LOCALAPPDATA=$originalLocalAppData
 $report|ConvertTo-Json -Depth 5|Set-Content "$ResultDir\qt-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
