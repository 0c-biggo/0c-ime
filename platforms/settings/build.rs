// 只有下列指令能從前端呼叫；其他（含 shell、檔案系統）一律不存在。
const COMMANDS: &[&str] = &["app_info", "get_status", "get_settings", "set_settings", "check_update", "download_update", "install_update", "export_summary",
    "profile_command", "vocab_request", "api_command", "vocab_watch", "vocab_unwatch", "models_command", "open_url"];

fn main() {
    tauri_build::try_build(
        tauri_build::Attributes::new().app_manifest(tauri_build::AppManifest::new().commands(COMMANDS)),
    )
    .expect("failed to run tauri-build");
}
