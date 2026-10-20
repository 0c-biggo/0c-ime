# 輸入指示器選單探針（#13）：在已切到靈犀注音的 WPF 視窗執行緒中，經 ITfLangBarItemMgr 取得本輸入法的
# GUID_LBI_INPUTMODE 按鈕，如同系統介面一樣呼叫 GetText／InitMenu／OnMenuSelect／OnClick，結果寫成 JSON。
# 由 update-e2e.ps1 以互動排程工作執行；只操作自己的測試視窗。
param([Parameter(Mandatory=$true)][string]$Out, [string]$Select = '', [switch]$ShotAfterSelect, [string]$ThenSet = '', [switch]$Toggle, [switch]$ShiftToggle, [string]$TypeAfter = '', [string]$ToggleBackThen = '', [switch]$RightClick)
$ErrorActionPreference = 'Stop'
$result = [ordered]@{ menu_after_set = @(); text_after_set = $null; tooltip_after_set = $null; text_after_select = $null; tooltip_after_select = $null; menu_after_select = @(); popup = @(); alive_after_popup = $false; text_after_back = $null; typed_after_back = $null; text = $null; tooltip = $null; clsid = $null; menu = @(); selected = $null; text_after_toggle = $null; typed = $null; error = '' }
$win = $null
try {
    Add-Type -AssemblyName PresentationFramework, PresentationCore, WindowsBase, System.Windows.Forms
    Add-Type @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
[StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)]
public struct TF_LANGBARITEMINFO { public Guid clsidService; public Guid guidItem; public uint dwStyle; public uint ulSort;
  [MarshalAs(UnmanagedType.ByValTStr, SizeConst=32)] public string szDescription; }
