# 等設定程式（或指定名稱的程式）出現主視窗，只擷取那個視窗存成 PNG；不關閉它。
# 優先用 PrintWindow（PW_RENDERFULLCONTENT）：即使被通知或其他視窗蓋住也只會得到那個視窗；整面空白時才改擷取螢幕上的範圍。
# 由 settings-test.ps1 以互動排程工作執行；結果文字寫到 <Out>.txt。
param([string]$Name = '0c-settings', [Parameter(Mandatory=$true)][string]$Out, [int]$WaitSeconds = 20)
Add-Type -AssemblyName System.Windows.Forms, System.Drawing
Add-Type @'
using System; using System.Runtime.InteropServices;
public static class SettingsShot {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int c);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr dc, uint flags);
}
'@
function Blank([System.Drawing.Bitmap]$bmp) {
    # 取樣：幾乎全是同一個顏色就當作沒有畫出內容
    $seen = @{}
    for ($x = 20; $x -lt $bmp.Width; $x += [Math]::Max(1, [int]($bmp.Width / 25))) {
        for ($y = 20; $y -lt $bmp.Height; $y += [Math]::Max(1, [int]($bmp.Height / 25))) { $seen[$bmp.GetPixel($x, $y).ToArgb()] = 1 }
    }
    return $seen.Count -lt 4
}
$deadline = (Get-Date).AddSeconds($WaitSeconds)
$p = $null
do {
    Start-Sleep -Milliseconds 400
    $p = Get-Process -Name $Name -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowHandle -ne [IntPtr]::Zero } | Select-Object -First 1
} while (-not $p -and (Get-Date) -lt $deadline)
if (-not $p) { 'found=false' | Set-Content "$Out.txt" -Encoding UTF8; exit 0 }
Start-Sleep -Milliseconds 1500
$h = $p.MainWindowHandle
[SettingsShot]::ShowWindow($h, 9) | Out-Null
$r = New-Object SettingsShot+RECT
[SettingsShot]::GetWindowRect($h, [ref]$r) | Out-Null
$w = $r.R - $r.L; $hh = $r.B - $r.T
$bmp = New-Object System.Drawing.Bitmap $w, $hh
$g = [System.Drawing.Graphics]::FromImage($bmp)
$dc = $g.GetHdc()
$ok = [SettingsShot]::PrintWindow($h, $dc, 2)
$g.ReleaseHdc($dc)
$method = 'PrintWindow'
if (-not $ok -or (Blank $bmp)) {
    [SettingsShot]::SetForegroundWindow($h) | Out-Null
    Start-Sleep -Milliseconds 800
    $g.CopyFromScreen($r.L, $r.T, 0, 0, (New-Object System.Drawing.Size $w, $hh))
    $method = 'CopyFromScreen'
}
$g.Dispose()
$bmp.Save($Out, [System.Drawing.Imaging.ImageFormat]::Png)
@("found=true", "pid=$($p.Id)", "title=$($p.MainWindowTitle)", "size=${w}x${hh}", "method=$method") | Set-Content "$Out.txt" -Encoding UTF8
