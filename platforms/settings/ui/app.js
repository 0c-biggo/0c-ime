// 靈犀注音設定的畫面。資料全部來自 0c-update 的 JSON 契約（docs/settings-contract.md）：
// 設定項目依契約動態產生，所以日後新增設定不必改這裡；所有文字都以 textContent 組成，不使用 innerHTML。
(function () {
  'use strict';

  const tauri = window.__TAURI__;
  const invoke = (command, args) => tauri.core.invoke(command, args);
  const PAGES = ['overview', 'styles', 'mode', 'candidates', 'keyboard', 'output', 'vocab', 'models', 'update', 'troubleshoot', 'about'];
  // 0.2.5 以前的「輸入設定」（#207 拆成三頁）：舊的 0c-update、捷徑與說明寫的 --page input 導到側欄的第一頁「中英切換」。
  const PAGE_ALIAS = { input: 'mode' };
  const pageName = (name) => PAGE_ALIAS[name] || (PAGES.includes(name) ? name : 'overview');

  const state = {
    info: null, status: null, settings: null, error: null,
    busy: new Set(), profileBusy: false, checking: false, updating: null, modeBusy: false, repairAsk: false, exporting: false, summary: null,
  };
  let page = 'overview';
  let toastTimer = null;
  // 「我的詞彙」只在 features.vocab 為 true 時出現（0.2.0 起恆為 true；舊版 0c-update 沒有這個欄位時藏起來）；
  // null 表示還不知道（狀態還沒讀到）。
  const vocabEnabled = () => (state.settings || state.status
    ? !!((state.settings && state.settings.features && state.settings.features.vocab) || (state.status && state.status.features && state.status.features.vocab))
    : null);
  let vocab = null;
  let styles = null;
  let models = null; // 「模型」頁（#180，models.js）

  // ---- DOM ----
  function h(tag, attrs, ...kids) {
    const node = document.createElement(tag);
    for (const [key, value] of Object.entries(attrs || {})) {
      if (value === false || value == null) continue;
      if (key === 'class') node.className = value;
      else if (key.startsWith('on')) node.addEventListener(key.slice(2), value);
      else if (value === true) node.setAttribute(key, '');
      else node.setAttribute(key, value);
    }
    for (const kid of kids.flat()) {
      if (kid == null || kid === false) continue;
      node.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
    }
    return node;
  }
  const clear = (node) => { while (node.firstChild) node.removeChild(node.firstChild); return node; };
  // 8×14 的箭頭（結果列右邊的 ›、「回到搜尋結果」的 ‹）。CSP 不允許行內樣式，大小與顏色由屬性與 class 決定。
  function chevron(d, width, height, cls) {
    const SVG = 'http://www.w3.org/2000/svg';
    const node = document.createElementNS(SVG, 'svg');
    const attrs = { 'aria-hidden': 'true', width, height, viewBox: '0 0 8 14', fill: 'none', stroke: 'currentColor', 'stroke-width': '1.8', 'stroke-linecap': 'round', 'stroke-linejoin': 'round', class: cls };
    for (const [key, value] of Object.entries(attrs)) if (value != null) node.setAttribute(key, String(value));
    const path = document.createElementNS(SVG, 'path');
    path.setAttribute('d', d);
    node.append(path);
    return node;
  }
  const row = (left, right) => h('div', { class: 'row' }, left, right);
  const labelBlock = (label, desc, id) => h('div', {}, h('div', { class: 'label', id }, label), desc ? h('div', { class: 'desc' }, desc) : null);

  async function copyText(text) {
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        await navigator.clipboard.writeText(text);
        toast('已複製。', 'ok');
        return;
      }
    } catch (_) { /* 退回 execCommand */ }
    const area = h('textarea', { class: 'offscreen', 'aria-hidden': 'true' });
    area.value = text;
    document.body.append(area);
    area.select();
    let ok = false;
    try { ok = document.execCommand('copy'); } catch (_) { ok = false; }
    area.remove();
    toast(ok ? '已複製。' : '無法複製，請手動選取文字。', ok ? 'ok' : 'error');
  }

  function toast(message, kind) {
    const node = document.getElementById('toast');
    node.textContent = message;
    node.className = 'toast' + (kind === 'error' ? ' error' : kind === 'warn' ? ' warn' : '');
    node.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { node.hidden = true; }, kind === 'ok' ? 5000 : 12000);
  }

  // ---- 文字 ----
  const fmtTime = (value) => (value ? new Date(value).toLocaleString('zh-TW', { hour12: false }) : '無紀錄');
  const ENGINE = {
    running: '執行中', not_running: '未執行（開始輸入時會自動啟動）', unavailable: '暫時無法查詢',
  };
  function modelLabel(value, fallback) {
    switch (value) {
      case 'ready': return '已就緒';
      case 'loading': return '載入中，暫用' + fallback;
      case 'fallback': return '模型無法使用，已退回' + fallback;
      case 'disabled_by_setting': return '依設定關閉';
      case 'not_installed': return '未附模型，使用' + fallback;
      // #181：只有 Linux 的系統安裝用系統的 python3，才提示 Debian 套件名；macOS、Windows 的推論元件內附在安裝包裡。
      case 'runtime_missing': return '缺少推論元件' + (/^linux\//.test(state.info?.platform || '') ? '（請安裝 python3-numpy、python3-onnxruntime）' : '') + '，使用' + fallback;
      case 'closed': return '已停止，使用' + fallback;
      default: return '未知（引擎未提供即時狀態）';
    }
  }
  // 引擎的 *_reason：模型退回的原因（與 0c-update status 的文字相同）。
  const MODEL_REASON = {
    missing_runtime: '（缺少系統元件：Visual C++ 執行階段）', timeout: '（模型載入逾時）', failed: '（模型程式無法啟動）',
  };
  const updateMode = () => window.ZcUpdateMode(state.status?.mode, state.info?.platform, !!state.status?.features?.update_mode);
  // 由系統套件管理員更新（#181，Linux 發行版的套件）：0c-update 不檢查、不下載、不安裝，這裡也不提供這些按鈕。
  const packageManaged = () => state.status?.updates === 'package-manager';
  const PACKAGE_MANAGED = '由系統套件管理員更新';
  const INSTALL_KIND = { personal: '官網安裝（家目錄）', system: '系統套件' };
  const REASON = {
    rollout: '分批發布還沒輪到這台電腦', no_asset: '沒有這個平台的安裝包',
    withdrawn: '該版本已撤回', failed: '該版本在這台電腦上檢查失敗過，已暫停',
  };
  const STAGE = {
    found: '可立即下載，也可等候背景下載', downloaded: '已下載，等待準備', prepared: '已下載並核對完成，可立即安裝',
    failed: '該版本檢查失敗，已暫停',
  };
  const BLOCKED = {
    needs_consent: '等待你確認安裝', needs_download: '等你按「立即下載」', engine_busy: '正在組字，閒置時才會更新', needs_relogin: '重新登入後生效',
    install_failed: '安裝沒有完成', health_failed: '新版檢查失敗，已回復原版',
  };
  // 與 internal/autoupdate 的 OldShellDetail 相同：重新登入後系統仍開著更新前留下的舊版輸入法（#98）。只有 macOS 會出現。
  const OLD_SHELL = '已安裝，但系統仍開著更新前留下的舊版輸入法；登出再登入後改用新版';
  // 與 internal/autoupdate 的 ShellRetiringDetail 相同：macOS 新版已裝好，舊的輸入法程式等使用者離開電腦時才換（#170、#178）。
  // 第二個是 0.2.1／0.2.2 寫進狀態檔的舊說明。
  const SHELL_RETIRING = ['已安裝，離開電腦（螢幕鎖定或關閉）時自動換成新版，不必登出', '已安裝，停止打字後自動換成新版，不必登出'];
  const retiringDetail = (detail) => SHELL_RETIRING.includes(detail);
  const SHELL_RETIRING_HELP = '新版已安裝，鎖定螢幕或離開電腦時換上，不必登出；在那之前照常可以打字。';
  const SHELL_RETIRING_NOTE = '不必登出，在那之前照常可以打字。';
  const OLD_SHELL_HELP = '新版已安裝，但系統仍開著更新前留下的舊版輸入法。請先儲存工作，登出再登入一次就會改用新版；如果之後仍看到這則訊息，請到「問題排解」匯出狀態摘要回報。';
  const VALUE = { neural: '神經選字', statistical: '統計引擎', off: '關閉', select: '僅供選字', auto: '自動修正（實驗）', vertical: '直排', grid: '多排', before: '游標前方', after: '游標後方', standard: '標準', eten: '倚天 41 鍵', hsu: '許氏', pinyin: '漢語拼音', xiaohe: '小鶴雙拼' };
  const ERROR = {
    read_only: '設定檔來自較新版本的靈犀注音，這個版本只能查看，不能修改。',
    corrupt: '設定檔無法解析。可到「總覽」修復。',
    busy: '另一個程序正在修改設定，請稍後再試。',
    in_progress: '更新正在進行，完成後可再檢查。',
  };
  const errorText = (error) => (error && (ERROR[error.code] || error.message)) || '發生未知的錯誤。';

  const bytesText = (bytes) => (Math.max(0, bytes || 0) / (1024 * 1024)).toLocaleString('zh-TW', { maximumFractionDigits: 1 }) + ' MB';
  const percentOf = (op) => op.total > 0 ? Math.min(100, Math.max(0, Math.floor(op.received * 100 / op.total))) : null;
  function operationText(op) {
    switch (op.phase) {
      case 'checking': return '正在檢查更新…';
      case 'downloading': return '正在下載' + (percentOf(op) == null ? '…' : ' ' + percentOf(op) + '%');
      case 'retrying': return '連線中斷，正在重試下載…';
      case 'verifying': return '下載完成，正在核對檔案…';
      case 'preparing': return '正在準備安裝…';
      case 'installing': return '正在安裝，請稍候…';
      default: return '更新正在進行…';
    }
  }

  // Windows 外殼裝好不等重新登入（#253）：待完成只會是新引擎還沒接手，說明由 0c-update 照實給（ShellEngine*Detail）。
  const isWindows = () => /^windows\//.test(state.info?.platform || '');
  const WINDOWS_REOPEN_NOTE = '之後開啟的應用程式直接使用新版；已經開著的要重新開啟才會換成新版。';

  function updateSummary(update) {
    if (!update) return { text: '尚無更新資訊。', tone: 'muted' };
    if (update.operation) return { text: operationText(update.operation), tone: 'warn' };
    if (update.stage === 'pending' && update.target) {
      if (update.target.kind !== 'shell') return { text: '正在確認新版引擎是否可用。', tone: 'warn' };
      if (isWindows()) return { text: update.target.version + '：' + (update.blocked?.detail || '已安裝，正在確認新版引擎') + '。', tone: 'warn' };
      if (update.blocked?.detail === OLD_SHELL) return { text: update.target.version + ' 已安裝，仍在使用舊版輸入法。', tone: 'warn' };
      if (retiringDetail(update.blocked?.detail)) return { text: update.target.version + ' 已安裝，鎖定螢幕或離開電腦時換上。', tone: 'warn' };
      return { text: update.target.version + ' 等待重新登入後生效。', tone: 'warn' };
    }
    switch (update.state) {
      case 'never_checked': return { text: '尚未檢查更新。', tone: 'muted' };
      case 'update_available': return { text: '有新版 ' + update.target.version + '。', tone: 'warn' };
      case 'check_failed': return { text: '目前無法連線更新伺服器，目前版本仍可使用。', tone: 'warn' };
      default: {
        let text = '已是目前可用的最新版本。';
        const latest = update.latest;
        if (latest && !latest.eligible && latest.reason) {
          text += '索引上有 ' + latest.version + '，但' + (REASON[latest.reason] || '暫時不可更新') + '。';
        }
        return { text, tone: 'ok' };
      }
    }
  }

  function applyText(apply) {
    switch (apply && apply.state) {
      case 'applied': return ['已儲存，下一個按鍵起生效。', 'ok'];
      // 引擎沒在執行：外殼在下一個按鍵啟動引擎（macOS、Windows 由 0c-update 先叫起常駐引擎），啟動時讀新設定。
      case 'engine_not_running': return ['已儲存，下一個按鍵起生效。', 'ok'];
      case 'deferred_composing': return ['已儲存。你正在組字，完成後會在輸入法下次重新啟動時生效。', 'warn'];
      case 'timeout': return ['已儲存，但等候套用逾時，尚未生效。', 'warn'];
      case 'failed': return ['已儲存，但尚未套用' + (apply.detail ? '（' + apply.detail + '）' : '') + '。', 'warn'];
      default: return ['已儲存，輸入法下次啟動時生效。', 'ok'];
    }
  }

  // ---- 資料 ----
  async function refresh() {
    try {
      const [status, settings] = await Promise.all([invoke('get_status'), invoke('get_settings')]);
      state.status = status;
      state.settings = settings;
      state.error = null;
    } catch (error) {
      state.error = String(error);
    }
    if (vocabEnabled() && page === 'overview') await vocab.refreshOverview();
    if (page === 'models') await models.refresh();
    render();
  }

  async function change(key, value) {
    return changePairs([[key, value]]);
  }

  // note：成功時接在套用結果前面的說明（複製成自訂後再改一項時，#275、#318）；failNote：接在失敗原因前面
  // （#318：已複製成自訂、這一項卻沒改成時說明）。
  async function changePairs(pairs, note, failNote) {
    const focused = document.activeElement?.dataset?.focus;
    for (const [key] of pairs) state.busy.add(key);
    render();
    try {
      const result = await invoke('set_settings', { pairs, apply: true, repair: false });
      if (!result.ok) toast((failNote || '') + errorText(result.error), 'error');
      else {
        const [text, tone] = applyText(result.apply);
        toast((note || '') + text, tone);
      }
    } catch (error) {
      toast((failNote || '') + String(error), 'error');
    }
    for (const [key] of pairs) state.busy.delete(key);
    await refresh();
    if (focused && document.activeElement === document.body) {
      const control = Array.from(document.querySelectorAll('[data-focus]')).find((node) => node.dataset.focus === focused);
      if (control && !control.disabled) control.focus();
    }
  }

  async function repair() {
    state.repairAsk = false;
    try {
      const result = await invoke('set_settings', { pairs: [['selection', 'neural']], apply: true, repair: true });
      if (!result.ok) toast(errorText(result.error), 'error');
      else toast('設定檔已修復，原檔已改名保留。', 'ok');
    } catch (error) {
      toast(String(error), 'error');
    }
    await refresh();
  }

  async function checkNow() {
    state.checking = true;
    render();
    try {
      const result = await invoke('check_update');
      if (!result.ok) toast(errorText(result.error), result.error?.code === 'in_progress' ? 'warn' : 'error');
      // 結果由頁面上的狀態框顯示，toast 只確認檢查做完，不重複同一句（#330）。
      else toast(result.update.state === 'check_failed' ? '檢查沒有完成，原因見下方。' : '檢查完成。', result.update.state === 'check_failed' ? 'warn' : 'ok');
    } catch (error) {
      toast(String(error), 'error');
    }
    state.checking = false;
    await refresh();
  }

  async function updateNow(kind) {
    const target = state.status?.update?.target;
    if (!target || state.updating) return;
    state.updating = kind;
    render();
    try {
      const result = await invoke(kind === 'download' ? 'download_update' : 'install_update', { version: target.version, sha256: target.sha256 });
      if (!result.ok) toast(errorText(result.error), result.error?.code === 'in_progress' ? 'warn' : 'error');
      else if (result.update?.blocked && result.update.stage !== 'pending' && !(kind === 'download' && result.update.stage === 'prepared')) toast(result.update.blocked.detail || '更新尚未完成，請再試一次。', 'warn');
      else if (kind === 'download') toast(result.update?.operation ? '已開始下載，進度見下方。' : result.update?.stage === 'pending' ? updateSummary(result.update).text : '下載完成，可按「立即安裝」。', 'ok');
      else toast(result.update?.stage === 'pending'
        ? (retiringDetail(result.update.blocked?.detail) ? SHELL_RETIRING_HELP : '請先儲存工作，登出再登入以完成更新。')
        : '更新作業已完成。', 'ok');
    } catch (error) {
      toast(String(error), 'error');
    } finally {
      state.updating = null;
      await refresh();
    }
  }

  // 更新頁的「更新方式」下拉選單（#340）：0c-update mode auto|ask|manual -json，只改狀態檔，不連網。
  const MODE_CHANGED = {
    auto: '已改成自動更新。',
    ask: '已改成下載後詢問：有新版本時會通知你，按「立即安裝」才更新。',
    manual: '已改成手動更新：有新版本時會通知你，不會自己下載。',
  };
  async function setUpdateMode(mode) {
    if (state.modeBusy || mode === state.status?.mode) return;
    state.modeBusy = true;
    render();
    try {
      const result = await invoke('set_update_mode', { mode });
      if (!result.ok) toast('沒有改成：' + errorText(result.error), 'error');
      else toast(MODE_CHANGED[mode] || '已改好。', 'ok');
    } catch (error) {
      toast('沒有改成：' + String(error), 'error');
    } finally {
      state.modeBusy = false;
      await refresh();
    }
    // 處理中選單停用，焦點掉到頁面上；鍵盤操作的人接著在選單上繼續（同 changePairs）。
    if (document.activeElement === document.body) {
      const control = document.querySelector('[data-focus="update-mode"]');
      if (control && !control.disabled) control.focus();
    }
  }
  // 0.2.15 起的 0c-update 才接受 mode -json（status 的 features.update_mode）；舊版只顯示更新方式。
  const updateModeSelect = () => !!(state.status?.features?.update_mode && typeof state.status.mode === 'string' && state.status.mode);

  let pollingStatus = false;
  async function pollUpdate() {
    if (document.hidden || pollingStatus || (page !== 'update' && !state.updating)) return;
    pollingStatus = true;
    try {
      state.status = await invoke('get_status');
      render();
    } catch (_) { /* refresh reports persistent read failures; retain the last known progress */ }
    finally { pollingStatus = false; }
  }

  async function exportSummary() {
    state.exporting = true;
    render();
    try {
      state.summary = (await invoke('export_summary')).path;
    } catch (error) {
      toast(String(error), 'error');
    }
    state.exporting = false;
    render();
  }

  // ---- 頁面 ----
  // 資料還沒到、或讀取失敗時的畫面；回傳 true 表示這一頁沒有資料可畫。
  function problem(root) {
    if (state.error) {
      root.append(
        h('div', { class: 'notice error' }, h('p', {}, '無法讀取靈犀注音的狀態：' + state.error)),
        h('button', { type: 'button', class: 'secondary', onclick: refresh }, '重試'));
    }
    if (state.status && state.settings) return false;
    if (!state.error) root.append(h('p', { class: 'lead' }, '讀取中…'));
    return true;
  }

  function fileNotices(settings) {
    const out = [];
    if (!settings) return out;
    const file = settings.file;
    if (file.state === 'corrupt') {
      out.push(h('div', { class: 'notice error' },
        h('p', {}, '設定檔無法解析，目前顯示的是預設值。'),
        state.repairAsk
          ? [h('p', {}, '修復會把原檔改名保留（settings.json.corrupt-…），再從預設值重新開始；你的個人詞庫不受影響。'),
            h('div', { class: 'actions' },
              h('button', { type: 'button', class: 'primary', onclick: repair }, '確認修復'),
              h('button', { type: 'button', class: 'secondary', onclick: () => { state.repairAsk = false; render(); } }, '取消'))]
          : h('div', { class: 'actions' },
            h('button', { type: 'button', class: 'primary', 'data-focus': 'repair', onclick: () => { state.repairAsk = true; render(); } }, '修復設定檔'))));
    }
    if (file.read_only) {
      out.push(h('div', { class: 'notice' }, h('p', {}, '設定檔來自較新版本的靈犀注音，這個版本只能查看，不能修改。')));
    }
    return out;
  }

  function overview(root) {
    root.append(h('h1', {}, '總覽'));
    if (problem(root)) return;
    const status = state.status;
    const settings = state.settings;
    // 版本、是否仍在跑舊版的判斷在 engine-state.js：核心更新後以引擎指標為準，不只看外殼的收據。
    const engine = window.ZcEngineState(status, state.info?.platform);
    root.append(h('p', { class: 'lead' }, '已安裝版本 ' + (engine.expected || '（尚無版本收據）')));
    root.append(...fileNotices(settings));
    if (settings.pending_restart) {
      // 帶到第一個尚未套用的項目所在的那一頁（#207）。
      const pending = (settings.items || []).find((item) => item.pending);
      const target = pending ? pageOfKey(pending.key) : 'mode';
      root.append(h('div', { class: 'notice' },
        h('p', {}, '有設定已儲存、尚未套用；完成組字後，輸入法重新啟動時會生效。'),
        h('div', { class: 'actions' }, h('button', { type: 'button', class: 'secondary', onclick: () => show(target) }, '查看「' + pageTitle(target) + '」'))));
    }
    const runtime = status.runtime || {};
    const deferredInstall = engine.pendingDetail === '登出再登入時自動完成安裝';
    const oldShell = engine.pendingDetail === OLD_SHELL;
    if (oldShell) {
      root.append(h('div', { class: 'notice' }, h('p', {}, OLD_SHELL_HELP)));
    } else if (engine.retiring) {
      root.append(h('div', { class: 'notice' }, h('p', {}, SHELL_RETIRING_HELP)));
    } else if (engine.engineWaiting) {
      root.append(h('div', { class: 'notice' },
        h('p', {}, engine.runningOld ? '已安裝 ' + engine.expected + '，目前輸入引擎使用 ' + engine.running + '。' : '已安裝 ' + engine.expected + '，正在確認輸入引擎。'),
        engine.runningOld ? h('p', {}, '完成組字後，閒置時會自動換上這一版的引擎，不必登出；這段時間照常可以打字。') : null,
        h('p', {}, '已經開著的應用程式要重新開啟才會換成這一版。')));
    } else if (engine.shellPending || engine.runningOld) {
      root.append(h('div', { class: 'notice' },
        h('p', {}, deferredInstall ? '套件已下載，等待重新登入時安裝。' : '新版已安裝；目前執行的輸入法仍未全部切換。'),
        engine.runningOld ? h('p', {}, '已安裝 ' + engine.expected + '，目前輸入引擎使用 ' + engine.running + '。') : null,
        h('p', {}, engine.relogin ? '請先儲存工作，登出再登入，讓所有應用程式載入新版輸入法。' : '請先完成組字，等待輸入引擎重新啟動以載入新版。')));
    }
    const rows = [
      row(labelBlock('目前執行的輸入引擎'), h('div', { class: 'value' }, (ENGINE[runtime.state] || '未知') + (runtime.version ? '（' + runtime.version + '）' : ''))),
    ];
    if (runtime.state === 'running') {
      rows.push(
        row(labelBlock('神經選字'), h('div', { class: 'value' }, modelLabel(runtime.selection_state, '統計引擎') + (MODEL_REASON[runtime.selection_reason] || ''), ' ',
          h('button', { type: 'button', class: 'secondary', 'data-focus': 'models-card', onclick: () => show('models') }, '查看'))),
        row(labelBlock('補標點（Beta）'), h('div', { class: 'value' }, modelLabel(runtime.punctuation_state, '原文提交') + (MODEL_REASON[runtime.punctuation_reason] || ''))));
    }
    const managed = packageManaged();
    const summary = managed ? { text: PACKAGE_MANAGED, tone: 'muted' } : updateSummary(status.update);
    rows.push(
      row(labelBlock('更新'), h('div', { class: 'value' },
        h('span', { class: summary.tone === 'ok' ? 'status-ok' : summary.tone === 'warn' ? 'status-warn' : 'muted' }, summary.text), ' ',
        h('button', { type: 'button', class: 'secondary', onclick: () => show('update') }, '查看'))),
      row(labelBlock('更新方式'), h('div', { class: 'value' }, managed ? '系統套件管理員' : updateMode().label)));
    root.append(h('div', { class: 'card' }, ...rows));
    if (vocabEnabled()) {
      const text = vocab.overviewText();
      root.append(h('div', { class: 'card vocab-card' }, row(
        labelBlock('我的詞彙', text || '讀取中…'),
        h('button', { type: 'button', class: 'secondary', 'data-focus': 'vocab-card', onclick: () => show('vocab') }, text && text.startsWith('把') ? '開始加詞' : '管理'))));
    }
  }

  // 被內建風格鎖住的項目的滑鼠提示（#318）。
  const LOCKED_TITLE = '修改時會自動複製成你自己的輸入習慣';

  function control(item, locked) {
    const values = item.values || [];
    // item.disabled：另一個設定讓這一項不起作用（倚天 41 鍵時的混打與 Emoji，#83；許氏時的混打，#183；不用 Shift 時的開始模式，#184）。開關顯示實際狀態（關閉），
    // 設定檔中的值不動，條件解除後照舊。
    const view = window.ZcInputStylesLogic.controlState(item, locked || state.profileBusy || state.busy.has(item.key));
    // 被內建風格鎖住（#275）：外觀淡一些、標著由哪個風格決定，但按得到；按了直接複製成自訂並改這一項（#318）。
    const ask = view.copyToChange ? (value) => copyAndChange(item.key, value) : null;
    const disabled = view.disabled && !ask;
    const labelId = 'label-' + item.key;
    if (values.length === 2 && values.includes('off')) {
      const on = values.find((v) => v !== 'off');
      const checked = view.checked;
      return h('button', {
        type: 'button', class: ask ? 'switch locked' : 'switch', role: 'switch', 'aria-checked': String(checked), 'aria-labelledby': labelId,
        title: ask ? LOCKED_TITLE : null, 'data-focus': item.key, disabled,
        onclick: () => (ask ? ask(checked ? 'off' : on) : change(item.key, checked ? 'off' : on)),
      });
    }
    // 選項也畫實際使用的值：不用 Shift 時開始模式固定為注音（#184，item.effective），設定檔裡的英文保留到改回 Shift。
    const shown = view.value;
    return h('fieldset', { class: 'choices', 'aria-labelledby': labelId, disabled },
      ...values.map((value) => h('label', {},
        h('input', {
          type: 'radio', name: item.key, value, checked: shown === value, 'data-focus': item.key + ':' + value,
          class: ask ? 'locked' : null, title: ask ? LOCKED_TITLE : null,
          onclick: ask ? (event) => { event.preventDefault(); if (value !== shown) ask(value); } : null,
          onchange: () => (ask ? ask(value) : change(item.key, value)),
        }), (item.value_labels && item.value_labels[value]) || VALUE[value] || value)));
  }

  // 改被內建風格鎖住的項目（#318）：先把目前的內建風格複製成自訂並套用（與「輸入習慣」頁的「建立並套用」相同），
  // 成功後才改這一項，只用一則提示說已改用哪個自訂習慣。複製失敗時什麼都沒改，提示原因；複製成功但這一項沒改成時，
  // 提示已換成自訂、這一項為什麼沒改，使用者可以直接再按一次（這時已是自訂，不再複製）。
  async function copyAndChange(key, value) {
    if (state.profileBusy) return;
    const target = window.ZcInputStylesLogic.copyTarget(state.settings?.profiles);
    if (!target) return change(key, value);
    state.profileBusy = true;
    state.busy.add(key);
    render();
    let copied = false;
    try {
      const result = await invoke('profile_command', { action: 'copy', id: target.id, name: target.name });
      copied = !!result.ok;
      if (!copied) toast('沒有改成：' + errorText(result.error), 'error');
    } catch (error) {
      toast('沒有改成：' + String(error), 'error');
    }
    state.profileBusy = false;
    state.busy.delete(key);
    if (copied) await changePairs([[key, value]], target.done, target.failed);
    else await refresh();
    // 複製期間這一項停用，焦點掉到頁面上；鍵盤操作的人接著在原處繼續（開關，或單選裡選中的那一項）。
    if (document.activeElement === document.body) {
      const section = document.getElementById('page-' + page);
      const control = section && (section.querySelector('input[name="' + key + '"]:checked') ||
        Array.from(section.querySelectorAll('[data-focus]')).find((node) => node.dataset.focus === key));
      if (control && !control.disabled) control.focus();
    }
  }

  function profileLockLabel() {
    const profiles = state.settings?.profiles;
    const active = profiles?.items?.find((profile) => profile.id === profiles.active);
    return active ? '由「' + active.name + '」風格決定' : '由目前的輸入習慣決定';
  }

  function itemRow(item, locked) {
    const name = h('span', {}, item.label || item.key);
    if (item.locked) name.append(h('span', { class: 'badge muted-badge', title: LOCKED_TITLE }, profileLockLabel()));
    if (item.disabled && item.disabled_reason) name.append(h('span', { class: 'badge muted-badge' }, item.disabled_reason));
    else if (item.pending) name.append(h('span', { class: 'badge' }, '已儲存，尚未套用'));
    const view = window.ZcInputStylesLogic.controlState(item, false);
    const hint = hintLine(item, view.toggle ? (view.checked ? (item.values || []).find((v) => v !== 'off') : 'off') : view.value);
    const help = window.ZcItemHelp.url(item);
    const input = control(item, locked);
    if (hint) input.setAttribute('aria-describedby', hint.id);
    const node = row(
      h('div', {}, h('div', { class: 'label', id: 'label-' + item.key }, name),
        item.description || help ? h('div', { class: 'desc' }, item.description || '', helpLink(help, item.label || item.key, item.key)) : null,
        hint, causeLink(item)),
      input);
    // 「說明」連結不在 Tab 順序裡（每頁十幾項，逐項多一站太慢）；鍵盤在這一項上按 F1 開同一個網址。
    if (help) node.addEventListener('keydown', (event) => { if (event.key === 'F1') { event.preventDefault(); openHelp(help); } });
    return node;
  }

  // ---- 選項提示與「說明」連結（#362） ----
  // 多選一只顯示目前選中那個選項的提示（契約的 value_hints），換選項後跟著換；沒有提示的選項不顯示。
  function hintLine(item, shown) {
    const hint = window.ZcItemHelp.hint(item, shown, VALUE);
    if (!hint) return null;
    return h('div', { class: 'hint', id: 'hint-' + item.key }, hint.label ? h('strong', {}, hint.label + '：') : null, hint.text);
  }
  // 官網的設定說明頁用系統瀏覽器開（Rust 的 open_url；設定視窗本身不離開本機頁面）。
  function openHelp(url) {
    invoke('open_url', { url }).catch((error) => toast('無法開啟瀏覽器：' + error + '。網址：' + url, 'error'));
  }
  function helpLink(url, label, key) {
    if (!url) return null;
    return h('button', {
      type: 'button', class: 'link help', tabindex: '-1', title: url, 'data-help': key,
      'aria-label': '「' + label + '」的詳細說明（在瀏覽器開啟）', 'aria-keyshortcuts': 'F1', onclick: () => openHelp(url),
    }, '說明', h('span', { 'aria-hidden': 'true' }, ' ↗'));
  }
  // 頁首的「說明」連結：只有輸入習慣、更新、我的詞彙三頁有（ZcItemHelp.pageUrl），接在頁首那一句後面，
  // 樣式與行為同逐項的連結。data-help 是設定說明頁的錨點。
  function pageHelpLink(name, title) {
    const url = window.ZcItemHelp.pageUrl(name);
    return helpLink(url, title, url ? url.slice(url.indexOf('#') + 1) : '');
  }

  // ---- 中英切換、選字、鍵盤與符號（#207：原本的「輸入設定」一頁 19 項，拆成三頁、不摺疊） ----
  // 各頁依這裡的順序畫（也是 Tab 順序）。不在任何一頁的新項目（較新的 0c-update 才有的設定）放在「選字」最後，
  // 所以契約新增設定時畫面不必先改；要放別頁再加進 keys。輸出設定的兩項在 output()。
  const SAVE_NOTE = '改動會立即儲存。';
  const INPUT_PAGES = {
    mode: {
      title: '中英切換', lead: '用哪個鍵切換注音和英文，以及在注音中直接打英文。',
      // shift-tap-ms（#319，單按 Shift 的時限）是中英切換鍵的細節，接在它下面。
      keys: ['toggle-key', 'shift-tap-ms', 'start-mode', 'mode-memory', 'mixed-input', 'english-autocomplete', 'caps-lock-english', 'style-shift-letter', 'style-caps-letter'],
    },
    candidates: {
      title: '選字', lead: '候選字怎麼排、怎麼選，以及要不要列出近音的字（模糊音）。',
      // zhuyin-error-hint（#300，打錯注音時提示）跟不打聲調一樣是按聲調時的規則，接在它後面。
      // personal-learning（#297，記住我選的字詞）是個人偏好，排在選字項目最後、輸入習慣的規則之前。
      keys: ['selection', 'document-context', 'space-selects', 'candidate-cursor', 'advance-after-select', 'candidate-layout', 'selection-keys', 'esc-clear', 'toneless', 'zhuyin-error-hint', 'personal-learning', 'style-tab', 'style-up', 'style-candidate-tab', 'style-candidate-space', 'style-candidate-enter', 'style-page-size', 'style-candidate-navigation'],
    },
    keyboard: {
      title: '鍵盤與符號', lead: '注音的鍵盤排列，以及標點符號和 Emoji 怎麼打。',
      // emoji-equals-first 是 Emoji 選單的子選項，畫在它下面。
      // shift-symbol-halfwidth（標點寬度，#258、#282 三選一）跟 Ctrl＋標點是一對（半形與全形），排在它下面；
      // cjk-latin-space（自動加空格，#273；#365 擴成三選一：關閉／中英文之間／中英文與數字之間）也是半形排版，接在後面。
      keys: ['keyboard-layout', 'ctrl-punctuation', 'shift-symbol-halfwidth', 'cjk-latin-space', 'punctuation', 'emoji-shortcut', 'emoji-equals-first', 'style-punctuation', 'style-shift-space', 'style-shift-delete'],
    },
  };
  const OUTPUT_KEYS = ['output-simplified', 'simplified-hotkey'];
  const OUTPUT_LEAD = '送出的文字要不要轉成簡體字；也可以從輸入法選單的「輸出簡體字」切換。';
  // 四組模糊音合成一個開關時的說明（設定頁與搜尋結果共用）。
  const FUZZY_DESCRIPTION = '候選多列出 ㄗ／ㄓ、ㄘ／ㄔ、ㄙ／ㄕ、ㄣ／ㄥ 的近音字，原音排在前面。';
  const pageTitle = (name) => (INPUT_PAGES[name] ? INPUT_PAGES[name].title : '輸入設定');
  function pageOfKey(key) {
    if (OUTPUT_KEYS.includes(key)) return 'output';
    if (key.startsWith('fuzzy-')) return 'candidates';
    for (const [name, spec] of Object.entries(INPUT_PAGES)) if (spec.keys.includes(key)) return name;
    return 'candidates';
  }

  // 某一頁由上到下的設定項目（不含合成一個開關的四組模糊音）：設定頁照它畫，搜尋結果（#363）在同一頁裡也照它排。
  function itemsOnPage(name, items) {
    if (name === 'output') return OUTPUT_KEYS.map((key) => items.find((item) => item.key === key)).filter(Boolean);
    const spec = INPUT_PAGES[name];
    if (!spec) return [];
    const order = (item) => (spec.keys.includes(item.key) ? spec.keys.indexOf(item.key) : spec.keys.length);
    return items.filter((item) => pageOfKey(item.key) === name && !item.key.startsWith('fuzzy-'))
      .map((item, i) => [order(item), i, item]).sort((a, b) => a[0] - b[0] || a[1] - b[1]).map(([, , item]) => item);
  }

  // 停用的原因在另一頁時（許氏時的中英混打在「中英切換」、鍵盤排列在「鍵盤與符號」），說明要去哪裡改，按了就過去。
  // 原因在同一頁（不用 Shift 時的開始模式就在中英切換鍵下面）不必另外寫。
  function causeLink(item) {
    if (!item.disabled || !item.disabled_by) return null;
    const target = pageOfKey(item.disabled_by);
    if (target === page) return null;
    const cause = (state.settings?.items || []).find((other) => other.key === item.disabled_by);
    const what = cause ? '「' + (cause.label || cause.key) + '」' : '';
    return h('div', { class: 'desc' }, h('button', {
      type: 'button', class: 'link goto', 'data-focus': 'goto:' + item.key, onclick: () => goTo(item.disabled_by),
    }, '到「' + pageTitle(target) + '」修改' + what));
  }

  // 切到某一項所在的頁面，並把焦點放在它的開關或選中的選項上。
  function goTo(key) {
    show(pageOfKey(key));
    const section = document.getElementById('page-' + page);
    const target = section && (section.querySelector('input[name="' + key + '"]:checked') ||
      Array.from(section.querySelectorAll('[data-focus]')).find((node) => node.dataset.focus === key || node.dataset.focus.startsWith(key + ':')));
    if (target && !target.disabled) target.focus();
  }

  function settingsPage(name, root) {
    const spec = INPUT_PAGES[name];
    root.append(h('h1', {}, spec.title), h('p', { class: 'lead' }, spec.lead + SAVE_NOTE));
    if (problem(root)) return;
    const settings = state.settings;
    root.append(...fileNotices(settings));
    const locked = settings.file.read_only;
    const items = settings.items || [];
    if (items.some((item) => item.locked && pageOfKey(item.key) === name)) {
      root.append(h('div', { class: 'notice info' }, h('p', {}, '部分設定' + profileLockLabel() + '；改這些項目時，會先複製成你自己的輸入習慣再改。'),
        h('button', { type: 'button', class: 'secondary', 'data-focus': 'style-settings', onclick: () => show('styles') }, '選用風格／複製成自訂')));
    }
    const emoji = items.find((item) => item.key === 'emoji-shortcut');
    const equalsFirst = items.find((item) => item.key === 'emoji-equals-first');
    const mine = itemsOnPage(name, items).filter((item) => !(emoji && item === equalsFirst));
    const rows = mine.map((item) => {
      const parent = itemRow(item, locked ||
        (item.key === 'emoji-shortcut' && state.busy.has('emoji-equals-first')));
      if (item.key !== 'emoji-shortcut' || !equalsFirst) return parent;
      const child = itemRow(equalsFirst, locked || item.value !== 'equals' || !!item.disabled || state.busy.has(item.key));
      child.classList.add('suboption');
      return h('div', { class: 'setting-group', role: 'group', 'aria-labelledby': 'label-' + item.key }, parent, child);
    });
    if (rows.length) root.append(h('div', { class: 'card' }, ...rows));
    const fuzzy = name === 'candidates' ? items.filter((item) => item.key.startsWith('fuzzy-')) : [];
    if (fuzzy.length) {
      const enabled = fuzzy.some((item) => item.value !== 'off');
      const label = h('span', {}, '模糊音');
      if (fuzzy.some((item) => item.pending)) label.append(h('span', { class: 'badge' }, '已儲存，尚未套用'));
      // 四組合成一個開關；「說明」連到第一組的那一節（設定說明頁把四個錨點放在同一節）。
      const fuzzyHelp = window.ZcItemHelp.url(fuzzy[0]);
      const fuzzyRow = row(
        h('div', {}, h('div', { class: 'label', id: 'label-fuzzy' }, label),
          h('div', { class: 'desc' }, FUZZY_DESCRIPTION, helpLink(fuzzyHelp, '模糊音', 'fuzzy'))),
        h('button', {
          type: 'button', class: 'switch', role: 'switch', 'aria-checked': String(enabled), 'aria-labelledby': 'label-fuzzy',
          'data-focus': 'fuzzy', disabled: locked || fuzzy.some((item) => state.busy.has(item.key)),
          onclick: () => changePairs(fuzzy.map((item) => [item.key, enabled ? 'off' : 'select'])),
        }));
      if (fuzzyHelp) fuzzyRow.addEventListener('keydown', (event) => { if (event.key === 'F1') { event.preventDefault(); openHelp(fuzzyHelp); } });
      root.append(h('div', { class: 'card' }, fuzzyRow));
    }
    if ((settings.file.notes || []).length) {
      root.append(h('h2', {}, '注意'), h('div', { class: 'notice' }, ...settings.file.notes.map((note) => h('p', {}, note))));
    }
  }

  function update(root) {
    root.append(h('h1', {}, '更新'));
    if (problem(root)) return;
    const status = state.status;
    if (packageManaged()) {
      root.append(h('p', { class: 'lead' }, '目前版本 ' + (status.installed || '未知'), pageHelpLink('update', '更新')),
        h('div', { class: 'notice info' },
          h('p', {}, PACKAGE_MANAGED + '。'),
          h('p', {}, '這份靈犀注音是用系統的套件管理員（例如 apt）安裝的，更新與移除也請用套件管理員；這裡不會檢查、下載或安裝更新。')));
      return;
    }
    const view = status.update || {};
    const op = view.operation;
    const blocked = view.blocked || status.blocked;
    const summary = updateSummary(view);
    root.append(h('p', { class: 'lead' }, '目前版本 ' + (window.ZcEngineState(status, state.info?.platform).expected || '未知'), pageHelpLink('update', '更新')));
    // 進行中的作業由下面的進度列顯示同一句話，狀態框就不再出現（#330）。
    if (!op) root.append(h('div', { class: 'notice' + (summary.tone === 'warn' ? '' : ' info') },
      h('p', {}, summary.text),
      view.target && !op ? h('p', {}, view.stage === 'pending' && view.target.kind === 'shell'
        ? (isWindows() ? WINDOWS_REOPEN_NOTE : blocked?.detail === '登出再登入時自動完成安裝' ? '套件已下載，儲存工作後登出再登入，會完成安裝。'
          : blocked?.detail === OLD_SHELL ? OLD_SHELL_HELP : retiringDetail(blocked?.detail) ? SHELL_RETIRING_NOTE
          : '新版已安裝；請儲存工作後登出再登入，讓應用程式載入新版輸入法。')
        : view.stage === 'pending' ? '正在確認新版引擎是否可用。' : (view.stage === 'found' && !updateMode().background(status.paused_until) ? '可立即下載' : STAGE[view.stage] || ('進度：' + view.stage)) + '。') : null,
      blocked && !op && view.stage !== 'pending' && !(view.stage === 'found' && ['needs_relogin', 'needs_consent', 'needs_download'].includes(blocked.reason))
        ? h('p', {}, blocked.detail || BLOCKED[blocked.reason] || '更新暫時受阻。') : null));
    if (op) {
      const downloading = ['downloading', 'retrying'].includes(op.phase);
      const percent = downloading ? percentOf(op) : null;
      root.append(h('div', { class: 'download-progress', role: 'status' },
        h('div', { class: 'label' }, operationText(op)),
        h('progress', { max: 100, value: percent, 'aria-label': operationText(op) }),
        downloading ? h('div', { class: 'muted' }, bytesText(op.received) + ' / ' + bytesText(op.total), op.interactive ? ' · 立即下載' : ' · 背景下載') : null));
    }
    const canDownload = view.target && ['found', 'downloaded'].includes(view.stage) && (!op || (['downloading', 'retrying'].includes(op.phase) && !op.interactive));
    const canInstall = view.target && view.stage === 'prepared' && !op;
    root.append(h('div', { class: 'actions' },
      h('button', { type: 'button', class: 'secondary', disabled: state.checking || !!state.updating || !!op, 'data-focus': 'check', onclick: checkNow }, state.checking ? '檢查中…' : '檢查更新'),
      canDownload ? h('button', { type: 'button', class: 'primary', disabled: !!state.updating, 'data-focus': 'download', onclick: () => updateNow('download') }, state.updating === 'download' ? '下載中…' : op ? '立即加速下載' : '立即下載') : null,
      canInstall ? h('button', { type: 'button', class: 'primary', disabled: !!state.updating, 'data-focus': 'install', onclick: () => updateNow('install') }, state.updating === 'install' ? '安裝中…' : '立即安裝') : null));
    const mode = updateMode();
    const selectable = updateModeSelect();
    if (selectable) {
      // 三種更新方式（#340）：選到的那項底下一句說明。不認得的值（較新版本寫的）照實列出、不能選回。
      const options = mode.options.map((o) => h('option', { value: o.value, selected: o.value === status.mode }, o.label));
      if (!mode.known) options.push(h('option', { value: status.mode, selected: true, disabled: true }, mode.label));
      root.append(h('div', { class: 'card' }, row(
        h('div', {}, h('label', { class: 'label', id: 'label-update-mode', for: 'update-mode' }, '更新方式'),
          h('div', { class: 'desc', id: 'update-mode-note' }, mode.note || '目前的更新方式是「' + mode.label + '」。')),
        h('select', {
          id: 'update-mode', class: 'update-mode', 'aria-labelledby': 'label-update-mode', 'aria-describedby': 'update-mode-note',
          'data-focus': 'update-mode', disabled: state.modeBusy || !!state.updating || state.checking,
          onchange: (event) => setUpdateMode(event.target.value),
          onblur: () => { if (renderLater) setTimeout(render); },
        }, ...options))));
    }
    const facts = [
      ['上次檢查', fmtTime(view.last_check)],
      ['上次成功', fmtTime(view.last_success)],
    ];
    // 舊版 0c-update 的手動檢查排程不查詢（狀態檔留著的時間不算數）；#340 起手動更新也照排程查詢。
    if (updateMode().scheduled) facts.push(['下次排定檢查', fmtTime(view.next_check)]);
    if (view.check_failures) facts.push(['連續失敗', view.check_failures + ' 次（' + (view.error || '原因不明') + '）']);
    if (view.latest) facts.push(['索引最新版本', view.latest.version]);
    if (status.paused_until) facts.push(['暫停到', fmtTime(status.paused_until)]);
    // 有下拉選單時它就說明了目前的方式；舊版 0c-update（沒有 features.update_mode）照舊列出方式與說明。
    if (!selectable) facts.push(['更新方式', mode.label]);
    root.append(h('h2', {}, '詳細資訊'), h('div', { class: 'card' }, h('dl', { class: 'facts' }, ...facts.flatMap(([k, v]) => [h('dt', {}, k), h('dd', {}, v)]))));
    if (!selectable && mode.note) root.append(h('p', { class: 'muted', id: 'update-mode-note' }, mode.label + '：' + mode.note));
    // 按鈕的名稱就說明了用途，底下只留 Windows 安裝前要知道的事（#362）；完整說明在設定說明頁的「更新」。
    if (isWindows()) root.append(h('p', { class: 'muted' }, '安裝時可能要求系統管理員確認；已經開著的應用程式要重新開啟才會換成新版。'));
  }

  // 設定視窗不開外部網址（main.rs 的 local_origin），所以信箱只寫成文字，點一下整段選取方便複製（#152）。
  const SUPPORT_EMAIL = 'code@biggo.vip';
  const email = () => h('strong', { class: 'email' }, SUPPORT_EMAIL);

  function troubleshoot(root) {
    root.append(h('h1', {}, '問題排解'),
      h('p', { class: 'lead' }, '遇到問題，寫信到 ', email(), '，描述發生了什麼（在哪個程式、打了什麼、結果怎樣），並附上這裡匯出的檔案。'));
    root.append(h('div', { class: 'card' }, h('div', { class: 'row' },
      labelBlock('匯出狀態摘要', '內容是版本、平台、設定與更新狀態；不含你打的字、個人詞庫和使用者名稱。'),
      h('button', { type: 'button', class: 'primary', disabled: state.exporting, 'data-focus': 'export', onclick: exportSummary }, state.exporting ? '匯出中…' : '匯出'))));
    if (state.summary) {
      root.append(h('div', { class: 'notice info' },
        h('p', {}, '已匯出到：'), h('p', {}, h('code', {}, state.summary)),
        h('p', {}, '寫信到 ', email(), '，附上這個檔案，並描述發生了什麼。')));
    }
    // 同一個帳號有官網安裝與系統套件時（#181），外殼用官網安裝（使用者自己裝的通常比較新）；兩份都列出來。
    const installs = Array.isArray(state.status?.installs) ? state.status.installs : [];
    const active = installs.find((item) => item.active);
    if (installs.length > 1 || active?.kind === 'system') {
      root.append(h('h2', {}, '使用中的安裝'), h('div', { class: 'card' }, ...installs.map((item) => row(
        h('div', {}, h('div', { class: 'label' }, INSTALL_KIND[item.kind] || item.kind),
          h('div', { class: 'desc paths' }, '引擎 ' + item.server + '\n字典 ' + item.data)),
        h('div', { class: 'value' }, h('span', { class: item.active ? 'status-ok' : 'muted' }, item.active ? '使用中' : '未使用（官網安裝優先）'))))));
    }
    // 系統套件的 0c-update 不在 PATH 上，命令列範例寫完整路徑。
    const cli = active?.kind === 'system' && active.updater ? active.updater : '0c-update';
    root.append(h('h2', {}, '命令列'),
      h('p', { class: 'muted' }, '設定程式打不開、或你偏好終端機時，同樣的設定可以這樣查看與修改：'),
      h('pre', { class: 'cmd' }, cli + ' settings\n' + cli + ' set punctuation off -apply'));
  }

  function about(root) {
    root.append(h('h1', {}, '關於'));
    const info = state.info || {};
    const status = state.status || {};
    const settings = state.settings || {};
    const facts = [
      ['這個設定視窗的版本', info.version || '未知'],
      ['已安裝版本', status.installed || '（尚無版本收據）'],
      ['目前執行的引擎', status.runtime?.version || '尚未執行'],
      ['已選用的核心版本', status.engine || status.installed || '未知'],
      ['平台', info.platform || '未知'],
      ['設定檔', settings.file ? settings.file.path : '未知'],
      ['契約版本', String(settings.schema || status.schema || '未知')],
    ];
    root.append(h('p', { class: 'lead' }, '靈犀注音設定'),
      h('div', { class: 'card' }, h('dl', { class: 'facts' }, ...facts.flatMap(([k, v]) => [h('dt', {}, k), h('dd', {}, v)]))));
  }

  // 「我的詞彙」的 DOM 只建一次（vocab.js），不在每次重畫時清掉：輸入框裡正在組字時重建會打斷輸入法。
  function vocabPage(root) {
    if (vocabEnabled() === null) { clear(root).append(h('h1', {}, '我的詞彙')); problem(root); return; }
    vocab.render(root);
  }

  // 輸出設定：送出的文字怎麼轉換（#118 簡體字），跟打字、選字分開，左側獨立一頁。
  function output(root) {
    root.append(h('h1', {}, '輸出設定'), h('p', { class: 'lead' }, OUTPUT_LEAD));
    if (problem(root)) return;
    const settings = state.settings;
    root.append(...fileNotices(settings));
    // 「用快捷鍵切換簡體字」（#192）排在「輸出簡體字」下面；說明由 0c-update 依平台寫出快捷鍵。
    const items = itemsOnPage('output', settings.items || []);
    if (items.length) root.append(h('div', { class: 'card' }, ...items.map((item) => itemRow(item, settings.file.read_only))));
  }

  // ---- 搜尋（#363） ----
  // 側邊欄最上面的搜尋框打字就找設定；有字時右邊換成結果頁（蓋在目前的頁面上，page 不變，所以清除搜尋就回到原本那一頁）。
  // 比對、分組與標示在 search.js（ZcSearch，不碰畫面）；這裡是要搜哪些東西、結果頁怎麼畫、鍵盤怎麼操作。
  // 資料只用已經讀到的 settings／status，搜尋不呼叫後端。
  //   query：搜尋框裡的字。open：結果頁是否蓋在頁面上。back：頁首上方是否有「回到搜尋結果」（從結果跳過來時）。
  //   index：選取的是第幾個結果；fresh：查詢剛變，下一次畫結果時重新挑預設選取的那一筆（第一筆沒有停用的）。
  //   hit：跳過去之後要標出的那一項。focus：進搜尋框之前焦點在哪裡（Esc 還回去）。
  const find = { query: '', open: false, back: false, index: 0, fresh: false, results: [], scroll: 0, focus: null, hit: null, hitTimer: null, heading: null, composing: false, dom: null, titles: {} };
  const SEARCH_SUGGESTIONS = ['候選', '標點', 'Shift', '聲調', '簡體', '英文', '更新'];
  const SEARCH_GUIDE = 'https://0c.biggo.com/guide/settings/';
  const HIT_MS = 2500; // 標出那一項多久
  const HIT_FADE_MS = 600; // 淡掉的時間（app.css 的 .row.search-hit 的 transition）
  // 沒有設定項的頁面，頁面本身是一筆結果：名稱、一句話，加上幾個使用者會打的詞。這幾頁的文字本來就寫在畫面這一側，
  // 所以關鍵字也放這裡；設定項的關鍵字由 0c-update 的契約（keywords）提供。
  const PAGE_ENTRIES = {
    overview: { description: '已安裝的版本、輸入引擎與更新的狀態。', keywords: ['首頁', '概況', '狀況'] },
    styles: { description: '選一種熟悉的操作習慣，看過差異再套用；想微調可以複製成自訂。', keywords: ['風格', '微軟注音', 'Apple'] },
    vocab: { description: '加進來的人名、術語打得出來，也看得到靈犀自己學到了什麼。', keywords: ['加詞', '詞庫', '片語', '學到的', '匯入匯出', '清除學習紀錄'] },
    models: { description: '神經選字與補標點使用的模型。', keywords: ['下載模型', '刪除模型', 'AI', 'Hugging Face'] },
    update: { description: '檢查有沒有新版，下載並安裝。', keywords: ['版本', '自動更新', '升級'] },
    troubleshoot: { description: '匯出狀態摘要，寫信回報問題。', keywords: ['客服', '信箱', '故障', '命令列'] },
    about: { description: '設定程式與輸入法的版本、平台與設定檔的位置。', keywords: ['資訊', 'About'] },
  };
  // 四個設定頁（中英切換、選字、鍵盤與符號、輸出設定）本身也是一筆結果，排在那一頁的分組最前面，前往就是切到那一頁。
  // 只比對頁名：頁首那一句（note）只顯示、不比對，不然搜「標點」「候選」這類的字每次都多出一筆。
  const SETTINGS_PAGE_NOTES = { mode: INPUT_PAGES.mode.lead, candidates: INPUT_PAGES.candidates.lead, keyboard: INPUT_PAGES.keyboard.lead, output: OUTPUT_LEAD };
  const isMac = () => (state.info ? /^macos\//.test(state.info.platform || '') : /Mac/i.test(navigator.platform || ''));
  const searchInput = () => document.getElementById('search-input');
  const mainPane = () => document.querySelector('main');
  const reducedMotion = () => !!(window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches);

  // 所有搜得到的東西，照側邊欄的頁面順序、每一頁由上到下的順序。
  function searchEntries() {
    const items = state.settings?.items || [];
    const out = [];
    for (const name of PAGES) {
      if (name === 'vocab' && !vocabEnabled()) continue;
      const spec = PAGE_ENTRIES[name];
      const note = SETTINGS_PAGE_NOTES[name];
      if (spec || note) {
        const entry = { id: 'page:' + name, kind: 'page', page: name, label: find.titles[name] || name, description: spec ? spec.description : '', note: note || '',
          keywords: spec ? spec.keywords : [], options: [], value: '' };
        // 輸入習慣：有哪些風格、目前選用哪一個也搜得到。
        const profiles = name === 'styles' ? state.settings?.profiles : null;
        if (profiles) {
          entry.options = (profiles.items || []).map((profile) => ({ value: profile.id, name: profile.name, label: profile.name, hint: '' }));
          entry.value = (profiles.items || []).find((profile) => profile.id === profiles.active)?.name || '';
        }
        out.push(entry);
      }
      for (const item of itemsOnPage(name, items)) out.push(window.ZcSearch.itemEntry(item, name, VALUE));
      const fuzzy = name === 'candidates' ? items.filter((item) => item.key.startsWith('fuzzy-')) : [];
      if (fuzzy.length) {
        // 四組模糊音在「選字」頁合成一個開關，搜尋也只列一筆；四組的關鍵字合在一起。
        const keywords = [];
        for (const item of fuzzy) for (const word of (Array.isArray(item.keywords) ? item.keywords : [])) if (typeof word === 'string' && word && !keywords.includes(word)) keywords.push(word);
        out.push({ id: 'fuzzy', kind: 'item', page: name, key: 'fuzzy', label: '模糊音', description: FUZZY_DESCRIPTION, options: [], keywords,
          value: fuzzy.some((item) => item.value !== 'off') ? '開啟' : '關閉', disabled: false, reason: '' });
      }
      if (name === 'update' && updateModeSelect() && !packageManaged()) {
        // 更新頁的「更新方式」下拉選單不是 settings 的項目（0c-update mode），照一般項目列出：選項是三種方式，提示是各自的說明。
        const current = updateMode();
        const platform = state.info?.platform;
        out.push({ id: 'update-mode', kind: 'item', page: name, key: 'update-mode', label: '更新方式',
          description: current.note || '目前的更新方式是「' + current.label + '」。',
          options: current.options.map((option) => ({ value: option.value, name: option.label, label: option.label,
            hint: option.value === state.status.mode ? '' : window.ZcUpdateMode(option.value, platform, true).note })),
          keywords: [], value: current.label, disabled: false, reason: '' });
      }
    }
    return out;
  }

  // 結果頁的骨架只建一次：報讀筆數的那一行（aria-live）要是同一個節點，換掉就不會唸。
  function searchDom() {
    if (find.dom) return find.dom;
    const kbd = (text) => h('kbd', {}, text);
    const dom = {
      section: document.getElementById('page-search'),
      title: h('h1', { id: 'search-title' }),
      count: h('p', { class: 'lead', id: 'search-count', role: 'status', 'aria-live': 'polite' }),
      list: h('div', { id: 'search-results', role: 'listbox', 'aria-labelledby': 'search-title' }),
      empty: h('div', { class: 'card search-empty', hidden: true },
        h('div', {}, h('div', { class: 'search-empty-title' }, '換個說法試試'),
          h('div', { class: 'search-chips' }, ...SEARCH_SUGGESTIONS.map((word) => h('button', {
            type: 'button', class: 'search-chip', 'data-suggest': word, onclick: () => setQuery(word),
          }, word)))),
        h('div', { class: 'search-more' }, h('div', { class: 'search-empty-title' }, '還是找不到？'),
          h('p', {}, '靈犀注音可能還沒有這個設定。每一項設定的例子和細節都在官網：', h('button', {
            type: 'button', class: 'link', 'data-focus': 'search-guide', title: SEARCH_GUIDE, 'aria-label': '設定說明（在瀏覽器開啟）',
            onclick: () => openHelp(SEARCH_GUIDE),
          }, '設定說明', h('span', { 'aria-hidden': 'true' }, ' ↗'))))),
      keys: h('div', { class: 'search-keys' }),
      keysFull: [h('span', {}, kbd('↑'), ' ', kbd('↓'), ' 選擇'), h('span', {}, kbd('Enter'), ' 前往'), h('span', {}, kbd('Esc'), ' 清除，回到原本那一頁')],
      keysEmpty: [h('span', {}, kbd('Esc'), ' 清除，回到原本那一頁')],
    };
    dom.section.append(dom.title, dom.count, dom.list, dom.empty, dom.keys);
    find.dom = dom;
    return dom;
  }

  // 命中的字包成 <mark>。
  const marked = (text, words) => window.ZcSearch.pieces(text, words).map((piece) => (piece.hit ? h('mark', {}, piece.text) : piece.text));
  const joined = (parts) => parts.flatMap((part, i) => (i ? ['、', ...part] : part));

  function resultRow(result, index, words) {
    const { entry, match } = result;
    const name = h('span', { class: 'result-name' }, marked(entry.label, words));
    if (entry.reason) name.append(' ', h('span', { class: 'result-reason' }, entry.reason));
    const lines = [];
    if (entry.description) lines.push(h('span', { class: 'result-line' }, marked(entry.description, words)));
    else if (entry.note) lines.push(h('span', { class: 'result-line', 'data-line': 'note' }, entry.note)); // 設定頁的頁首那一句：只顯示，不比對也不標
    if (match.options.length) lines.push(h('span', { class: 'result-line', 'data-line': 'options' }, '選項：', joined(match.options.map((option) => marked(option.name, words)))));
    for (const option of match.hints) lines.push(h('span', { class: 'result-line', 'data-line': 'hint' }, '選項「' + option.name + '」：', marked(option.hint, words)));
    if (match.keywords.length) lines.push(h('span', { class: 'result-line', 'data-line': 'keywords' }, '也可以找：', joined(match.keywords.map((word) => marked(word, words)))));
    return h('div', {
      class: 'result' + (entry.disabled ? ' is-disabled' : ''), role: 'option', id: 'search-result-' + index, 'data-result': entry.id,
      'aria-selected': String(index === find.index), onclick: () => go(index),
    }, h('span', { class: 'result-text' }, name, lines),
    // 值的小標籤；報讀軟體唸成「目前：開啟」，跟前面的說明分得開。
    entry.value ? h('span', { class: 'result-value' }, h('span', { class: 'sr-only' }, '目前：'), entry.value) : null,
    chevron('M1 1l6 6-6 6', 8, 14, 'result-go'));
  }

  function setText(node, text) { if (node.textContent !== text) node.textContent = text; }

  function renderResults(found) {
    const dom = searchDom();
    const count = found.results.length;
    find.results = found.results;
    if (find.fresh) find.index = window.ZcSearch.firstEnabled(found.results);
    else find.index = count ? Math.min(Math.max(find.index, 0), count - 1) : -1;
    find.fresh = false;
    setText(dom.title, '搜尋「' + find.query.trim() + '」');
    setText(dom.count, count ? '找到 ' + count + ' 個設定。' : '沒有符合的設定。');
    // 內容沒變就不重建清單：背景更新（更新頁每秒一次）換掉節點的話，滑鼠按下到放開之間那一列不見了，會點不到。
    const signature = JSON.stringify([found.terms, found.results.map(({ entry, match }) => [entry.id, entry.label, entry.description, entry.value, entry.reason, match])]);
    if (signature !== dom.signature) {
      dom.signature = signature;
      clear(dom.list);
      let index = 0;
      for (const group of found.groups) {
        const id = 'search-group-' + group.page;
        dom.list.append(h('div', { role: 'group', 'aria-labelledby': id },
          h('div', { class: 'search-group', id, role: 'presentation' }, find.titles[group.page] || group.page),
          h('div', { class: 'card search-card', role: 'presentation' }, ...group.results.map((result) => resultRow(result, index++, found.terms)))));
      }
    } else {
      for (const node of dom.list.querySelectorAll('[role="option"]')) node.setAttribute('aria-selected', String(node.id === 'search-result-' + find.index));
    }
    dom.list.hidden = !count;
    dom.empty.hidden = !!count;
    const keys = count ? dom.keysFull : dom.keysEmpty;
    if (dom.keys.firstChild !== keys[0]) clear(dom.keys).append(...keys);
    const input = searchInput();
    if (count) input.setAttribute('aria-activedescendant', 'search-result-' + find.index);
    else input.removeAttribute('aria-activedescendant');
  }

  // 側邊欄：搜尋中各頁旁標出命中的筆數，沒命中的頁面變淡；count 為 null 表示不在搜尋。
  function navBadge(button, count) {
    let badge = button.querySelector('.nav-count');
    if (!count) { if (badge) badge.remove(); } else {
      if (!badge) { badge = h('span', { class: 'nav-count' }, h('span', { 'aria-hidden': 'true' }), h('span', { class: 'sr-only' })); button.append(badge); }
      setText(badge.firstChild, String(count));
      setText(badge.lastChild, '，' + count + ' 個符合');
    }
    button.classList.toggle('nav-dim', count === 0);
  }

  // 頁首上方的「回到「…」的搜尋結果」：從結果跳到某一頁之後才有，換頁或清除搜尋就消失。
  function renderBack() {
    const node = document.getElementById('search-back');
    const shown = !find.open && find.back && !!find.query.trim();
    node.hidden = !shown;
    const text = '回到「' + find.query.trim() + '」的搜尋結果';
    if (shown && node.dataset.text !== text) {
      node.dataset.text = text;
      clear(node).append(chevron('M7 1L1 7l6 6', 7, 12), text);
    }
  }

  function syncSearchBox() {
    const input = searchInput();
    const has = input.value !== '';
    input.parentElement.classList.toggle('has-query', has);
    document.getElementById('search-clear').hidden = !has;
  }

  // 顯示結果頁。記下原本那一頁捲到哪裡，清除搜尋時還原。
  function openSearch() {
    if (find.open || !find.query.trim()) return;
    const main = mainPane();
    find.scroll = main.scrollTop;
    find.open = true;
    find.back = false;
    find.heading = null;
    clearHit();
    render();
    main.scrollTop = 0;
    selectResult(find.index);
  }

  function closeSearch() {
    if (!find.open) return;
    find.open = false;
    render();
    mainPane().scrollTop = find.scroll;
  }

  // 搜尋框的內容變了（打字、貼上、刪除）。輸入法組字中先不搜，組完（compositionend）再搜：不然注音打到一半就會閃出「沒有符合的設定」。
  function onSearchInput() {
    if (find.composing) return;
    find.query = searchInput().value;
    syncSearchBox();
    if (!find.query.trim()) {
      find.back = false;
      if (find.open) closeSearch(); else render();
      return;
    }
    find.index = 0;
    find.fresh = true;
    if (!find.open) { openSearch(); return; }
    render();
    mainPane().scrollTop = 0;
    selectResult(find.index);
  }

  // 直接搜某個詞（找不到時的建議關鍵字）。
  function setQuery(word) {
    const input = searchInput();
    input.value = word;
    find.composing = false;
    input.focus();
    onSearchInput();
  }

  function clearSearch() {
    searchInput().value = '';
    find.composing = false;
    onSearchInput();
  }

  // ↑↓ 只換選取的那一列，不重畫整頁。
  function selectResult(index) {
    if (!find.open || !find.results.length) return;
    find.index = Math.min(Math.max(index, 0), find.results.length - 1);
    const dom = searchDom();
    for (const node of dom.list.querySelectorAll('[role="option"]')) node.setAttribute('aria-selected', String(node.id === 'search-result-' + find.index));
    searchInput().setAttribute('aria-activedescendant', 'search-result-' + find.index);
    const node = document.getElementById('search-result-' + find.index);
    if (!node) return;
    // 第一個結果連同上面的標題一起露出來。
    if (find.index === 0) mainPane().scrollTop = 0; else node.scrollIntoView({ block: 'nearest' });
  }

  // 搜尋框有焦點時的按鍵：↑↓ 移動選取、Enter 前往、Esc 清除（已經是空的就把焦點還給原本的位置）。
  // 輸入法組字中這些鍵是輸入法的（選字、送出、取消），不處理。其他按鍵都不碰。
  function onSearchKey(event) {
    if (event.isComposing || event.keyCode === 229 || find.composing || event.altKey || event.ctrlKey || event.metaKey) return;
    if ((event.key === 'ArrowDown' || event.key === 'ArrowUp') && !event.shiftKey) {
      if (!find.open) return;
      event.preventDefault();
      selectResult(window.ZcSearch.step(find.index, event.key === 'ArrowDown' ? 1 : -1, find.results.length));
    } else if (event.key === 'Enter') {
      if (!find.open || !find.results.length) return;
      event.preventDefault();
      go(find.index);
    } else if (event.key === 'Escape') {
      event.preventDefault();
      if (searchInput().value !== '') clearSearch(); else returnFocus();
    }
  }

  // Ctrl＋F（macOS 是 ⌘F）：聚焦搜尋框並全選。記下原本的焦點，Esc 還回去。
  function focusSearch() {
    const input = searchInput();
    const active = document.activeElement;
    if (active && active !== input && active !== document.body) find.focus = { node: active, key: active.dataset ? active.dataset.focus || null : null };
    input.focus();
    input.select();
  }

  function returnFocus() {
    const before = find.focus;
    find.focus = null;
    const usable = (node) => node && node.isConnected && !node.disabled && node.getClientRects().length > 0;
    let node = before && usable(before.node) ? before.node : null;
    if (!node && before && before.key) node = Array.from(document.querySelectorAll('[data-focus]')).find((other) => other.dataset.focus === before.key && usable(other));
    if (node) node.focus(); else searchInput().blur();
  }

  // 結果對應到頁面上的哪一列、哪個控制項。
  function hitTarget(hit) {
    const section = document.getElementById('page-' + hit.page);
    if (!section || hit.kind !== 'item') return {};
    const key = hit.key;
    const control = section.querySelector('input[name="' + CSS.escape(key) + '"]:checked') || section.querySelector('input[name="' + CSS.escape(key) + '"]') ||
      Array.from(section.querySelectorAll('[data-focus]')).find((node) => node.dataset.focus === key);
    const label = document.getElementById('label-' + key);
    const row = (control && control.closest('.row')) || (label && section.contains(label) ? label.closest('.row') : null);
    return { row, control };
  }

  function clearHit() {
    clearTimeout(find.hitTimer);
    find.hit = null;
    document.querySelectorAll('.search-hit').forEach((node) => node.classList.remove('search-hit', 'search-hit-fade'));
  }

  // 重畫之後把標示加回去（每次重畫那一列都是新的節點）。fading 之後是淡掉的階段。
  function applyHit() {
    const hit = find.hit;
    if (!hit || find.open || hit.page !== page) return;
    const { row } = hitTarget(hit);
    if (!row) return;
    row.classList.add('search-hit');
    row.classList.toggle('search-hit-fade', !!hit.fading);
  }

  // 前往某個結果：切到那一頁、捲到那一項並標出來（約 2.5 秒後淡掉），焦點移到它的控制項，鍵盤可以直接改。
  function go(index) {
    const result = find.results[index];
    if (!result) return;
    const entry = result.entry;
    clearHit();
    find.index = index;
    find.open = false;
    find.back = true;
    find.heading = entry.kind === 'page' ? entry.page : null;
    find.hit = entry.kind === 'item' ? { kind: 'item', page: entry.page, key: entry.key, fading: false } : null;
    page = entry.page;
    if (page === 'overview' && vocabEnabled()) vocab.refreshOverview().then(() => { if (page === 'overview') render(); });
    if (page === 'models') models.refresh().then(() => { if (page === 'models') render(); });
    const main = mainPane();
    main.scrollTop = 0;
    render();
    const section = document.getElementById('page-' + page);
    const { row, control } = find.hit ? hitTarget(find.hit) : {};
    if (row) {
      // 這一項本來就在畫面裡就不捲（頁首與「回到搜尋結果」留在原位）；在下面看不到的才捲到中間。
      const box = row.getBoundingClientRect();
      const view = main.getBoundingClientRect();
      if (box.top < view.top + 56 || box.bottom > view.bottom - 16) row.scrollIntoView({ block: 'center' });
    }
    // 停用的項目沒有能按的控制項：有「到某頁修改」的連結就給它，否則把焦點放在那一列上（報讀軟體會唸出名稱與原因）。
    let target = control && !control.matches(':disabled') ? control : null;
    if (!target && row) target = row.querySelector('button.link.goto');
    if (!target && row) { row.tabIndex = -1; target = row; }
    if (!target) target = section.querySelector('h1[tabindex]');
    if (target) target.focus({ preventScroll: true }); else searchInput().blur();
    if (find.hit) {
      const hit = find.hit;
      find.hitTimer = setTimeout(() => {
        if (find.hit !== hit) return;
        // 減少動態效果：不做漸變，時間到直接移除。
        if (reducedMotion()) { clearHit(); return; }
        hit.fading = true;
        applyHit();
        find.hitTimer = setTimeout(() => { if (find.hit === hit) clearHit(); }, HIT_FADE_MS);
      }, HIT_MS);
    }
  }

  function initSearch() {
    const input = searchInput();
    document.querySelectorAll('.nav').forEach((button) => { find.titles[button.dataset.page] = button.textContent; });
    input.maxLength = 60;
    input.addEventListener('input', onSearchInput);
    input.addEventListener('compositionstart', () => { find.composing = true; });
    input.addEventListener('compositionend', () => { find.composing = false; onSearchInput(); });
    input.addEventListener('keydown', onSearchKey);
    // 回到搜尋框（點它、Tab、Ctrl＋F）而且裡面有字：回到結果頁。焦點從頁面上的控制項來時記下來，Esc 還回去。
    input.addEventListener('focus', (event) => {
      const from = event.relatedTarget;
      if (from && from !== input && from.id !== 'search-back' && !input.parentElement.contains(from)) find.focus = { node: from, key: from.dataset ? from.dataset.focus || null : null };
      openSearch();
    });
    document.getElementById('search-clear').addEventListener('click', () => { clearSearch(); input.focus(); });
    document.getElementById('search-back').addEventListener('click', () => { input.focus(); input.select(); });
    // 只攔 Ctrl＋F（macOS 是 ⌘F；Mac 的 Ctrl＋F 是文字框裡游標往前一格，不能搶）。其他鍵都不在這裡處理，
    // 「我的詞彙」等輸入框的按鍵照舊。
    document.addEventListener('keydown', (event) => {
      if (event.isComposing || event.altKey || event.shiftKey) return;
      // 輸入法開著時 key 可能不是字母（注音符號、Process）：這時看實體鍵位。
      const key = event.key || '';
      const letter = key.length === 1 && /[a-z]/i.test(key) ? key.toLowerCase() : (event.code === 'KeyF' ? 'f' : '');
      if (letter !== 'f' || (isMac() ? !(event.metaKey && !event.ctrlKey) : !(event.ctrlKey && !event.metaKey))) return;
      event.preventDefault();
      focusSearch();
    });
    searchPlatform();
  }

  // 快捷鍵提示照平台：macOS 是 ⌘F，其他是 Ctrl F。平台要等 app_info；之前先看瀏覽器回報的。
  function searchPlatform() {
    setText(document.getElementById('search-key'), isMac() ? '⌘F' : 'Ctrl F');
    searchInput().setAttribute('aria-keyshortcuts', isMac() ? 'Meta+F' : 'Control+F');
  }

  const RENDER = {
    overview, output, styles: (root) => { if (!problem(root)) styles.render(root); }, vocab: vocabPage, models: (root) => models.render(root), update, troubleshoot, about,
    mode: (root) => settingsPage('mode', root), candidates: (root) => settingsPage('candidates', root), keyboard: (root) => settingsPage('keyboard', root),
  };

  // 更新頁每秒重畫（pollUpdate）；「更新方式」下拉選單（#340）有焦點時重畫會換掉正在打開的選單，所以先不畫，
  // 離開選單（blur）時補畫。改方式時（modeBusy）照常畫。
  let renderLater = false;
  const holdingUpdateMode = () => page === 'update' && !state.modeBusy && document.activeElement?.dataset?.focus === 'update-mode';
  function render() {
    // 名稱輸入仍在組字時保留原欄位；背景 refresh 的結果等 compositionend 再畫。
    if (!find.open && page === 'styles' && styles?.composing()) return;
    if (holdingUpdateMode()) { renderLater = true; return; }
    renderLater = false;
    const main = document.querySelector('main');
    // 搜尋中（#363）：結果頁蓋在目前的頁面上，頁面本身先不畫；側邊欄沒有「目前頁面」，改標各頁命中的筆數。
    const found = find.open ? window.ZcSearch.search(searchEntries(), find.query, PAGES) : null;
    const scroll = { top: main.scrollTop, left: main.scrollLeft };
    const focused = document.activeElement && document.activeElement.dataset ? document.activeElement.dataset.focus : null;
    const selection = focused === 'style-name' ? [document.activeElement.selectionStart, document.activeElement.selectionEnd] : null;
    const vocabOn = vocabEnabled();
    if (page === 'vocab' && vocabOn === false) { page = 'overview'; vocab.setActive(false); }
    document.querySelectorAll('.nav').forEach((button) => {
      if (button.dataset.page === 'vocab') button.hidden = !vocabOn;
      if (!found && button.dataset.page === page) button.setAttribute('aria-current', 'page');
      else button.removeAttribute('aria-current');
      navBadge(button, found ? found.counts[button.dataset.page] || 0 : null);
    });
    for (const name of PAGES) {
      const section = document.getElementById('page-' + name);
      section.hidden = !!found || name !== page;
      if (!found && name === page) {
        RENDER[name](name === 'vocab' ? section : clear(section));
        // 從搜尋結果跳到一整頁時，焦點放在頁首標題上（go()）；背景重畫後還原焦點要找得到它。
        const heading = find.heading === name ? section.querySelector('h1') : null;
        if (heading) { heading.tabIndex = -1; heading.dataset.focus = 'heading'; }
      }
    }
    document.getElementById('page-search').hidden = !found;
    main.classList.toggle('searching', !!found);
    if (found) renderResults(found);
    renderBack();
    applyHit();
    if (page === 'vocab' && vocabOn && !vocabActive) { vocabActive = true; vocab.setActive(true); }
    else if (page !== 'vocab' && vocabActive) { vocabActive = false; vocab.setActive(false); }
    // 背景更新（每 15 秒、視窗重新取得焦點）會重建控制項。區段暫時清空時被歸零的捲動位置要還原，
    // 還原焦點也不能把畫面拉回舊控制項（#268）。
    // 先還原捲動再還原焦點：設定 scrollTop 會先排版，新控制項這時才有 renderer。WebKit（WebKitGTK、
    // WKWebView）的 <input> 若在還沒排版前就取得焦點，排版時會另排一個工作把它捲進畫面，
    // 不理 preventScroll；點過的選項（radio）、風格名稱欄位都是 <input>。
    main.scrollTop = scroll.top;
    main.scrollLeft = scroll.left;
    if (focused) {
      const again = Array.from(document.querySelectorAll('[data-focus]')).find((node) => node.dataset.focus === focused);
      if (again && !again.disabled) {
        again.focus({ preventScroll: true });
        if (selection && typeof again.setSelectionRange === 'function') again.setSelectionRange(...selection);
      }
    }
  }

  let vocabActive = false;
  function show(name) {
    const next = pageName(name);
    // 換頁（側邊欄、頁面上的連結、外部的 --page）就離開搜尋結果；搜尋框裡的字留著，回到搜尋框就回到結果頁（#363）。
    if (find.open) { find.open = false; document.querySelector('main').scrollTop = 0; }
    find.back = false;
    find.heading = null;
    clearHit();
    if (next !== page) document.querySelector('main').scrollTop = 0;
    page = next;
    if (page === 'overview' && vocabEnabled()) vocab.refreshOverview().then(() => { if (page === 'overview') render(); });
    if (page === 'models') models.refresh().then(() => { if (page === 'models') render(); });
    render();
  }

  async function init() {
    // 「清除學習紀錄」（#301）在「我的詞彙」的「靈犀學到的」清單底下，由 vocab.js 決定何時顯示：
    // 0.2.12 起的 0c-update 才有（settings -json 的 features.personal_reset）。「我的詞彙」不顯示時（features.vocab 為假的舊版）就不出現。
    const personalReset = window.ZcPersonalReset({ h, clear, invoke, toast, onDone: () => vocab.afterReset() });
    vocab = window.ZcVocab({
      h, clear, invoke, toast,
      platform: () => (state.info && state.info.platform) || '',
      mcpPath: () => state.info && state.info.mcp_path,
      mcpPresent: () => !!(state.info && state.info.mcp_present),
      // 引擎是否在執行（status 的 runtime）；還不知道時 null。
      engineRunning: () => (state.status && state.status.runtime ? state.status.runtime.state === 'running' : null),
      personalReset,
      personalResetEnabled: () => !!(state.settings && state.settings.features && state.settings.features.personal_reset),
      helpLink: () => pageHelpLink('vocab', '我的詞彙'),
    });
    styles = window.ZcInputStyles({
      h, render, settings: () => state.settings, showKeyboard: () => show('keyboard'), helpLink: () => pageHelpLink('styles', '輸入習慣'),
      valueLabel: (item, value) => window.ZcInputStylesLogic.valueLabel(item, value, VALUE),
      mutate: async (action, id, name) => {
        state.profileBusy = true;
        try {
          const result = await invoke('profile_command', { action, id, name });
          if (result.ok) toast(...(result.apply ? applyText(result.apply) : ['已儲存。', 'ok']));
          else toast(errorText(result.error), 'error');
          await refresh();
          return result;
        } finally { state.profileBusy = false; }
      },
    });
    models = window.ZcModels({
      h, invoke, toast, copyText, modelLabel, render,
      visible: () => page === 'models' && !document.hidden,
    });
    document.querySelectorAll('.nav').forEach((button) => button.addEventListener('click', () => show(button.dataset.page)));
    initSearch();
    // 頁首有「說明」連結的頁面（#362），F1 開那一頁的說明；焦點在某一項上時那一項自己處理了（preventDefault），這裡不再開一次。
    document.addEventListener('keydown', (event) => {
      if (event.key !== 'F1' || event.defaultPrevented || find.open) return;
      const help = window.ZcItemHelp.pageUrl(page);
      if (help) { event.preventDefault(); openHelp(help); }
    });
    try { state.info = await invoke('app_info'); } catch (_) { /* 關於頁顯示未知 */ }
    searchPlatform();
    // 要開的頁面由網址參數帶進來（?page=update）；舊的 input 導到中英切換，不認得的開總覽。
    page = pageName(new URLSearchParams(window.location.search).get('page'));
    if (tauri.event) {
      await tauri.event.listen('navigate', (event) => show(event.payload));
      // 詞彙版本改變（AI 助手、組字中加詞、另一個視窗）：Rust 端的 0c-update vocab watch 轉來的事件。
      await tauri.event.listen('vocab-changed', (event) => vocab.onChanged(event.payload));
    }
    render();
    refresh();
    setInterval(() => models.poll(), 1000);
    setInterval(() => { if (!document.hidden) refresh(); }, 15000);
    setInterval(pollUpdate, 1000);
    window.addEventListener('focus', () => { refresh(); vocab.focus(); });
    // 視窗隱藏時結束 watch，再出現時重新抓並恢復。
    document.addEventListener('visibilitychange', () => vocab.visibility());
  }

  // 任何沒被處理的錯誤都顯示在畫面上，而不是留下一片空白。
  window.addEventListener('error', (event) => toast('畫面發生錯誤：' + event.message, 'error'));
  window.addEventListener('unhandledrejection', (event) => toast('發生錯誤：' + ((event.reason && event.reason.message) || event.reason), 'error'));

  init();
})();
