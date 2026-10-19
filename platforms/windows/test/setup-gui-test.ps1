# EXE 安裝精靈驗收；僅在明確的互動測試機執行。
param([Parameter(Mandatory=$true)][string]$Setup, [Parameter(Mandatory=$true)][string]$ResultDir)
$ErrorActionPreference = "Stop"
$report = [ordered]@{passed=$false; error=''; welcome=''; license=$false; finish=''; exit_code=-1}
$p = $null
try {
    Add-Type -AssemblyName System.Drawing
    Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public static class WizardProbe {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [DllImport("user32.dll")] public static extern IntPtr GetDlgItem(IntPtr h, int id);
  [DllImport("user32.dll")] public static extern bool IsWindowEnabled(IntPtr h);
  [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int GetWindowText(IntPtr h, StringBuilder b, int n);
  [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr h,uint m,IntPtr w,IntPtr l);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h,out RECT r);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h,IntPtr dc,uint flags);
  public static string Text(IntPtr h) { var b=new StringBuilder(4096); GetWindowText(h,b,b.Capacity);return b.ToString(); }
}
'@
    function Shot([IntPtr]$Window, [string]$Name) {
        $rect = New-Object WizardProbe+RECT
        if (-not [WizardProbe]::GetWindowRect($Window,[ref]$rect)) { throw 'cannot measure wizard' }
        $bmp = [Drawing.Bitmap]::new($rect.Right-$rect.Left,$rect.Bottom-$rect.Top)
        $g = [Drawing.Graphics]::FromImage($bmp)
        $dc = $g.GetHdc()
        try { if (-not [WizardProbe]::PrintWindow($Window,$dc,2)) { throw 'cannot capture wizard' } }
        finally { $g.ReleaseHdc($dc); $g.Dispose() }
        try { $bmp.Save((Join-Path $ResultDir $Name),[Drawing.Imaging.ImageFormat]::Png) } finally { $bmp.Dispose() }
    }
    $p = Start-Process -FilePath $Setup -PassThru
    $processHandle=$p.Handle
    $deadline=(Get-Date).AddSeconds(30)
    do {
        Start-Sleep -Milliseconds 200
        $p.Refresh(); $handle=$p.MainWindowHandle
    } while ($handle -eq [IntPtr]::Zero -and -not $p.HasExited -and (Get-Date) -lt $deadline)
    if ($handle -eq [IntPtr]::Zero) { throw 'installer welcome window missing' }
    Start-Sleep -Milliseconds 700
    $report.welcome=[WizardProbe]::Text($handle)
    Shot $handle 'setup-welcome.png'
    $next=[WizardProbe]::GetDlgItem($handle,1)
    [WizardProbe]::SendMessage($next,0xF5,[IntPtr]::Zero,[IntPtr]::Zero) | Out-Null
    $deadline=(Get-Date).AddSeconds(55)
    do {
        Start-Sleep -Milliseconds 300
        $p.Refresh()
        if ($p.HasExited) { throw 'installer exited before finish page' }
        $text=[WizardProbe]::Text($next)
        # 使用條款頁：記錄並截圖後按「我同意」（同一個按鈕位置）。
        if ($text -match '我同意' -and [WizardProbe]::IsWindowEnabled($next) -and -not $report.license) {
            $report.license = $true
            Start-Sleep -Milliseconds 500
            Shot $handle 'setup-license.png'
            [WizardProbe]::SendMessage($next,0xF5,[IntPtr]::Zero,[IntPtr]::Zero) | Out-Null
        }
    } while (($text -notmatch '完成' -or -not [WizardProbe]::IsWindowEnabled($next)) -and (Get-Date) -lt $deadline)
    if ($text -notmatch '完成') { throw "finish page missing; button: $text" }
    if (-not $report.license) { throw 'terms page was not shown' }
    $report.finish=$text
    Start-Sleep -Milliseconds 800
    Shot $handle 'setup-finish.png'
    [WizardProbe]::SendMessage($next,0xF5,[IntPtr]::Zero,[IntPtr]::Zero) | Out-Null
    if (-not $p.WaitForExit(15000)) { throw 'installer did not exit' }
    $report.exit_code=$p.ExitCode
    if ($p.ExitCode -ne 0) { throw "installer exit code: $($p.ExitCode)" }
    $report.passed=$true
} catch { $report.error=$_.Exception.ToString() }
finally {
    if ($null -ne $p -and -not $p.HasExited) { Stop-Process -Id $p.Id }
    $report | ConvertTo-Json | Set-Content (Join-Path $ResultDir 'setup-gui.json') -Encoding UTF8
}
if (-not $report.passed) { exit 1 }
