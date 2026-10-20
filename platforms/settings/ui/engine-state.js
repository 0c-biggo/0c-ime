// 總覽要不要提示「輸入法仍未全部切換」：只看 0c-update status -json，不碰畫面，node 測試直接載入
// （platforms/settings/test/engine_state_test.js）。
//
// 應該在執行的版本與 0c-update 算 update.installed 的方式相同：做過相容核心更新時是引擎指標
// （status.engine，只換引擎、外殼不變），沒有指標時是完整安裝的收據（status.installed）。
// 只拿收據比，核心更新後執行中的新引擎會被當成「舊版」，總覽一直誤報仍未切換。
window.ZcEngineState = function (status, platform) {
  const runtime = (status && status.runtime) || {};
  const update = (status && status.update) || {};
  const expected = (status && (status.engine || status.installed)) || '';
  const shellPending = update.stage === 'pending' && !!update.target && update.target.kind === 'shell';
  const blocked = update.blocked || (status && status.blocked);
  const runningOld = runtime.state === 'running' && !!runtime.version && !!expected && runtime.version !== expected;
  // 核心更新只換引擎：舊引擎在組字結束、安全時機交接，任何平台都不用登出。
  // 完整安裝（外殼）後還在跑舊引擎時，Windows／macOS 要重新登入才會全部換成新版。
  const coreSwitch = !!(status && status.engine);
  // macOS 安裝時已請舊的輸入法程式結束（#170）：還沒結束的是使用者正在打字，停手後自動換新，不必登出。
  // 與 internal/autoupdate 的 ShellRetiringDetail 相同。
  const retiring = shellPending && !!blocked && blocked.detail === '已安裝，停止打字後自動換成新版，不必登出';
  const relogin = !retiring && (shellPending || (runningOld && !coreSwitch && /^(windows|macos)\//.test(platform || '')));
  return {
    expected,
    running: runtime.version || '',
    shellPending,
    pendingDetail: shellPending && blocked ? blocked.detail || '' : '',
    runningOld,
    retiring,
    relogin,
  };
};
