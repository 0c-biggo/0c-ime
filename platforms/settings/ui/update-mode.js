// 更新方式的顯示名稱與說明（#244、#340）：只看 0c-update status -json 的 mode 與平台，不碰畫面，node 測試直接載入
// （platforms/settings/test/update_mode_test.js）。值 auto／ask／manual 由 internal/autoupdate 定義，這裡不改。
//
// 說明要照 coordinator 實際做的事寫：
// - Windows 的背景排程沒有系統管理員權限：外殼更新的 TSF 註冊內容與已安裝的相同時（#294，release.json 的
//   tsf_registration）以一般權限在閒置時裝好；要改註冊的在 auto 模式同樣停在 needs_consent
//   （cmd/0c-update/auto.go 的 InstallShell、elevation_windows.go 的 shellKeepsRegistration），不能寫成全部自動完成。
// - #340 起「手動更新」（manual）也在背景檢查，有新版時跟「下載後詢問」一樣通知，只是不自己下載（needs_download）。
//   current 為 false 是舊版 0c-update（沒有 features.update_mode）：它的 manual 背景不檢查，照舊那樣說明。
window.ZcUpdateMode = function (mode, platform, current = true) {
  const windows = /^windows\//.test(platform || '');
  const OPTIONS = [
    ['auto', '自動更新（建議）'],
    ['ask', '下載後詢問'],
    ['manual', '手動更新'],
  ];
  const NOTES = {
    auto: windows
      ? '在背景下載，大部分更新會在閒置時完成。少數要改輸入法註冊的更新，背景沒有系統管理員權限，下載好會通知你，確認系統管理員權限後才安裝。'
      : '在背景下載與核對，在閒置或離開電腦時完成更新。',
    ask: '在背景下載與核對，有新版本時通知你，按「立即安裝」才更新。',
    manual: current
      ? '有新版本時通知你，不會自己下載；按「立即下載」下載，再按「立即安裝」更新。'
      : '背景不檢查也不下載，要更新時按「檢查更新」。',
  };
  const known = OPTIONS.find(([value]) => value === mode);
  const checks = mode !== 'manual' || current; // 排程會不會自己查詢新版
  return {
    label: known ? known[1] : mode || '未知',
    note: NOTES[mode] || '',
    options: OPTIONS.map(([value, label]) => ({ value, label })),
    known: !!known,
    scheduled: checks,
    // 排程現在會不會自己下載：手動更新與暫停中都不會。
    background: (pausedUntil) => mode !== 'manual' && !pausedUntil,
  };
};
