# Word 文件驗收；只能由隔離引擎／暫時註冊的 acceptance-test.ps1 -Suite word 啟動。
# 拒絕接手既有 Word，建立自己的空白文件，測後不存檔並退出自己的 Word。
param([Parameter(Mandatory=$true)][string]$ResultDir,[Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();error='';version='';dll='';document_policy=@()}
$word=$null;$doc=$null;$savedLocalAppData=$env:LOCALAPPDATA
try {
    if(Get-Process WINWORD -ErrorAction SilentlyContinue){throw 'Close the existing Word before this isolated test'}
    Add-Type -AssemblyName System.Windows.Forms,Accessibility
    Add-Type -ReferencedAssemblies Accessibility -TypeDefinition @'
using System; using System.Runtime.InteropServices; using System.Text;
public static class WordProbe {
 [ComImport, Guid("71C6E74C-0F28-11D8-A82A-00065B84435C"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
 interface IProfiles { [PreserveSig] int ActivateProfile(uint type,ushort language,ref Guid clsid,ref Guid profile,IntPtr hkl,uint flags); }
 public delegate bool EnumProc(IntPtr hwnd,IntPtr data);
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr hwnd);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hwnd,out uint pid);
 [DllImport("user32.dll")] public static extern bool EnumChildWindows(IntPtr hwnd,EnumProc callback,IntPtr data);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern int GetClassName(IntPtr hwnd,StringBuilder name,int size);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern IntPtr LoadKeyboardLayout(string name,uint flags);
 [DllImport("user32.dll")] static extern bool PostMessage(IntPtr hwnd,uint msg,IntPtr wp,IntPtr lp);
 [DllImport("user32.dll")] public static extern IntPtr GetKeyboardLayout(uint thread);
 [DllImport("user32.dll")] static extern void keybd_event(byte key,byte scan,uint flags,UIntPtr extra);
 [DllImport("oleacc.dll")] static extern int AccessibleObjectFromWindow(IntPtr hwnd,uint id,ref Guid iid,[MarshalAs(UnmanagedType.Interface)] out object value);
 public static string[] Policy(IntPtr hwnd) {
   var result=new System.Collections.Generic.List<string>();
   EnumChildWindows(hwnd,(child,data)=>{
     var name=new StringBuilder(128);GetClassName(child,name,128);
     if(name.ToString()=="_WwG") {
       Guid iid=new Guid("618736E0-3C3D-11CF-810C-00AA00389B71");object value;
       int hr=AccessibleObjectFromWindow(child,0xFFFFFFFC,ref iid,out value);
       if(hr>=0 && value!=null) {try {
         var a=(Accessibility.IAccessible)value;result.Add("_WwG role="+a.get_accRole(0)+" state="+a.get_accState(0)+" children="+a.accChildCount);
         object focus=a.accFocus;
         if(focus is Accessibility.IAccessible) {var f=(Accessibility.IAccessible)focus;result.Add("focused object role="+f.get_accRole(0)+" state="+f.get_accState(0));Marshal.ReleaseComObject(focus);}
         else if(focus is int) result.Add("focused child="+focus+" role="+a.get_accRole(focus)+" state="+a.get_accState(focus));
         for(int i=1;i<=Math.Min(a.accChildCount,5);i++){try{result.Add("child="+i+" role="+a.get_accRole(i)+" state="+a.get_accState(i));}catch(Exception){}}
       }catch(Exception e){result.Add(e.GetType().Name);}finally{Marshal.ReleaseComObject(value);}}
       else result.Add("_WwG accessibility HRESULT="+hr);
     }
     return true;
   },IntPtr.Zero);
   return result.ToArray();
 }
 public static void Activate(IntPtr hwnd) {
   IntPtr hkl=LoadKeyboardLayout("00000404",1);if(hkl==IntPtr.Zero || !PostMessage(hwnd,0x50,IntPtr.Zero,hkl))throw new Exception("Chinese keyboard unavailable");
   object obj=Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("33C53A50-F456-4884-B049-85FD643ECFED")));
   try {Guid c=new Guid("138C35D0-7F08-4CEF-BA90-C34C16301528"),p=new Guid("EF62F743-9855-4C94-A658-1F0F98B7B840");int hr=((IProfiles)obj).ActivateProfile(1,0x404,ref c,ref p,IntPtr.Zero,0x30000000);if(hr!=0)Marshal.ThrowExceptionForHR(hr<0?hr:unchecked((int)0x80004005));}
   finally {Marshal.FinalReleaseComObject(obj);}
 }
 public static void English(IntPtr hwnd) {
   IntPtr hkl=LoadKeyboardLayout("00000409",1);if(hkl==IntPtr.Zero || !PostMessage(hwnd,0x50,IntPtr.Zero,hkl))throw new Exception("English keyboard unavailable");
 }
 public static void Latin(string text) {
   foreach(char c in text){byte key=c=='='?(byte)0xBB:(byte)char.ToUpperInvariant(c);keybd_event(key,0,0,UIntPtr.Zero);System.Threading.Thread.Sleep(30);keybd_event(key,0,2,UIntPtr.Zero);System.Threading.Thread.Sleep(30);}
 }
 public static void Key(byte key) {keybd_event(key,0,0,UIntPtr.Zero);System.Threading.Thread.Sleep(30);keybd_event(key,0,2,UIntPtr.Zero);}
 public static void SelectAll() {keybd_event(0x11,0,0,UIntPtr.Zero);System.Threading.Thread.Sleep(30);Key(0x41);System.Threading.Thread.Sleep(30);keybd_event(0x11,0,2,UIntPtr.Zero);}
 // 左 Shift 一律以 VK_LSHIFT 與掃描碼 0x2A 送出（#206）。
 public static void LShift(bool down) {keybd_event(0xA0,0x2A,down?0u:2u,UIntPtr.Zero);}
 public static void LShiftTap() {LShift(true);System.Threading.Thread.Sleep(80);LShift(false);}
 public static void ShiftLeft() {LShift(true);System.Threading.Thread.Sleep(60);Key(0x25);System.Threading.Thread.Sleep(60);LShift(false);}
 [DllImport("user32.dll")] static extern bool SetCursorPos(int x,int y);
 [DllImport("user32.dll")] static extern void mouse_event(uint flags,int dx,int dy,uint data,UIntPtr extra);
 public static void ShiftClick(int x,int y) {LShift(true);System.Threading.Thread.Sleep(40);SetCursorPos(x,y);mouse_event(2,0,0,0,UIntPtr.Zero);System.Threading.Thread.Sleep(30);mouse_event(4,0,0,0,UIntPtr.Zero);System.Threading.Thread.Sleep(60);LShift(false);}
 public static void CtrlSpace() {keybd_event(0x11,0x1D,0,UIntPtr.Zero);System.Threading.Thread.Sleep(60);keybd_event(0x20,0x39,0,UIntPtr.Zero);System.Threading.Thread.Sleep(60);keybd_event(0x20,0x39,2,UIntPtr.Zero);System.Threading.Thread.Sleep(60);keybd_event(0x11,0x1D,2,UIntPtr.Zero);}
}
'@
    $env:LOCALAPPDATA=$ResultDir
    New-Item -ItemType Directory -Force "$ResultDir\Zuin" | Out-Null
    New-Item -ItemType File -Force "$ResultDir\Zuin\tsf-debug" | Out-Null
    $word=New-Object -ComObject Word.Application
    $report.version=[string]$word.Version
    $word.Visible=$true
    $doc=$word.Documents.Add()
    $word.Activate()
    $hwnd=[IntPtr]$word.ActiveWindow.Hwnd
    $pidWord=[uint32]0
    $tidWord=[WordProbe]::GetWindowThreadProcessId($hwnd,[ref]$pidWord)
    $report.pid=$pidWord
    $report.build=(Get-Process -Id $pidWord).MainModule.FileVersionInfo.FileVersion
    function Pump([int]$ms){$clock=[Diagnostics.Stopwatch]::StartNew();while($clock.ElapsedMilliseconds -lt $ms){[Windows.Forms.Application]::DoEvents();Start-Sleep -Milliseconds 10}}
    Pump 1000
    [WordProbe]::SetForegroundWindow($hwnd) | Out-Null
    function Send([string]$keys){
        if([WordProbe]::GetForegroundWindow() -ne $hwnd){throw 'Word test document lost focus'}
        if($keys -match '^[a-z0-9=]+$'){[WordProbe]::Latin($keys)}
        elseif($keys -eq '^a'){[WordProbe]::SelectAll()}
        elseif($keys -eq '^{SPACE}'){[WordProbe]::CtrlSpace()}
        elseif($keys -eq '{ENTER}'){[WordProbe]::Key(13)}
        elseif($keys -eq '{ESC}'){[WordProbe]::Key(27)}
        elseif($keys -eq '{BACKSPACE}'){[WordProbe]::Key(8)}
        elseif($keys -eq '{LSHIFT}'){[WordProbe]::LShiftTap()}
        elseif($keys -eq '+{LEFT}'){[WordProbe]::ShiftLeft()}
        elseif($keys -eq '+{CLICKEND}'){
            # 按著左 Shift 在文件最後一個字右邊點一下（Shift＋點選延伸選取）。
            $end=$doc.Content;$end.SetRange($end.End-1,$end.End-1)
            $l=0;$t=0;$w=0;$h=0;$word.ActiveWindow.GetPoint([ref]$l,[ref]$t,[ref]$w,[ref]$h,$end)
            [WordProbe]::ShiftClick($l+40,$t+[int]($h/2))
        }
        else{throw "Unsupported Word test key $keys"}
        Pump 500
    }
    function Expect([string]$text,[string]$name){$actual=([string]$doc.Content.Text).TrimEnd([char[]]@([char]13,[char]7));if($actual -ne $text){throw "$name expected [$text], got [$actual]"};$report.checks+=$name}
    function ClearDocument { Send '^a';Send '{BACKSPACE}';if(([string]$doc.Content.Text).TrimEnd([char[]]@([char]13,[char]7)) -ne ''){throw 'Word test document could not be cleared'} }
    [WordProbe]::English($hwnd);Pump 800
    $report.native_layout=[WordProbe]::GetKeyboardLayout($tidWord).ToInt64().ToString('X')
    if(([WordProbe]::GetKeyboardLayout($tidWord).ToInt64() -band 0xFFFF) -ne 0x409){throw 'Word did not switch to the English keyboard'}
    Send '123456';Expect '123456' 'word_native_keyboard_input'
    # 原生鍵盤與 TSF 分別使用自己的空白文件，避免控制案例影響中文驗收。
    $save=0;$doc.Close([ref]$save)
    [Runtime.InteropServices.Marshal]::FinalReleaseComObject($doc) | Out-Null
    $doc=$word.Documents.Add();$word.Activate()
    $hwnd=[IntPtr]$word.ActiveWindow.Hwnd
    [WordProbe]::SetForegroundWindow($hwnd) | Out-Null
    $tidWord=[WordProbe]::GetWindowThreadProcessId($hwnd,[ref]$pidWord)
    [WordProbe]::Activate($hwnd);Pump 800
    $report.ime_layout=[WordProbe]::GetKeyboardLayout($tidWord).ToInt64().ToString('X')
    $report.dll=((Get-Process -Id $pidWord).Modules | Where-Object ModuleName -eq '0c_tsf.dll' | Select-Object -First 1).FileName
    if($report.dll -ne $ExpectedDLL){throw 'Word loaded an unexpected TSF DLL'}
    $report.checks+='word_expected_dll'
    $report.document_policy=@([WordProbe]::Policy($hwnd))
    Send 'su3cl3';Expect '你好' 'word_preedit'
    Send '{ENTER}';Expect '你好' 'word_commit'
    ClearDocument
    Send 'su3';Send '{ESC}';Expect '' 'word_cancel'
    Send 'su3';Send '{ENTER}';Send 'su3';Send '{ENTER}';Expect '你你' 'word_consecutive_commits'
    ClearDocument
    Send '==';Expect '=' 'word_empty_emoji_double_equals'
    ClearDocument
    # 系統的輸入法開關熱鍵 Ctrl＋Space：Word 收到熱鍵當下不給同步鎖，0.2.0 沒有切換、還清掉組字狀態（#164）。
    Send 'su3';Send '{ENTER}';Send '^{SPACE}';Send 'ab';Expect '你ab' 'word_ctrl_space_english'
    Send '^{SPACE}';Send 'su3';Send '{ENTER}';Expect '你ab你' 'word_ctrl_space_back'
    ClearDocument
    # #206：Windows 預設左右 Shift：單按左 Shift 切英文再切回；按著左 Shift 點文件（延伸選取）、Shift＋← 選取都不切換。
    Send 'su3';Send '{ENTER}';Send '{LSHIFT}';Send 'ab';Expect '你ab' 'word_left_shift_english'
    Send '{LSHIFT}';Send 'su3';Send '{ENTER}';Expect '你ab你' 'word_left_shift_back'
    Send '+{CLICKEND}';Send 'su3';Send '{ENTER}';Expect '你ab你你' 'word_shift_click_no_toggle'
    Send '+{LEFT}';Send 'su3';Send '{ENTER}';Expect '你ab你你' 'word_shift_left_select_no_toggle'
    ClearDocument
    $table=$doc.Tables.Add($word.Selection.Range,1,1)
    $table.Cell(1,1).Range.Select();$word.Selection.Collapse(1)
    Send 'su3cl3';Send '{ENTER}';Expect '你好' 'word_table_input'
    # 自己的文件改成唯讀後，不能把按鍵送到引擎或寫入文件。
    $protection=3;$doc.Protect([ref]$protection)
    Pump 500
    $before=([string]$doc.Content.Text)
    function OwnKeyEvents { @(Get-Content "$ResultDir\Zuin\tsf.log" | Where-Object {$_ -match " pid=$pidWord " -and $_ -match 'OnTestKeyDown vk=|snapshot ok='}) }
    $eventsBefore=@(OwnKeyEvents).Count
    $report.readonly_policy=@([WordProbe]::Policy($hwnd))
    Send 'su3'
    if(([string]$doc.Content.Text) -ne $before -or @(OwnKeyEvents).Count -ne $eventsBefore){throw 'Protected Word document accepted engine input'}
    $report.checks+='word_readonly_no_engine_input'
    $doc.Unprotect()
    $report.passed=$true
} catch {$report.error=$_.Exception.ToString()} finally {
    foreach($item in @(@($doc,'Close'),@($word,'Quit'))){
        $object=$item[0]
        if($object){
            try {$save=0;if($item[1] -eq 'Close'){$object.Close([ref]$save)}else{$object.Quit([ref]$save)}}
            catch {$report.passed=$false;$report.error+="; cleanup: $($_.Exception.Message)"}
            finally {[Runtime.InteropServices.Marshal]::FinalReleaseComObject($object) | Out-Null}
        }
    }
    $env:LOCALAPPDATA=$savedLocalAppData
    $report | ConvertTo-Json -Depth 5 | Set-Content "$ResultDir\word-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
