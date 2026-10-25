# 左 Shift 切換中英驗收（#65、#206、#217）：Windows 的中英切換鍵預設左右 Shift（#206），單按左 Shift 切換，按住超過 0.3 秒、
# 按著 Shift 點滑鼠（Shift＋點選延伸選取）不切換；切換時 Shift 放開跟按下一樣交給應用程式（#217，同系統注音）；以 0c-update set left-shift off -apply 關掉後左 Shift 照系統原本的用途、
# 放開事件交給應用程式；再以 set left-shift on -apply 開啟後，單按左 Shift 像右 Shift 一樣切換並保留組字，
# Shift＋字母、左右同按、Ctrl／Alt＋Shift 不切換，放開後 Shift 不會卡住；Edge、Chrome 一般與無痕視窗同樣可切換；
# 關回 off 後恢復系統行為。左 Shift 一律以 VK_LSHIFT（0xA0）與掃描碼 0x2A 送出。
# 本測試自己管理隔離引擎：LOCALAPPDATA 指到結果目錄內的沙盒，0c-update 寫入的設定就是重啟後引擎讀的那份。
param([Parameter(Mandatory=$true)][string]$ResultDir, [Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();error='';layouts=[ordered]@{};cli=[ordered]@{};browsers=[ordered]@{}}
$form=$null;$server=$null
# 0.1.0-dev.62 起註冊的是固定路徑 Zuin\tsf\0c_tsf.dll（#121）：引擎與 0c-update 在收據記下的版本目錄。
$installed=Split-Path -Parent $ExpectedDLL
if(-not(Test-Path (Join-Path $installed '0c-server.exe'))){
 $receipt=Get-Content (Join-Path $env:LOCALAPPDATA 'Zuin\install-receipt.json') -Raw|ConvertFrom-Json
 $installed=Join-Path (Join-Path $env:LOCALAPPDATA 'Zuin') $receipt.directory
}
$la=Join-Path $ResultDir 'la'
New-Item -ItemType Directory -Force "$la\Zuin" | Out-Null
$env:LOCALAPPDATA=$la
try {
 Add-Type -AssemblyName System.Windows.Forms,System.Drawing
 Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public static class LeftShiftProbe {
 [ComImport, Guid("71C6E74C-0F28-11D8-A82A-00065B84435C"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
 interface IProfiles { [PreserveSig] int ActivateProfile(uint type, ushort language, ref Guid clsid, ref Guid profile, IntPtr hkl, uint flags); }
 public static void Activate() { ActivateWith(0x10000000); }
 // 0x4＝TF_IPPMF_DONTCARECURRENTINPUTLANGUAGE：系統熱鍵把語言換成英文後，仍可直接換回靈犀。
 public static void ActivateWith(uint flags) {
  object obj=Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("33C53A50-F456-4884-B049-85FD643ECFED")));
  try { Guid c=new Guid("138C35D0-7F08-4CEF-BA90-C34C16301528"), p=new Guid("EF62F743-9855-4C94-A658-1F0F98B7B840");
   Marshal.ThrowExceptionForHR(((IProfiles)obj).ActivateProfile(1,0x404,ref c,ref p,IntPtr.Zero,flags));
  } finally { Marshal.FinalReleaseComObject(obj); }
 }
 public delegate bool EnumProc(IntPtr h, IntPtr l);
 [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc f, IntPtr l);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("user32.dll")] public static extern bool BringWindowToTop(IntPtr h);
 [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int cmd);
 [DllImport("user32.dll")] public static extern bool AttachThreadInput(uint a, uint b, bool attach);
 [DllImport("kernel32.dll")] public static extern uint GetCurrentThreadId();
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
 [DllImport("user32.dll")] public static extern short GetAsyncKeyState(int vk);
 [DllImport("user32.dll")] public static extern IntPtr GetKeyboardLayout(uint thread);
 [DllImport("user32.dll")] public static extern void keybd_event(byte key, byte scan, uint flags, UIntPtr extra);
 [DllImport("user32.dll")] public static extern bool SetCursorPos(int x, int y);
 [DllImport("user32.dll")] public static extern void mouse_event(uint flags, int dx, int dy, uint data, UIntPtr extra);
 public static void Click(int x, int y) { SetCursorPos(x, y); mouse_event(2, 0, 0, 0, UIntPtr.Zero); System.Threading.Thread.Sleep(30); mouse_event(4, 0, 0, 0, UIntPtr.Zero); }
 [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
 public static string Title(IntPtr h) { var s = new StringBuilder(1024); GetWindowText(h, s, 1024); return s.ToString(); }
 public static IntPtr Find(string marker) {
  IntPtr found = IntPtr.Zero;
  EnumWindows((h, l) => { if (IsWindowVisible(h) && Title(h).Contains(marker)) { found = h; return false; } return true; }, IntPtr.Zero);
  return found;
 }
 public static void Focus(IntPtr h) {
  uint pid; uint target = GetWindowThreadProcessId(h, out pid);
  uint fg = GetWindowThreadProcessId(GetForegroundWindow(), out pid);
  uint me = GetCurrentThreadId();
  AttachThreadInput(me, fg, true); AttachThreadInput(me, target, true);
  ShowWindow(h, 9); BringWindowToTop(h); SetForegroundWindow(h);
  AttachThreadInput(me, target, false); AttachThreadInput(me, fg, false);
 }
 public static long Layout(IntPtr h) { uint pid; return GetKeyboardLayout(GetWindowThreadProcessId(h, out pid)).ToInt64(); }
 // 左右 Shift、Ctrl、Alt 都用左右分開的虛擬鍵與真實掃描碼。
 public static void Key(byte vk, byte scan, bool down) { keybd_event(vk, scan, down ? 0u : 2u, UIntPtr.Zero); }
 public static void Tap(byte vk) { keybd_event(vk, 0, 0, UIntPtr.Zero); keybd_event(vk, 0, 2, UIntPtr.Zero); }
 public static void TypeText(string text) { foreach (char c in text) Tap((byte)char.ToUpperInvariant(c)); }
 public static bool ShiftDown() { return (GetAsyncKeyState(0x10) & 0x8000) != 0 || (GetAsyncKeyState(0xA0) & 0x8000) != 0; }
}
'@
 function Pump([int]$ms){$w=[Diagnostics.Stopwatch]::StartNew();while($w.ElapsedMilliseconds -lt $ms){[Windows.Forms.Application]::DoEvents();Start-Sleep -Milliseconds 10}}
 function Check([bool]$ok,[string]$name,[string]$detail){if(-not $ok){throw "$name failed: $detail"};$report.checks+=$name}
 # ---- 引擎：只啟動、停止本測試版本目錄的 0c-server，個人詞庫與設定在沙盒 ----
 function StopServers {
  Get-Process 0c-server -ErrorAction SilentlyContinue|Where-Object {$_.Path -and $_.Path.StartsWith($installed,[StringComparison]::OrdinalIgnoreCase)}|Stop-Process -ErrorAction SilentlyContinue
  $deadline=(Get-Date).AddSeconds(10)
  while((Get-Process 0c-server -ErrorAction SilentlyContinue|Where-Object {$_.Path -and $_.Path.StartsWith($installed,[StringComparison]::OrdinalIgnoreCase)}) -and (Get-Date) -lt $deadline){Start-Sleep -Milliseconds 200}
 }
 function StartServer([string]$tag){
  $args='-data "'+$installed+'\data" -user "'+$la+'\Zuin\user.json" -idle-exit 300s'
  $script:server=Start-Process "$installed\0c-server.exe" -ArgumentList $args -PassThru -NoNewWindow -RedirectStandardOutput "$ResultDir\server-$tag.out" -RedirectStandardError "$ResultDir\server-$tag.err"
  Start-Sleep -Seconds 2
  if($script:server.HasExited){throw "engine ($tag) failed to start"}
 }
 function SetLeftShift([string]$value){
  $out=& "$installed\0c-update.exe" set left-shift $value -apply 2>&1|Out-String
  $report.cli[$value]=@{exit=$LASTEXITCODE;output=$out.Trim();settings=[IO.File]::ReadAllText("$la\Zuin\settings.json")}
  if($LASTEXITCODE -ne 0){throw "0c-update set left-shift $value failed: $out"}
  $want=if($value -eq 'on'){'"left_shift": true'}else{'"left_shift": false'}
  Check ((Get-Content "$la\Zuin\settings.json" -Raw).Contains($want)) "cli_set_left_shift_$value" $report.cli[$value].settings
  # -apply 讓引擎閒置後結束；以新設定重新啟動（沙盒沒有安裝收據，0c-update 不會自己啟動引擎）。
  StopServers;StartServer $value
 }
 # ---- 原生文字框（WinForms EDIT）：計算應用程式收到的 Shift 放開事件 ----
 $form=New-Object Windows.Forms.Form
 $form.Text='0c left Shift acceptance';$form.Width=640;$form.Height=160;$form.TopMost=$true;$form.StartPosition='Manual';$form.Left=20;$form.Top=20
 $box=New-Object Windows.Forms.TextBox
 $box.Font=New-Object Drawing.Font('Microsoft JhengHei UI',20);$box.Dock='Top';$form.Controls.Add($box)
 $script:shiftUps=0
 $box.Add_KeyUp({param($s,$e) if($e.KeyCode -eq [Windows.Forms.Keys]::ShiftKey){$script:shiftUps++}})
 function NativeFocus {
  if([LeftShiftProbe]::GetForegroundWindow() -ne $form.Handle){[LeftShiftProbe]::Focus($form.Handle);$box.Focus()|Out-Null;Pump 300}
  if([LeftShiftProbe]::GetForegroundWindow() -ne $form.Handle){throw 'test window lost focus'}
 }
 function Send([string]$keys){NativeFocus;[Windows.Forms.SendKeys]::SendWait($keys);Pump 400}
 function Expect([string]$text,[string]$name){$deadline=(Get-Date).AddSeconds(2);while($box.Text -ne $text -and (Get-Date) -lt $deadline){Pump 100};Check ($box.Text -eq $text) $name "expected [$text] got [$($box.Text)]"}
 function ClearBox {$box.Clear();Pump 150}
 function LTap {[LeftShiftProbe]::Key(0xA0,0x2A,$true);Pump 80;[LeftShiftProbe]::Key(0xA0,0x2A,$false);Pump 500}
 function LChord([byte]$vk){[LeftShiftProbe]::Key(0xA0,0x2A,$true);Pump 80;[LeftShiftProbe]::Tap($vk);Pump 80;[LeftShiftProbe]::Key(0xA0,0x2A,$false);Pump 400}
 function LHold {[LeftShiftProbe]::Key(0xA0,0x2A,$true);Pump 600;[LeftShiftProbe]::Key(0xA0,0x2A,$false);Pump 500}
 # 按著左 Shift 在文字框右端點一下（Shift＋點選延伸選取，#206）：放開在 0.3 秒內，只差中間點了滑鼠。
 function ShiftClick {
  $r=$box.RectangleToScreen($box.ClientRectangle)
  [LeftShiftProbe]::Key(0xA0,0x2A,$true);Pump 40;[LeftShiftProbe]::Click($r.Right-12,[int](($r.Top+$r.Bottom)/2));Pump 60;[LeftShiftProbe]::Key(0xA0,0x2A,$false);Pump 500
 }
 function BothShifts {[LeftShiftProbe]::Key(0xA0,0x2A,$true);Pump 60;[LeftShiftProbe]::Key(0xA1,0x36,$true);Pump 60;[LeftShiftProbe]::Key(0xA1,0x36,$false);Pump 60;[LeftShiftProbe]::Key(0xA0,0x2A,$false);Pump 500}
 # Ctrl／Alt（左側）＋左 Shift：系統的輸入法／語言切換熱鍵。不得被靈犀當成單按；若系統真的換了配置就換回來。
 function SystemHotkey([byte]$vk,[byte]$scan,[string]$name){
  NativeFocus
  $before=[LeftShiftProbe]::Layout($form.Handle)
  [LeftShiftProbe]::Key($vk,$scan,$true);Pump 60;[LeftShiftProbe]::Key(0xA0,0x2A,$true);Pump 60;[LeftShiftProbe]::Key(0xA0,0x2A,$false);Pump 60;[LeftShiftProbe]::Key($vk,$scan,$false);Pump 600
  $after=[LeftShiftProbe]::Layout($form.Handle)
  $report.layouts[$name]=@{before=('{0:X}' -f $before);after=('{0:X}' -f $after)}
  # 系統照常切換了配置（靈犀沒有攔截熱鍵）：繼續按同一熱鍵直到回到原語言，再明確啟用靈犀，
  # 避免之後開的瀏覽器沿用英文配置。
  for($i=0;$i -lt 4 -and [LeftShiftProbe]::Layout($form.Handle) -ne $before;$i++){
   NativeFocus;[LeftShiftProbe]::Key($vk,$scan,$true);Pump 60;[LeftShiftProbe]::Key(0xA0,0x2A,$true);Pump 60;[LeftShiftProbe]::Key(0xA0,0x2A,$false);Pump 60;[LeftShiftProbe]::Key($vk,$scan,$false);Pump 600
  }
  $report.layouts[$name].restored=('{0:X}' -f [LeftShiftProbe]::Layout($form.Handle))
  [LeftShiftProbe]::ActivateWith(0x10000004);Pump 500
  ClearBox;Send 'su3{ENTER}';Expect '你' "${name}_not_toggled"
  ClearBox
 }
 function Shot([IntPtr]$h,[string]$name,[int]$extra){
  $r=New-Object LeftShiftProbe+RECT;[LeftShiftProbe]::GetWindowRect($h,[ref]$r)|Out-Null
  $screen=[Windows.Forms.Screen]::FromHandle($h).Bounds
  $left=[Math]::Max($screen.Left,$r.Left);$top=[Math]::Max($screen.Top,$r.Top)
  $w=[Math]::Min($screen.Right,$r.Right+40)-$left;$hh=[Math]::Min($screen.Bottom,$r.Bottom+$extra)-$top
  $bmp=New-Object Drawing.Bitmap $w,$hh;$g=[Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($left,$top,0,0,$bmp.Size);$bmp.Save("$ResultDir\$name.png",[Drawing.Imaging.ImageFormat]::Png);$g.Dispose();$bmp.Dispose()
 }
 function Warmup {ClearBox;Send 'su3{ESC}';Pump 300;ClearBox}

 # 外層驗收啟動的引擎讀的是另一份設定；換成本測試管理的引擎（設定檔不存在＝預設左右 Shift，#206）。
 StopServers;StartServer 'default'
 $form.Show();$form.Activate();$box.Focus()|Out-Null;Pump 500
 [LeftShiftProbe]::Activate();Pump 500;NativeFocus;Warmup
 $report.dll=((Get-Process -Id $PID).Modules|Where-Object ModuleName -eq '0c_tsf.dll'|Select-Object -First 1).FileName
 Check ($report.dll -eq $ExpectedDLL) 'native_loads_test_dll' $report.dll

 # ---- 0. 預設（#206：Windows 左右 Shift 都切換）----
 $n=$script:shiftUps;NativeFocus;LTap
 Check ($script:shiftUps -eq $n+1) 'default_left_shift_up_reaches_app' "app saw $($script:shiftUps-$n) Shift key-ups"
 Check (-not [LeftShiftProbe]::ShiftDown()) 'default_shift_not_stuck' 'Shift still down after toggling key-up'
 Send 'abc';Expect 'abc' 'default_left_tap_english'
 NativeFocus;LTap;Send 'su3{ENTER}';Expect 'abc你' 'default_left_tap_back_to_zhuyin'
 ClearBox;Send 'su3{ENTER}';NativeFocus;ShiftClick;Send 'su3{ENTER}';Expect '你你' 'default_shift_click_no_toggle'
 Check (-not [LeftShiftProbe]::ShiftDown()) 'default_shift_click_not_stuck' 'Shift still down after Shift+click'
 ClearBox;NativeFocus;LHold;Send 'su3{ENTER}';Expect '你' 'default_long_hold_no_toggle'
 ClearBox;NativeFocus;LChord 0x41;Send 'su3{ENTER}';Expect 'A你' 'default_shift_letter_uppercase_no_toggle'
 ClearBox;NativeFocus;Send 'su3{ENTER}';LChord 0x25;Send 'su3{ENTER}';Expect '你' 'default_shift_arrow_select_no_toggle'
 ClearBox

 # ---- 1. 關閉左 Shift（只剩右 Shift）----
 SetLeftShift 'off';NativeFocus;Warmup
 $n=$script:shiftUps;NativeFocus;LTap
 Check ($script:shiftUps -eq $n+1) 'off_left_shift_up_reaches_app' "app saw $($script:shiftUps-$n) Shift key-ups"
 Check (-not [LeftShiftProbe]::ShiftDown()) 'off_shift_not_stuck' 'Shift still down'
 Send 'su3cl3{ENTER}';Expect '你好' 'off_tap_does_not_toggle'
 ClearBox;NativeFocus;LChord 0x41;Send 'su3{ENTER}';Expect 'A你' 'off_shift_letter_uppercase'
 ClearBox;NativeFocus;Send 'su3';LTap;Send 'cl3{ENTER}';Expect '你好' 'off_tap_keeps_zhuyin_composition'
 ClearBox;SystemHotkey 0xA2 0x1D 'off_ctrl_shift';SystemHotkey 0xA4 0x38 'off_alt_shift'

 # ---- 2. 開啟 ----
 SetLeftShift 'on';NativeFocus;Warmup
 $n=$script:shiftUps;NativeFocus;LTap
 Check ($script:shiftUps -eq $n+1) 'on_left_shift_up_reaches_app' "app saw $($script:shiftUps-$n) Shift key-ups"
 Check (-not [LeftShiftProbe]::ShiftDown()) 'on_shift_not_stuck' 'Shift still down after toggling key-up'
 Send 'abc';Expect 'abc' 'on_tap_english_lowercase'
 Shot $form.Handle 'native-on-english' 60
 NativeFocus;LTap;Send 'su3{ENTER}';Expect 'abc你' 'on_tap_back_to_zhuyin'
 ClearBox;Send 'su3';NativeFocus;LTap;Send 'ab';Pump 300;Shot $form.Handle 'native-on-mixed-composition' 160;NativeFocus;LTap;Send '{ENTER}';Expect '你ab' 'on_tap_keeps_composition'
 ClearBox;NativeFocus;LChord 0x41;Send 'su3{ENTER}';Expect 'A你' 'on_shift_letter_uppercase_no_toggle'
 ClearBox;NativeFocus;BothShifts;Send 'su3{ENTER}';Expect '你' 'on_both_shifts_no_toggle'
 ClearBox;NativeFocus;[LeftShiftProbe]::Key(0xA1,0x36,$true);Pump 80;[LeftShiftProbe]::Key(0xA1,0x36,$false);Pump 500;Send 'abc';NativeFocus;LTap;Send 'su3{ENTER}';Expect 'abc你' 'on_right_then_left_shift_round_trip'
 ClearBox;SystemHotkey 0xA2 0x1D 'on_ctrl_shift';SystemHotkey 0xA4 0x38 'on_alt_shift'
 ClearBox;NativeFocus;LTap;Send 'xyz';Expect 'xyz' 'on_lowercase_after_toggle'
 NativeFocus;LTap;ClearBox
 $form.Hide();Pump 300

 # ---- 3. 瀏覽器（開啟狀態）：Edge、Chrome 一般與無痕 ----
 $marker='0clshift'+[guid]::NewGuid().ToString('N').Substring(0,6)
 $html=@"
<html><head><meta charset="utf-8"><title>$marker</title></head><body>
<textarea id="text" autofocus style="font-size:28px;width:500px;height:120px"></textarea>
<script>
const t=document.getElementById('text');
setInterval(()=>{document.title='$marker|'+encodeURIComponent(t.value)+'|'+(document.activeElement?document.activeElement.id:'')+'|end'},80);
document.addEventListener('keydown',e=>{if(e.key==='F4'){e.preventDefault();t.value='';t.focus()}});
</script></body></html>
"@
 [IO.File]::WriteAllText("$ResultDir\leftshift.html",$html,[Text.UTF8Encoding]::new($false))
 $url='file:///'+("$ResultDir\leftshift.html" -replace '\\','/')
 $script:hwnd=[IntPtr]::Zero
 function State {
  $title=[LeftShiftProbe]::Title($script:hwnd)
  $i=$title.IndexOf($marker+'|');$j=$title.IndexOf('|end')
  if($i -lt 0 -or $j -lt 0){return $null}
  $parts=$title.Substring($i,$j-$i).Split('|')
  return [pscustomobject]@{text=[Uri]::UnescapeDataString($parts[1]);focus=$parts[2]}
 }
 function Ensure {if([LeftShiftProbe]::GetForegroundWindow() -ne $script:hwnd){[LeftShiftProbe]::Focus($script:hwnd);Pump 400;if([LeftShiftProbe]::GetForegroundWindow() -ne $script:hwnd){throw 'test browser window lost focus'}}}
 function BType([string]$text){Ensure;[LeftShiftProbe]::TypeText($text);Pump 700}
 function BKey([byte]$vk){Ensure;[LeftShiftProbe]::Tap($vk);Pump 500}
 function BLTap{Ensure;LTap}
 function BExpect([string]$expected,[string]$name){
  $deadline=(Get-Date).AddSeconds(3)
  do {$s=State;if($s -and $s.text -eq $expected){$report.checks+=$name;return};Pump 100}while((Get-Date) -lt $deadline)
  throw "$name expected [$expected], got [$(if($s){$s.text}else{'?'})]"
 }
 $browsers=@(
  @{name='edge';exe='C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe';args=@(('--app="'+$url+'"'))},
  @{name='chrome';exe='C:\Program Files\Google\Chrome\Application\chrome.exe';args=@()},
  @{name='chrome-incognito';exe='C:\Program Files\Google\Chrome\Application\chrome.exe';args=@('--incognito')}
 )
 foreach($b in $browsers){
  $name=$b.name
  if(-not(Test-Path $b.exe)){$report.browsers[$name]='not installed';continue}
  $profile=Join-Path $ResultDir "$name-profile";New-Item -ItemType Directory $profile|Out-Null
  $arguments=@('--no-first-run','--no-default-browser-check','--disable-background-networking','--window-position=20,20','--window-size=760,330',('--user-data-dir="'+$profile+'"'))+$b.args
  if($name -ne 'edge'){$arguments+=@($url)}
  Start-Process $b.exe -ArgumentList $arguments|Out-Null
  $deadline=(Get-Date).AddSeconds(30)
  do {Pump 300;$script:hwnd=[LeftShiftProbe]::Find($marker)}while($script:hwnd -eq [IntPtr]::Zero -and (Get-Date) -lt $deadline)
  if($script:hwnd -eq [IntPtr]::Zero){throw "$name window missing"}
  Pump 1500;[LeftShiftProbe]::Focus($script:hwnd);Pump 800
  $winPid=[uint32]0;[LeftShiftProbe]::GetWindowThreadProcessId($script:hwnd,[ref]$winPid)|Out-Null
  $dll=(Get-Process -Id $winPid).Modules|Where-Object ModuleName -eq '0c_tsf.dll'|Select-Object -First 1
  if(-not $dll -or $dll.FileName -ne $ExpectedDLL){throw "$name did not load the test DLL ($($dll.FileName))"}
  $report.checks+="${name}_loads_dll"
  $report.browsers[$name]=@{version=(Get-Item $b.exe).VersionInfo.ProductVersion}
  BType 'su3';BKey 27;BKey 0x73
  BLTap;BType 'abc';BExpect 'abc' "${name}_left_shift_english"
  BLTap;BType 'su3';BKey 13;BExpect 'abc你' "${name}_left_shift_back_zhuyin"
  Check (-not [LeftShiftProbe]::ShiftDown()) "${name}_shift_not_stuck" 'Shift still down'
  BKey 0x73;BType 'su3';BLTap;BType 'ab';BLTap;BKey 13;BExpect '你ab' "${name}_left_shift_keeps_composition"
  if($name -eq 'chrome-incognito'){BKey 0x73;BLTap;BType 'abc';BLTap;BType 'su3';Pump 300;Shot $script:hwnd 'chrome-incognito-on' 0;BKey 13}
  Get-CimInstance Win32_Process -Filter "Name='chrome.exe' or Name='msedge.exe'"|Where-Object {$_.CommandLine -and $_.CommandLine.Contains($profile)}|ForEach-Object {Stop-Process -Id $_.ProcessId -ErrorAction SilentlyContinue}
  $script:hwnd=[IntPtr]::Zero;Pump 1500
 }

 # ---- 4. 關回 off：回到系統行為 ----
 SetLeftShift 'off'
 $form.Show();Pump 300;NativeFocus;Warmup
 $n=$script:shiftUps;NativeFocus;LTap
 Check ($script:shiftUps -eq $n+1) 'off_again_left_shift_up_reaches_app' "app saw $($script:shiftUps-$n) Shift key-ups"
 Send 'su3cl3{ENTER}';Expect '你好' 'off_again_tap_does_not_toggle'
 $report.passed=$true
} catch {
 $report.error=$_.Exception.ToString()
 if($form -and $form.Visible){try{Shot $form.Handle 'failure' 90}catch{}}
} finally {
 Get-CimInstance Win32_Process -Filter "Name='chrome.exe' or Name='msedge.exe'"|Where-Object {$_.CommandLine -and $_.CommandLine.Contains($ResultDir)}|ForEach-Object {Stop-Process -Id $_.ProcessId -ErrorAction SilentlyContinue}
 if($form){$form.Close()}
 try{StopServers}catch{}
 $report|ConvertTo-Json -Depth 5|Set-Content "$ResultDir\leftshift-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
