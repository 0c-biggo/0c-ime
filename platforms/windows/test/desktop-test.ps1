# 桌面驗收腳本：在已登入的互動工作階段執行（例如以 LogonType Interactive 的排程工作）。
# 開一個 WPF 文字框（原生 TSF），用 SendKeys 打字，每步記錄文字框內容到 job.out 並截圖 shot-NN.png。
# 前提：Zuin 已是目前輸入法（Set-WinDefaultInputMethodOverride 或 ActivateProfile，見 README）。

$ErrorActionPreference = "Continue"
$out = "$PSScriptRoot\job.out"
function Log($m) { ("[{0}] {1}" -f (Get-Date -Format "HH:mm:ss.fff"), $m) | Out-File $out -Append -Encoding utf8 }
Remove-Item $out -ErrorAction SilentlyContinue
Log "session=$([System.Diagnostics.Process]::GetCurrentProcess().SessionId)"
try {
  Add-Type -AssemblyName PresentationFramework, PresentationCore, WindowsBase, System.Windows.Forms, System.Drawing
  $win = New-Object System.Windows.Window
  $win.Title = "Zuin TSF test"; $win.Width = 600; $win.Height = 260; $win.Left = 100; $win.Top = 100; $win.Topmost = $true
  $box = New-Object System.Windows.Controls.TextBox
  $box.AcceptsReturn = $true; $box.FontSize = 24; $box.FontFamily = "Microsoft JhengHei"
  $win.Content = $box
  $win.Show(); $win.Activate(); $box.Focus() | Out-Null
  function Pump($ms) { $t = [Diagnostics.Stopwatch]::StartNew(); while ($t.ElapsedMilliseconds -lt $ms) { $box.Dispatcher.Invoke([Action]{}, [Windows.Threading.DispatcherPriority]::Background); Start-Sleep -Milliseconds 20 } }
  Pump 1500
  $script:n = 0
  function Shot() { $script:n++; $bmp = New-Object System.Drawing.Bitmap 700, 520; $g = [System.Drawing.Graphics]::FromImage($bmp); $g.CopyFromScreen(80, 80, 0, 0, $bmp.Size); $g.Dispose(); $bmp.Save(("$PSScriptRoot\shot-{0:D2}.png" -f $script:n)); $bmp.Dispose() }
  function Step($keys, $label) {
    [System.Windows.Forms.SendKeys]::SendWait($keys); Pump 600
    Shot
    Log ("{0,2} after {1,-10} text={2}  caret={3}" -f $script:n, $label, ($box.Text -replace "`r`n", "\n"), $box.CaretIndex)
  }
  Step "su3" "su3"
  Step "{DOWN}" "Down"
  Step "{DOWN}" "Down"
  Step "{RIGHT}" "Right"
  Step "{ENTER}" "Enter"
  Step "cl3" "cl3"
  Step "{ENTER}" "Enter"
  Step "a86qo6" "a86qo6"
  Step "{TAB}" "Tab"
  Step "{ENTER}" "Enter"
  Step "su3" "su3"
  Step "{DOWN}" "Down"
  Step "3" "3"
  Step "{ESC}" "Esc"
  Log ("0c-ui running: " + ((Get-Process 0c-ui -ErrorAction SilentlyContinue) -ne $null) + " server: " + ((Get-Process 0c-server -ErrorAction SilentlyContinue) -ne $null))
  $win.Close()
} catch { Log ("form error: " + $_.Exception.ToString()) }
Log "done"
