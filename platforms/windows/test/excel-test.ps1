# Excel 儲存格驗收（#189）；只能由隔離引擎／暫時註冊的 acceptance-test.ps1 -Suite excel 啟動。
# 拒絕接手既有 Excel：以 COM 建立自己的空白活頁簿，測後不存檔並退出自己的 Excel。
# 儲存格還沒進入編輯（「就緒」）時，第一個鍵要由輸入法組字，不能被 Excel 當成字元直接打進儲存格（第一次 → 2億一次）。
param([Parameter(Mandatory=$true)][string]$ResultDir,[Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference='Stop'
$report=[ordered]@{passed=$false;checks=@();error='';version='';dll='';cells=[ordered]@{}}
$xl=$null;$book=$null;$savedLocalAppData=$env:LOCALAPPDATA
try {
    if(Get-Process EXCEL -ErrorAction SilentlyContinue){throw 'Close the existing Excel before this isolated test'}
    Add-Type -AssemblyName System.Windows.Forms,System.Drawing
    Add-Type -TypeDefinition @'
using System; using System.Runtime.InteropServices;
public static class ExcelProbe {
 [ComImport, Guid("71C6E74C-0F28-11D8-A82A-00065B84435C"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
 interface IProfiles { [PreserveSig] int ActivateProfile(uint type,ushort language,ref Guid clsid,ref Guid profile,IntPtr hkl,uint flags); }
 [StructLayout(LayoutKind.Sequential)] public struct RECT {public int l,t,r,b;}
 [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
 [DllImport("user32.dll")] static extern bool SetForegroundWindow(IntPtr hwnd);
 [DllImport("user32.dll")] static extern bool BringWindowToTop(IntPtr hwnd);
 [DllImport("user32.dll")] static extern bool AttachThreadInput(uint a,uint b,bool attach);
 [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hwnd,out uint pid);
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hwnd,out RECT rect);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern IntPtr LoadKeyboardLayout(string name,uint flags);
 [DllImport("user32.dll")] static extern bool PostMessage(IntPtr hwnd,uint msg,IntPtr wp,IntPtr lp);
 [DllImport("user32.dll")] static extern void keybd_event(byte key,byte scan,uint flags,UIntPtr extra);
 public static void Front(IntPtr hwnd){uint pid;uint thread=GetWindowThreadProcessId(GetForegroundWindow(),out pid);uint me=GetCurrentThreadId();AttachThreadInput(me,thread,true);BringWindowToTop(hwnd);SetForegroundWindow(hwnd);AttachThreadInput(me,thread,false);}
 public static void Activate(IntPtr hwnd){
   IntPtr hkl=LoadKeyboardLayout("00000404",1);if(hkl==IntPtr.Zero || !PostMessage(hwnd,0x50,IntPtr.Zero,hkl))throw new Exception("Chinese keyboard unavailable");
   object obj=Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("33C53A50-F456-4884-B049-85FD643ECFED")));
   try {Guid c=new Guid("138C35D0-7F08-4CEF-BA90-C34C16301528"),p=new Guid("EF62F743-9855-4C94-A658-1F0F98B7B840");int hr=((IProfiles)obj).ActivateProfile(1,0x404,ref c,ref p,IntPtr.Zero,0x30000000);if(hr!=0)Marshal.ThrowExceptionForHR(hr<0?hr:unchecked((int)0x80004005));}
   finally {Marshal.FinalReleaseComObject(obj);}
 }
 // 字母、數字與空白；按鍵之間留時間讓 Excel 進入編輯。
 public static void Keys(string text){foreach(char c in text){byte key=(byte)char.ToUpperInvariant(c);keybd_event(key,0,0,UIntPtr.Zero);System.Threading.Thread.Sleep(40);keybd_event(key,0,2,UIntPtr.Zero);System.Threading.Thread.Sleep(80);}}
 public static void Key(byte key){keybd_event(key,0,0,UIntPtr.Zero);System.Threading.Thread.Sleep(40);keybd_event(key,0,2,UIntPtr.Zero);}
}
'@
    $env:LOCALAPPDATA=$ResultDir
    New-Item -ItemType Directory -Force "$ResultDir\Zuin" | Out-Null
    New-Item -ItemType File -Force "$ResultDir\Zuin\tsf-debug" | Out-Null
    $xl=New-Object -ComObject Excel.Application
    $report.version=[string]$xl.Version
    $xl.Visible=$true
    $xl.DisplayAlerts=$false
    $book=$xl.Workbooks.Add()
    $sheet=$book.Worksheets.Item(1)
    $hwnd=[IntPtr]$xl.Hwnd
    $pidExcel=[uint32]0
    [ExcelProbe]::GetWindowThreadProcessId($hwnd,[ref]$pidExcel) | Out-Null
    $report.pid=$pidExcel
    $report.build=(Get-Process -Id $pidExcel).MainModule.FileVersionInfo.FileVersion
    function Pump([int]$ms){$clock=[Diagnostics.Stopwatch]::StartNew();while($clock.ElapsedMilliseconds -lt $ms){[Windows.Forms.Application]::DoEvents();Start-Sleep -Milliseconds 10}}
    function Shot([string]$name){
        $r=New-Object ExcelProbe+RECT;[ExcelProbe]::GetWindowRect($hwnd,[ref]$r) | Out-Null
        $w=[Math]::Min($r.r-$r.l,900);$h=[Math]::Min($r.b-$r.t,420)
        if($w -le 0 -or $h -le 0){return}
        $bmp=New-Object Drawing.Bitmap $w,$h;$g=[Drawing.Graphics]::FromImage($bmp)
        $g.CopyFromScreen($r.l,$r.t,0,0,$bmp.Size);$g.Dispose();$bmp.Save("$ResultDir\$name.png");$bmp.Dispose()
    }
    $xl.WindowState=-4137 # xlMaximized
    Pump 1500
    [ExcelProbe]::Front($hwnd);Pump 800
    if([ExcelProbe]::GetForegroundWindow() -ne $hwnd){throw 'Excel did not become the foreground window'}
    [ExcelProbe]::Activate($hwnd);Pump 800
    $report.dll=((Get-Process -Id $pidExcel).Modules | Where-Object ModuleName -eq '0c_tsf.dll' | Select-Object -First 1).FileName
    if($report.dll -ne $ExpectedDLL){throw "Excel loaded an unexpected TSF DLL: $($report.dll)"}
    $report.checks+='excel_expected_dll'
    function Send([string]$keys){
        if([ExcelProbe]::GetForegroundWindow() -ne $hwnd){throw 'Excel test workbook lost focus'}
        if($keys -eq '{ENTER}'){[ExcelProbe]::Key(13)}
        elseif($keys -eq '{F2}'){[ExcelProbe]::Key(0x71)}
        elseif($keys -match '^[a-z0-9 ]+$'){[ExcelProbe]::Keys($keys)}
        else{throw "Unsupported Excel test key $keys"}
        Pump 500
    }
    # 選一格（就緒模式），打完按 Enter 送出組字、再按 Enter 結束儲存格編輯，Excel 回到就緒才讀值。
    function Cell([string]$address,[string]$keys,[string]$expected,[string]$name,[switch]$Edit){
        $sheet.Range($address).Select() | Out-Null;Pump 400
        if($Edit){Send '{F2}'}
        Send $keys
        Shot $name
        Send '{ENTER}';Send '{ENTER}';Pump 300
        $actual=[string]$sheet.Range($address).Text
        $report.cells[$address]=$actual
        if($actual -ne $expected){throw "$name expected [$expected], got [$actual]"}
        $report.checks+=$name
    }
    # 客戶的兩個例子：第一鍵是聲母數字 2（ㄉ）與韻母數字 0（ㄢ）。
    Cell 'A1' '2u4u h4' '第一次' 'excel_ready_first_key_digit_initial'
    Cell 'A2' '0 0 ' '安安' 'excel_ready_first_key_digit_final'
    # 第一鍵是字母，與已在編輯中的儲存格（F2）對照。
    Cell 'A3' 'su3cl3' '你好' 'excel_ready_first_key_letter'
    Cell 'A4' 'su3cl3' '你好' 'excel_edit_mode' -Edit
    $report.passed=$true
} catch {$report.error=$_.Exception.ToString()} finally {
    if($book){try{$book.Close($false)}catch{$report.passed=$false;$report.error+="; cleanup: $($_.Exception.Message)"};[Runtime.InteropServices.Marshal]::FinalReleaseComObject($book) | Out-Null}
    if($xl){try{$xl.Quit()}catch{$report.passed=$false;$report.error+="; cleanup: $($_.Exception.Message)"};[Runtime.InteropServices.Marshal]::FinalReleaseComObject($xl) | Out-Null}
    $env:LOCALAPPDATA=$savedLocalAppData
    $report | ConvertTo-Json -Depth 5 | Set-Content "$ResultDir\excel-result.json" -Encoding UTF8
}
if(-not $report.passed){exit 1}
