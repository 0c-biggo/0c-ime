# 在 Windows x64 建置機執行，不註冊輸入法、不使用日常詞庫。
param([Parameter(Mandatory=$true)][string]$BundleDir)
$ErrorActionPreference = "Stop"
$bundle = (Resolve-Path -LiteralPath $BundleDir).Path
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..")).Path
$updater = Join-Path $bundle "0c-update.exe"
$server = Join-Path $bundle "0c-server.exe"
& $updater verify -bundle $bundle
if ($LASTEXITCODE -ne 0) { throw "套件驗證失敗" }
& $server -check-data -data (Join-Path $bundle "data")
if ($LASTEXITCODE -ne 0) { throw "字典驗證失敗" }
$temp = Join-Path ([IO.Path]::GetTempPath()) ("0c-test-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory $temp | Out-Null
$pipe = "\\.\pipe\0c-test-" + [guid]::NewGuid().ToString("N")
$previousPipe = $env:ZUIN_TEST_PIPE
$process = $null
Push-Location $root
try {
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw "Go 測試失敗" }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw "Go vet 失敗" }
    $arguments = @('-data', ('"' + (Join-Path $bundle "data") + '"'),
                   '-user', ('"' + (Join-Path $temp "user.json") + '"'), '-socket', $pipe)
    $process = Start-Process -FilePath $server -ArgumentList $arguments -PassThru -NoNewWindow `
        -RedirectStandardOutput (Join-Path $temp "server.out") -RedirectStandardError (Join-Path $temp "server.err")
    # 等待此測試自己的管道；避免 cargo 測試在 server 啟動前連線。
    $ready = $false
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        if ($process.HasExited) { throw "測試引擎提前結束：$(Get-Content (Join-Path $temp 'server.err') -Raw)" }
        $client = [IO.Pipes.NamedPipeClientStream]::new('.', $pipe.Substring(9), [IO.Pipes.PipeDirection]::InOut)
        try { $client.Connect(1000); $ready = $true; break } catch [TimeoutException] { } finally { $client.Dispose() }
    }
    if (-not $ready) { throw "測試管道啟動逾時" }
    $env:ZUIN_TEST_PIPE = $pipe
    Push-Location (Join-Path $root "platforms\windows")
    try {
        # Windows PowerShell 5.1 在外層重新導向紀錄時，會把 Cargo 的一般 stderr
        # 編譯進度視為 NativeCommandError；仍以 Cargo 的結束碼判斷測試結果。
        $savedErrorAction = $ErrorActionPreference
        try {
            $ErrorActionPreference = 'Continue'
            cargo test --locked --release --target x86_64-pc-windows-msvc
        } finally { $ErrorActionPreference = $savedErrorAction }
        if ($LASTEXITCODE -ne 0) { throw "Rust／管道整合測試失敗" }
    } finally { Pop-Location }
    # 測試引擎（含內附神經 worker）執行後，套件內容不得改變；否則安裝後的收據核對會失敗。
    & $updater verify -bundle $bundle
    if ($LASTEXITCODE -ne 0) { throw "執行後套件內容改變（例如寫入 bytecode）" }
    Write-Host "套件、Go 與 Rust 管道測試通過；TSF 桌面操作與註冊／回復仍須另外驗收。"
} finally {
    $env:ZUIN_TEST_PIPE = $previousPipe
    if ($null -ne $process -and -not $process.HasExited) { Stop-Process -Id $process.Id -ErrorAction SilentlyContinue }
    Pop-Location
    Write-Host "測試資料與紀錄：$temp"
}
