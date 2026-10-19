# 檔案總管（#105）：網址列、搜尋框與「新增資料夾」重新命名欄位的組字、選字與送出。
# 只在測試機的互動工作階段執行（acceptance-test.ps1 -Suite explorer）；在暫存目錄開自己的檔案總管視窗，結束時關閉並刪除。
# 檔案總管與工作列同一個 explorer.exe，DLL 的 tsf.log 寫在真正的 %LOCALAPPDATA%\Zuin；只取本行程、本次測試的紀錄。
param([Parameter(Mandatory=$true)][string]$ResultDir,[Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();error='';dll='';fields=[ordered]@{}}
$folder=Join-Path $env:TEMP ('0c-explorer-'+[guid]::NewGuid().ToString('N').Substring(0,8))
$zuin=Join-Path $env:LOCALAPPDATA 'Zuin'
$debugFlag=Join-Path $zuin 'tsf-debug'
$createdFlag=$false
$window=[IntPtr]::Zero
try {
 Add-Type -AssemblyName System.Windows.Forms,System.Drawing,UIAutomationClient,UIAutomationTypes
 Add-Type @'
using System; using System.Text; using System.Runtime.InteropServices;
public static class ExplorerProbe {
 [ComImport, Guid("71C6E74C-0F28-11D8-A82A-00065B84435C"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
 interface IProfiles { [PreserveSig] int ActivateProfile(uint type, ushort language, ref Guid clsid, ref Guid profile, IntPtr hkl, uint flags); }
 [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
 [StructLayout(LayoutKind.Sequential)] public struct GUITHREADINFO { public int cbSize, flags; public IntPtr hwndActive, hwndFocus, hwndCapture, hwndMenuOwner, hwndMoveSize, hwndCaret; public RECT rcCaret; }
 public delegate bool EnumProc(IntPtr h, IntPtr l);
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("user32.dll")] public static extern bool BringWindowToTop(IntPtr h);
 [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int cmd);
 [DllImport("user32.dll")] public static extern bool AttachThreadInput(uint a, uint b, bool attach);
 [DllImport("kernel32.dll")] public static extern uint GetCurrentThreadId();
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
 [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc p, IntPtr l);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetClassName(IntPtr h, StringBuilder s, int n);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
 [DllImport("user32.dll")] public static extern bool GetGUIThreadInfo(uint tid, ref GUITHREADINFO info);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint msg, IntPtr w, IntPtr l);
 [DllImport("user32.dll")] static extern void keybd_event(byte k, byte s, uint f, UIntPtr e);
 public static string Class(IntPtr h){ var s=new StringBuilder(256); GetClassName(h,s,256); return s.ToString(); }
 public static string Title(IntPtr h){ var s=new StringBuilder(512); GetWindowText(h,s,512); return s.ToString(); }
 public static IntPtr Find(string title){
  IntPtr found=IntPtr.Zero;
  EnumWindows((h,l)=>{ if(IsWindowVisible(h) && Class(h)=="CabinetWClass" && Title(h).Contains(title)){ found=h; return false; } return true; }, IntPtr.Zero);
  return found;
 }
 public static uint Pid(IntPtr h){ uint p; GetWindowThreadProcessId(h, out p); return p; }
 // 看得到的候選窗：0c-ui 的 ZuinCandidateWindow，或沙箱宿主行程內的 0cCandidateInProcess。回傳「類別:行程 id」。
 public static string Candidates(){
  string found="";
  EnumWindows((h,l)=>{ string c=Class(h); RECT r; if(IsWindowVisible(h) && (c=="ZuinCandidateWindow" || c=="0cCandidateInProcess") && GetWindowRect(h, out r) && r.Right>r.Left+20){ found=c+":"+Pid(h); return false; } return true; }, IntPtr.Zero);
  return found;
 }
 public static uint ForegroundPid(){ return Pid(GetForegroundWindow()); }
 public static IntPtr Focus(IntPtr top){
  uint p; uint tid=GetWindowThreadProcessId(top, out p);
  var info=new GUITHREADINFO(); info.cbSize=Marshal.SizeOf(typeof(GUITHREADINFO));
  return GetGUIThreadInfo(tid, ref info) ? info.hwndFocus : IntPtr.Zero;
 }
 public static void Down(byte vk){ keybd_event(vk,0,0,UIntPtr.Zero); System.Threading.Thread.Sleep(30); }
 public static void Up(byte vk){ keybd_event(vk,0,2,UIntPtr.Zero); System.Threading.Thread.Sleep(30); }
 public static void Key(byte vk){ Down(vk); Up(vk); System.Threading.Thread.Sleep(150); }
 public static void Combo(byte[] mods, byte vk){ foreach(var m in mods) Down(m); Key(vk); for(int i=mods.Length-1;i>=0;i--) Up(mods[i]); System.Threading.Thread.Sleep(150); }
 public static void Keys(string s){ foreach(char c in s){ Key((byte)char.ToUpperInvariant(c)); } }
 // 前景屬於別的程式（例如系統對話框）時，先掛到它的輸入佇列再搶前景。
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
 public static int Activate(){
  object o = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("33C53A50-F456-4884-B049-85FD643ECFED")));
  try { Guid c=new Guid("138C35D0-7F08-4CEF-BA90-C34C16301528"), p=new Guid("EF62F743-9855-4C94-A658-1F0F98B7B840");
   return ((IProfiles)o).ActivateProfile(1,0x0404,ref c,ref p,IntPtr.Zero,0x20000000); } finally { Marshal.FinalReleaseComObject(o); }
 }
}
'@
 function Check([bool]$ok,[string]$name,[string]$detail){ if(-not $ok){throw "$name failed: $detail"}; $script:report.checks+=$name }
 function Guard { if([ExplorerProbe]::GetForegroundWindow() -ne $script:window){ throw "explorer test window lost focus to $([ExplorerProbe]::Class([ExplorerProbe]::GetForegroundWindow()))" } }
 # 只截自己的檔案總管視窗（含下方 360px 的候選窗範圍），不存整張螢幕。
 function Shot([string]$name){
  $r=New-Object ExplorerProbe+RECT; [ExplorerProbe]::GetWindowRect($script:window,[ref]$r)|Out-Null
  $b=[Windows.Forms.Screen]::PrimaryScreen.Bounds
  $x=[Math]::Max($b.Left,$r.Left); $y=[Math]::Max($b.Top,$r.Top)
  $w=[Math]::Min($b.Right,$r.Right)-$x; $h=[Math]::Min($b.Bottom,$r.Top+300)-$y
  if($w -le 0 -or $h -le 0){return}
  $bmp=New-Object Drawing.Bitmap $w,$h
  $g=[Drawing.Graphics]::FromImage($bmp); $g.CopyFromScreen($x,$y,0,0,(New-Object Drawing.Size $w,$h))
  $bmp.Save((Join-Path $ResultDir "$name.png")); $g.Dispose(); $bmp.Dispose()
 }
 # 焦點欄位：UIA 的控制項與框架（不讀名稱以外的內容）、Win32 焦點視窗類別；值只在本次測試字時讀。
 function Field {
  $f=[ExplorerProbe]::Focus($script:window)
  $o=[ordered]@{hwnd_class=[ExplorerProbe]::Class($f);uia_class='';uia_type='';framework='';process='';value=$null}
  # 組字或重新命名進行中 UIA 偶爾回 ElementNotAvailable；稍等再讀一次。
  for($try=0;$try -lt 3;$try++){
   try {
    $e=[Windows.Automation.AutomationElement]::FocusedElement
    if($e){
     $c=$e.Current
     $o.uia_class=$c.ClassName;$o.uia_type=$c.ControlType.ProgrammaticName;$o.framework=$c.FrameworkId
     $o.process=(Get-Process -Id $c.ProcessId -ErrorAction SilentlyContinue).ProcessName
     $p=$null
     if($e.TryGetCurrentPattern([Windows.Automation.ValuePattern]::Pattern,[ref]$p)){$o.value=$p.Current.Value}
    }
    break
   } catch [System.Windows.Automation.ElementNotAvailableException] { Start-Sleep -Milliseconds 300 }
  }
  return $o
 }
 function Exercise([string]$name){
  Guard
  $info=[ordered]@{focus=(Field)}
  [ExplorerProbe]::Keys('su3cl3'); Start-Sleep -Milliseconds 1200
  $info.composed=(Field).value
  Shot "$name-composed"
  Guard
  [ExplorerProbe]::Key(0x28); Start-Sleep -Milliseconds 1000
  $info.candidates=[ExplorerProbe]::Candidates()
  Shot "$name-candidates"
  [ExplorerProbe]::Key(0x31); Start-Sleep -Milliseconds 800
  $info.selected=(Field).value
  # 傳統 Edit（網址列、重新命名）走 IMM32 相容層，組字顯示在浮動的組字窗，UIA 的值在送出前不變；
  # 以「按鍵沒有以英文字母進入欄位」判斷輸入法吃下了按鍵，才可以按 Enter 送出。
  $info.eaten=-not ($info.composed -match 'su|cl' -or $info.selected -match 'su|cl')
  $script:report.fields[$name]=$info
  return $info
 }
 function Has([string]$value){ return $value -and $value -match '[一-鿿]' }

 New-Item -ItemType Directory $folder | Out-Null
 if((Test-Path $zuin) -and -not(Test-Path $debugFlag)){New-Item -ItemType File $debugFlag|Out-Null;$createdFlag=$true}
 $logStart=if(Test-Path "$zuin\tsf.log"){(Get-Item "$zuin\tsf.log").Length}else{0}
 $hr=[ExplorerProbe]::Activate()
 Check ($hr -eq 0) 'explorer_profile_activated' ('0x{0:X8}' -f $hr)
 $leaf=Split-Path $folder -Leaf
 Start-Process explorer.exe -ArgumentList ('"'+$folder+'"')
 $deadline=(Get-Date).AddSeconds(20)
 do { Start-Sleep -Milliseconds 300; $window=[ExplorerProbe]::Find($leaf) } while($window -eq [IntPtr]::Zero -and (Get-Date) -lt $deadline)
 Check ($window -ne [IntPtr]::Zero) 'explorer_window_opened' $leaf
 $explorerPid=[ExplorerProbe]::Pid($window)
 Check ([ExplorerProbe]::Foreground($window)) 'explorer_window_focused' ([ExplorerProbe]::Class([ExplorerProbe]::GetForegroundWindow()))
 Start-Sleep -Milliseconds 800

 # 1. 網址列：Ctrl+L 進入編輯。未被輸入法吃掉的 Enter 會讓檔案總管去開 su3cl3，所以只在確定組字中才按 Enter。
 [ExplorerProbe]::Combo(@(0x11),0x4C); Start-Sleep -Milliseconds 800
 $a=Exercise 'address'
 if($a.eaten){ [ExplorerProbe]::Key(0x0D); Start-Sleep -Milliseconds 800; $a.committed=(Field).value; Shot 'address-committed' }
 [ExplorerProbe]::Key(0x1B); [ExplorerProbe]::Key(0x1B); Start-Sleep -Milliseconds 600

 # 2. 搜尋框：Ctrl+E。Enter 只會開始搜尋。
 Guard
 [ExplorerProbe]::Combo(@(0x11),0x45); Start-Sleep -Milliseconds 1000
 $s=Exercise 'search'
 [ExplorerProbe]::Key(0x0D); Start-Sleep -Milliseconds 1000
 $s.committed=(Field).value; Shot 'search-committed'
 [ExplorerProbe]::Key(0x1B); [ExplorerProbe]::Key(0x1B); Start-Sleep -Milliseconds 800

 # 3. 新增資料夾後的重新命名欄位：Ctrl+Shift+N；送出後以實際資料夾名稱核對。
 Guard
 if([ExplorerProbe]::Find($leaf) -eq [IntPtr]::Zero){
  # 搜尋後標題會變成搜尋結果；回到測試目錄。
  [ExplorerProbe]::Combo(@(0x11),0x4C); Start-Sleep -Milliseconds 500
  [Windows.Forms.SendKeys]::SendWait($folder); [ExplorerProbe]::Key(0x0D); Start-Sleep -Milliseconds 1200
 }
 [ExplorerProbe]::Combo(@(0x11,0x10),0x4E); Start-Sleep -Milliseconds 1500
 $n=Exercise 'rename'
 [ExplorerProbe]::Key(0x0D); Start-Sleep -Milliseconds 600
 if($n.eaten){ [ExplorerProbe]::Key(0x0D); Start-Sleep -Milliseconds 1000 }
 $n.folders=@(Get-ChildItem -LiteralPath $folder -Directory | ForEach-Object Name)
 Shot 'rename-committed'

 $report.dll=(Get-Process -Id $explorerPid | ForEach-Object { $_.Modules } | Where-Object ModuleName -eq '0c_tsf.dll' | Select-Object -First 1).FileName
 $text=if(Test-Path "$zuin\tsf.log"){Get-Content "$zuin\tsf.log" -Raw -Encoding UTF8}else{''}
 if($text.Length -gt $logStart){$text=$text.Substring([int]$logStart)}else{$text=''}
 ($text -split "`n" | Where-Object { $_ -match " pid=$explorerPid " }) -join "`n" | Set-Content "$ResultDir\explorer-tsf.log" -Encoding UTF8

 Check ($report.dll -eq $ExpectedDLL) 'explorer_new_dll_loaded' "$($report.dll)"
 Check ($a.eaten) 'address_keys_composed' "$($a.composed)"
 Check ((Has $a.committed) -and $a.committed -notmatch 'su|cl') 'address_commits_chinese' "$($a.committed)"
 Check ([bool]$a.candidates) 'address_candidates_visible' ''
 Check ([bool]$s.candidates) 'search_candidates_visible' ''
 Check ([bool]$n.candidates) 'rename_candidates_visible' ''
 Check ($s.composed -eq '你好') 'search_composes_chinese' "$($s.composed)"
 Check ((Has $s.committed) -and $s.committed -notmatch '[a-z]') 'search_commits_chinese' "$($s.committed)"
 Check ($n.eaten) 'rename_keys_composed' "$($n.composed)"
 Check (@($n.folders | Where-Object { Has $_ }).Count -eq 1) 'rename_commits_chinese' "$($n.folders -join ',')"
 $report.passed=$true
} catch {$report.error=$_.Exception.ToString()} finally {
 try {
  if($window -ne [IntPtr]::Zero){
   [ExplorerProbe]::Key(0x1B); [ExplorerProbe]::Key(0x1B)
   [ExplorerProbe]::PostMessage($window,0x10,[IntPtr]::Zero,[IntPtr]::Zero)|Out-Null
   Start-Sleep -Seconds 1
  }
 } catch {}
 if($createdFlag){Remove-Item $debugFlag -Force -ErrorAction SilentlyContinue}
 Remove-Item -LiteralPath $folder -Recurse -Force -ErrorAction SilentlyContinue
 $report|ConvertTo-Json -Depth 6|Set-Content "$ResultDir\explorer-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
