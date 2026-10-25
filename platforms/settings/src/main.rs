// 靈犀注音設定（#47）。畫面是純 HTML／CSS／JS（ui/），這裡的 Rust 只做兩件事：把前端的幾個固定動作轉成
// 0c-update 的 JSON 契約呼叫，以及管視窗。設定與更新驗證全在 Go；網路、登入與機密都不在這個程式裡。
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

#[cfg(target_os = "macos")]
mod navigate_mac;
mod updater;
mod watch;

use serde_json::{json, Value};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU32, Ordering};
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tauri::{AppHandle, Emitter, Manager, RunEvent, State, WebviewUrl, WebviewWindowBuilder};

static WINDOWS: AtomicU32 = AtomicU32::new(0);

/// 開一個設定視窗，開在指定的頁面（網址參數 ?page=，已過白名單）。標籤 main-N 符合 capability 的 `main-*`。
fn open_window(app: &AppHandle, page: &'static str) -> tauri::Result<()> {
    let label = format!("main-{}", WINDOWS.fetch_add(1, Ordering::Relaxed));
    let builder = WebviewWindowBuilder::new(app, label, WebviewUrl::App(format!("index.html?page={page}").into()))
        .title("靈犀注音設定")
        .inner_size(960.0, 680.0)
        .min_inner_size(640.0, 480.0)
        .center()
        .on_navigation(local_origin);
    // WebView2 的使用者資料夾放在個人資料目錄內，不另外在 LOCALAPPDATA 留一個以 identifier 命名的目錄。
    #[cfg(windows)]
    let builder = match std::env::var_os("LOCALAPPDATA") {
        Some(base) => builder.data_directory(PathBuf::from(base).join("Zuin").join("settings-webview")),
        None => builder,
    };
    builder.build().map(|_| ())
}

/// Wayland 的合成器不准程式自己把視窗拉到最前面（沒有 xdg-activation token），所以第二次啟動時
/// 既有視窗被別的視窗蓋住就不會浮上來；但新建立的視窗會正常出現在最前面。這時改成開新視窗、關掉舊的。
fn wayland_session() -> bool {
    std::env::var("XDG_SESSION_TYPE").is_ok_and(|t| t == "wayland") || std::env::var_os("WAYLAND_DISPLAY").is_some()
}

/// 把既有視窗帶到前面並切到要求的頁面（前端的 `navigate` 事件）。
fn focus_and_navigate(app: &AppHandle, page: &'static str) {
    for window in app.webview_windows().into_values() {
        let _ = window.unminimize();
        let _ = window.show();
        let _ = window.set_focus();
    }
    let _ = app.emit("navigate", page);
}

fn home() -> Option<PathBuf> {
    std::env::var_os("HOME").or_else(|| std::env::var_os("USERPROFILE")).map(PathBuf::from)
}

/// Linux 系統套件的位置（#181）：make install 依 PREFIX 在建置時設定，預設是 PREFIX=/usr 的位置。
fn system_dir(dir: &'static str) -> Option<&'static Path> {
    cfg!(target_os = "linux").then(|| Path::new(dir))
}

fn system_libexec() -> Option<&'static Path> {
    system_dir(option_env!("ZC_SYSTEM_LIBEXECDIR").unwrap_or("/usr/libexec/0c"))
}

fn system_bin() -> Option<&'static Path> {
    system_dir(option_env!("ZC_SYSTEM_BINDIR").unwrap_or("/usr/bin"))
}

fn updater_path() -> Result<PathBuf, String> {
    let local_app_data = std::env::var_os("LOCALAPPDATA").map(PathBuf::from);
    updater::find_updater(
        std::env::var("ZC_UPDATER").ok().as_deref(),
        std::env::current_exe().ok().as_deref(),
        home().as_deref(),
        local_app_data.as_deref(),
        system_libexec(),
    )
}

/// 在背景執行緒呼叫 0c-update，避免卡住視窗。
async fn call(args: Vec<String>, timeout_secs: u64) -> Result<Value, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let path = updater_path()?;
        updater::run_blocking(&path, &args, Duration::from_secs(timeout_secs))
    })
    .await
    .map_err(|e| e.to_string())?
}

fn args(items: &[&str]) -> Vec<String> {
    items.iter().map(|s| s.to_string()).collect()
}

#[tauri::command]
fn app_info() -> Value {
    let local_app_data = std::env::var_os("LOCALAPPDATA").map(PathBuf::from);
    let mcp = updater::planned_mcp_path(std::env::consts::OS, std::env::current_exe().ok().as_deref(), home().as_deref(), local_app_data.as_deref(), system_bin());
    json!({
        "version": option_env!("ZC_BUILD_VERSION").unwrap_or(env!("CARGO_PKG_VERSION")),
        "platform": format!("{}/{}", std::env::consts::OS, std::env::consts::ARCH),
        "updater": updater_path().ok().map(|p| p.display().to_string()),
        // 「讓 AI 幫你加詞」範例 5：只支援 stdio 的 AI 工具要填的 0c-mcp 路徑（不隨版本改變），與這個安裝有沒有附它。
        "mcp_path": mcp.as_ref().map(|p| p.display().to_string()),
        "mcp_present": mcp.as_ref().is_some_and(|p| p.is_file()),
    })
}

