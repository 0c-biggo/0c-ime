// 「模型」頁（#180）：選字與補標點模型的來源、版本與雜湊；沒有內建模型的 Linux 系統安裝可以從 Hugging Face 下載。
// 下載、核對、配對與刪除都由 0c-update models（Go）執行，這裡只呼叫它並每秒讀一次進度，做法同「更新」頁。
// 契約：0c-update models status|download|cancel|remove -json（docs/settings-contract.md 的「模型」一節）。
//
// 上半是不碰畫面的判斷（ZcModelsLogic，node 測試直接載入：test/models_test.js），下半是畫面。
(function () {
  'use strict';

  const ROLE_FALLBACK = { select: '統計引擎', punct: '原文提交' };
  const DISABLED = {
    platform: '這個平台沒有可下載的模型，目前使用統計選字。',
    distribution: '這個發行版的套件關閉了模型下載，目前使用統計選字。',
    // 安裝的字典不在 pairing.lock 上（例如發行版自己重建字典）：下載的模型配不上，不能用。
    dictionary_mismatch: '目前安裝的字典沒有對應的模型配對清單，不能使用下載的模型。',
  };
  const HF = /^https:\/\/huggingface\.co\/[A-Za-z0-9._\/-]+$/;

  // 十進位 MB：跟 Hugging Face 頁面上的大小一致（約 262 MB）。
  const mb = (bytes) => (Math.max(0, bytes || 0) / 1e6).toLocaleString('zh-TW', { maximumFractionDigits: 1 }) + ' MB';
  const aboutMB = (bytes) => '約 ' + Math.round(Math.max(0, bytes || 0) / 1e6).toLocaleString('zh-TW') + ' MB';
  const percent = (op) => (op && op.total_bytes > 0 ? Math.min(100, Math.max(0, Math.floor(op.done_bytes * 100 / op.total_bytes))) : null);
  const shortRev = (rev) => (rev || '').slice(0, 8);

  function operationText(op) {
    switch (op && op.phase) {
      case 'downloading': return '正在下載' + (percent(op) == null ? '…' : ' ' + percent(op) + '%');
      case 'verifying': return '核對中…';
      case 'pairing': return '配對中…';
      default: return '處理中…';
    }
  }

  // 頁首的狀態一句話；bundled 也走這裡（沒有按鈕）。
  function summary(status) {
    if (!status) return { text: '讀取中…', tone: 'muted' };
    if (status.operation) return { text: operationText(status.operation), tone: 'warn' };
    if (status.source === 'bundled') return { text: '隨安裝包內附。', tone: 'ok' };
    if (status.disabled_reason === 'dictionary_mismatch') return { text: DISABLED.dictionary_mismatch, tone: 'warn' };
    if (status.source === 'user') {
      if (status.need === 'redownload') return { text: '模型有新版，舊的已不能用，需要重新下載。', tone: 'warn' };
      if (status.need === 'repair') return { text: '字典已更新，正在重新配對，不必重新下載。', tone: 'warn' };
      return { text: '已安裝。', tone: 'ok' };
    }
    if (!status.can_download) return { text: DISABLED[status.disabled_reason] || '沒有模型，目前使用統計選字。', tone: 'muted' };
    if (status.need === 'redownload') return { text: '模型有新版，需要重新下載。', tone: 'warn' };
    return { text: '未安裝，目前使用統計選字。', tone: 'muted' };
  }

  // 可以按的按鈕。下載只在可下載、沒有進行中的作業、還沒有可用模型（need 是 download 或 redownload）時；
  // repair 由 0c-update 自動重新配對，不給按鈕。刪除只刪個人目錄的模型。
  function actions(status) {
    const s = status || {};
    const op = !!s.operation;
    return {
      download: !!s.can_download && !op && s.source !== 'bundled' && (s.need === 'download' || s.need === 'redownload'),
      redownload: s.need === 'redownload',
      cancel: op,
      remove: s.source === 'user' && !op,
    };
  }

  // 推論元件（系統的 python3、numpy、onnxruntime）缺少時要裝的 apt 套件；不缺或沒有可下載的情境回 null。
  function runtimeMissing(status) {
    const s = status || {};
    if (s.source === 'bundled' || !s.runtime || s.runtime.ok !== false) return null;
    if (!s.can_download && s.source !== 'user') return null;
    return (s.runtime.missing || []).filter((p) => /^[a-z0-9][a-z0-9.+-]*$/.test(p));
  }

  // 下載前要寫明的大小、來源（repo＋revision）與授權。
  function downloadNote(status) {
    const models = (status && status.models) || [];
    const sources = models.filter((m) => m.repository).map((m) => m.repository + (m.revision ? '（' + shortRev(m.revision) + '）' : ''));
    const licenses = [...new Set(models.map((m) => m.license).filter(Boolean))];
    return aboutMB(status && status.download_size) +
      (sources.length ? '，從 Hugging Face 下載：' + sources.join('、') : '') +
      (licenses.length ? '，授權 ' + licenses.join('、') : '') + '。';
  }

  function shortSha(sha) {
    return sha && sha.length > 20 ? sha.slice(0, 12) + '…' + sha.slice(-8) : (sha || '');
  }

  const safeUrl = (url) => typeof url === 'string' && HF.test(url) && !url.includes('..');

  // 引擎回報的狀態（同總覽的 modelLabel）；引擎沒在跑時是空字串。
  function stateText(model, modelLabel) {
    if (!model.state) return '輸入法未執行';
    return modelLabel(model.state, ROLE_FALLBACK[model.role] || '統計引擎');
  }

  const logic = { summary, actions, runtimeMissing, downloadNote, shortSha, safeUrl, operationText, percent, mb, aboutMB, stateText };
  window.ZcModelsLogic = logic;

  // ---- 畫面 ----
  // ctx：h、invoke、toast、copyText、modelLabel、visible()（這一頁是否正在顯示）、render()（重畫整個畫面）。
  window.ZcModels = function (ctx) {
    const { h, invoke, toast, copyText, modelLabel } = ctx;
    const m = { status: null, error: null, busy: null, removeAsk: false, polling: false };

    async function load() {
      try {
        const result = await invoke('models_command', { action: 'status' });
        if (result && result.ok === false) { m.error = (result.error && result.error.message) || '無法讀取模型狀態。'; }
        else {
          const before = m.status;
          m.status = result;
          m.error = null;
          if (before && before.operation && !result.operation) finished(result);
        }
      } catch (error) {
        m.error = String(error);
      }
    }

    // 作業結束（下載完成、失敗或取消）：成功時提示一次；失敗的原因留在頁面上的 last_error。
    function finished(status) {
      if (status.source === 'user' && status.need === 'none') toast('模型已安裝。', 'ok');
    }

    async function act(action) {
      if (m.busy) return;
      m.busy = action;
      m.removeAsk = false;
      ctx.render();
      try {
        const result = await invoke('models_command', { action });
        if (result && result.ok === false) toast((result.error && result.error.message) || '沒有完成，請再試一次。', 'error');
        else if (result && result.schema) {
          const before = m.status;
          m.status = result;
          if (action === 'remove') toast('已刪除模型，改用統計選字。', 'ok');
          else if (action === 'cancel') toast('已取消下載。', 'ok');
          else if (before && before.operation && !result.operation) finished(result);
        }
      } catch (error) {
        toast(String(error), 'error');
      } finally {
        m.busy = null;
        await load();
        ctx.render();
      }
    }

    // 每秒呼叫一次：只在這一頁顯示中、而且有進行中的作業時讀進度。
    async function poll() {
      if (m.polling || !ctx.visible() || !(m.status && m.status.operation)) return;
      m.polling = true;
      try { await load(); ctx.render(); } finally { m.polling = false; }
    }

    async function refresh() { await load(); }

    // 確認刪除出現時焦點先放在「取消」（連按兩下不會誤刪），取消後回到「刪除」。
    function focus(key) {
      const node = document.querySelector('[data-focus="' + key + '"]');
      if (node) node.focus();
    }

    function openLink(url) {
      invoke('open_url', { url }).catch((error) => toast('無法開啟瀏覽器：' + error + '。網址：' + url, 'error'));
    }

    function shaRow(sha) {
      return h('span', { class: 'sha' },
        h('code', { class: 'mono', title: sha }, shortSha(sha)),
        h('button', { type: 'button', class: 'link', 'data-focus': 'sha:' + sha.slice(0, 12), 'aria-label': '複製 SHA-256', onclick: () => copyText(sha) }, '複製'));
    }

    function modelCard(model) {
      const ready = model.state === 'ready';
      const facts = [];
      if (model.version) facts.push(['版本', model.version]);
      if (model.path) facts.push(['路徑', h('span', { class: 'mono path' }, model.path)]);
      else if (model.size) facts.push(['大小', aboutMB(model.size)]);
      if (model.sha256) facts.push(['SHA-256', shaRow(model.sha256)]);
      if (model.url && safeUrl(model.url)) {
        facts.push(['來源', h('button', {
          type: 'button', class: 'link ext', title: model.url, 'data-focus': 'hf:' + model.role, onclick: () => openLink(model.url),
        }, 'Hugging Face ' + (model.repository || '') + (model.revision ? ' @ ' + shortRev(model.revision) : ''), h('span', { 'aria-hidden': 'true' }, ' ↗'))]);
      }
      if (model.license) facts.push(['授權', model.license]);
      return h('div', { class: 'card model-card' },
        h('div', { class: 'row' },
          h('div', { class: 'label' }, model.label || model.role),
          h('div', { class: 'value' }, h('span', { class: ready ? 'status-ok' : 'muted' }, stateText(model, modelLabel)))),
        facts.length ? h('dl', { class: 'facts' }, ...facts.flatMap(([k, v]) => [h('dt', {}, k), h('dd', {}, v)])) : null);
    }

    function render(root) {
      root.append(h('h1', {}, '模型'), h('p', { class: 'lead' }, '神經選字與補標點使用的模型。'));
      if (!m.status) {
        if (m.error) {
          root.append(h('div', { class: 'notice error' }, h('p', {}, '無法讀取模型狀態：' + m.error)),
            h('button', { type: 'button', class: 'secondary', onclick: async () => { await load(); ctx.render(); } }, '重試'));
        } else root.append(h('p', { class: 'muted' }, '讀取中…'));
        return;
      }
      const s = m.status;
      const op = s.operation;
      const sum = summary(s);
      const can = actions(s);
      // 有進行中的作業時，下面的進度列就是狀態，不再重複一句。
      if (!op) root.append(h('div', { class: 'notice' + (sum.tone === 'warn' ? '' : ' info'), role: 'status' }, h('p', {}, sum.text)));
      const missing = runtimeMissing(s);
      if (missing) {
        const cmd = 'sudo apt install ' + (missing.length ? missing.join(' ') : 'python3 python3-numpy python3-onnxruntime');
        root.append(h('div', { class: 'notice' },
          h('p', {}, '缺少推論元件，模型裝好也無法使用。請先安裝：'),
          h('div', { class: 'snippet' }, h('pre', { class: 'cmd' }, cmd),
            h('button', { type: 'button', class: 'secondary small', 'data-focus': 'apt-copy', onclick: () => copyText(cmd) }, '複製'))));
      }
      if (s.last_error && s.last_error.message && !op) {
        const canceled = s.last_error.code === 'canceled';
        root.append(h('div', { class: 'notice' + (canceled ? ' info' : ' error') }, h('p', {}, s.last_error.message)));
      }
      if (op) {
        const downloading = op.phase === 'downloading';
        root.append(h('div', { class: 'download-progress', role: 'status' },
          h('div', { class: 'label' }, operationText(op)),
          h('progress', { max: 100, value: downloading ? percent(op) : null, 'aria-label': operationText(op) }),
          downloading && op.total_bytes ? h('div', { class: 'muted' }, mb(op.done_bytes) + ' / ' + mb(op.total_bytes)) : null));
      }
      if (can.download) root.append(h('p', { class: 'muted small download-note' }, downloadNote(s)));
      const buttons = [];
      if (can.download) {
        buttons.push(h('button', { type: 'button', class: 'primary', disabled: !!m.busy, 'data-focus': 'models-download', onclick: () => act('download') },
          m.busy === 'download' ? '開始下載…' : can.redownload ? '重新下載' : '下載模型'));
      }
      if (can.cancel) {
        buttons.push(h('button', { type: 'button', class: 'secondary', disabled: !!m.busy, 'data-focus': 'models-cancel', onclick: () => act('cancel') },
          m.busy === 'cancel' ? '取消中…' : '取消'));
      }
      if (can.remove && !m.removeAsk) {
        buttons.push(h('button', { type: 'button', class: 'secondary', disabled: !!m.busy, 'data-focus': 'models-remove', onclick: () => { m.removeAsk = true; ctx.render(); focus('models-remove-cancel'); } },
          m.busy === 'remove' ? '刪除中…' : '刪除'));
      }
      if (buttons.length) root.append(h('div', { class: 'actions model-actions' }, ...buttons));
      if (can.remove && m.removeAsk) {
        root.append(h('div', { class: 'notice' },
          h('p', {}, '刪除後改用統計選字；要再用得重新下載（' + aboutMB(s.download_size) + '）。'),
          h('div', { class: 'actions' },
            h('button', { type: 'button', class: 'primary', 'data-focus': 'models-remove-confirm', onclick: () => act('remove') }, '確認刪除'),
            h('button', { type: 'button', class: 'secondary', 'data-focus': 'models-remove-cancel', onclick: () => { m.removeAsk = false; ctx.render(); focus('models-remove'); } }, '取消'))));
      }
      for (const model of s.models || []) root.append(modelCard(model));
    }

    return { render, refresh, poll };
  };
})();
