# OneNote 頁面驗收（#163）；只能由隔離引擎／暫時註冊的 acceptance-test.ps1 -Suite onenote 啟動。
# 拒絕接手既有 OneNote：以 OneNote 的 COM 介面在結果目錄建立自己的本機筆記本與新頁面，測後關閉筆記本並退出 OneNote。
# 需先開啟一次 OneNote 關掉首次使用的登入視窗。
param([Parameter(Mandatory=$true)][string]$ResultDir,[Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();error='';dll='';focus='';focus_policy=''}
$on=$null;$notebook=$null;$process=$null;$savedLocalAppData=$env:LOCALAPPDATA
try {
    if(Get-Process ONENOTE -ErrorAction SilentlyContinue){throw 'Close the existing OneNote before this isolated test'}
    $exe=@('C:\Program Files\Microsoft Office\root\Office16\ONENOTE.EXE','C:\Program Files (x86)\Microsoft Office\root\Office16\ONENOTE.EXE')|Where-Object {Test-Path $_}|Select-Object -First 1
    if(-not $exe){throw 'OneNote desktop is not installed'}
    Add-Type -AssemblyName System.Windows.Forms,Accessibility
    Add-Type -ReferencedAssemblies Accessibility -TypeDefinition @'
using System; using System.Runtime.InteropServices; using System.Text;
public static class OneNoteProbe {
 [ComImport, Guid("71C6E74C-0F28-11D8-A82A-00065B84435C"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
 interface IProfiles { [PreserveSig] int ActivateProfile(uint type,ushort language,ref Guid clsid,ref Guid profile,IntPtr hkl,uint flags); }
 public delegate bool EnumProc(IntPtr hwnd,IntPtr data);
 [StructLayout(LayoutKind.Sequential)] struct RECT {public int l,t,r,b;}
 [StructLayout(LayoutKind.Sequential)] struct GUITHREADINFO {public int cbSize,flags; public IntPtr active,focus,capture,menuOwner,moveSize,caret; public RECT rc;}
 [DllImport("user32.dll")] static extern bool GetGUIThreadInfo(uint thread,ref GUITHREADINFO info);
 [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc callback,IntPtr data);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hwnd,out uint pid);
 [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr hwnd);
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] static extern bool SetForegroundWindow(IntPtr hwnd);
 [DllImport("user32.dll")] static extern bool BringWindowToTop(IntPtr hwnd);
 [DllImport("user32.dll")] static extern bool AttachThreadInput(uint a,uint b,bool attach);
 [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int GetClassName(IntPtr hwnd,StringBuilder name,int size);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern IntPtr LoadKeyboardLayout(string name,uint flags);
 [DllImport("user32.dll")] static extern bool PostMessage(IntPtr hwnd,uint msg,IntPtr wp,IntPtr lp);
 [DllImport("user32.dll")] static extern void keybd_event(byte key,byte scan,uint flags,UIntPtr extra);
 [DllImport("oleacc.dll")] static extern int AccessibleObjectFromWindow(IntPtr hwnd,uint id,ref Guid iid,[MarshalAs(UnmanagedType.Interface)] out object value);
 public static string ClassOf(IntPtr hwnd){var name=new StringBuilder(256);GetClassName(hwnd,name,256);return name.ToString();}
 public static IntPtr Frame(uint pid){IntPtr found=IntPtr.Zero;EnumWindows((h,d)=>{uint p;GetWindowThreadProcessId(h,out p);if(p==pid&&IsWindowVisible(h)&&ClassOf(h)=="Framework::CFrame"){found=h;return false;}return true;},IntPtr.Zero);return found;}
 public static IntPtr Focus(IntPtr top){uint pid;uint thread=GetWindowThreadProcessId(top,out pid);var info=new GUITHREADINFO();info.cbSize=Marshal.SizeOf(info);GetGUIThreadInfo(thread,ref info);return info.focus;}
 public static void Front(IntPtr hwnd){uint pid;uint thread=GetWindowThreadProcessId(GetForegroundWindow(),out pid);uint me=GetCurrentThreadId();AttachThreadInput(me,thread,true);BringWindowToTop(hwnd);SetForegroundWindow(hwnd);AttachThreadInput(me,thread,false);}
 // 只讀 MSAA 角色與狀態（#163 的判斷依據），不讀 Name／Value。
 public static string Policy(IntPtr hwnd){
   Guid iid=new Guid("618736E0-3C3D-11CF-810C-00AA00389B71");object value;
   int hr=AccessibleObjectFromWindow(hwnd,0xFFFFFFFC,ref iid,out value);
   if(hr<0||value==null)return "accessibility HRESULT="+hr;
   try {
     var a=(Accessibility.IAccessible)value;string result="role="+a.get_accRole(0)+" state="+a.get_accState(0);
     object focus=a.accFocus;
     if(focus is Accessibility.IAccessible){var f=(Accessibility.IAccessible)focus;result+=" focused role="+f.get_accRole(0)+" state="+f.get_accState(0);Marshal.ReleaseComObject(focus);}
     else if(focus is int) result+=" focused child="+focus;
     return result;
   } finally {Marshal.ReleaseComObject(value);}
 }
 public static void Activate(IntPtr hwnd){
   IntPtr hkl=LoadKeyboardLayout("00000404",1);if(hkl==IntPtr.Zero || !PostMessage(hwnd,0x50,IntPtr.Zero,hkl))throw new Exception("Chinese keyboard unavailable");
   object obj=Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("33C53A50-F456-4884-B049-85FD643ECFED")));
   try {Guid c=new Guid("138C35D0-7F08-4CEF-BA90-C34C16301528"),p=new Guid("EF62F743-9855-4C94-A658-1F0F98B7B840");int hr=((IProfiles)obj).ActivateProfile(1,0x404,ref c,ref p,IntPtr.Zero,0x30000000);if(hr!=0)Marshal.ThrowExceptionForHR(hr<0?hr:unchecked((int)0x80004005));}
   finally {Marshal.FinalReleaseComObject(obj);}
 }
 public static void Latin(string text){foreach(char c in text){byte key=(byte)char.ToUpperInvariant(c);keybd_event(key,0,0,UIntPtr.Zero);System.Threading.Thread.Sleep(30);keybd_event(key,0,2,UIntPtr.Zero);System.Threading.Thread.Sleep(30);}}
 public static void Key(byte key){keybd_event(key,0,0,UIntPtr.Zero);System.Threading.Thread.Sleep(30);keybd_event(key,0,2,UIntPtr.Zero);}
 public static void RightShift(){keybd_event(0xA1,0x36,0,UIntPtr.Zero);System.Threading.Thread.Sleep(60);keybd_event(0xA1,0x36,2,UIntPtr.Zero);}
 public static void CtrlSpace(){keybd_event(0x11,0x1D,0,UIntPtr.Zero);System.Threading.Thread.Sleep(60);keybd_event(0x20,0x39,0,UIntPtr.Zero);System.Threading.Thread.Sleep(60);keybd_event(0x20,0x39,2,UIntPtr.Zero);System.Threading.Thread.Sleep(60);keybd_event(0x11,0x1D,2,UIntPtr.Zero);}
}
'@
    $env:LOCALAPPDATA=$ResultDir
    New-Item -ItemType Directory -Force "$ResultDir\Zuin" | Out-Null
    New-Item -ItemType File -Force "$ResultDir\Zuin\tsf-debug" | Out-Null
    # 由本工作直接啟動，OneNote 才會繼承上面的 LOCALAPPDATA（tsf.log 寫進結果目錄）。
    $process=Start-Process $exe -PassThru
    $clock=[Diagnostics.Stopwatch]::StartNew()
    function Pump([int]$ms){$c=[Diagnostics.Stopwatch]::StartNew();while($c.ElapsedMilliseconds -lt $ms){[Windows.Forms.Application]::DoEvents();Start-Sleep -Milliseconds 10}}
    do {Pump 500;$hwnd=[OneNoteProbe]::Frame([uint32]$process.Id)} while($hwnd -eq [IntPtr]::Zero -and $clock.Elapsed.TotalSeconds -lt 60)
    if($hwnd -eq [IntPtr]::Zero){throw 'OneNote window did not appear (finish its first-run sign-in prompt once)'}
    $on=New-Object -ComObject OneNote.Application
    $notebook='';$section='';$page=''
    $on.OpenHierarchy("$ResultDir\notebook",'',[ref]$notebook,1)
    $on.OpenHierarchy('Test.one',$notebook,[ref]$section,3)
    $on.CreateNewPage($section,[ref]$page,0)
    $on.NavigateTo($page,'',$false);Pump 2000
    [OneNoteProbe]::Front($hwnd);Pump 800
    if([OneNoteProbe]::GetForegroundWindow() -ne $hwnd){throw 'OneNote did not take focus'}
    [OneNoteProbe]::Activate($hwnd);Pump 1000
    $report.dll=((Get-Process -Id $process.Id).Modules | Where-Object ModuleName -eq '0c_tsf.dll' | Select-Object -First 1).FileName
    if($report.dll -ne $ExpectedDLL){throw 'OneNote loaded an unexpected TSF DLL'}
    $report.checks+='onenote_expected_dll'
    $focus=[OneNoteProbe]::Focus($hwnd)
    $report.focus=[OneNoteProbe]::ClassOf($focus)
    $report.focus_policy=[OneNoteProbe]::Policy($focus)
    # 頁面內容（標題與內文）的文字：CDATA 可能跨行、夾著標語言的 span。
    function Text {$xml='';$on.GetPageContent($page,[ref]$xml,0);((([regex]::Matches($xml,'(?s)<one:T><!\[CDATA\[(.*?)\]\]></one:T>')|ForEach-Object {$_.Groups[1].Value}) -join '') -replace '<[^>]+>','')}
    function Send([string]$keys){
        if([OneNoteProbe]::GetForegroundWindow() -ne $hwnd){throw 'OneNote test page lost focus'}
        switch($keys){
            '{ENTER}' {[OneNoteProbe]::Key(13)}
            '{RSHIFT}' {[OneNoteProbe]::RightShift()}
            '^{SPACE}' {[OneNoteProbe]::CtrlSpace()}
            default {if($keys -notmatch '^[a-z0-9]+$'){throw "Unsupported OneNote test key $keys"};[OneNoteProbe]::Latin($keys)}
        }
        Pump 600
    }
    function Expect([string]$text,[string]$name){$actual=Text;if($actual -ne $text){throw "$name expected [$text], got [$actual]"};$report.checks+=$name}
    # 頁面標題：0.2.0 在這裡只打得出 Su3cl3（#163）。
    Send 'su3cl3';Send '{ENTER}';Expect '你好' 'onenote_title_commit'
    # 右 Shift 切英文、再切回注音（客戶回報左右 Shift 都切不了）。
    Send '{RSHIFT}';Send 'ab';Expect '你好ab' 'onenote_shift_english'
    Send '{RSHIFT}';Send 'su3';Send '{ENTER}';Expect '你好ab你' 'onenote_shift_back'
    # 系統的輸入法開關熱鍵 Ctrl＋Space（#117、#164）。
    Send '^{SPACE}';Send 'cd';Expect '你好ab你cd' 'onenote_ctrl_space_english'
    Send '^{SPACE}';Send 'su3';Send '{ENTER}';Expect '你好ab你cd你' 'onenote_ctrl_space_back'
    $report.passed=$true
} catch {$report.error=$_.Exception.ToString()} finally {
    if($on){
        try {if($notebook){$on.CloseNotebook($notebook)}} catch {$report.error+="; close notebook: $($_.Exception.Message)"}
        [Runtime.InteropServices.Marshal]::FinalReleaseComObject($on) | Out-Null
    }
    if($process -and -not $process.HasExited){
        $process.CloseMainWindow() | Out-Null
        if(-not $process.WaitForExit(15000)){Stop-Process -Id $process.Id -Force}
    }
    $env:LOCALAPPDATA=$savedLocalAppData
    $report | ConvertTo-Json -Depth 5 | Set-Content "$ResultDir\onenote-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
