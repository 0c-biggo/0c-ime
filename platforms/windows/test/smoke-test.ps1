# 互動桌面驗收；執行前先啟動使用暫存 -user 詞庫的配對引擎。
# 只向自己的測試視窗送鍵；JSON／PNG 不擷取其他視窗。
param([Parameter(Mandatory=$true)][string]$ResultDir, [Parameter(Mandatory=$true)][string]$ExpectedDLL)
$ErrorActionPreference = "Stop"
$report = [ordered]@{ passed = $false; session = [Diagnostics.Process]::GetCurrentProcess().SessionId; checks = @(); dll = ''; error = '' }
$win = $null
try {
    Add-Type -AssemblyName PresentationFramework, PresentationCore, WindowsBase, System.Windows.Forms
    Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class TSFProbe {
  [ComImport, Guid("71C6E74C-0F28-11D8-A82A-00065B84435C"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
  interface IProfiles { [PreserveSig] int ActivateProfile(uint type, ushort language, ref Guid clsid, ref Guid profile, IntPtr hkl, uint flags); }
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] static extern void keybd_event(byte key, byte scan, uint flags, UIntPtr extra);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern IntPtr FindWindow(string cls, string title);
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr hwnd, uint msg, IntPtr wp, IntPtr lp);
  [DllImport("user32.dll")] public static extern uint GetDpiForWindow(IntPtr hwnd);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hwnd);
  public static void Latin(string text) { foreach(char c in text) { byte k = (byte)char.ToUpperInvariant(c); keybd_event(k, 0, 0, UIntPtr.Zero); keybd_event(k, 0, 2, UIntPtr.Zero); } }
  public static void Shift(bool down) { keybd_event(0xA1, 0x36, down ? 0u : 2u, UIntPtr.Zero); }

  public static void Activate() {
    object obj = Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("33C53A50-F456-4884-B049-85FD643ECFED")));
    try {
      Guid clsid = new Guid("138C35D0-7F08-4CEF-BA90-C34C16301528"), profile = new Guid("EF62F743-9855-4C94-A658-1F0F98B7B840");
      Marshal.ThrowExceptionForHR(((IProfiles)obj).ActivateProfile(1, 0x0404, ref clsid, ref profile, IntPtr.Zero, 0x10000000));
    } finally { Marshal.FinalReleaseComObject(obj); }
  }
}
'@
    # 除錯只寫此測試的暫存目錄，不開啟日常程序的鍵碼紀錄。
    $savedLocalAppData = $env:LOCALAPPDATA
    $env:LOCALAPPDATA = $ResultDir
    New-Item -ItemType Directory -Force (Join-Path $ResultDir 'Zuin') | Out-Null
    New-Item -ItemType File -Force (Join-Path $ResultDir 'Zuin\tsf-debug') | Out-Null
    $report.pid = $PID
    $win = New-Object Windows.Window
    $win.Title = "0c Windows acceptance test"; $win.Width = 600; $win.Height = 220; $win.Topmost = $true
    $box = New-Object Windows.Controls.TextBox
    $box.FontSize = 28; $box.AcceptsReturn = $true
    $password = New-Object Windows.Controls.PasswordBox
    $password.FontSize = 28
    $panel = New-Object Windows.Controls.StackPanel
    $panel.Children.Add($box) | Out-Null
    $panel.Children.Add($password) | Out-Null
    $win.Content = $panel
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
    [TSFProbe]::Activate()
    Pump 800
    function Send([string]$Keys) {
        if ([TSFProbe]::GetForegroundWindow() -ne $handle) { throw "test window lost focus; refusing to type" }
        [System.Windows.Forms.SendKeys]::SendWait($Keys)
        Pump 500
    }
    function Shift([bool]$Down) {
        if ([TSFProbe]::GetForegroundWindow() -ne $handle) { throw "test window lost focus; refusing modifier input" }
        [TSFProbe]::Shift($Down)
        $script:rightShiftDown = $Down
    }
    function Latin([string]$Text) {
        if ([TSFProbe]::GetForegroundWindow() -ne $handle) { throw "test window lost focus; refusing to type" }
        [TSFProbe]::Latin($Text)
    }
    function Expect([string]$Expected, [string]$Name) {
        if ($box.Text -ne $Expected) { throw "$Name expected [$Expected], got [$($box.Text)]" }
        $report.checks += $Name
    }
    function Shot([string]$Name) {
    $bitmap = New-Object Windows.Media.Imaging.RenderTargetBitmap([int]$win.ActualWidth, [int]$win.ActualHeight, 96, 96, [Windows.Media.PixelFormats]::Pbgra32)
    $bitmap.Render($win)
    $encoder = New-Object Windows.Media.Imaging.PngBitmapEncoder
    $encoder.Frames.Add([Windows.Media.Imaging.BitmapFrame]::Create($bitmap))
    $stream = [IO.File]::Create((Join-Path $ResultDir $Name))
    try { $encoder.Save($stream) } finally { $stream.Dispose() }
    }
    # 空白欄位直接按 = 必須顯示候選；第二次 = 只輸出一個等號。
    Send '='
    Expect '' 'emoji_empty_preedit'
    $emojiWindow = [TSFProbe]::FindWindow('ZuinCandidateWindow', 'Zuin')
    if ($emojiWindow -eq [IntPtr]::Zero -or -not [TSFProbe]::IsWindowVisible($emojiWindow)) { throw 'emoji palette did not open from an empty field' }
    $report.checks += 'emoji_opens_from_empty_field'
    Send '='
    Expect '=' 'emoji_double_equals_literal'
    $box.Clear(); Pump 200
    Send '={TAB}{ESC}'
    Expect '' 'kaomoji_open_switch_cancel'
    Send '={RIGHT}{ENTER}{ENTER}'
    Expect ([char]::ConvertFromUtf32(0x1F600)) 'emoji_keyboard_commit'
    $box.Clear(); Pump 200
    Send 'su3cl3'
    Expect '你好' 'compose_nihao'
    Send '{ENTER}'
    Expect '你好' 'commit_enter'
    $box.Clear(); Pump 200
    Send 'su3'
    Expect '你' 'compose_ni'
    Send '{ESC}'
    Expect '' 'cancel_escape'
    # 連續輸入：提交後游標仍緊接剛提交的文字，下一段開始前外殼應確認前文（紀錄只有原因代碼、沒有文字）。
    $contextLog = Join-Path $ResultDir 'Zuin\tsf.log'
    $contextBefore = (Get-Content $contextLog -Raw -Encoding UTF8).Length
    Send 'su3cl3{ENTER}'
    Expect '你好' 'context_first_commit'
    Send 'su3'
    Expect '你好你' 'context_second_segment'
    $contextNew = (Get-Content $contextLog -Raw -Encoding UTF8).Substring($contextBefore)
    $report.context_probe = @([regex]::Matches($contextNew, 'document context probe=(\w+)') | ForEach-Object { $_.Groups[1].Value })
    if ($contextNew -notmatch 'document context confirmed=true') { throw "consecutive typing did not confirm document context: $($report.context_probe -join ',')" }
    $report.checks += 'document_context_confirmed'
    Send '{ESC}'
    Expect '你好' 'context_cancel_second'
    $box.Clear(); Pump 200
    Send 'su3{DOWN}{ENTER}{ENTER}'
    Expect '你' 'candidate_enter'
    $box.Clear(); Pump 200
    Send 's{BACKSPACE}'
    Expect '' 'backspace_pending'
    Send 'su3cl3{ENTER}'
    Expect '你好' 'compose_after_cancel'
    $box.Clear(); Pump 200
    Send 'a86qi6'
    Expect '麻婆' 'tab_completion_prefix'
    Shot 'composition-normal.png'
    Send '{TAB}'
    Expect '麻婆豆腐' 'tab_completion_expands'
    Shot 'composition-locked.png'
    Send '{ENTER}'
    Expect '麻婆豆腐' 'tab_completion_commit'
    $box.Clear(); Pump 200
    $script:idleTabSeen=$false
    $box.add_PreviewKeyDown({param($sender,$event);if($event.Key -eq [Windows.Input.Key]::Tab){$script:idleTabSeen=$true}})
    # SendKeys 的 Tab 不會可靠觸發 WPF 焦點導覽；用原生鍵盤事件驗證放行。
    Latin ([string][char]9); Pump 500
    $report.tab_seen=$script:idleTabSeen
    $report.tab_focus=[Windows.Input.Keyboard]::FocusedElement.GetType().FullName
    $report.tab_within=$password.IsKeyboardFocusWithin
    if (-not $password.IsKeyboardFocused) { throw 'idle Tab did not move focus to the next field' }
    $report.checks += 'idle_tab_passthrough'
    $box.Focus() | Out-Null; Pump 300
    # 右 Shift 單按切換；Shift + 字母保留大寫，不觸發切換。
    $box.Clear(); Pump 200
    Shift $true; Pump 80; Shift $false; Pump 400
    Latin 'su3'; Pump 600
    Expect 'su3' 'right_shift_english'
    $box.Clear(); Pump 200
    Shift $true; Pump 80; Shift $false; Pump 400
    Send 'su3{ENTER}'
    Expect '你' 'right_shift_zhuyin'
    $box.Clear(); Pump 200
    Shift $true; Pump 80
    Latin 'a'; Pump 300
    Shift $false; Pump 300
    Send 'su3{ENTER}'
    Expect 'A你' 'shift_chord_does_not_toggle'
    $box.Clear(); Pump 200
    Send 'su3'
    Shift $true; Pump 80; Shift $false; Pump 400
    Send 'abc'
    Expect '你abc' 'shift_preserves_composition'
    Send '{ENTER}'
    Shift $true; Pump 80; Shift $false; Pump 400
    $box.Clear(); Pump 200
    Send 'su3{DOWN}'
    $candidate = [TSFProbe]::FindWindow('ZuinCandidateWindow', 'Zuin')
    if ($candidate -eq [IntPtr]::Zero -or -not [TSFProbe]::IsWindowVisible($candidate)) { throw 'candidate window missing' }
    # 第一列上緣沒有補全提示（Down 開啟候選時核心清除 suggestion）。
    $dpi = [TSFProbe]::GetDpiForWindow($candidate)
    # 第一列是小字標題「修改「你」· Tab 切換詞長」（dev.54 起），第二個候選在 pad 8＋標題 20＋一列 28 之後。
    $x = [int](20 * $dpi / 96); $y = [int](70 * $dpi / 96) # 第二個候選：妳
    [TSFProbe]::PostMessage($candidate, 0x202, [IntPtr]::Zero, [IntPtr](($y -shl 16) -bor $x)) | Out-Null
    Pump 700
    Expect '妳' 'mouse_select_second_candidate'
    Send '{ENTER}'
    Expect '妳' 'mouse_selection_commit'
    # PasswordBox 的 TSF 停用：字元直接進宿主，候選不顯示。
    $password.Focus() | Out-Null; Pump 300
    Latin 'su3cl3'; Pump 500
    if ($password.Password -ne 'su3cl3') { throw 'password input was intercepted' }
    if ([TSFProbe]::IsWindowVisible($candidate)) { throw 'candidate remained visible in password field' }
    $report.checks += 'password_passthrough'
    $password.Clear()
    $box.Focus() | Out-Null; $box.Clear(); Pump 300
    Send 'su3{ENTER}'
    Expect '你' 'normal_field_after_password' # 只手選過一次「妳」不會改變預設（代名詞性別只依反覆的明確選取）
    # 組字中焦點移到別的欄位：組字照舊送出到原欄位（#189 只在宿主把組字搬到新的焦點文件時才繼續組字）。
    # 回到原欄位按 Esc：組字還在的話會被取消，已送出就不受影響。
    $box.Clear(); Pump 200
    Send 'su3'
    $password.Focus() | Out-Null; Pump 500
    $box.Focus() | Out-Null; Pump 300
    Send '{ESC}'
    Expect '你' 'focus_change_commits_composition'
    $module = (Get-Process -Id $PID).Modules | Where-Object ModuleName -eq '0c_tsf.dll' | Select-Object -First 1
    $report.dll = $module.FileName
    if ($report.dll -ne $ExpectedDLL) { throw "host loaded unexpected DLL: $($report.dll)" }
    $report.checks += 'new_version_dll_loaded'
    Shot 'wpf.png'
    $report.passed = $true
} catch {
    $report.error = $_.Exception.ToString()
} finally {
    if ($script:rightShiftDown) { [TSFProbe]::Shift($false) }
    if ($null -ne $win) { $win.Close() }
    if ($savedLocalAppData) { $env:LOCALAPPDATA = $savedLocalAppData }
    $report | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $ResultDir 'desktop-result.json') -Encoding UTF8
}
if (-not $report.passed) { exit 1 }
