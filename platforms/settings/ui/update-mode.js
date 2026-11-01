// 更新方式的顯示名稱與說明（#244）：只看 0c-update status -json 的 mode 與平台，不碰畫面，node 測試直接載入
// （platforms/settings/test/update_mode_test.js）。值 auto／ask／manual 由 internal/autoupdate 定義，這裡不改。
//
// 說明要照 coordinator 實際做的事寫：Windows 的背景排程沒有系統管理員權限，外殼更新（要改 TSF 註冊）在 auto 模式
// 同樣停在 needs_consent（cmd/0c-update/auto_windows.go 的 shellUnattended），不能寫成全部自動完成。
window.ZcUpdateMode = function (mode, platform) {
  const windows = /^windows\//.test(platform || '');
  const known = {
    auto: {
      label: '自動（建議）',
      note: windows
        ? '自動：只換引擎、字典或模型的更新，會在閒置時於背景完成。要換輸入法本身的更新，背景沒有系統管理員權限，所以下載好後會通知你，確認系統管理員權限才安裝。裝好不必登出；已經開著的應用程式要重新開啟才會換成新版。'
        : '自動：在背景下載與核對，在閒置或離開電腦時完成更新；需要你處理時才提醒。',
    },
    ask: { label: '下載後詢問', note: '下載後詢問：在背景下載與核對，每一版都等你按「立即安裝」。' },
    manual: { label: '手動檢查', note: '手動檢查：背景不檢查也不下載，要更新時按「檢查更新」。' },
  }[mode];
  const scheduled = mode !== 'manual';
  return {
    label: known ? known.label : mode || '未知',
    note: known ? known.note : '',
    scheduled, // 排程會不會自己查詢新版
    // 排程現在會不會自己下載：手動檢查與暫停中都不會。
    background: (pausedUntil) => scheduled && !pausedUntil,
  };
};
