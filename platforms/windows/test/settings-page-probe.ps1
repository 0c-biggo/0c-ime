# 看設定程式目前開在哪一頁（UI Automation）：隱藏的頁面不在無障礙樹裡，所以名稱為 -Expect 的元素存在就代表那一頁正顯示著。
# 給 -Nav 時先按該導覽按鈕（把視窗切到別頁，再測從選單切回來）。WebView2 的無障礙樹要被查詢過一次才會建立，所以先走訪一次再等幾秒。
# 結果寫到 -Out（found=true／false 與原因）。由 settings-test.ps1 以互動排程工作執行（#125）。
param([string]$Nav = '', [Parameter(Mandatory=$true)][string]$Expect, [Parameter(Mandatory=$true)][string]$Out)
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
if (-not $p) { 'found=false no settings window' | Set-Content $Out -Encoding UTF8; exit 0 }
Start-Sleep -Seconds 2
$win = [Windows.Automation.AutomationElement]::FromHandle($p.MainWindowHandle)
$null = $win.FindAll([Windows.Automation.TreeScope]::Descendants, [Windows.Automation.Condition]::TrueCondition)
Start-Sleep -Seconds 4
if ($Nav) {
    $button = Find $win $Nav
    if (-not $button) { "found=false no navigation button $Nav" | Set-Content $Out -Encoding UTF8; exit 0 }
    $button.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
    Start-Sleep -Seconds 2
}
$found = $false
for ($i = 0; $i -lt 10 -and -not $found; $i++) {
    $null = $win.FindAll([Windows.Automation.TreeScope]::Descendants, [Windows.Automation.Condition]::TrueCondition)
    $found = [bool](Find $win $Expect)
    if (-not $found) { Start-Sleep -Milliseconds 800 }
}
"found=$($found.ToString().ToLower()) expect=$Expect pid=$($p.Id)" | Set-Content $Out -Encoding UTF8
