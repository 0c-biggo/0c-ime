// 設定搜尋的比對、分組與標示（#363）：不碰畫面，node 測試直接載入（platforms/settings/test/search_test.js）。
// 畫面與鍵盤操作在 app.js；資料全部來自已經讀到的 0c-update settings -json，搜尋不另外呼叫後端。
//
// 一筆可以被搜到的東西（entry）：
//   { id, kind: 'item' | 'page', page, key, label, description, options: [{ value, name, label, hint }],
//     keywords: [字串], value, disabled, reason, note }
// - label、description、options[].label（選項名稱）、options[].hint（選項提示，契約的 value_hints）、keywords
//   （契約的 keywords，舊版 0c-update 沒有就是空的）都會比對。開關的選項名稱（開啟／關閉）不比對：label 是空字串，
//   name 才是顯示用的名稱。
// - note 只給畫面顯示，不比對：四個設定頁本身當成結果時只認頁名，頁首那一句放在 note。
// - 比對不分大小寫，全形英數與符號（Ｓｈｉｆｔ、＋）視為半形；多個詞用空白分開，每個詞都要在某個欄位找得到。
// - 結果照頁面順序分組，組內照傳進來的順序（就是那一頁由上到下的順序），不依相關度重排。
// - 預設選取的是第一筆沒有停用的結果（firstEnabled）。
(function () {
  'use strict';

  const MAX_TERMS = 8;
  const MAX_QUERY = 60;

  // 摺成比對用的樣子。逐字換、長度不變，所以在摺過的字串上找到的位置可以直接拿來標原文。
  function fold(text) {
    const source = String(text == null ? '' : text);
    let out = '';
    for (let i = 0; i < source.length; i++) {
      let code = source.charCodeAt(i);
      if (code >= 0xFF01 && code <= 0xFF5E) code -= 0xFEE0; // 全形英數與符號
      else if (code === 0x3000) code = 0x20; // 全形空白
      const ch = String.fromCharCode(code);
      const lower = ch.toLowerCase();
      out += lower.length === 1 ? lower : ch;
    }
    return out;
  }

  // 查詢字串拆成詞：前後空白忽略、空白分開、重複的只留一個。
  function terms(query) {
    const out = [];
    for (const term of fold(query).slice(0, MAX_QUERY * 4).split(/\s+/)) {
      if (term && !out.includes(term) && out.length < MAX_TERMS) out.push(term);
    }
    return out;
  }

  // text 裡命中任何一個詞的區段 [[開始, 結束), …]，由前到後、重疊的合併。
  function ranges(text, words) {
    const folded = fold(text);
    const found = [];
    for (const word of words) {
      if (!word) continue;
      for (let at = folded.indexOf(word); at !== -1; at = folded.indexOf(word, at + word.length)) found.push([at, at + word.length]);
    }
    found.sort((a, b) => a[0] - b[0] || a[1] - b[1]);
    const out = [];
    for (const [start, end] of found) {
      const last = out[out.length - 1];
      if (last && start <= last[1]) last[1] = Math.max(last[1], end);
      else out.push([start, end]);
    }
    return out;
  }

  // 把 text 切成 [{ text, hit }]，畫面把 hit 的那幾段包成 <mark>。
  function pieces(text, words) {
    const source = String(text == null ? '' : text);
    const out = [];
    let at = 0;
    for (const [start, end] of ranges(source, words)) {
      if (start > at) out.push({ text: source.slice(at, start), hit: false });
      out.push({ text: source.slice(start, end), hit: true });
      at = end;
    }
    if (at < source.length) out.push({ text: source.slice(at), hit: false });
    return out;
  }

  // 這一筆符不符合；不符合回 null。符合時回要多畫哪幾行：
  // - options：名稱命中的選項（「選項：…」）。
  // - hints：名稱、說明與選項名稱都沒有某個詞，而提示裡有的選項（「選項「…」：提示」）。
  // - keywords：上面都沒有某個詞、關鍵字裡才有時，這一項全部的關鍵字（「也可以找：…」）；否則是空的。
  function match(entry, words) {
    if (!entry || !words.length) return null;
    const label = fold(entry.label);
    const description = fold(entry.description);
    const options = (entry.options || []).map((option) => ({ option, label: fold(option.label), hint: fold(option.hint) }));
    const keywords = (entry.keywords || []).map(fold);
    const inText = (word) => label.includes(word) || description.includes(word) || options.some((o) => o.label.includes(word));
    const inHints = (word) => options.some((o) => o.hint.includes(word));
    const inKeywords = (word) => keywords.some((keyword) => keyword.includes(word));
    if (!words.every((word) => inText(word) || inHints(word) || inKeywords(word))) return null;
    const rest = words.filter((word) => !inText(word));
    const byKeyword = rest.filter((word) => !inHints(word));
    return {
      options: options.filter((o) => words.some((word) => o.label.includes(word))).map((o) => o.option),
      hints: options.filter((o) => rest.some((word) => o.hint.includes(word))).map((o) => o.option),
      keywords: byKeyword.length ? (entry.keywords || []).slice() : [],
    };
  }

  // 搜尋：{ terms, results: [{ entry, match }], groups: [{ page, results }], counts: { 頁面: 筆數 } }。
  // results 是畫面由上到下的順序（↑↓ 照它移動）：頁面照 pages 的順序，同一頁照 entries 裡的順序。
  function search(entries, query, pages) {
    const words = terms(query);
    const byPage = new Map();
    for (const entry of entries || []) {
      const hit = match(entry, words);
      if (!hit) continue;
      if (!byPage.has(entry.page)) byPage.set(entry.page, []);
      byPage.get(entry.page).push({ entry, match: hit });
    }
    const order = (pages || []).filter((page) => byPage.has(page));
    for (const page of byPage.keys()) if (!order.includes(page)) order.push(page);
    const groups = order.map((page) => ({ page, results: byPage.get(page) }));
    const counts = {};
    for (const group of groups) counts[group.page] = group.results.length;
    return { terms: words, results: groups.flatMap((group) => group.results), groups, counts };
  }

  // 結果一出來預設選取哪一筆：第一筆沒有停用的（反灰的項目按 Enter 過去也不能改）。排列順序不變，反灰的照樣在原位，
  // ↑↓ 也走得到；全部都是反灰的項目時才選第一筆。沒有結果是 -1。
  function firstEnabled(results) {
    if (!results || !results.length) return -1;
    const index = results.findIndex((result) => !result.entry.disabled);
    return index < 0 ? 0 : index;
  }

  // ↑↓ 移動選取：到頭就停，不繞回去。
  function step(index, delta, count) {
    if (count <= 0) return -1;
    return Math.min(count - 1, Math.max(0, (index < 0 ? 0 : index) + delta));
  }

  // 契約的一個設定項目 → entry。值與選項名稱照設定頁顯示的（停用時是實際使用的值，契約的 effective）。
  // labels 是畫面自己的選項名稱對照表（契約沒給 value_labels 的項目用）。
  function itemEntry(item, page, labels) {
    const logic = window.ZcInputStylesLogic;
    const view = logic.controlState(item, false);
    const hints = item.value_hints && typeof item.value_hints === 'object' ? item.value_hints : {};
    const hintOf = (value) => (Object.prototype.hasOwnProperty.call(hints, value) && typeof hints[value] === 'string' ? hints[value] : '');
    const options = (item.values || []).map((value) => {
      const name = logic.valueLabel(item, value, labels);
      return { value, name, label: view.toggle ? '' : name, hint: hintOf(value) };
    }).filter((option) => option.label || option.hint);
    return {
      id: 'item:' + item.key, kind: 'item', page, key: item.key,
      label: item.label || item.key, description: item.description || '', options,
      keywords: Array.isArray(item.keywords) ? item.keywords.filter((word) => typeof word === 'string' && word) : [],
      value: logic.valueLabel(item, view.value, labels),
      disabled: !!item.disabled, reason: item.disabled && item.disabled_reason ? item.disabled_reason : '',
    };
  }

  window.ZcSearch = { fold, terms, ranges, pieces, match, search, firstEnabled, step, itemEntry };
})();