[StructLayout(LayoutKind.Sequential)] public struct POINT { public int x, y; }
[ComImport, Guid("6f8a98e4-aaa0-4f15-8c5b-07e0df0a3dd8"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
public interface ITfMenu { [PreserveSig] int AddMenuItem(uint id, uint flags, IntPtr bmp, IntPtr mask, IntPtr text, uint len, IntPtr sub); }
[ComImport, Guid("28c7f1d0-de25-11d2-afdd-00105a2799b5"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
public interface ITfLangBarItemButton {
  void GetInfo(out TF_LANGBARITEMINFO info); uint GetStatus(); void Show(int show); [return: MarshalAs(UnmanagedType.BStr)] string GetTooltipString();
  void OnClick(int click, POINT pt, IntPtr area); void InitMenu(ITfMenu menu); void OnMenuSelect(uint id); IntPtr GetIcon(); [return: MarshalAs(UnmanagedType.BStr)] string GetText(); }
[ComImport, Guid("ba468c55-9956-4fb1-a59d-52a7dd7cc6aa"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
public interface ITfLangBarItemMgr { void EnumItems(out IntPtr e); [return: MarshalAs(UnmanagedType.IUnknown)] object GetItem(ref Guid guid); }
[ComVisible(true), ClassInterface(ClassInterfaceType.None)]
public class Menu : ITfMenu {
  public List<string> Items = new List<string>();
  public int AddMenuItem(uint id, uint flags, IntPtr bmp, IntPtr mask, IntPtr text, uint len, IntPtr sub) {
    Items.Add(id + "|" + flags + "|" + (text == IntPtr.Zero ? "" : Marshal.PtrToStringUni(text, (int)len))); return 0; }
}
public static class MenuProbe {
  [ComImport, Guid("71C6E74C-0F28-11D8-A82A-00065B84435C"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
  interface IProfiles { [PreserveSig] int ActivateProfile(uint type, ushort language, ref Guid clsid, ref Guid profile, IntPtr hkl, uint flags); }
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] static extern bool SetForegroundWindow(IntPtr hwnd);
  [DllImport("user32.dll")] static extern bool GetWindowRect(IntPtr hwnd, out RECT r);
  [DllImport("user32.dll")] static extern bool SetCursorPos(int x, int y);
  [DllImport("user32.dll")] static extern void mouse_event(uint flags, int dx, int dy, uint data, UIntPtr extra);
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  // 與 type-probe 相同：前景被系統通知持有時，像使用者一樣點一下測試視窗自己的標題列。
  public static bool Claim(IntPtr hwnd) {
    if (GetForegroundWindow() == hwnd) return true;
    SetForegroundWindow(hwnd);
    if (GetForegroundWindow() == hwnd) return true;
    RECT r; if (GetWindowRect(hwnd, out r)) { SetCursorPos((r.Left + r.Right) / 2, r.Top + 12); mouse_event(2, 0, 0, 0, UIntPtr.Zero); mouse_event(4, 0, 0, 0, UIntPtr.Zero); }
    return GetForegroundWindow() == hwnd;
  }
  public static void Activate() {
    object obj = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("33C53A50-F456-4884-B049-85FD643ECFED")));
    try { Guid c = new Guid("138C35D0-7F08-4CEF-BA90-C34C16301528"), p = new Guid("EF62F743-9855-4C94-A658-1F0F98B7B840");
      Marshal.ThrowExceptionForHR(((IProfiles)obj).ActivateProfile(1, 0x0404, ref c, ref p, IntPtr.Zero, 0x10000000)); }
    finally { Marshal.FinalReleaseComObject(obj); }
  }
  static ITfLangBarItemButton Button() {
    object tm = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("529a9e6b-6587-4f23-ab9e-9c7d683e3c50")));
    Guid g = new Guid("2c77a81e-41cc-4178-a3a7-5f8a987568e6");
    return (ITfLangBarItemButton)((ITfLangBarItemMgr)tm).GetItem(ref g);
  }
  // PowerShell 以 IDispatch 呼叫 COM 物件，找不到 TSF 介面方法；所以全部在 C# 內呼叫。
  public static string Clsid() { TF_LANGBARITEMINFO i; Button().GetInfo(out i); return i.clsidService.ToString(); }
  public static string Text() { return Button().GetText(); }
  public static string Tooltip() { return Button().GetTooltipString(); }
  public static string[] Items() { Menu m = new Menu(); Button().InitMenu(m); return m.Items.ToArray(); }
  public static void Select(uint id) { Button().OnMenuSelect(id); }
  [DllImport("user32.dll")] static extern void keybd_event(byte key, byte scan, uint flags, UIntPtr extra);
  // 英文模式的字母以實體按鍵送出（同 smoke-test 的 Latin）；SendKeys 在此情境不可靠。
  public static void Latin(string text) { foreach (char c in text) { byte k = (byte)char.ToUpperInvariant(c); keybd_event(k, 0, 0, UIntPtr.Zero); keybd_event(k, 0, 2, UIntPtr.Zero); } }
  public static void RightShift() { keybd_event(0xA1, 0x36, 0, UIntPtr.Zero); keybd_event(0xA1, 0x36, 2, UIntPtr.Zero); }
  // 系統在使用者於輸入指示器按右鍵時呼叫的就是這個（TF_LBI_CLK_RIGHT = 1）；選單是強制回應的，會等到關閉。
  public static void RightClick(int x, int y) { POINT p = new POINT(); p.x = x; p.y = y; Button().OnClick(1, p, IntPtr.Zero); }
  public static void Toggle() { Button().OnClick(2, new POINT(), IntPtr.Zero); } // TF_LBI_CLK_LEFT = 2
}
'@
    $win = New-Object Windows.Window
    $win.Title = '0c menu probe'; $win.Width = 500; $win.Height = 140; $win.Topmost = $true
    $box = New-Object Windows.Controls.TextBox; $box.FontSize = 24; $win.Content = $box
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
    [MenuProbe]::Activate()
    Pump 800
    $result.clsid = [MenuProbe]::Clsid()
    $result.text = [MenuProbe]::Text()
    # 第一次開選單時狀態在背景讀取；如同使用者稍後再開一次。
    [MenuProbe]::Items() | Out-Null; Pump 3000
    $items = @([MenuProbe]::Items())
    $result.menu = $items
    $result.tooltip = [MenuProbe]::Tooltip()
    if ($RightClick) {
        # 另一個程序：等選單出現後以 UI Automation 讀出項目、截圖，再按 Esc 關閉。
        $popupOut = $Out + '.popup.txt'
        $shot = $Out + '.popup.png'
        $helper = @"
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes,System.Windows.Forms,System.Drawing
Add-Type -Namespace W -Name U -MemberDefinition '[DllImport("user32.dll")] public static extern bool PostMessage(System.IntPtr h, uint m, System.IntPtr w, System.IntPtr l);'
`$root=[Windows.Automation.AutomationElement]::RootElement
`$menu=`$null; for(`$i=0; `$i -lt 30 -and -not `$menu; `$i++){ Start-Sleep -Milliseconds 200; `$menu=`$root.FindFirst([Windows.Automation.TreeScope]::Children,(New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::ClassNameProperty,'#32768'))) }
`$names=@('found=' + [bool]`$menu)
`$names | Set-Content -Encoding UTF8 '$popupOut'
try { `$b=[Windows.Forms.Screen]::PrimaryScreen.Bounds; `$m=New-Object Drawing.Bitmap `$b.Width,`$b.Height; `$g=[Drawing.Graphics]::FromImage(`$m); `$g.CopyFromScreen(`$b.Location,[Drawing.Point]::Empty,`$b.Size); `$m.Save('$shot') } catch {}
# 只對選單視窗送 Esc（WM_KEYDOWN），不送全域按鍵：畫面上可能有其他程式的按鈕持有焦點。
if(`$menu){ [W.U]::PostMessage([IntPtr]`$menu.Current.NativeWindowHandle, 0x0100, [IntPtr]0x1B, [IntPtr]0) | Out-Null }
"@
        $enc = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($helper))
        Start-Process powershell.exe -ArgumentList "-NoProfile -WindowStyle Hidden -EncodedCommand $enc" -WindowStyle Hidden
        [MenuProbe]::RightClick(700, 600)
        Pump 500
        $result.popup = @(Get-Content $popupOut -Encoding UTF8 -ErrorAction SilentlyContinue | ForEach-Object { [string]$_ })
        $result.alive_after_popup = $true
    }
    if ($Select) {
        $hit = $items | Where-Object { ($_ -split '\|', 3)[2] -like "*$Select*" } | Select-Object -First 1
        if (-not $hit) { throw "menu has no item like $Select" }
        [MenuProbe]::Select([uint32](($hit -split '\|')[0]))
        $result.selected = $hit
        Pump 1500
        if ($ShotAfterSelect) { # 選了之後的整個螢幕（游標旁的提示、工作列的指示器）
            Add-Type -AssemblyName System.Drawing
            $b = [Windows.Forms.Screen]::PrimaryScreen.Bounds; $m = New-Object Drawing.Bitmap $b.Width, $b.Height
            [Drawing.Graphics]::FromImage($m).CopyFromScreen($b.Location, [Drawing.Point]::Empty, $b.Size); $m.Save($Out + '.select.png')
        }
        # 「輸出簡體字」等會改變指示器的項目（#169）：選了之後的字與說明。
        $result.text_after_select = [MenuProbe]::Text()
        $result.tooltip_after_select = [MenuProbe]::Tooltip()
        $result.menu_after_select = @([MenuProbe]::Items())
    }
    if ($ThenSet) {
        # 像設定程式一樣在別的行程改設定（例如 -ThenSet 'output-simplified off'），這個視窗沒有按鍵、沒換焦點，
        # 再開一次選單：勾選與指示器要跟上（#169）。
        $updater = Join-Path $env:LOCALAPPDATA 'Zuin\bin\0c-update.exe'
        $p = Start-Process $updater -ArgumentList (@('set') + ($ThenSet -split ' ') + @('-apply')) -PassThru -WindowStyle Hidden; $p.WaitForExit()
        Pump 500
        $result.menu_after_set = @([MenuProbe]::Items())
        $result.text_after_set = [MenuProbe]::Text()
        $result.tooltip_after_set = [MenuProbe]::Tooltip()
    }
    if ($Toggle) {
        [MenuProbe]::Toggle()
        Pump 800
        $result.text_after_toggle = [MenuProbe]::Text()
    }
    if ($ShiftToggle) {
        for ($i = 0; $i -lt 4 -and -not [MenuProbe]::Claim($handle); $i++) { Pump 400 }
        $box.Focus() | Out-Null
        [MenuProbe]::RightShift()
        Pump 800
        $result.text_after_toggle = [MenuProbe]::Text()
    }
    if ($TypeAfter) {
        for ($i = 0; $i -lt 4 -and -not [MenuProbe]::Claim($handle); $i++) { Pump 400 }
        if ([MenuProbe]::GetForegroundWindow() -ne $handle) { throw 'probe window is not in the foreground; refusing to type' }
        $box.Focus() | Out-Null
        if ($TypeAfter -cmatch '^[a-z]+$') { [MenuProbe]::Latin($TypeAfter) } else { [System.Windows.Forms.SendKeys]::SendWait($TypeAfter) }
        Pump 1200
        $result.typed = $box.Text
    }
    if ($ToggleBackThen) {
        [MenuProbe]::Toggle()
        Pump 800
        $result.text_after_back = [MenuProbe]::Text()
        [System.Windows.Forms.SendKeys]::SendWait($ToggleBackThen)
        Pump 1500
        $result.typed_after_back = $box.Text
    }
} catch { $result.error = $_.Exception.Message } finally {
    if ($win) { $win.Close() }
    $result | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $Out -Encoding UTF8
}
