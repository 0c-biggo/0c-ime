# 無痕／私密視窗驗收（#68、#70）：Chrome 無痕與訪客、Firefox 私密視窗的欄位帶 IS_PRIVATE，
# 要能照常組字、選字、右 Shift，密碼欄仍繞過，而且不學習；最後以一般 Chrome 視窗對照確認學習本身有效。
# 使用專用瀏覽器設定目錄與本機頁面，不接觸日常瀏覽資料。欄位內容與焦點由頁面寫進視窗標題，
# 不依賴 CDP（Firefox 沒有），三種瀏覽器同一種讀法。
param([Parameter(Mandatory=$true)][string]$ResultDir, [Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();error='';browsers=[ordered]@{};learned=[ordered]@{}}
$launched=$null
try {
 Add-Type -AssemblyName System.Windows.Forms,System.Drawing
 Add-Type @'
using System;
using System.Text;
using System.Collections.Generic;
using System.Runtime.InteropServices;
public static class PrivateProbe {
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
 [DllImport("user32.dll")] public static extern void keybd_event(byte key, byte scan, uint flags, UIntPtr extra);
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
 public static void Tap(byte vk) { keybd_event(vk, 0, 0, UIntPtr.Zero); keybd_event(vk, 0, 2, UIntPtr.Zero); }
 public static void RightShift() { keybd_event(0xA1, 0x36, 0, UIntPtr.Zero); keybd_event(0xA1, 0x36, 2, UIntPtr.Zero); }
 public static void Type(string text) { foreach (char c in text) Tap((byte)char.ToUpperInvariant(c)); }
}
'@
 function Pump([int]$ms){$w=[Diagnostics.Stopwatch]::StartNew();while($w.ElapsedMilliseconds -lt $ms){[Windows.Forms.Application]::DoEvents();Start-Sleep -Milliseconds 10}}
 $marker='0cpriv'+[guid]::NewGuid().ToString('N').Substring(0,6)
 # F2 移到密碼欄、F4 清空並回到文字欄；標題 = 標記|文字|密碼|焦點|end（都以 encodeURIComponent 編碼）。
 $html=@"
<html><head><meta charset="utf-8"><title>$marker</title></head><body>
<textarea id="text" autofocus style="font-size:28px;width:500px;height:120px"></textarea><br><input id="password" type="password">
<script>
const t=document.getElementById('text'),p=document.getElementById('password');
setInterval(()=>{document.title='$marker|'+encodeURIComponent(t.value)+'|'+encodeURIComponent(p.value)+'|'+(document.activeElement?document.activeElement.id:'')+'|end'},80);
document.addEventListener('keydown',e=>{if(e.key==='F2'){e.preventDefault();p.focus()}if(e.key==='F4'){e.preventDefault();t.value='';p.value='';t.focus()}});
</script></body></html>
"@
 [IO.File]::WriteAllText("$ResultDir\private.html",$html,[Text.UTF8Encoding]::new($false))
 $url='file:///'+("$ResultDir\private.html" -replace '\\','/')
 $user=Join-Path $ResultDir 'test-user.json'
 $chrome='C:\Program Files\Google\Chrome\Application\chrome.exe'
 $firefox='C:\Program Files\Mozilla Firefox\firefox.exe'
 $script:hwnd=[IntPtr]::Zero
 function State {
  $title=[PrivateProbe]::Title($script:hwnd)
  $i=$title.IndexOf($marker+'|');$j=$title.IndexOf('|end')
  if($i -lt 0 -or $j -lt 0){return $null}
  $parts=$title.Substring($i,$j-$i).Split('|')
  return [pscustomobject]@{text=[Uri]::UnescapeDataString($parts[1]);password=[Uri]::UnescapeDataString($parts[2]);focus=$parts[3]}
 }
 function Ensure { if([PrivateProbe]::GetForegroundWindow() -ne $script:hwnd){[PrivateProbe]::Focus($script:hwnd);Pump 400;if([PrivateProbe]::GetForegroundWindow() -ne $script:hwnd){throw 'test browser window lost focus'}} }
 function SendText([string]$text){Ensure;[PrivateProbe]::Type($text);Pump 700}
 function Key([byte]$vk){Ensure;[PrivateProbe]::Tap($vk);Pump 500}
 function Expect([string]$field,[string]$expected,[string]$name){
  $deadline=(Get-Date).AddSeconds(3)
  do {$s=State;if($s -and $s.$field -eq $expected){$report.checks+=$name;return};Pump 100}while((Get-Date) -lt $deadline)
  throw "$name expected [$expected], got [$(if($s){$s.$field}else{'?'})]"
 }
 function Shot([string]$name){
  $r=New-Object PrivateProbe+RECT;[PrivateProbe]::GetWindowRect($script:hwnd,[ref]$r)|Out-Null
  $screen=[Windows.Forms.Screen]::FromHandle($script:hwnd).Bounds
  $left=[Math]::Max($screen.Left,$r.Left);$top=[Math]::Max($screen.Top,$r.Top)
  $w=[Math]::Min($screen.Right,$r.Right+60)-$left;$h=[Math]::Min($screen.Bottom,$r.Top+520)-$top
  $bmp=New-Object Drawing.Bitmap $w,$h;$g=[Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($left,$top,0,0,$bmp.Size);$bmp.Save("$ResultDir\$name.png",[Drawing.Imaging.ImageFormat]::Png);$g.Dispose();$bmp.Dispose()
 }
 function Run([string]$name,[string]$exe,[string[]]$arguments,[bool]$private){
  $profile=Join-Path $ResultDir "$name-profile";New-Item -ItemType Directory $profile|Out-Null
  if($exe -eq $firefox){
   # 不跳歡迎頁與預設瀏覽器詢問；只影響這個專用設定目錄。
   @('user_pref("browser.shell.checkDefaultBrowser", false);','user_pref("browser.aboutwelcome.enabled", false);','user_pref("datareporting.policy.dataSubmissionPolicyBypassNotification", true);','user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);','user_pref("browser.startup.homepage_override.mstone", "ignore");','user_pref("browser.privatebrowsing.vpnpromourl", "");','user_pref("trailhead.firstrun.didSeeAboutWelcome", true);')|Set-Content "$profile\user.js" -Encoding ASCII
   $arguments=@('-no-remote','-profile',('"'+$profile+'"'))+$arguments
  } else {
   $arguments=@('--no-first-run','--no-default-browser-check','--disable-background-networking',('--user-data-dir="'+$profile+'"'))+$arguments
  }
  $script:launched=Start-Process $exe -ArgumentList ($arguments+@($url)) -PassThru
  $deadline=(Get-Date).AddSeconds(30)
  do {Pump 300;$script:hwnd=[PrivateProbe]::Find($marker)}while($script:hwnd -eq [IntPtr]::Zero -and (Get-Date) -lt $deadline)
  if($script:hwnd -eq [IntPtr]::Zero){throw "$name window missing"}
  $info=[ordered]@{version=(Get-Item $exe).VersionInfo.ProductVersion;title=([PrivateProbe]::Title($script:hwnd) -replace [regex]::Escape($marker)+'\|[^|]*\|[^|]*\|[^|]*\|end','<page>')}
  Pump 1500;[PrivateProbe]::Focus($script:hwnd);Pump 800
  $winPid=[uint32]0;[PrivateProbe]::GetWindowThreadProcessId($script:hwnd,[ref]$winPid)|Out-Null
  $dll=(Get-Process -Id $winPid).Modules|Where-Object ModuleName -eq '0c_tsf.dll'|Select-Object -First 1
  if(-not $dll -or $dll.FileName -ne $ExpectedDLL){throw "$name did not load the test DLL ($($dll.FileName))"}
  $report.checks+="${name}_loads_dll"
  Expect 'focus' 'text' "${name}_focus"
  SendText 'su3cl3';Expect 'text' '你好' "${name}_compose_nihao"
  Key 13;Expect 'text' '你好' "${name}_commit"
  Key 0x73;Expect 'text' '' "${name}_cleared"
  SendText 'su3';Key 40;Pump 500;Shot "$name-candidates"
  Key 0x33;Key 13;Pump 300
  $chosen=(State).text
  if(-not $chosen -or $chosen -match '[a-z0-9]'){throw "$name candidate selection committed [$chosen]"}
  $info.chosen=$chosen;$report.checks+="${name}_candidate_select"
  Key 0x73;Ensure;[PrivateProbe]::RightShift();Pump 500;SendText 'abc';Expect 'text' 'abc' "${name}_right_shift_english"
  # 私密視窗不學習：剛選過的字不會變成 ㄋㄧˇ 的首選；一般視窗選一次就學到（對照組）。
  $back=if($private){'你'}else{$chosen}
  $tag=if($private){'not_learned'}else{'learned'}
  Ensure;[PrivateProbe]::RightShift();Pump 500;SendText 'su3';Key 13;Expect 'text' ('abc'+$back) "${name}_right_shift_back_$tag"
  Key 0x71;Expect 'focus' 'password' "${name}_password_focus"
  SendText 'su3cl3';Expect 'password' 'su3cl3' "${name}_password_passthrough"
  Key 0x73;Shot "$name-final"
  $report.browsers[$name]=$info
  Stop-Process -Id $script:launched.Id -ErrorAction SilentlyContinue
  Get-Process chrome,firefox -ErrorAction SilentlyContinue|Where-Object {$_.Path -and (Get-CimInstance Win32_Process -Filter "ProcessId=$($_.Id)").CommandLine -like "*$profile*"}|Stop-Process -ErrorAction SilentlyContinue
  $script:launched=$null;$script:hwnd=[IntPtr]::Zero;Pump 1500
  return $chosen
 }
 $privateWords=@()
 $privateWords+=Run 'chrome-incognito' $chrome @('--incognito') $true
 $privateWords+=Run 'chrome-guest' $chrome @('--guest') $true
 if(Test-Path $firefox){$privateWords+=Run 'firefox-private' $firefox @('-private-window') $true}else{$report.browsers['firefox-private']='not installed'}
 Pump 12500 # 引擎在停止打字 10 秒後存檔
 $afterPrivate=if(Test-Path $user){[IO.File]::ReadAllText($user)}else{''}
 foreach($word in ($privateWords|Select-Object -Unique)){
  if($afterPrivate.Contains($word)){throw "private window taught [$word] to the personal dictionary"}
 }
 $report.learned.private=if($afterPrivate){'no private words'}else{'no user.json written'}
 $report.checks+='private_windows_not_learned'
 # 對照：一般 Chrome 視窗同樣的選字要學得到，證明上面的檢查有意義。
 $control=Run 'chrome-normal' $chrome @() $false
 Pump 12500
 $afterControl=if(Test-Path $user){[IO.File]::ReadAllText($user)}else{''}
 if(-not $afterControl.Contains($control)){throw "control window did not learn [$control]; learning check is not meaningful"}
 $report.learned.control=$control
 $report.checks+='normal_window_learns'
 $report.passed=$true
} catch {
 $report.error=$_.Exception.ToString()
 if($script:hwnd -ne [IntPtr]::Zero){try{Shot 'failure'}catch{}}
} finally {
 # 失敗時也要關掉本輪開的瀏覽器（含子行程），否則它們會持續載著測試 DLL。
 Get-CimInstance Win32_Process -Filter "Name='chrome.exe' or Name='firefox.exe'"|Where-Object {$_.CommandLine -and $_.CommandLine.Contains($ResultDir)}|ForEach-Object {Stop-Process -Id $_.ProcessId -ErrorAction SilentlyContinue}
 $report|ConvertTo-Json -Depth 5|Set-Content "$ResultDir\private-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
