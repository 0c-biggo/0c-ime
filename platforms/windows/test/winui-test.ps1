# WinUI 3（Windows App SDK）文字框（#105）：以「相片」的搜尋框代表 23H2 起檔案總管的 XAML 網址列與搜尋框
# （同樣是 XAML island，焦點視窗 InputSiteWindowClass，TextBox 沒設 InputScope）。
# 只在測試機的互動工作階段執行（acceptance-test.ps1 -Suite winui）；需安裝 Windows App SDK 版的「相片」，拒絕接手已開啟的相片。
# 相片的 tsf.log 寫在真正的 %LOCALAPPDATA%\Zuin；只取本次測試、相片行程的紀錄。
param([Parameter(Mandatory=$true)][string]$ResultDir,[Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();error='';dll='';fields=[ordered]@{};edits=@()}
$zuin=Join-Path $env:LOCALAPPDATA 'Zuin'
$debugFlag=Join-Path $zuin 'tsf-debug'
$createdFlag=$false
$window=[IntPtr]::Zero
$proc=$null
try {
 if(Get-Process Photos,Microsoft.Photos -ErrorAction SilentlyContinue){throw 'Photos already running'}
 Add-Type -AssemblyName System.Windows.Forms,System.Drawing,UIAutomationClient,UIAutomationTypes
 Add-Type @'
using System; using System.Text; using System.Runtime.InteropServices;
public static class WinUIProbe {
 [ComImport, Guid("71C6E74C-0F28-11D8-A82A-00065B84435C"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
 interface IProfiles { [PreserveSig] int ActivateProfile(uint type, ushort language, ref Guid clsid, ref Guid profile, IntPtr hkl, uint flags); }
 [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
 [StructLayout(LayoutKind.Sequential)] public struct GUITHREADINFO { public int cbSize, flags; public IntPtr hwndActive, hwndFocus, hwndCapture, hwndMenuOwner, hwndMoveSize, hwndCaret; public RECT rcCaret; }
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("user32.dll")] public static extern bool BringWindowToTop(IntPtr h);
 [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int cmd);
 [DllImport("user32.dll")] public static extern bool AttachThreadInput(uint a, uint b, bool attach);
 [DllImport("kernel32.dll")] public static extern uint GetCurrentThreadId();
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetClassName(IntPtr h, StringBuilder s, int n);
 [DllImport("user32.dll")] public static extern bool GetGUIThreadInfo(uint tid, ref GUITHREADINFO info);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint msg, IntPtr w, IntPtr l);
 [DllImport("user32.dll")] public static extern IntPtr GetParent(IntPtr h);
 [DllImport("user32.dll")] static extern void keybd_event(byte k, byte s, uint f, UIntPtr e);
 public static string Class(IntPtr h){ var s=new StringBuilder(256); GetClassName(h,s,256); return s.ToString(); }
 public static string Chain(IntPtr h){ var sb=new StringBuilder(); while(h!=IntPtr.Zero){ sb.Append(Class(h)).Append(" < "); h=GetParent(h);} return sb.ToString(); }
 public static uint Pid(IntPtr h){ uint p; GetWindowThreadProcessId(h, out p); return p; }
 public static IntPtr Focus(IntPtr top){
  uint p; uint tid=GetWindowThreadProcessId(top, out p);
  var info=new GUITHREADINFO(); info.cbSize=Marshal.SizeOf(typeof(GUITHREADINFO));
  return GetGUIThreadInfo(tid, ref info) ? info.hwndFocus : IntPtr.Zero;
 }
 public static void Key(byte vk){ keybd_event(vk,0,0,UIntPtr.Zero); System.Threading.Thread.Sleep(30); keybd_event(vk,0,2,UIntPtr.Zero); System.Threading.Thread.Sleep(180); }
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
 public static int Activate(){
  object o = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("33C53A50-F456-4884-B049-85FD643ECFED")));
  try { Guid c=new Guid("138C35D0-7F08-4CEF-BA90-C34C16301528"), p=new Guid("EF62F743-9855-4C94-A658-1F0F98B7B840");
   return ((IProfiles)o).ActivateProfile(1,0x0404,ref c,ref p,IntPtr.Zero,0x20000000); } finally { Marshal.FinalReleaseComObject(o); }
 }
}
'@
 function Check([bool]$ok,[string]$name,[string]$detail){ if(-not $ok){throw "$name failed: $detail"}; $script:report.checks+=$name }
 function Shot([string]$name){
  $r=New-Object WinUIProbe+RECT; [WinUIProbe]::GetWindowRect($script:window,[ref]$r)|Out-Null
  $b=[Windows.Forms.Screen]::PrimaryScreen.Bounds
  $x=[Math]::Max($b.Left,$r.Left); $y=[Math]::Max($b.Top,$r.Top)
  $w=[Math]::Min($b.Right,$r.Right)-$x; $h=[Math]::Min($b.Bottom,$r.Top+420)-$y
  if($w -le 0 -or $h -le 0){return}
  $bmp=New-Object Drawing.Bitmap $w,$h
  $g=[Drawing.Graphics]::FromImage($bmp); $g.CopyFromScreen($x,$y,0,0,(New-Object Drawing.Size $w,$h))
  $bmp.Save((Join-Path $ResultDir "$name.png")); $g.Dispose(); $bmp.Dispose()
 }
 function Field {
  $e=[Windows.Automation.AutomationElement]::FocusedElement
  $f=[WinUIProbe]::Focus($script:window)
  $o=[ordered]@{hwnd_chain=[WinUIProbe]::Chain($f);uia_class='';uia_type='';framework='';pid=0;value=$null}
  if($e){
   $c=$e.Current
   $o.uia_class=$c.ClassName;$o.uia_type=$c.ControlType.ProgrammaticName;$o.framework=$c.FrameworkId;$o.pid=$c.ProcessId
   $p=$null
   if($e.TryGetCurrentPattern([Windows.Automation.ValuePattern]::Pattern,[ref]$p)){$o.value=$p.Current.Value}
  }
  return $o
 }
 if((Test-Path $zuin) -and -not(Test-Path $debugFlag)){New-Item -ItemType File $debugFlag|Out-Null;$createdFlag=$true}
 $logStart=if(Test-Path "$zuin\tsf.log"){(Get-Item "$zuin\tsf.log").Length}else{0}
 $hr=[WinUIProbe]::Activate()
 Check ($hr -eq 0) 'profile_activated' ('0x{0:X8}' -f $hr)
 Start-Process 'shell:AppsFolder\Microsoft.Windows.Photos_8wekyb3d8bbwe!App'
 $deadline=(Get-Date).AddSeconds(40)
 do { Start-Sleep -Milliseconds 500; $proc=Get-Process Photos,Microsoft.Photos -ErrorAction SilentlyContinue | ? { $_.MainWindowHandle -ne 0 } | select -First 1 } while(-not $proc -and (Get-Date) -lt $deadline)
 Check ($null -ne $proc) 'photos_started' ''
 $window=$proc.MainWindowHandle
 $report.window_class=[WinUIProbe]::Class($window)
 Start-Sleep -Seconds 6
 Check ([WinUIProbe]::Foreground($window)) 'photos_focused' ([WinUIProbe]::Class([WinUIProbe]::GetForegroundWindow()))
 $root=[Windows.Automation.AutomationElement]::FromHandle($window)
 $cond=New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::ControlTypeProperty,[Windows.Automation.ControlType]::Edit)
 $edits=$root.FindAll([Windows.Automation.TreeScope]::Descendants,$cond)
 foreach($e in $edits){ $report.edits+=[ordered]@{name=$e.Current.Name;class=$e.Current.ClassName;aid=$e.Current.AutomationId;framework=$e.Current.FrameworkId} }
 Check ($edits.Count -gt 0) 'photos_has_edit' ''
 $edit=$edits[0]
 $edit.SetFocus(); Start-Sleep -Milliseconds 800
 [WinUIProbe]::Foreground($window)|Out-Null
 $info=[ordered]@{focus=(Field)}
 [WinUIProbe]::Keys('su3cl3'); Start-Sleep -Milliseconds 1200
 $info.composed=(Field).value; Shot 'photos-composed'
 [WinUIProbe]::Key(0x28); Start-Sleep -Milliseconds 1000; Shot 'photos-candidates'
 [WinUIProbe]::Key(0x31); Start-Sleep -Milliseconds 800
 $info.selected=(Field).value
 [WinUIProbe]::Key(0x0D); Start-Sleep -Milliseconds 1000
 $info.committed=(Field).value; Shot 'photos-committed'
 $report.fields.photos=$info
 $proc.Refresh(); $report.dll=($proc.Modules | Where-Object ModuleName -eq '0c_tsf.dll' | Select-Object -First 1).FileName
 Check ($report.dll -eq $ExpectedDLL) 'winui_new_dll_loaded' "$($report.dll)"
 Check ($info.focus.hwnd_chain -like 'InputSiteWindowClass*') 'winui_island_focus' "$($info.focus.hwnd_chain)"
 Check ($info.composed -eq '你好') 'winui_composes_chinese' "$($info.composed)"
 Check ($info.selected -eq '你好') 'winui_selects_candidate' "$($info.selected)"
 Check ($info.committed -eq '你好') 'winui_commits_chinese' "$($info.committed)"
 $report.passed=$true
} catch {$report.error=$_.Exception.ToString()} finally {
 try { if($proc){ Stop-Process -Id $proc.Id -Force } } catch {}
 if($createdFlag){Remove-Item $debugFlag -Force -ErrorAction SilentlyContinue}
 try { $t=Get-Content "$zuin\tsf.log" -Raw -Encoding UTF8; if($proc -and $t.Length -gt $logStart){ ($t.Substring([int]$logStart) -split "`n" | Where-Object { $_ -match " pid=$($proc.Id) " }) -join "`n" | Set-Content "$ResultDir\winui-tsf.log" -Encoding UTF8 } } catch {}
 $report|ConvertTo-Json -Depth 6|Set-Content "$ResultDir\winui-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
