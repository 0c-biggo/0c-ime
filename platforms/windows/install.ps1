# 靈犀注音套件安裝入口；以同一位桌面使用者的系統管理員 PowerShell 執行。
[CmdletBinding(DefaultParameterSetName="Install")]
param(
    [string]$BundleDir = $PSScriptRoot,
    [ValidateSet("public-beta", "internal-preview")]
    [string]$Channel = "public-beta",
    [Parameter(ParameterSetName="Rollback", Mandatory=$true)][switch]$Rollback,
    [Parameter(ParameterSetName="Uninstall", Mandatory=$true)][switch]$Uninstall
)
$ErrorActionPreference = "Stop"
$updater = Join-Path $BundleDir "0c-update.exe"
if (-not (Test-Path -LiteralPath $updater -PathType Leaf)) {
    throw "請從完整套件執行 install.ps1；原始碼建置請用 python scripts/package_windows.py --help"
}
if ($Rollback) {
    & $updater rollback
} elseif ($Uninstall) {
    & $updater uninstall
} else {
    & $updater install -bundle $BundleDir -channel $Channel
}
if ($LASTEXITCODE -ne 0) { throw "0c-update 執行失敗（$LASTEXITCODE）；請查看上方錯誤。" }
