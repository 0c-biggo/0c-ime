// 「我的詞彙」頁（#125，入口依 features.vocab：0.2.0 起恆為 true，舊版 0c-update 沒有這個欄位）。資料全部經 Rust 呼叫 0c-update vocab／api，
// WebView 不自己連線；文字一律用 textContent 組（app.js 的 h()），不使用 innerHTML。
//
// 這一頁的 DOM 只建一次：新增列、搜尋框這些輸入欄位在整個頁面存在期間不重建（打字中、選字中重建會打斷輸入法），
// 資料變動時只重畫各自的區塊。
window.ZcVocab = function (ctx) {
  'use strict';
  const { h, clear, invoke, toast } = ctx;

  const PAGE_SIZE = 1000; // 超過 1,000 筆才分頁；搜尋交給引擎
  const RANK_CHUNK = 100;
  const UNDO_MS = 5000;
  const SOURCE = { manual: '手動', compose: '打字時加入', assistant: 'AI 助手', learned: '學到的' };
  // compose 是打字當下的操作：組字中加詞，或候選窗用反悔鍵刪除；「組字中加入　刪除」讀起來矛盾（Fcitx5 驗收）。
  const WHO = { manual: '你', compose: '打字時', assistant: 'AI 助手', learned: '學到的' };
  const KIND = { phrase: '常一起用', reading: '你選過的寫法' };
  const HAN = /^\p{Script=Han}+$/u;
  // 英文詞（#146）：終端機指令、工具名。沒有讀音，中英混打時打出完全相同的字母（大小寫照字面）就照英文送出。
  const ENGLISH = /^(?=.*[A-Za-z])[A-Za-z0-9._-]{1,32}$/;
  const ASCII = /^[\x21-\x7e]+$/;
  const isEnglish = (text) => ENGLISH.test(text);
  const TOOLS = [
    ['chat', '直接跟 AI 說'], ['claude-code', 'Claude Code'], ['cursor', 'Cursor'], ['vscode', 'VS Code'],
    ['stdio', '只支援 stdio 的工具'], ['script', '自己寫腳本'],
  ];

  const v = {
    tab: 'words', version: null, count: null, limit: 10000, notes: [], loaded: false, disabled: false, engineDown: null,
    words: [], total: 0, offset: 0, query: '', listError: null, ranks: new Map(),
    learned: null, learnedError: null, phrases: null, phrasesError: null,
    learnedView: 'learned', learnedSel: new Set(), learnedBusy: false, forgotten: null, forgottenSel: new Set(), forgottenCount: 0,
    batches: null, showAllBatches: false, openBatch: null, batchDetail: new Map(),
    draft: null, paste: null, editing: null, staleList: false, flash: new Set(),
    undo: null, ai: { open: false, view: null, busy: false, tool: 'chat', shown: false, regenAsk: false, test: null, testing: false, port: '' },
    active: false, watching: false, overview: null, overviewAt: 0, seq: 0,
  };
  const el = {};
  let draftTimer = null;
  let searchTimer = null;
  let undoTimer = null;
  let flashTimer = null;

  // ---- 共用 ----
  async function req(body) {
    const r = await invoke('vocab_request', { request: body });
    if (r && r.error && r.error.code === 'disabled') v.disabled = true;
    // 引擎沒執行時只有 status／list／batches／phrases 能用（直接讀檔）；其他指令成功就表示引擎在。
    const wasDown = v.engineDown;
    if (r && r.error && r.error.code === 'engine') v.engineDown = true;
    else if (r && r.ok && !['status', 'list', 'batches', 'phrases'].includes(body.op)) v.engineDown = false;
    if (wasDown !== v.engineDown) drawNotices();
    if (r && (!r.error || !['engine', 'disabled', 'unknown_op'].includes(r.error.code)) && typeof r.version === 'number') {
      v.count = r.count;
      if (r.limit) v.limit = r.limit;
      v.notes = r.notes || [];
    }
    return r || {};
  }
  const errText = (r) => (r && r.error && r.error.message) || '發生未知的錯誤。';
  const num = (n) => Number(n || 0).toLocaleString('zh-TW');
  const readingsText = (rs) => (rs || []).join('／');
  function when(secs) {
    if (!secs) return '';
    const d = new Date(secs * 1000);
    const now = new Date();
    const pad = (n) => String(n).padStart(2, '0');
    if (now - d < 90 * 1000 && now - d > -60 * 1000) return '剛剛';
    if (d.toDateString() === now.toDateString()) return pad(d.getHours()) + ':' + pad(d.getMinutes());
    if (d.getFullYear() === now.getFullYear()) return (d.getMonth() + 1) + '/' + d.getDate();
    return d.getFullYear() + '/' + (d.getMonth() + 1) + '/' + d.getDate();
  }
  const quoteList = (items, count) => items.map((x) => '「' + x + '」').join('、') + (count > items.length ? ' 等 ' + num(count) + ' 個' : '');

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

  // ---- 讀音選擇：每個字一格；破音字是黃色下拉選單，查不到的字自己填 ----
  function picker(chars, chosen, onchange) {
    return h('div', { class: 'picker' }, chars.map((c, i) => {
      const options = c.options || [];
      let control;
      if (options.length > 1 || (options.length === 1 && chosen[i] && chosen[i] !== options[0])) {
        const values = options.includes(chosen[i]) || !chosen[i] ? options : [chosen[i], ...options];
        control = h('select', {
          class: c.ambiguous ? 'poly' : '', 'aria-label': '「' + c.char + '」的讀音' + (c.ambiguous ? '（破音字，請確認）' : ''),
          onchange: (event) => { chosen[i] = event.target.value; if (onchange) onchange(); },
        }, values.map((o) => h('option', { value: o, selected: o === chosen[i] }, o)));
      } else if (options.length === 1) {
        chosen[i] = chosen[i] || options[0];
        control = h('span', { class: 'syl' }, chosen[i]);
      } else {
        control = h('input', {
          class: 'syl-input', value: chosen[i] || '', placeholder: '注音', 'aria-label': '「' + c.char + '」的注音（查不到，請自己填）',
          oninput: (event) => { chosen[i] = event.target.value.trim(); if (onchange) onchange(); },
        });
      }
      return h('span', { class: 'pchar' + (c.ambiguous ? ' poly' : '') + (options.length ? '' : ' unknown') }, h('span', { class: 'pc' }, c.char), control);
    }));
  }
  // 重畫一個區塊時保留鍵盤焦點：可聚焦的控制項帶 data-k，重畫後找回同一個；找不到（例如「保留」換成了文字）時
  // 交給目前的分頁標籤，不讓焦點掉回頁首。
  // 控制項在處理中暫時停用（例如按下「測試連線」）時記住它，處理完重畫時把焦點還給它。
  let pendingFocus = null;
  function keepFocus(container, draw) {
    const active = document.activeElement;
    const had = !!(container && active && container.contains(active));
    let key = had ? active.dataset.k : null;
    if (!had && pendingFocus && pendingFocus.container === container && (!active || active === document.body)) key = pendingFocus.key;
    pendingFocus = null;
    draw();
    if (container.contains(document.activeElement) && document.activeElement !== active) return; // 重畫時自己放好了焦點
    if (!had && !key) return;
    const again = key ? container.querySelector('[data-k="' + CSS.escape(key) + '"]') : null;
    if (again && !again.disabled) again.focus();
    else if (again) pendingFocus = { container, key };
    else if (el.tabs && el.tabs[v.tab]) el.tabs[v.tab].focus();
  }

  const charsOf = (text, ann) => (ann && ann.chars && ann.chars.length === [...text].length ? ann.chars : [...text].map((c) => ({ char: c, options: [] })));
  const complete = (chosen) => chosen.length > 0 && chosen.every((x) => x && x.trim());

  function wordProblem(text) {
    if (isEnglish(text)) return null;
    if (ASCII.test(text)) {
      if (text.length > 32) return '英文詞最多 32 個字元；比較長的內容請放到「我的片語」。';
      if (!/[A-Za-z]/.test(text)) return '英文詞至少要有一個英文字母。';
      return '英文詞只能有英文字母、數字與 - _ .；其他符號請放到「我的片語」。';
    }
    if (!HAN.test(text)) return '只能加漢字，或英文字母、數字與 - _ . 組成的英文詞（例如 ls）；符號、假名或句子請放到「我的片語」。';
    if ([...text].length > 32) return '一個詞最多 32 個字；比較長的句子請放到「我的片語」。';
    return null;
  }
  const englishWord = (w) => !(w.readings || []).length;

  // ---- 建立頁面 ----
  function mount(section) {
    if (el.root && el.root.parentNode === section) return;
    clear(section);
    el.count = h('span', { class: 'vcount muted' });
    el.tabs = {};
    const tabBar = h('div', { class: 'tabs', role: 'tablist', 'aria-label': '詞彙分類' },
      [['words', '我加的詞'], ['learned', '靈犀學到的'], ['phrases', '我的片語']].map(([key, label]) => {
        el.tabs[key] = h('button', {
          type: 'button', role: 'tab', class: 'tab', id: 'vtab-' + key, 'aria-controls': 'vpanel-' + key,
          onclick: () => setTab(key), onkeydown: tabKeys,
        }, label);
        return el.tabs[key];
      }), el.count);
    el.notices = h('div', {});

    // 我加的詞
    el.search = h('input', { type: 'search', class: 'vsearch', placeholder: '搜尋詞或注音…', 'aria-label': '搜尋我加的詞', oninput: onSearch });
    el.add = h('input', {
      type: 'text', class: 'vadd', placeholder: '輸入要加的詞，例如人名、術語或英文指令', 'aria-label': '新增詞彙', autocomplete: 'off',
      oninput: onDraftInput, onkeydown: onDraftKey, onpaste: onPaste,
    });
    el.add.addEventListener('compositionend', onDraftInput);
    el.addButton = h('button', { type: 'button', class: 'primary', onclick: () => addDraft() }, '加入');
    el.draft = h('div', { class: 'vdraft', 'aria-live': 'polite' });
    el.paste = h('div', {});
    el.list = h('div', { class: 'vlist' });
    el.pager = h('div', {});
    el.panels = {
      words: h('div', { role: 'tabpanel', id: 'vpanel-words', 'aria-labelledby': 'vtab-words' },
        h('div', { class: 'card vtop' },
          el.search,
          h('div', { class: 'vaddrow' }, h('span', { class: 'plus', 'aria-hidden': 'true' }, '＋'), el.add, el.addButton),
          el.draft),
        el.paste, el.list, el.pager),
      learned: h('div', { role: 'tabpanel', id: 'vpanel-learned', 'aria-labelledby': 'vtab-learned' }),
      phrases: h('div', { role: 'tabpanel', id: 'vpanel-phrases', 'aria-labelledby': 'vtab-phrases' }),
    };
    el.learned = el.panels.learned;
    el.phraseInput = h('textarea', {
      class: 'vphrase', rows: '2', placeholder: '輸入一則片語，例如地址、簽名或常用句子', 'aria-label': '新增片語',
      onkeydown: (event) => { if (event.key === 'Enter' && (event.ctrlKey || event.metaKey) && !event.isComposing) { event.preventDefault(); addPhrase(); } },
    });
    el.phraseList = h('div', {});
    el.panels.phrases.append(
      h('p', { class: 'muted vlead' }, '在注音模式按 = 開選單，就能從「我的片語」選取。'),
      h('div', { class: 'card vtop' }, h('div', { class: 'vaddrow' }, el.phraseInput, h('button', { type: 'button', class: 'primary', onclick: addPhrase }, '加入'))),
      el.phraseList);
    el.batches = h('div', {});
    el.ai = h('div', { class: 'vai' });
    el.undo = h('div', { class: 'undo-bar', role: 'status', 'aria-live': 'polite', hidden: true });
    el.undo.addEventListener('mouseenter', () => clearTimeout(undoTimer));
    el.undo.addEventListener('mouseleave', () => armUndo());

    el.root = h('div', { class: 'vocab' },
      h('h1', {}, '我的詞彙'),
      h('p', { class: 'lead' }, '加進來的人名、術語打得出來，也看得到靈犀自己學到了什麼。'),
      tabBar, el.notices, el.panels.words, el.panels.learned, el.panels.phrases,
      h('h2', {}, '最近變更'), el.batches, el.ai, el.undo);
    section.append(el.root);
    drawTabs();
    drawDraft();
    drawAI();
    reload();
  }

  function tabKeys(event) {
    const keys = ['words', 'learned', 'phrases'];
    const i = keys.indexOf(v.tab);
    if (event.key === 'ArrowRight' || event.key === 'ArrowLeft') {
      event.preventDefault();
      setTab(keys[(i + (event.key === 'ArrowRight' ? 1 : keys.length - 1)) % keys.length]);
      el.tabs[v.tab].focus();
    }
  }

  function setTab(key) {
    v.tab = key;
    drawTabs();
    if (key === 'learned') loadLearned();
    if (key === 'phrases') loadPhrases();
  }

  function drawTabs() {
    for (const [key, button] of Object.entries(el.tabs)) {
      const on = key === v.tab;
      button.setAttribute('aria-selected', String(on));
      button.tabIndex = on ? 0 : -1;
      el.panels[key].hidden = !on;
    }
    drawCount();
  }

  function drawCount() {
    if (!el.count) return;
    const n = v.count;
    if (n == null) { el.count.textContent = ''; return; }
    if (v.tab === 'phrases' && v.phrases) { el.count.textContent = num(v.phrases.length) + '／200 則'; el.count.className = 'vcount muted'; return; }
    const near = n >= v.limit * 0.9;
    el.count.textContent = near ? '已加 ' + num(n) + '／' + num(v.limit) + ' 個' : '已加 ' + num(n) + ' 個';
    el.count.className = 'vcount ' + (near ? 'status-warn' : 'muted');
  }

  function drawNotices() {
    if (!el.notices) return;
    clear(el.notices);
    if (v.disabled) {
      el.notices.append(h('div', { class: 'notice' }, h('p', {}, '「我的詞彙」目前沒有開放。')));
      return;
    }
    // 引擎沒在執行時 0c-update 在寫入者鎖內直接改 vocab.json，照樣能新增與修改（#120 的退路）；
    // 只有修改被拒絕（例如另一個引擎正拿著個人資料）時才暫時唯讀，不讓人按了才看到錯誤。
    const readOnly = v.engineDown === true;
    el.root.classList.toggle('readonly', readOnly);
    el.add.disabled = el.addButton.disabled = el.phraseInput.disabled = readOnly;
    if (readOnly) {
      el.notices.append(h('div', { class: 'notice info' }, h('p', {}, '暫時不能修改自訂詞彙（輸入法引擎正在切換），稍後會自動恢復。')));
    }
    if (v.notes.length) {
      el.notices.append(h('div', { class: 'notice' }, h('p', {}, '詞彙檔有部分內容沒有使用：'), ...v.notes.map((note) => h('p', {}, note))));
    }
    if (v.count != null && v.count >= v.limit) {
      el.notices.append(h('div', { class: 'notice' }, h('p', {}, '已經加到上限 ' + num(v.limit) + ' 個，刪掉一些才能再加。')));
    }
  }

  // ---- 資料 ----
  async function reload() {
    const seq = ++v.seq;
    let list, batches;
    try {
      [list, batches] = await Promise.all([
        req({ op: 'list', query: v.query, offset: v.offset, limit: PAGE_SIZE }),
        req({ op: 'batches' }),
      ]);
    } catch (error) {
      if (seq !== v.seq) return;
      v.listError = String(error);
      drawWords();
      return;
    }
    if (seq !== v.seq) return;
    if (list.ok) {
      const before = new Map(v.words.map((w) => [w.word, readingsText(w.readings)]));
      const first = !v.loaded;
      v.words = list.words || [];
      v.total = list.total || 0;
      v.version = list.version;
      v.listError = null;
      if (!first) {
        for (const w of v.words) if (before.get(w.word) !== readingsText(w.readings)) v.flash.add(w.word);
      }
      v.loaded = true;
      if (v.offset > 0 && v.offset >= v.total) { v.offset = Math.max(0, v.total - (v.total % PAGE_SIZE || PAGE_SIZE)); return reload(); }
    } else {
      v.listError = errText(list);
    }
    v.batches = batches.ok ? (batches.batches || []) : v.batches;
    drawNotices();
    drawCount();
    drawWords();
    drawBatches();
    if (v.tab === 'learned') loadLearned();
    if (v.tab === 'phrases') loadPhrases();
    loadRanks(seq);
    syncWatch();
    if (v.flash.size) {
      clearTimeout(flashTimer);
      flashTimer = setTimeout(() => { v.flash.clear(); document.querySelectorAll('.vocab .flash').forEach((n) => n.classList.remove('flash')); }, 2500);
    }
  }

  async function loadRanks(seq) {
    const texts = v.words.filter((w) => !englishWord(w)).map((w) => w.word); // 英文詞沒有讀音，不排名
    for (let i = 0; i < texts.length; i += RANK_CHUNK) {
      let r;
      try { r = await req({ op: 'rank', texts: texts.slice(i, i + RANK_CHUNK), limit: 500 }); } catch (_) { return; }
      if (seq !== v.seq || !r.ok) return;
      const got = new Map();
      for (const rank of r.ranks || []) {
        if (!got.has(rank.word)) got.set(rank.word, []);
        got.get(rank.word).push(rank);
      }
      for (const [word, ranks] of got) {
        v.ranks.set(word, ranks);
        const badge = el.list.querySelector('[data-rank="' + CSS.escape(word) + '"]');
        if (badge) fillRank(badge, word);
      }
    }
  }

  function rankLabel(r) {
    if (r.first) return ['首選', 'ok'];
    if (!r.page) return ['不在候選', 'bad'];
    if (r.page === 1) return ['第 ' + r.index + ' 個', 'mid'];
    return ['第 ' + r.page + ' 頁', 'mid'];
  }

  function fillRank(badge, word) {
    const w = v.words.find((x) => x.word === word);
    const ranks = v.ranks.get(word) || [];
    const r = (w && ranks.find((x) => x.reading === w.readings[0])) || ranks[0];
    if (!r) { badge.textContent = ''; badge.className = 'rank'; badge.removeAttribute('title'); return; }
    const [text, tone] = rankLabel(r);
    badge.textContent = text;
    badge.className = 'rank ' + tone;
    const lines = ranks.map((x) => {
      if (x.first) return '只打「' + x.reading + '」就是首選';
      if (!x.page) return '只打「' + x.reading + '」時不在候選裡';
      return '只打「' + x.reading + '」時排第 ' + x.index + ' 個' + (x.ahead ? '，前面是「' + x.ahead + '」' : '');
    });
    badge.title = lines.join('\n');
    badge.setAttribute('aria-label', text + '：' + lines.join('；'));
  }

  // ---- 我加的詞 ----
  function drawWords() { keepFocus(el.list, drawWordsNow); }
  function drawWordsNow() {
    if (!el.list) return;
    if (v.editing) { v.staleList = true; return; } // 改讀音時不重畫，免得編輯框消失；改完再畫
    v.staleList = false;
    clear(el.list);
    clear(el.pager);
    if (v.listError) {
      el.list.append(h('div', { class: 'notice error' }, h('p', {}, '無法讀取詞彙：' + v.listError),
        h('div', { class: 'actions' }, h('button', { type: 'button', class: 'secondary', onclick: reload }, '重試'))));
      return;
    }
    if (!v.loaded) { el.list.append(h('p', { class: 'muted' }, '讀取中…')); return; }
    if (!v.words.length) {
      el.list.append(h('div', { class: 'card vempty' }, h('p', { class: 'muted' }, v.query
        ? '找不到符合「' + v.query + '」的詞。'
        : '還沒有加任何詞。把常打錯的人名、公司名或術語打在上面的框裡，按 Enter 就加入了。')));
      return;
    }
    if (v.query) el.list.append(h('p', { class: 'muted vfound' }, '找到 ' + num(v.total) + ' 個'));
    el.list.append(h('div', { class: 'card vrows' }, v.words.map(wordRow)));
    if (v.focusWord) { // 改完讀音：焦點回到那一列的讀音
      const again = el.list.querySelector('[data-word="' + CSS.escape(v.focusWord) + '"] .vreading');
      v.focusWord = null;
      if (again) again.focus();
    }
    if (v.total > PAGE_SIZE) {
      const from = v.offset + 1;
      const to = Math.min(v.total, v.offset + PAGE_SIZE);
      el.pager.append(h('div', { class: 'pager' },
        h('button', { type: 'button', class: 'secondary', disabled: v.offset === 0, onclick: () => { v.offset = Math.max(0, v.offset - PAGE_SIZE); reload(); } }, '上一頁'),
        h('span', { class: 'muted' }, '第 ' + num(from) + '–' + num(to) + ' 個，共 ' + num(v.total) + ' 個'),
        h('button', { type: 'button', class: 'secondary', disabled: to >= v.total, onclick: () => { v.offset += PAGE_SIZE; reload(); } }, '下一頁')));
    }
  }

  function wordRow(w) {
    const badge = h('span', { class: 'rank', 'data-rank': w.word });
    fillRank(badge, w.word);
    const meta = [SOURCE[w.source] || SOURCE.manual, when(w.added)].filter(Boolean).join(' · ');
    const english = englishWord(w);
    return h('div', { class: 'vrow' + (english ? ' english' : '') + (v.flash.has(w.word) ? ' flash' : ''), 'data-word': w.word },
      h('div', { class: 'vword' }, w.word),
      english ? h('span', { class: 'vreading plain', title: '中英混打時打「' + w.word + '」就照英文送出' }, h('span', { class: 'vtag' }, '英文'))
        : h('button', { type: 'button', class: 'vreading', 'data-k': 'read:' + w.word, title: '修改讀音', 'aria-label': '「' + w.word + '」的讀音 ' + readingsText(w.readings) + '，按一下修改', onclick: () => startEdit(w) }, readingsText(w.readings)),
      badge,
      h('span', { class: 'vmeta muted' }, meta),
      h('button', { type: 'button', class: 'link', 'data-k': 'del:' + w.word, 'aria-label': '刪除「' + w.word + '」', onclick: () => removeWord(w.word) }, '刪除'));
  }

  async function removeWord(word) {
    const r = await req({ op: 'remove', texts: [word] });
    if (!r.ok) { toast(errText(r), 'error'); return; }
    v.version = r.version;
    const row = el.list.querySelector('[data-word="' + CSS.escape(word) + '"]');
    if (row) row.remove(); // 先拿掉那一列，不等重新整理
    showUndo('已刪除「' + word + '」', r.batch && r.batch.id);
    reload();
  }

  // 改讀音：每個讀音一列選擇器（破音字下拉），可再加一個讀音（最多 4 個）。送出時附上看到的原讀音（expect），
  // 別人（AI 助手或另一個視窗）剛改過時引擎回 conflict，不悄悄覆蓋。
  async function startEdit(w) {
    if (el.root.classList.contains('readonly') || englishWord(w)) return;
    if (v.editing) cancelEdit();
    const row = el.list.querySelector('[data-word="' + CSS.escape(w.word) + '"]');
    if (!row) return;
    v.editing = { word: w.word, expect: w.readings.slice(), lines: w.readings.map((r) => r.split(' ')), ann: null, row };
    drawEditor();
    const r = await req({ op: 'annotate', texts: [w.word] }).catch(() => null);
    if (v.editing && v.editing.word === w.word && r && r.ok) { v.editing.ann = (r.annotations || [])[0]; drawEditor(); }
  }

  function drawEditor() {
    const e = v.editing;
    if (!e) return;
    const chars = charsOf(e.word, e.ann);
    const old = e.row.querySelector('.veditor');
    const editor = h('div', { class: 'veditor', onkeydown: (event) => {
      if (event.key === 'Escape') { event.preventDefault(); cancelEdit(); }
      if (event.key === 'Enter' && event.target.tagName !== 'BUTTON') { event.preventDefault(); saveEdit(); }
    } },
    e.lines.map((line, i) => h('div', { class: 'vline' }, picker(chars, line),
      e.lines.length > 1 ? h('button', { type: 'button', class: 'link', 'aria-label': '移除這個讀音', onclick: () => { e.lines.splice(i, 1); drawEditor(); } }, '移除') : null)),
    h('div', { class: 'actions' },
      h('button', { type: 'button', class: 'primary', onclick: saveEdit }, '儲存'),
      h('button', { type: 'button', class: 'secondary', onclick: cancelEdit }, '取消'),
      e.lines.length < 4 ? h('button', { type: 'button', class: 'link', onclick: () => { e.lines.push(chars.map((c) => (c.options || [])[0] || '')); drawEditor(); } }, '再加一個讀音') : null));
    if (old) old.replaceWith(editor); else e.row.append(editor);
    e.row.classList.add('editing');
    const first = editor.querySelector('select, input');
    if (first && !editor.contains(document.activeElement)) first.focus();
  }

  function cancelEdit() {
    if (!v.editing) return;
    const row = v.editing.row;
    v.editing = null;
    const editor = row.querySelector('.veditor');
    if (editor) editor.remove();
    row.classList.remove('editing');
    if (v.staleList) drawWords();
    const again = el.list.querySelector('[data-word="' + CSS.escape(row.dataset.word) + '"] .vreading');
    if (again) again.focus();
  }

  async function saveEdit() {
    const e = v.editing;
    if (!e) return;
    if (!e.lines.every(complete)) { toast('請先填好每個字的讀音。', 'warn'); return; }
    const readings = [...new Set(e.lines.map((line) => line.join(' ')))];
    const r = await req({ op: 'readings', text: e.word, readings, expect: e.expect });
    if (!r.ok) {
      toast(errText(r), r.error && r.error.code === 'conflict' ? 'warn' : 'error');
      if (r.error && r.error.code === 'conflict') { v.editing = null; v.focusWord = e.word; reload(); }
      return;
    }
    v.version = r.version;
    v.flash.add(e.word);
    v.editing = null;
    v.focusWord = e.word;
    toast('已修改「' + e.word + '」的讀音。', 'ok');
    reload();
  }

  // 新增列：打字當下標注音（只在組字結束後查，不打斷輸入法），Enter 加入。
  function onDraftInput(event) {
    if (event && event.isComposing) return;
    clearTimeout(draftTimer);
    draftTimer = setTimeout(annotateDraft, 250);
  }

  async function annotateDraft() {
    const text = el.add.value.trim();
    if (!text) { v.draft = null; drawDraft(); return v.draft; }
    if (v.draft && v.draft.text === text && (v.draft.ann || v.draft.problem)) return v.draft;
    const problem = wordProblem(text);
    if (problem) { v.draft = { text, problem }; drawDraft(); return v.draft; }
    if (isEnglish(text)) { v.draft = { text, english: true, chosen: [] }; drawDraft(); return v.draft; }
    v.draft = { text, pending: true, chosen: [] };
    drawDraft();
    let r;
    try { r = await req({ op: 'annotate', texts: [text] }); } catch (error) { r = { error: { message: String(error) } }; }
    if (el.add.value.trim() !== text) return null; // 已經又打了別的字
    if (!r.ok) { v.draft = { text, problem: errText(r) }; drawNotices(); drawDraft(); return v.draft; }
    const ann = (r.annotations || [])[0] || { chars: [] };
    v.draft = { text, ann, chosen: charsOf(text, ann).map((c) => c.reading || '') };
    drawDraft();
    return v.draft;
  }

  function drawDraft() {
    if (!el.draft) return;
    clear(el.draft);
    const d = v.draft;
    if (!d) return;
    if (d.problem) { el.draft.append(h('p', { class: 'status-warn small' }, d.problem)); return; }
    if (d.pending) { el.draft.append(h('p', { class: 'muted small' }, '查讀音中…')); return; }
    if (d.english) {
      el.draft.append(h('p', { class: 'muted small' }, '英文詞不需要讀音：中英混打時打「' + d.text + '」就照英文送出，不會變成注音（大小寫要相同）。按 Enter 加入。'));
      return;
    }
    const chars = charsOf(d.text, d.ann);
    el.draft.append(picker(chars, d.chosen));
    const hints = [];
    if (chars.some((c) => c.ambiguous)) hints.push('黃色的字有其他讀法，請確認是不是這個音。');
    if (chars.some((c) => !(c.options || []).length)) hints.push('有字查不到讀音，請自己填注音。');
    if (d.ann && d.ann.exists) hints.push('詞庫裡已經有這個詞；加入後會優先選它。');
    hints.push('按 Enter 加入。');
    el.draft.append(h('p', { class: 'muted small' }, hints.join('')));
  }

  function onDraftKey(event) {
    if (event.key !== 'Enter' || event.isComposing || event.keyCode === 229) return;
    event.preventDefault();
    addDraft();
  }

  async function addDraft() {
    clearTimeout(draftTimer);
    const d = await annotateDraft();
    if (!d) return;
    if (d.problem) { toast(d.problem, 'warn'); return; }
    if (!d.english && !complete(d.chosen)) { toast('請先填好每個字的讀音。', 'warn'); return; }
    const r = await req({ op: 'add', words: [d.english ? { word: d.text } : { word: d.text, readings: [d.chosen.join(' ')] }] });
    if (!r.ok) { toast(errText(r), 'error'); drawNotices(); return; }
    v.version = r.version;
    el.add.value = '';
    v.draft = null;
    drawDraft();
    v.flash.add(d.text);
    toast('已加入「' + d.text + '」。', 'ok');
    el.add.focus();
    reload();
  }

  // 一次貼上多行：變成預覽清單，勾選後一起加入（一個批次，可整批復原）。
  async function onPaste(event) {
    const text = (event.clipboardData && event.clipboardData.getData('text')) || '';
    const lines = [...new Set(text.split(/[\r\n\t]+/).map((x) => x.trim()).filter(Boolean))];
    if (lines.length < 2) return;
    event.preventDefault();
    if (lines.length > 500) toast('一次最多加 500 個，只取前 500 行。', 'warn');
    const rows = lines.slice(0, 500).map((t) => ({ text: t, problem: wordProblem(t), english: isEnglish(t), chosen: [], checked: isEnglish(t) }));
    v.paste = { rows, pending: true };
    drawPaste();
    const valid = rows.filter((x) => !x.problem && !x.english);
    let r = { ok: true, annotations: [] };
    if (valid.length) {
      try { r = await req({ op: 'annotate', texts: valid.map((x) => x.text) }); } catch (error) { r = { error: { message: String(error) } }; }
    }
    if (!v.paste || v.paste.rows !== rows) return;
    if (!r.ok) { v.paste = null; drawPaste(); toast(errText(r), 'error'); drawNotices(); return; }
    (r.annotations || []).forEach((ann, i) => {
      const row = valid[i];
      row.ann = ann;
      row.chosen = charsOf(row.text, ann).map((c) => c.reading || '');
      row.checked = complete(row.chosen);
    });
    v.paste.pending = false;
    drawPaste();
  }

  function drawPaste() {
    clear(el.paste);
    const p = v.paste;
    if (!p) return;
    const picked = p.rows.filter((x) => x.checked);
    const count = h('span', {});
    const addButton = h('button', { type: 'button', class: 'primary', disabled: p.pending, onclick: addPaste });
    const update = () => {
      const n = p.rows.filter((x) => x.checked).length;
      addButton.textContent = '加入勾選的 ' + n + ' 個';
      addButton.disabled = p.pending || n === 0;
      count.textContent = '貼上了 ' + p.rows.length + ' 行，勾選要加入的詞';
    };
    el.paste.append(h('div', { class: 'card vpaste' },
      h('div', { class: 'vpaste-head' }, h('strong', {}, count), p.pending ? h('span', { class: 'muted' }, '查讀音中…') : null),
      p.rows.map((row) => h('div', { class: 'vprow' + (row.problem ? ' bad' : '') },
        h('input', { type: 'checkbox', checked: row.checked, disabled: !!row.problem || p.pending, 'aria-label': '加入「' + row.text + '」',
          onchange: (event) => { row.checked = event.target.checked; update(); } }),
        h('span', { class: 'vword' }, row.text),
        row.problem ? h('span', { class: 'status-warn small' }, row.problem)
          : row.english ? h('span', { class: 'vtag' }, '英文')
          : row.ann ? picker(charsOf(row.text, row.ann), row.chosen, () => { if (!complete(row.chosen)) { row.checked = false; drawPaste(); } }) : null)),
      h('div', { class: 'actions' }, addButton,
        h('button', { type: 'button', class: 'secondary', onclick: () => { v.paste = null; drawPaste(); } }, '取消'))));
    update();
    void picked;
  }

  async function addPaste() {
    const rows = v.paste.rows.filter((x) => x.checked);
    if (rows.some((x) => !x.english && !complete(x.chosen))) { toast('請先填好每個字的讀音。', 'warn'); return; }
    const r = await req({ op: 'add', words: rows.map((x) => (x.english ? { word: x.text } : { word: x.text, readings: [x.chosen.join(' ')] })) });
    if (!r.ok) {
      const bad = r.error && r.error.item >= 0 ? rows[r.error.item] : null;
      if (bad) { bad.problem = errText(r); bad.checked = false; drawPaste(); }
      toast(errText(r), 'error');
      return;
    }
    v.version = r.version;
    rows.forEach((x) => v.flash.add(x.text));
    v.paste = null;
    drawPaste();
    showUndo('已加入 ' + rows.length + ' 個詞', r.batch && r.batch.id);
    reload();
  }

  function onSearch(event) {
    if (event.isComposing) return;
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      const q = el.search.value.trim();
      if (q === v.query) return;
      v.query = q;
      v.offset = 0;
      reload();
    }, 300);
  }

  // ---- 靈犀學到的（#182：保留／移除／不再學、勾選多個一起處理、復原，以及「不再學的詞」） ----
  // 學到的詞存在學習資料（user.json），不是 vocab.json 的批次：移除、不再學、恢復學習的復原編號由引擎發（learned-undo），
  // 只留在引擎記憶體裡，所以只在底部提示給「復原」，不進「最近變更」。
  const undoKey = () => (/^macos\//.test(ctx.platform ? ctx.platform() : '') ? 'Shift＋⌫' : 'Shift＋Delete');
  async function loadLearned() {
    const forgotten = v.learnedView === 'forgotten';
    let r, f;
    try {
      [r, f] = await Promise.all([req({ op: 'learned' }), forgotten ? req({ op: 'forgotten' }) : null]);
    } catch (error) { r = { error: { message: String(error) } }; }
    if (r.ok) {
      v.learned = r.learned || [];
      v.learnedError = null;
      v.forgottenCount = r.forgotten_count || 0;
      const words = new Set(v.learned.filter((l) => !l.vocab).map((l) => l.word));
      for (const w of [...v.learnedSel]) if (!words.has(w)) v.learnedSel.delete(w);
    } else { v.learnedError = r.error && r.error.code === 'engine' ? 'engine' : errText(r); }
    if (f && f.ok) {
      v.forgotten = f.forgotten || [];
      v.forgottenCount = f.forgotten_count || 0;
      const words = new Set(v.forgotten);
      for (const w of [...v.forgottenSel]) if (!words.has(w)) v.forgottenSel.delete(w);
    }
    if (v.learnedView === 'forgotten' && r.ok && !v.forgottenCount) v.learnedView = 'learned'; // 全部恢復了：回到學到的詞
    drawLearned();
    drawNotices();
  }

  function setLearnedView(view) {
    v.learnedView = view;
    if (view === 'forgotten') v.forgotten = null;
    drawLearnedNow();
    const target = el.learned.querySelector(view === 'forgotten' ? '[data-k="back-learned"]' : '[data-k="show-forgotten"]');
    if (target) target.focus();
    loadLearned();
  }

  // 勾選列：全選（部分勾選時顯示半勾）、已選幾個、批次按鈕。
  function bulkBar(items, sel, actions, label) {
    const all = h('input', { type: 'checkbox', 'data-k': 'all:' + label, 'aria-label': '全選' + label });
    all.checked = items.length > 0 && items.every((w) => sel.has(w));
    all.indeterminate = !all.checked && items.some((w) => sel.has(w));
    all.disabled = !items.length;
    all.addEventListener('change', () => {
      if (all.checked) items.forEach((w) => sel.add(w)); else items.forEach((w) => sel.delete(w));
      drawLearned();
    });
    const n = items.filter((w) => sel.has(w)).length;
    return h('div', { class: 'vbulk' },
      h('label', { class: 'vcheck' }, all, h('span', {}, '全選')),
      h('span', { class: 'muted small vselcount' }, n ? '已選 ' + num(n) + ' 個' : '勾選要一起處理的詞'),
      h('span', { class: 'vactions' }, actions.map(([op, text]) => h('button', {
        type: 'button', class: 'secondary small', 'data-k': 'bulk:' + op, disabled: !n,
        onclick: () => learnedAction(op, items.filter((w) => sel.has(w))),
      }, text))));
  }

  function rowCheck(word, sel, disabled) {
    const box = h('input', { type: 'checkbox', class: 'vsel', 'data-k': 'sel:' + word, 'aria-label': '選取「' + word + '」' });
    box.checked = sel.has(word);
    box.disabled = !!disabled;
    box.addEventListener('change', () => { if (box.checked) sel.add(word); else sel.delete(word); drawLearned(); });
    return box;
  }

  function drawLearned() { keepFocus(el.learned, drawLearnedNow); }
  function drawLearnedNow() {
    clear(el.learned);
    if (v.learnedView === 'forgotten') { drawForgottenNow(); return; }
    // 沒有任何不再學的詞時不顯示入口（#182 負責人決定）。
    const link = v.forgottenCount > 0 && !v.learnedError
      ? h('button', { type: 'button', class: 'link', 'data-k': 'show-forgotten', onclick: () => setLearnedView('forgotten') }, '不再學的詞（' + num(v.forgottenCount) + '）›')
      : null;
    el.learned.append(h('div', { class: 'vhead' },
      h('p', { class: 'muted vlead' }, '靈犀從你的選字自動學到的詞。可以勾選多個一起處理，做錯了按底部提示的「復原」。'), link));
    el.learned.append(h('dl', { class: 'vhelp muted' },
      h('dt', {}, '保留'), h('dd', {}, '變成「我加的詞」，一直留著。'),
      h('dt', {}, '移除'), h('dd', {}, '清掉這個詞的學習紀錄。之後照常打字、選字，還會再學回來。'),
      h('dt', {}, '不再學'), h('dd', {}, '清掉紀錄，之後也不再自動學它。「你選過的寫法」在候選窗再親手選一次就會恢復；「常一起用」的詞要到「不再學的詞」恢復。')));
    if (v.learnedError) {
      el.learned.append(h('div', { class: 'card vempty' }, h('p', { class: 'muted' },
        v.learnedError === 'engine' ? '輸入法引擎執行時才看得到學到的詞。' : '無法讀取：' + v.learnedError)));
      return;
    }
    if (!v.learned) { el.learned.append(h('p', { class: 'muted' }, '讀取中…')); return; }
    if (!v.learned.length) {
      el.learned.append(h('div', { class: 'card vempty' }, h('p', { class: 'muted' }, '還沒有學到的詞。常一起選的字、你親手選過的寫法會出現在這裡。')));
      return;
    }
    const sel = v.learnedSel;
    const items = v.learned.filter((l) => !l.vocab).map((l) => l.word);
    el.learned.append(h('div', { class: 'card vrows' },
      bulkBar(items, sel, [['keep', '保留'], ['unlearn', '移除'], ['forget', '不再學']], '學到的詞'),
      v.learned.map((l) => h('div', { class: 'vrow learned' + (v.flash.has(l.word) ? ' flash' : ''), 'data-word': l.word },
        rowCheck(l.word, sel, l.vocab),
        h('div', { class: 'vword' }, l.word),
        h('span', { class: 'vreading plain' }, l.reading),
        h('span', { class: 'vmeta muted' }, KIND[l.kind] || ''),
        l.vocab ? h('span', { class: 'muted small' }, '已在我加的詞')
          : h('span', { class: 'vactions' },
            h('button', { type: 'button', class: 'secondary small', 'data-k': 'keep:' + l.word, 'aria-label': '保留「' + l.word + '」', onclick: () => learnedAction('keep', [l.word]) }, '保留'),
            h('button', { type: 'button', class: 'link', 'data-k': 'unlearn:' + l.word, 'aria-label': '移除「' + l.word + '」', onclick: () => learnedAction('unlearn', [l.word]) }, '移除'),
            h('button', { type: 'button', class: 'link', 'data-k': 'forget:' + l.word, 'aria-label': '不再學「' + l.word + '」', onclick: () => learnedAction('forget', [l.word]) }, '不再學'))))));
  }

  function drawForgottenNow() {
    el.learned.append(h('div', { class: 'vhead' },
      h('button', { type: 'button', class: 'link vback', 'data-k': 'back-learned', onclick: () => setLearnedView('learned') }, '‹ 靈犀學到的'),
      h('strong', { class: 'vsubtitle' }, '不再學的詞')));
    el.learned.append(h('p', { class: 'muted vlead' }, '你按過「不再學」，或在候選窗用 ' + undoKey() + ' 忘記的詞，打字時不會自動學它們。按「恢復學習」之後，照常打字、選字就會再學回來；已經清掉的紀錄不會回來。'));
    if (v.learnedError) {
      el.learned.append(h('div', { class: 'card vempty' }, h('p', { class: 'muted' },
        v.learnedError === 'engine' ? '輸入法引擎執行時才看得到不再學的詞。' : '無法讀取：' + v.learnedError)));
      return;
    }
    if (!v.forgotten) { el.learned.append(h('p', { class: 'muted' }, '讀取中…')); return; }
    if (!v.forgotten.length) {
      el.learned.append(h('div', { class: 'card vempty' }, h('p', { class: 'muted' }, '沒有不再學的詞。')));
      return;
    }
    const sel = v.forgottenSel;
    el.learned.append(h('div', { class: 'card vrows' },
      bulkBar(v.forgotten, sel, [['relearn', '恢復學習']], '不再學的詞'),
      v.forgotten.map((word) => h('div', { class: 'vrow forgotten', 'data-word': word },
        rowCheck(word, sel, false),
        h('div', { class: 'vword' }, word),
        h('button', { type: 'button', class: 'secondary small', 'data-k': 'relearn:' + word, 'aria-label': '恢復學習「' + word + '」', onclick: () => learnedAction('relearn', [word]) }, '恢復學習')))));
  }

  const LEARNED_DONE = {
    keep: (q) => '已把' + q + '加到我加的詞',
    unlearn: (q) => '已移除' + q + '，之後還會再學',
    forget: (q) => '已設成不再學：' + q,
    relearn: (q) => '已恢復學習' + q,
  };

  async function learnedAction(op, words) {
    if (!words.length || v.learnedBusy) return;
    const hadFocus = el.learned.contains(document.activeElement);
    v.learnedBusy = true;
    let r;
    try { r = await req({ op, texts: words }); } catch (error) { r = { error: { message: String(error) } }; } finally { v.learnedBusy = false; }
    if (!r.ok) { toast(errText(r), 'error'); return; }
    const sel = op === 'relearn' ? v.forgottenSel : v.learnedSel;
    words.forEach((w) => sel.delete(w));
    const q = words.length === 1 ? '「' + words[0] + '」' : ' ' + num(words.length) + ' 個詞';
    if (op === 'keep') {
      v.version = r.version;
      words.forEach((w) => v.flash.add(w));
      showUndo(LEARNED_DONE.keep(q), r.batch && r.batch.id);
      reload();
    } else {
      v.forgottenCount = r.forgotten_count || 0;
      // 先拿掉那幾列，不等重新整理。
      if (op === 'relearn' && v.forgotten) v.forgotten = v.forgotten.filter((w) => !words.includes(w));
      else if (v.learned) v.learned = v.learned.filter((l) => !words.includes(l.word));
      if (v.learnedView === 'forgotten' && !v.forgottenCount) v.learnedView = 'learned';
      drawLearned();
      if (r.undo) showUndo(LEARNED_DONE[op](q), () => undoLearned(r.undo));
      else toast(op === 'relearn' ? '這些詞已經不在「不再學的詞」裡。' : '沒有需要清掉的學習紀錄。', 'ok');
      loadLearned();
    }
    // 按下的按鈕隨著那一列消失（或批次按鈕變成停用）時，焦點交給「復原」，用鍵盤的人可以馬上按 Enter 反悔。
    const active = document.activeElement;
    const lost = !active || active === document.body || !active.isConnected || active.disabled || (el.tabs && active === el.tabs[v.tab]);
    const undo = !el.undo.hidden && el.undo.querySelector('button');
    if (hadFocus && lost && undo) undo.focus();
  }

  async function undoLearned(id) {
    hideUndo();
    let r;
    try { r = await req({ op: 'learned-undo', undo: id }); } catch (error) { r = { error: { message: String(error) } }; }
    if (!r.ok) { toast(errText(r), 'warn'); return; }
    toast('已復原。', 'ok');
    loadLearned();
  }

  // ---- 我的片語 ----
  async function loadPhrases() {
    let r;
    try { r = await req({ op: 'phrases' }); } catch (error) { r = { error: { message: String(error) } }; }
    if (r.ok) { v.phrases = r.phrases || []; v.phrasesError = null; } else v.phrasesError = errText(r);
    drawPhrases();
    drawCount();
  }

  function drawPhrases() { keepFocus(el.phraseList, drawPhrasesNow); }
  function drawPhrasesNow() {
    clear(el.phraseList);
    if (v.phrasesError) { el.phraseList.append(h('div', { class: 'notice error' }, h('p', {}, '無法讀取片語：' + v.phrasesError))); return; }
    if (!v.phrases) { el.phraseList.append(h('p', { class: 'muted' }, '讀取中…')); return; }
    if (!v.phrases.length) {
      el.phraseList.append(h('div', { class: 'card vempty' }, h('p', { class: 'muted' }, '還沒有片語。常打的地址、簽名、英文或符號都可以放這裡。')));
      return;
    }
    const n = v.phrases.length;
    el.phraseList.append(h('div', { class: 'card vrows phrases' }, v.phrases.map((p, i) => h('div', { class: 'vrow phrase' + (v.flash.has('#' + p.id) ? ' flash' : ''), 'data-id': String(p.id) },
      h('span', { class: 'handle', title: '拖曳排序', 'aria-hidden': 'true', onpointerdown: (event) => dragStart(event, p, i) }, '⠿'),
      h('div', { class: 'ptext', title: p.text }, p.text),
      h('span', { class: 'vactions' },
        h('button', { type: 'button', class: 'icon', 'data-k': 'up:' + p.id, disabled: i === 0, 'aria-label': '上移「' + clipText(p.text) + '」', onclick: () => movePhrase(p.id, i - 1) }, '↑'),
        h('button', { type: 'button', class: 'icon', 'data-k': 'down:' + p.id, disabled: i === n - 1, 'aria-label': '下移「' + clipText(p.text) + '」', onclick: () => movePhrase(p.id, i + 1) }, '↓'),
        h('button', { type: 'button', class: 'link', 'data-k': 'del:' + p.id, 'aria-label': '刪除「' + clipText(p.text) + '」', onclick: () => removePhrase(p) }, '刪除'))))));
  }
  const clipText = (t) => ([...t].length > 12 ? [...t].slice(0, 12).join('') + '…' : t);

  async function addPhrase() {
    const text = el.phraseInput.value.trim();
    if (!text) return;
    const r = await req({ op: 'phrase-add', texts: [text] });
    if (!r.ok) { toast(errText(r), 'error'); drawNotices(); return; }
    v.version = r.version;
    el.phraseInput.value = '';
    v.phrases = r.phrases || v.phrases;
    const added = r.batch && r.batch.changes && r.batch.changes[0] && r.batch.changes[0].phrase_after;
    if (added) { v.flash.add('#' + added.id); setTimeout(() => v.flash.delete('#' + added.id), 2500); }
    drawPhrases();
    drawCount();
    toast('已加入片語。', 'ok');
    reload();
  }

  async function removePhrase(p) {
    const r = await req({ op: 'phrase-remove', ids: [p.id] });
    if (!r.ok) { toast(errText(r), 'error'); return; }
    v.version = r.version;
    v.phrases = r.phrases || [];
    drawPhrases();
    showUndo('已刪除片語「' + clipText(p.text) + '」', r.batch && r.batch.id);
    reload();
  }

  async function movePhrase(id, to) {
    const r = await req({ op: 'phrase-move', id, to });
    if (!r.ok) { toast(errText(r), 'error'); return; }
    v.version = r.version;
    v.phrases = r.phrases || v.phrases;
    drawPhrases();
    const button = el.phraseList.querySelector('[data-id="' + id + '"] .icon:not(:disabled)');
    if (button) button.focus();
    reload();
  }

  // 拖曳排序：用指標事件，不依賴 HTML5 拖放（WebView 的拖放處理在各平台不一致）。
  function dragStart(event, p, from) {
    event.preventDefault();
    const rows = [...el.phraseList.querySelectorAll('.vrow.phrase')];
    const row = rows[from];
    let to = from;
    row.classList.add('dragging');
    const move = (e) => {
      to = rows.findIndex((r) => { const box = r.getBoundingClientRect(); return e.clientY < box.top + box.height / 2; });
      if (to < 0) to = rows.length;
      if (to > from) to -= 1;
      rows.forEach((r, i) => { r.classList.toggle('drop-before', i === to && to < from); r.classList.toggle('drop-after', i === to && to > from); });
    };
    const up = () => {
      document.removeEventListener('pointermove', move);
      document.removeEventListener('pointerup', up);
      rows.forEach((r) => r.classList.remove('dragging', 'drop-before', 'drop-after'));
      if (to !== from) movePhrase(p.id, to);
    };
    document.addEventListener('pointermove', move);
    document.addEventListener('pointerup', up);
  }

  // ---- 最近變更 ----
  function batchSummary(b) {
    const items = b.preview || [];
    const one = b.count === 1 && items.length === 1;
    const list = items.join('、') + (b.count > items.length ? '…' : '');
    switch (b.op) {
      case 'add': return b.source === 'learned' ? '保留學到的' + quoteList(items, b.count) : one ? '加入「' + items[0] + '」' : '加入 ' + num(b.count) + ' 個詞：' + list;
      case 'remove': return one ? '刪除「' + items[0] + '」' : '刪除 ' + num(b.count) + ' 個詞：' + list;
      case 'readings': return '修改' + quoteList(items, b.count) + '的讀音';
      case 'undo': return '復原了一次修改' + (list ? '（' + list + '）' : '');
      case 'phrase-add': return one ? '加入片語「' + items[0] + '」' : '加入 ' + num(b.count) + ' 則片語';
      case 'phrase-remove': return one ? '刪除片語「' + items[0] + '」' : '刪除 ' + num(b.count) + ' 則片語';
      case 'phrase-move': return '調整片語順序';
      default: return b.op;
    }
  }

  function drawBatches() { keepFocus(el.batches, drawBatchesNow); }
  function drawBatchesNow() {
    clear(el.batches);
    const all = v.batches || [];
    if (!all.length) { el.batches.append(h('p', { class: 'muted' }, v.batches ? '還沒有任何變更。' : '讀取中…')); return; }
    const shown = v.showAllBatches ? all : all.slice(0, 5);
    el.batches.append(h('div', { class: 'card vrows batches' }, shown.map((b) => {
      const open = v.openBatch === b.id;
      const detail = v.batchDetail.get(b.id);
      return h('div', { class: 'vbatch' + (open ? ' open' : '') },
        h('div', { class: 'vrow' },
          h('span', { class: 'btime muted' }, when(b.time)),
          h('span', { class: 'bwho' }, WHO[b.source] || b.source),
          h('button', { type: 'button', class: 'bsum', 'data-k': 'bsum:' + b.id, 'aria-expanded': String(open), onclick: () => toggleBatch(b.id) }, batchSummary(b)),
          b.undone ? h('span', { class: 'muted small' }, '已復原')
            : b.op === 'phrase-move' ? h('span', {})
              : h('button', { type: 'button', class: 'secondary small', 'data-k': 'undo:' + b.id, 'aria-label': '復原：' + batchSummary(b), onclick: () => undoBatch(b.id) }, b.op === 'undo' ? '取消復原' : '復原')),
        open ? h('div', { class: 'bdetail' }, detail ? changeLines(detail) : h('p', { class: 'muted small' }, '讀取中…')) : null);
    })));
    if (all.length > 5) {
      el.batches.append(h('button', { type: 'button', class: 'link', 'data-k': 'batches-all', onclick: () => { v.showAllBatches = !v.showAllBatches; drawBatches(); } },
        v.showAllBatches ? '只顯示最近 5 筆' : '顯示全部 ' + all.length + ' 筆'));
    }
  }

  function changeLines(batch) {
    const lines = (batch.changes || []).map((c) => {
      if (c.after && c.before) return readingsText(c.before.readings) === readingsText(c.after.readings)
        ? '「' + c.after.word + '」' : '「' + c.after.word + '」' + readingsText(c.before.readings) + ' → ' + readingsText(c.after.readings);
      if (c.after) return '＋ ' + c.after.word + '　' + (readingsText(c.after.readings) || '英文');
      if (c.before) return '－ ' + c.before.word + '　' + (readingsText(c.before.readings) || '英文');
      if (c.phrase_after && c.phrase_before) return '片語「' + clipText(c.phrase_after.text) + '」';
      if (c.phrase_after) return '＋ 片語「' + clipText(c.phrase_after.text) + '」';
      if (c.phrase_before) return '－ 片語「' + clipText(c.phrase_before.text) + '」';
      return '';
    }).filter(Boolean);
    if (!lines.length) return h('p', { class: 'muted small' }, batch.op === 'phrase-move' ? '調整了片語的順序。' : '沒有明細。');
    return h('ul', { class: 'changes' }, lines.map((line) => h('li', {}, line)));
  }

  async function toggleBatch(id) {
    v.openBatch = v.openBatch === id ? null : id;
    drawBatches();
    if (v.openBatch === id && !v.batchDetail.has(id)) {
      const r = await req({ op: 'batches', batch: id }).catch(() => null);
      if (r && r.ok && r.batch) v.batchDetail.set(id, r.batch);
      else if (r) toast(errText(r), 'error');
      drawBatches();
    }
  }

  async function undoBatch(id) {
    hideUndo();
    const r = await req({ op: 'undo', batch: id });
    if (!r.ok) { toast(errText(r), r.error && r.error.code === 'conflict' ? 'warn' : 'error'); return; }
    v.version = r.version;
    (r.batch && r.batch.changes || []).forEach((c) => { if (c.after) v.flash.add(c.after.word); });
    toast('已復原。', 'ok');
    reload();
  }

  // 刪除不跳確認框：底部提示「已刪除… [復原]」，5 秒內可以按；之後仍可從「最近變更」復原。
  // action 是「最近變更」的批次編號，或復原用的函式（學到的詞的移除、不再學、恢復學習，#182）。
  function showUndo(message, action) {
    v.undo = action ? { message, action } : null;
    clear(el.undo);
    if (!action) { toast(message + '。', 'ok'); return; }
    const button = h('button', { type: 'button', class: 'link', onclick: () => (typeof action === 'function' ? action() : undoBatch(action)) }, '復原');
    el.undo.append(h('span', {}, message), button);
    el.undo.hidden = false;
    // 按下的刪除鈕隨著那一列消失時，焦點交給「復原」，用鍵盤的人可以馬上按 Enter 反悔。
    if (!document.activeElement || document.activeElement === document.body || !document.activeElement.isConnected) button.focus();
    armUndo();
  }
  function armUndo() {
    clearTimeout(undoTimer);
    if (v.undo) undoTimer = setTimeout(hideUndo, UNDO_MS);
  }
  function hideUndo() {
    clearTimeout(undoTimer);
    v.undo = null;
    if (!el.undo) return;
    const had = el.undo.contains(document.activeElement);
    el.undo.hidden = true;
    if (had && v.tab === 'words') el.add.focus();
    else if (had && el.tabs[v.tab]) el.tabs[v.tab].focus();
  }

  // ---- 讓 AI 幫你加詞 ----
  async function apiCall(action, port) {
    v.ai.busy = action;
    drawAI();
    try {
      const r = await invoke('api_command', port ? { action, port } : { action });
      if (r.error) toast(typeof r.error === 'string' ? r.error : errText(r), 'error');
      else v.ai.view = r;
      if (action === 'test') v.ai.test = r.test || null;
      if (action === 'regen' && !r.error) { toast('已重新產生 token，舊的已經失效。', 'ok'); v.ai.test = null; }
      if ((action === 'enable' || action === 'disable') && r.apply && ['deferred_composing', 'timeout', 'failed'].includes(r.apply.state)) {
        toast('已儲存；完成組字後生效。', 'warn');
      }
    } catch (error) {
      toast(String(error), 'error');
    }
    v.ai.busy = false;
    drawAI();
  }

  function snippet(tool, view, token) {
    const base = 'http://' + view.addr;
    const mcp = view.mcp_url || base + '/mcp';
    const windows = /^windows\//.test(ctx.platform());
    const auth = 'Authorization: Bearer ' + token;
    switch (tool) {
      case 'chat': return [
        '我的電腦上有靈犀注音的個人詞庫 API。',
        '文件：' + (view.openapi_url || base + '/openapi.json'),
        '呼叫時加標頭 ' + auth,
        '請把我接下來給你的文字裡的人名、公司名和專有名詞挑出來，',
        '用 suggest_readings 查注音；破音字或查不到的先列出來問我，',
        '確認後再用 add_words 一次加入，最後告訴我加了哪些。',
      ].join('\n');
      case 'claude-code': return windows
        ? 'claude mcp add --transport http 0c ' + mcp + ' --header "' + auth + '"'
        : 'claude mcp add --transport http 0c ' + mcp + ' \\\n  --header "' + auth + '"';
      case 'cursor': return JSON.stringify({ mcpServers: { '0c': { url: mcp, headers: { Authorization: 'Bearer ' + token } } } }, null, 2);
      case 'vscode': return JSON.stringify({ servers: { '0c': { type: 'http', url: mcp, headers: { Authorization: 'Bearer ' + token } } } }, null, 2);
      case 'stdio': return JSON.stringify({ mcpServers: { '0c': { command: ctx.mcpPath() || '0c-mcp' } } }, null, 2);
      case 'script': return windows ? [
        '$h = @{ Authorization = "Bearer ' + token + '" }',
        'Invoke-RestMethod -Headers $h "' + base + '/v1/words?q=陳"',
        'Invoke-RestMethod -Method Post -Headers $h -ContentType "application/json; charset=utf-8" `',
        '  -Body ([Text.Encoding]::UTF8.GetBytes(\'{"words":[{"word":"陳怡君","readings":["ㄔㄣˊ ㄧˊ ㄐㄩㄣ"]}]}\')) `',
        '  ' + base + '/v1/words',
      ].join('\n') : [
        'curl -H "' + auth + '" \\',
        '  --get --data-urlencode "q=陳" ' + base + '/v1/words',
        'curl -X POST -H "' + auth + '" \\',
        '  -H "Content-Type: application/json" \\',
        '  -d \'{"words":[{"word":"陳怡君","readings":["ㄔㄣˊ ㄧˊ ㄐㄩㄣ"]}]}\' \\',
        '  ' + base + '/v1/words',
      ].join('\n');
      default: return '';
    }
  }

  function snippetNote(tool, view) {
    const base = 'http://' + view.addr;
    switch (tool) {
      case 'chat': return '貼給能在這台電腦上執行指令的 AI 助手（例如終端機或編輯器裡的 AI）。網頁版的聊天機器人連不到這台電腦。';
      case 'claude-code': return '在終端機執行一次。';
      case 'cursor': return '加到 Cursor 的 MCP 設定檔 ~/.cursor/mcp.json。';
      case 'vscode': return '加到專案的 .vscode/mcp.json，或使用者設定的 MCP 伺服器。';
      case 'stdio': return '加到 AI 工具的 MCP 設定檔（例如桌面版 AI 助手的 claude_desktop_config.json）。0c-mcp 自己會讀 token，這段不含 token，重新產生後也不用改。'
        + (ctx.mcpPresent() ? '' : '這個版本還沒附 0c-mcp，更新之後才能用。');
      case 'script': return 'API 說明：' + base + '/docs';
      default: return '';
    }
  }

  const MASK = '••••••••••••';
  const masked = (token) => (token ? token.slice(0, 3) + MASK : '');

  function drawAI() { keepFocus(el.ai, drawAINow); }
  function drawAINow() {
    if (!el.ai) return;
    clear(el.ai);
    const ai = v.ai;
    el.ai.append(h('button', { type: 'button', class: 'disclosure', 'data-k': 'ai-toggle', 'aria-expanded': String(ai.open), 'aria-controls': 'vai-body',
      onclick: () => { ai.open = !ai.open; drawAI(); if (ai.open && !ai.view) apiCall('status'); } },
    h('span', { class: 'caret', 'aria-hidden': 'true' }, ai.open ? '▾' : '▸'), '讓 AI 幫你加詞'));
    if (!ai.open) return;
    const body = h('div', { id: 'vai-body', class: 'card vai-body' });
    el.ai.append(body);
    const view = ai.view;
    if (!view) { body.append(h('p', { class: 'muted' }, ai.busy ? '讀取中…' : '無法讀取 AI 助手的狀態。')); return; }
    const enabled = !!view.enabled;
    body.append(h('div', { class: 'row' },
      h('div', {}, h('div', { class: 'label', id: 'label-vai' }, '讓 AI 幫你加詞'), h('div', { class: 'desc' }, 'AI 助手讀得到你加的詞。')),
      h('button', { type: 'button', class: 'switch', role: 'switch', 'data-k': 'ai-switch', 'aria-checked': String(enabled), 'aria-labelledby': 'label-vai',
        disabled: !!ai.busy, onclick: () => apiCall(enabled ? 'disable' : 'enable') })));
    if (!enabled) return;

    const port = (view.addr || '').split(':').pop();
    const stateLine = {
      listening: ['已開啟，AI 工具可以連到 ' + view.addr + '。', 'status-ok'],
      off: ['已儲存，完成組字後開始服務。', 'status-warn'],
      port_in_use: ['埠 ' + port + ' 被其他程式占用，請換一個。', 'status-warn'],
      engine_not_running: ['輸入法引擎沒有在執行；開始打字後就會開始服務。', 'muted'],
      unsupported: ['執行中的輸入法版本不支援，更新之後才能用。', 'status-warn'],
    }[view.state] || ['狀態：' + view.state, 'muted'];
    body.append(h('p', { class: stateLine[1] + ' small vai-state' }, stateLine[0]));
    if (view.state === 'port_in_use') {
      const input = h('input', { type: 'number', min: '1024', max: '65535', class: 'port', value: ai.port || String(Number(port) + 1), 'aria-label': '新的埠號',
        oninput: (event) => { ai.port = event.target.value; } });
      body.append(h('div', { class: 'actions' }, input,
        h('button', { type: 'button', class: 'secondary', disabled: !!ai.busy, onclick: () => {
          const n = Number(input.value);
          if (!(n >= 1024 && n <= 65535)) { toast('埠號要在 1024～65535 之間。', 'warn'); return; }
          apiCall('enable', n);
        } }, '換這個埠')));
    }

    const token = view.token || '';
    const times = ['建立於 ' + (view.token_created ? new Date(view.token_created * 1000).toLocaleString('zh-TW', { hour12: false }) : '—'),
      view.last_used ? '最後使用 ' + new Date(view.last_used * 1000).toLocaleString('zh-TW', { hour12: false }) : '還沒有 AI 工具用過'];
    body.append(h('div', { class: 'row token-row' },
      h('div', {}, h('div', { class: 'label' }, 'token'), h('div', { class: 'desc' }, times.join(' · '))),
      h('div', { class: 'token-tools' },
        h('code', { class: 'token', 'aria-label': ai.shown ? 'token' : 'token（已遮住）' }, ai.shown ? token : masked(token)),
        h('button', { type: 'button', class: 'link', 'data-k': 'ai-show', onclick: () => { ai.shown = !ai.shown; drawAI(); } }, ai.shown ? '隱藏' : '顯示'),
        h('button', { type: 'button', class: 'link', 'data-k': 'ai-copy', onclick: () => copyText(token) }, '複製'),
        h('button', { type: 'button', class: 'link', 'data-k': 'ai-regen', disabled: !!ai.busy, onclick: () => { ai.regenAsk = true; drawAI(); } }, '重新產生'))));
    if (ai.regenAsk) {
      body.append(h('div', { class: 'notice' },
        h('p', {}, '重新產生後，舊的 token 立刻失效；已設定的 AI 工具要重新貼上設定。'),
        h('div', { class: 'actions' },
          h('button', { type: 'button', class: 'primary', 'data-k': 'ai-regen', onclick: () => { ai.regenAsk = false; apiCall('regen'); } }, '重新產生'),
          h('button', { type: 'button', class: 'secondary', onclick: () => { ai.regenAsk = false; drawAI(); } }, '取消'))));
    }

    const select = h('select', { id: 'vai-tool', 'data-k': 'ai-tool', onchange: (event) => { ai.tool = event.target.value; drawAI(); } },
      TOOLS.map(([key, label]) => h('option', { value: key, selected: key === ai.tool }, label)));
    const full = snippet(ai.tool, view, token);
    const shownText = ai.shown || !token ? full : full.split(token).join(masked(token));
    const hasToken = token && full.includes(token);
    body.append(
      h('div', { class: 'row' }, h('label', { class: 'label', for: 'vai-tool' }, '你用哪個 AI 工具？'), select),
      h('div', { class: 'snippet' },
        h('pre', { class: 'cmd' }, shownText),
        h('button', { type: 'button', class: 'secondary small', 'data-k': 'ai-snippet-copy', onclick: () => copyText(full) }, '複製')),
      h('p', { class: 'muted small' }, snippetNote(ai.tool, view)),
      hasToken ? h('p', { class: 'status-warn small' }, '這段含有你的 token，不要分享給別人。') : null);

    const test = ai.test;
    body.append(h('div', { class: 'actions' },
      h('button', { type: 'button', class: 'secondary', 'data-k': 'ai-test', disabled: !!ai.busy, onclick: () => apiCall('test') }, ai.busy === 'test' ? '測試中…' : '測試連線'),
      test ? h('span', { class: (test.ok ? 'status-ok' : 'status-warn') + ' small vai-test' },
        test.ok ? '連線成功：AI 工具看得到你加的 ' + num(test.count) + ' 個詞。' : '連線失敗：' + (test.detail || test.reason)) : null));
  }

  // ---- 即時同步 ----
  function syncWatch() {
    const want = v.active && !document.hidden && v.loaded && !v.disabled;
    if (want && !v.watching) {
      v.watching = true;
      invoke('vocab_watch', v.version != null ? { since: v.version } : {}).catch(() => { v.watching = false; });
    } else if (!want && v.watching) {
      v.watching = false;
      invoke('vocab_unwatch').catch(() => {});
    }
  }

  function onChanged(payload) {
    if (!payload || payload.version === v.version) return;
    reload();
  }

  // ---- 總覽小卡 ----
  async function refreshOverview() {
    try {
      const [status, batches] = await Promise.all([req({ op: 'status' }), req({ op: 'batches' })]);
      if (!status.ok) { v.overview = null; return; }
      const recent = (batches.batches || []).find((b) => b.source === 'assistant' && b.op === 'add' && !b.undone);
      v.overview = { count: status.count, recent: recent && Date.now() / 1000 - recent.time < 86400 ? recent : null };
    } catch (_) { v.overview = null; }
  }

  function overviewText() {
    const o = v.overview;
    if (!o) return null;
    if (!o.count) return '把常打錯的人名、術語加進來';
    let text = num(o.count) + ' 個';
    if (o.recent) text += ' · AI 助手' + (Date.now() / 1000 - o.recent.time < 3600 ? '剛' : '今天') + '加了 ' + num(o.recent.count) + ' 個';
    return text;
  }

  return {
    render(section) {
      mount(section);
      // 引擎剛啟動（狀態每 15 秒更新一次）：重新抓一次，排名徽章與學到的詞跟著出現。
      if (v.engineDown === true && ctx.engineRunning() === true) { v.engineDown = null; reload(); }
      drawNotices();
    },
    setActive(on) {
      v.active = on;
      if (on && el.root && v.loaded) { reload(); if (v.ai.open) apiCall('status'); }
      syncWatch();
    },
    visibility() { syncWatch(); if (!document.hidden && v.active && v.loaded) reload(); },
    focus() { if (v.active && v.tab === 'learned') loadLearned(); },
    onChanged,
    refreshOverview,
    overviewText,
  };
};
