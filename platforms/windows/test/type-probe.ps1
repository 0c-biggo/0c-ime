# 更新 E2E 打字探針：在互動工作階段開一個 WPF 文字框，切到靈犀注音，送出按鍵並把結果寫成 JSON。
# 只向自己的測試視窗送鍵；由 update-e2e.ps1 以互動排程工作執行。
param([Parameter(Mandatory=$true)][string]$Keys, [Parameter(Mandatory=$true)][string]$Out, [int]$Hold = 0, [string]$LiveOut)
$ErrorActionPreference = 'Stop'
$result = [ordered]@{ keys = $Keys; text = $null; focus = $false; session = [Diagnostics.Process]::GetCurrentProcess().SessionId; pid = $PID; error = '' }
$win = $null
$liveTimer = $null
try {
    Add-Type -AssemblyName PresentationFramework, PresentationCore, WindowsBase, System.Windows.Forms
    Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class UpdateProbe {
  [ComImport, Guid("71C6E74C-0F28-11D8-A82A-00065B84435C"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
  interface IProfiles { [PreserveSig] int ActivateProfile(uint type, ushort language, ref Guid clsid, ref Guid profile, IntPtr hkl, uint flags); }
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr hwnd, out uint pid);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr hwnd);
  [DllImport("user32.dll")] static extern void keybd_event(byte key, byte scan, uint flags, UIntPtr extra);
  // 使用者的一次按鍵（Alt）讓本程序取得設定前景的權限，等同使用者點一下測試視窗。
  [DllImport("user32.dll")] static extern bool AttachThreadInput(uint from, uint to, bool attach);
  [DllImport("user32.dll")] static extern bool BringWindowToTop(IntPtr hwnd);
  [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
  // 前景被系統通知（例如 Windows Update）持有時：暫時與前景執行緒共用輸入狀態再切換，不去操作別人的通知。
  public static bool Claim(IntPtr hwnd) {
    uint pid; uint fg = GetWindowThreadProcessId(GetForegroundWindow(), out pid), me = GetCurrentThreadId();
    bool attached = fg != 0 && fg != me && AttachThreadInput(fg, me, true);
    keybd_event(0x12, 0, 0, UIntPtr.Zero); keybd_event(0x12, 0, 2, UIntPtr.Zero);
    BringWindowToTop(hwnd); bool ok = SetForegroundWindow(hwnd);
    if (attached) { AttachThreadInput(fg, me, false); }
    return ok;
  }
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] static extern bool GetWindowRect(IntPtr hwnd, out RECT r);
  [DllImport("user32.dll")] static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] static extern void mouse_event(uint flags, int dx, int dy, uint data, UIntPtr extra);
  // 最後手段：像使用者一樣點一下測試視窗自己的標題列。
  public static void ClickTitle(IntPtr hwnd) {
    RECT r; if (!GetWindowRect(hwnd, out r)) { return; }
    SetCursorPos((r.Left + r.Right) / 2, r.Top + 12);
    mouse_event(0x0002, 0, 0, 0, UIntPtr.Zero); mouse_event(0x0004, 0, 0, 0, UIntPtr.Zero);
  }
  public static uint ForegroundProcess() { uint pid; GetWindowThreadProcessId(GetForegroundWindow(), out pid); return pid; }
  public static void Activate() {
    object obj = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("33C53A50-F456-4884-B049-85FD643ECFED")));
    try {
      Guid clsid = new Guid("138C35D0-7F08-4CEF-BA90-C34C16301528"), profile = new Guid("EF62F743-9855-4C94-A658-1F0F98B7B840");
      Marshal.ThrowExceptionForHR(((IProfiles)obj).ActivateProfile(1, 0x0404, ref clsid, ref profile, IntPtr.Zero, 0x10000000));
    } finally { Marshal.FinalReleaseComObject(obj); }
  }
}
'@
    $win = New-Object Windows.Window
    $win.Title = '0c update E2E probe'; $win.Width = 500; $win.Height = 140; $win.Topmost = $true
    $box = New-Object Windows.Controls.TextBox
    $box.FontSize = 24
    $win.Content = $box
    $win.Show(); $win.Activate() | Out-Null; $box.Focus() | Out-Null
    $handle = (New-Object Windows.Interop.WindowInteropHelper($win)).Handle
    function Pump([int]$Milliseconds) {
        $frame = New-Object Windows.Threading.DispatcherFrame
        $timer = New-Object Windows.Threading.DispatcherTimer
        $timer.Interval = [TimeSpan]::FromMilliseconds($Milliseconds)
        $timer.add_Tick({ $frame.Continue = $false }.GetNewClosure())
        $timer.Start()
        try { [Windows.Threading.Dispatcher]::PushFrame($frame) } finally { $timer.Stop() }
    }
    Pump 500
    [UpdateProbe]::Activate()
    Pump 800
    for ($i = 0; $i -lt 5 -and [UpdateProbe]::GetForegroundWindow() -ne $handle; $i++) { if ($i -ge 2) { [UpdateProbe]::ClickTitle($handle) } else { [UpdateProbe]::Claim($handle) | Out-Null }; $win.Activate() | Out-Null; $box.Focus() | Out-Null; Pump 400 }
    $result.focus = [UpdateProbe]::GetForegroundWindow() -eq $handle
    if (-not $result.focus) {
        $fg = [UpdateProbe]::ForegroundProcess()
        $result.foreground = (Get-Process -Id $fg -ErrorAction SilentlyContinue).ProcessName
        throw "probe window is not in the foreground ($($result.foreground)); refusing to type"
    }
    [System.Windows.Forms.SendKeys]::SendWait($Keys)
    Pump 1500
    $result.text = $box.Text
    if ($Hold -gt 0) {
        # 保持在前景（例如讓測試在工作列輸入指示器上操作），結束時回報宿主仍在。
        Set-Content -LiteralPath ($Out + '.holding') 'holding'
        if ($LiveOut) {
            # 讓外部驗收可以核對同一個更新前宿主在更新後的實際文字；不自行補送按鍵。
            $liveTimer = New-Object Windows.Threading.DispatcherTimer
            $liveTimer.Interval = [TimeSpan]::FromSeconds(1)
            $liveTimer.add_Tick({
                $sample = [ordered]@{ utc = [DateTime]::UtcNow.ToString('o'); keys = $Keys; text = $box.Text; focus = ([UpdateProbe]::GetForegroundWindow() -eq $handle); session = $result.session; pid = $PID; error = '' }
                $sample | ConvertTo-Json | Set-Content -LiteralPath $LiveOut -Encoding UTF8
            }.GetNewClosure())
            $liveTimer.Start()
        }
        Pump ($Hold * 1000)
        $result.text = $box.Text
        $result.alive_after_hold = $true
    }
} catch { $result.error = $_.Exception.Message } finally {
    if ($liveTimer) { $liveTimer.Stop() }
    if ($win) { $win.Close() }
    $result | ConvertTo-Json | Set-Content -LiteralPath $Out -Encoding UTF8
}