#[tauri::command]
async fn get_status() -> Result<Value, String> {
    call(args(&["status", "-json"]), 20).await
}

#[tauri::command]
async fn get_settings() -> Result<Value, String> {
    call(args(&["settings", "-json"]), 20).await
}

/// 修改一到多項設定。apply 為真時請引擎在沒有組字時交接（Go 端最多等 40 秒），所以逾時放寬。
#[tauri::command]
async fn set_settings(pairs: Vec<(String, String)>, apply: bool, repair: bool) -> Result<Value, String> {
    updater::validate_pairs(&pairs)?;
    let mut command = args(&["set", "-json"]);
    if apply {
        command.push("-apply".to_string());
    }
    if repair {
        command.push("-repair".to_string());
    }
    for (key, value) in pairs {
        command.push(key);
        command.push(value);
    }
    call(command, 120).await
}

#[tauri::command]
async fn check_update() -> Result<Value, String> {
    call(args(&["check", "-json"]), 60).await
}

#[tauri::command]
async fn download_update(version: String, sha256: String) -> Result<Value, String> {
    call(updater::update_action_args("download", &version, &sha256)?, 2400).await
}

#[tauri::command]
async fn install_update(version: String, sha256: String) -> Result<Value, String> {
    call(updater::update_action_args("install", &version, &sha256)?, 2400).await
}

/// 風格管理與整份套用交給 0c-update，Rust 只限制可呼叫的動作與參數形式。
#[tauri::command]
async fn profile_command(action: String, id: String, name: Option<String>) -> Result<Value, String> {
    call(updater::profile_args(&action, &id, name.as_deref())?, 120).await
}

/// 「我的詞彙」的一個指令（core/engine 的 VocabRequest），經 0c-update vocab request 交給引擎。
/// 結果原樣交給前端；錯誤在回應的 error（code、message…），引擎沒執行時寫入類指令回 error.code=engine。
#[tauri::command]
async fn vocab_request(request: Value) -> Result<Value, String> {
    let body = updater::vocab_request_body(&request)?;
    tauri::async_runtime::spawn_blocking(move || {
        let path = updater_path()?;
        updater::run_with_input(&path, &args(&["vocab", "request"]), Some(body), Duration::from_secs(45))
    })
    .await
    .map_err(|e| e.to_string())?
}

/// 「讓 AI 幫你加詞」：status、enable（可帶埠號）、disable、regen、test。enable／disable 會請引擎在沒有組字時交接，
/// 所以逾時與 set -apply 一樣放寬。
#[tauri::command]
async fn api_command(action: String, port: Option<u16>) -> Result<Value, String> {
    let command = updater::api_args(&action, port)?;
    let timeout = if matches!(action.as_str(), "enable" | "disable") { 120 } else { 30 };
    call(command, timeout).await
}

/// 「我的詞彙」頁開著且視窗看得到時，常駐 0c-update vocab watch，版本改變時發出 `vocab-changed` 事件
/// （{"version":N,"count":M}）。since 是頁面目前顯示的版本；再呼叫一次會換掉舊的那支。
#[tauri::command]
fn vocab_watch(app: AppHandle, watcher: State<'_, watch::Watcher>, since: Option<u64>) -> Result<(), String> {
    let path = updater_path()?;
    watcher.start(path, since, move |value| {
        let _ = app.emit("vocab-changed", value);
    });
    Ok(())
}

/// 離開頁面或視窗隱藏時結束 watch。
#[tauri::command]
fn vocab_unwatch(watcher: State<'_, watch::Watcher>) {
    watcher.stop();
}

/// 「模型」頁（#180）：0c-update models status|download|cancel|remove -json。結果（status 或契約錯誤）原樣交給前端。
#[tauri::command]
async fn models_command(action: String) -> Result<Value, String> {
    let (command, timeout) = updater::models_args(&action)?;
    call(command, timeout).await
}

/// 用系統瀏覽器開外部網址（「模型」頁的 Hugging Face 連結）。設定視窗本身不離開本機頁面，只放行 Hugging Face。
#[tauri::command]
fn open_url(url: String) -> Result<(), String> {
    let url = updater::browser_url(&url)?;
    let mut command = browser_command(url);
    command.stdin(std::process::Stdio::null()).stdout(std::process::Stdio::null()).stderr(std::process::Stdio::null());
    let mut child = command.spawn().map_err(|e| e.to_string())?;
    std::thread::spawn(move || {
        let _ = child.wait(); // 回收子行程；xdg-open 等交出網址就結束
    });
    Ok(())
}

#[cfg(target_os = "macos")]
fn browser_command(url: &str) -> std::process::Command {
    let mut command = std::process::Command::new("/usr/bin/open");
    command.arg(url);
    command
}

