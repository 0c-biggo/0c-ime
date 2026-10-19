# 在已開啟的設定程式視窗裡切換一個開關（UI Automation）：先按導覽按鈕開啟頁面，再對名稱相符、支援 TogglePattern 的按鈕按一下。
# WebView2 的無障礙樹要被查詢過一次才會建立，所以先走訪一次再等幾秒。結果寫到 -Out（toggled=true／原因）。
# 由 settings-test.ps1 以互動排程工作執行。
param([Parameter(Mandatory=$true)][string]$Page, [Parameter(Mandatory=$true)][string]$Switch, [Parameter(Mandatory=$true)][string]$Out)
Add-Type -AssemblyName UIAutomationClient, UIAutomationTypes
function Find($root, [string]$name) {
    $cond = New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::NameProperty, $name)
    return $root.FindFirst([Windows.Automation.TreeScope]::Descendants, $cond)
}
$p = $null
for ($i = 0; $i -lt 30 -and -not $p; $i++) {
    $p = Get-Process -Name 0c-settings -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowHandle -ne [IntPtr]::Zero } | Select-Object -First 1
    if (-not $p) { Start-Sleep -Milliseconds 500 }
}
if (-not $p) { 'toggled=false no settings window' | Set-Content $Out -Encoding UTF8; exit 0 }
Start-Sleep -Seconds 2
$win = [Windows.Automation.AutomationElement]::FromHandle($p.MainWindowHandle)
$null = $win.FindAll([Windows.Automation.TreeScope]::Descendants, [Windows.Automation.Condition]::TrueCondition)
Start-Sleep -Seconds 4
$nav = Find $win $Page
if (-not $nav) { "toggled=false no navigation button $Page" | Set-Content $Out -Encoding UTF8; exit 0 }
$nav.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
Start-Sleep -Seconds 2
$button = $null
foreach ($e in $win.FindAll([Windows.Automation.TreeScope]::Descendants, (New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::NameProperty, $Switch)))) {
    if ($e.Current.ControlType -eq [Windows.Automation.ControlType]::Button) { $button = $e; break }
}
if (-not $button) { "toggled=false no switch $Switch" | Set-Content $Out -Encoding UTF8; exit 0 }
$toggle = $button.GetCurrentPattern([Windows.Automation.TogglePattern]::Pattern)
$before = $toggle.Current.ToggleState
$toggle.Toggle()
Start-Sleep -Seconds 4
"toggled=true before=$before after=$($toggle.Current.ToggleState)" | Set-Content $Out -Encoding UTF8