#[cfg(windows)]
fn browser_command(url: &str) -> std::process::Command {
    use std::os::windows::process::CommandExt;
    let mut command = std::process::Command::new("rundll32.exe");
    command.arg("url.dll,FileProtocolHandler").arg(url).creation_flags(0x0800_0000);
    command
}

#[cfg(not(any(target_os = "macos", windows)))]
fn browser_command(url: &str) -> std::process::Command {
    let mut command = std::process::Command::new("xdg-open");
    command.arg(url);
    command
}

/// 把狀態摘要寫成檔案（下載資料夾，沒有就家目錄），回傳路徑。內容不含輸入內容、個人詞庫或使用者名稱。
#[tauri::command]
async fn export_summary() -> Result<Value, String> {
    // -diagnostics：macOS 版本與正在執行的輸入法 App（#77）；只在匯出時收集，平常的狀態查詢不帶。
    // 較舊的更新器不認得這個旗標時退回一般的狀態。
    let status = match call(args(&["status", "-json", "-diagnostics"]), 20).await {
        Ok(status) => status,
        Err(_) => call(args(&["status", "-json"]), 20).await?,
    };
    let settings = call(args(&["settings", "-json"]), 20).await?;
    let home = home().ok_or("找不到家目錄")?;
    let secs = SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs()).unwrap_or(0);
    let text = updater::build_summary(&status, &settings, option_env!("ZC_BUILD_VERSION").unwrap_or(env!("CARGO_PKG_VERSION")), secs, &home.to_string_lossy());
    let downloads = home.join("Downloads");
    let dir = if downloads.is_dir() { downloads } else { home };
    let path = dir.join(format!("0c-status-{}.json", updater::file_stamp(secs)));
    write_private(&path, text.as_bytes()).map_err(|e| format!("無法寫入 {}：{e}", path.display()))?;
    Ok(json!({ "path": path.display().to_string() }))
}

fn write_private(path: &std::path::Path, bytes: &[u8]) -> std::io::Result<()> {
    use std::io::Write;
    let mut options = std::fs::OpenOptions::new();
    options.write(true).create_new(true); // 不覆寫既有的檔案
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    options.open(path)?.write_all(bytes)
}

/// 視窗只准留在自己的本機頁面：任何導向外部網址（連結、重新導向）一律擋下。
fn local_origin(url: &tauri::Url) -> bool {
    match url.scheme() {
        "tauri" => true,
        "http" | "https" => url.host_str() == Some("tauri.localhost"),
        _ => false,
    }
}

/// NVIDIA 私有驅動下，WebKitGTK 的 DMABUF 繪圖在 Wayland 上常白畫面或出現 Error 71（Tauri 文件列為已知問題）。
/// 只在偵測到該驅動時關閉；Tauri 建議不要無條件關閉。使用者自己設了就尊重。
#[cfg(target_os = "linux")]
fn linux_graphics_workaround() {
    if std::path::Path::new("/proc/driver/nvidia").exists() && std::env::var_os("WEBKIT_DISABLE_DMABUF_RENDERER").is_none() {
        std::env::set_var("WEBKIT_DISABLE_DMABUF_RENDERER", "1");
    }
}

#[cfg(not(target_os = "linux"))]
fn linux_graphics_workaround() {}

fn main() {
    linux_graphics_workaround();
    let argv: Vec<String> = std::env::args().collect();
    let page = updater::page_from_args(&argv);
    tauri::Builder::default()
        // 已經開著就聚焦既有視窗並切到要求的頁面，不開第二個。
        .plugin(tauri_plugin_single_instance::init(|app, args, _cwd| {
            let page = updater::page_from_args(&args);
            let existing: Vec<_> = app.webview_windows().into_values().collect();
            if wayland_session() && open_window(app, page).is_ok() {
                for window in existing {
                    let _ = window.destroy(); // 新視窗已經開好，這時關掉舊的不會讓程式結束
                }
                return;
            }
            focus_and_navigate(app, page);
        }))
        .manage(watch::Watcher::default())
        .invoke_handler(tauri::generate_handler![
            app_info, get_status, get_settings, set_settings, check_update, download_update, install_update, export_summary,
            profile_command, vocab_request, api_command, vocab_watch, vocab_unwatch, models_command, open_url
        ])
        .setup(move |app| {
            open_window(app.handle(), page)?;
            // macOS：已開著時 0c-update 以 Darwin 通知要求切頁（open 的參數到不了既有的行程）。
            #[cfg(target_os = "macos")]
            {
                let handle = app.handle().clone();
                navigate_mac::listen(move |page| focus_and_navigate(&handle, page));
            }
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("靈犀注音設定無法啟動")
        .run(|app, event| {
            // 程式結束時一併結束 watch，不留下常駐的 0c-update。
            if let RunEvent::Exit = event {
                app.state::<watch::Watcher>().stop();
            }
        });
}
