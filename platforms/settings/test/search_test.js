// 設定搜尋的比對、分組與標示（#363，ui/search.js）：node --test platforms/settings/test/
// 由 scripts/test_release.py 在有 node 時執行。畫面與鍵盤操作由 search_page_test.py（Playwright）與 test/search.sh 負責。
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

globalThis.window = globalThis;
require('../ui/input-styles.js');
require('../ui/search.js');
const S = window.ZcSearch;

const VALUE = { neural: '神經選字', statistical: '統計引擎' };
const width = {
  key: 'shift-symbol-halfwidth', label: '標點寬度', description: '注音模式的符號鍵打全形標點還是半形符號。',
  values: ['off', 'on', 'all'], value: 'all', value_labels: { off: '全形標點', on: 'Shift＋符號打半形', all: '所有符號鍵打半形' },
  value_hints: { on: 'Shift＋, 打半形逗號、Shift＋9 打 (；要全形改按 Ctrl＋符號，或在符號上按 ↓。', all: '符號鍵都打鍵盤上印的字；這時 Ctrl＋標點固定開啟，不能不打聲調選字。' },
  keywords: ['全半形', '英文標點', '括號'],
};
// #365：自動加空格是三選一（關閉／中英文之間／中英文與數字之間）。
const space = {
  key: 'cjk-latin-space', label: '自動加空格', description: '送出時在中文與英文、數字之間補半形空格，只看同一次送出的字。',
  values: ['off', 'on', 'all'], value: 'off', value_labels: { off: '關閉', on: '中英文之間', all: '中英文與數字之間' },
  value_hints: { on: '「用Python寫」送出「用 Python 寫」，數字旁不補。', all: '「第2版」送出「第 2 版」；沒在組字時打的數字不補。' },
  keywords: ['空白', '間距', '排版', '盤古'],
};
const ctrl = {
  key: 'ctrl-punctuation', label: 'Ctrl＋標點打全形', description: '注音模式按 Ctrl＋, 打「，」、Ctrl＋. 打「。」；會占用程式的這幾個快速鍵。',
  values: ['on', 'off'], value: 'off', effective: 'on', disabled: true, disabled_reason: '所有符號鍵打半形時固定開啟', disabled_by: 'shift-symbol-halfwidth',
  keywords: ['半形', '逗號', '句號', 'Control'],
};
const shiftSpace = {
  key: 'style-shift-space', label: 'Shift＋空白', description: '按 Shift＋空白時做什麼。', values: ['disabled', 'fullwidth', 'space', 'single'], value: 'space',
  value_labels: { disabled: '不使用', fullwidth: '切換全半形', space: '插入空白', single: '開單字候選' },
  value_hints: { fullwidth: '英文、數字和空白也一起切成全形。', space: '打出半形空白，不選字。', single: '開候選窗，只列單一個字。' }, keywords: ['Space', '空格'],
};
const layout = {
  key: 'keyboard-layout', label: '鍵盤排列', description: '注音鍵盤的排列方式。', values: ['standard', 'eten', 'hsu', 'pinyin'], value: 'standard',
  value_labels: { standard: '標準', eten: '倚天 41 鍵', hsu: '許氏', pinyin: '漢語拼音' },
  value_hints: { standard: '大千式，最常見的注音鍵盤。', eten: '= 是 ㄦ、\' 是 ㄘ，頓號改按 \\；這時沒有 Emoji 選單與中英混打。' }, keywords: ['配置', '鍵位', 'Layout'],
};
const toggle = { key: 'toggle-key', label: '中英切換鍵', description: '單按哪個 Shift 切換注音和英文。', values: ['right', 'both', 'left', 'none'], value: 'right',
  value_labels: { right: '右 Shift', both: '左右 Shift', left: '左 Shift', none: '不用 Shift' }, keywords: ['中文', '快速鍵', '熱鍵'] };
const selection = { key: 'selection', label: '選字方式', description: '決定候選字順序的方法。', values: ['neural', 'statistical'], value: 'neural',
  value_hints: { neural: '依前文重新排序候選，需要載入模型。', statistical: '只看詞頻和你的個人詞庫，不載入模型。' }, keywords: ['AI', '智慧'] };

const PAGES = ['overview', 'styles', 'mode', 'candidates', 'keyboard', 'output', 'vocab', 'models', 'update', 'troubleshoot', 'about'];
// 傳進來的順序故意跟側邊欄不同（鍵盤與符號排在中英切換前面）：分組要照 PAGES。
const entries = [
  S.itemEntry(layout, 'keyboard', VALUE), S.itemEntry(ctrl, 'keyboard', VALUE), S.itemEntry(width, 'keyboard', VALUE), S.itemEntry(space, 'keyboard', VALUE),
  S.itemEntry(shiftSpace, 'keyboard', VALUE), S.itemEntry(toggle, 'mode', VALUE), S.itemEntry(selection, 'candidates', VALUE),
  { id: 'page:update', kind: 'page', page: 'update', label: '更新', description: '檢查有沒有新版，下載並安裝。', keywords: ['版本', '自動更新', '升級'], options: [], value: '' },
];
const keys = (found) => found.results.map((r) => r.entry.key || r.entry.id);

test('比對用的摺疊：不分大小寫、全形英數與符號當半形、長度不變', () => {
  assert.strictEqual(S.fold('Ｓｈｉｆｔ＋空白'), 'shift+空白');
  assert.strictEqual(S.fold('Caps Lock'), 'caps lock');
  assert.strictEqual(S.fold('半　形'), '半 形');
  for (const text of ['Ｓｈｉｆｔ＋空白', 'İstanbul ǅ ß', '注音ㄓㄗ', '😀 emoji', '']) assert.strictEqual(S.fold(text).length, text.length, text);
  assert.strictEqual(S.fold(null), '');
});

test('查詢拆成詞：前後空白忽略、半形與全形空白都能分開、重複的只留一個', () => {
  assert.deepStrictEqual(S.terms('  半形  '), ['半形']);
  assert.deepStrictEqual(S.terms('Shift　空白 shift'), ['shift', '空白']);
  assert.deepStrictEqual(S.terms('   '), []);
  assert.deepStrictEqual(S.terms(''), []);
  assert.strictEqual(S.terms('a b c d e f g h i j k l').length, 8);
});

test('搜「半形」：說明、選項名稱、關鍵字命中的都列出，頁內照頁面由上到下的順序', () => {
  const found = S.search(entries, '半形', PAGES);
  assert.deepStrictEqual(keys(found), ['ctrl-punctuation', 'shift-symbol-halfwidth', 'cjk-latin-space', 'style-shift-space']);
  assert.deepStrictEqual(found.counts, { keyboard: 4 });
  const by = Object.fromEntries(found.results.map((r) => [r.entry.key, r]));
  // 標點寬度：說明裡有，兩個選項的名稱也有 → 多一行「選項：」，只列命中的兩個；不必再列提示與關鍵字。
  assert.deepStrictEqual(by['shift-symbol-halfwidth'].match.options.map((o) => o.name), ['Shift＋符號打半形', '所有符號鍵打半形']);
  assert.deepStrictEqual(by['shift-symbol-halfwidth'].match.hints, []);
  assert.deepStrictEqual(by['shift-symbol-halfwidth'].match.keywords, []);
  // 自動加空格：只有說明命中。
  assert.deepStrictEqual(by['cjk-latin-space'].match, { options: [], hints: [], keywords: [] });
  // Ctrl＋標點：名稱與說明都沒有「半形」，靠關鍵字 → 「也可以找：」列出這一項全部的關鍵字。
  assert.deepStrictEqual(by['ctrl-punctuation'].match.keywords, ['半形', '逗號', '句號', 'Control']);
  // Shift＋空白：選項「切換全半形」命中。
  assert.deepStrictEqual(by['style-shift-space'].match.options.map((o) => o.name), ['切換全半形']);
});

test('反灰停用的項目照樣找得到：帶停用原因，值是實際使用的（effective）', () => {
  const entry = S.itemEntry(ctrl, 'keyboard', VALUE);
  assert.strictEqual(entry.disabled, true);
  assert.strictEqual(entry.reason, '所有符號鍵打半形時固定開啟');
  assert.strictEqual(entry.value, '開啟'); // 設定檔裡是 off，固定開啟
  // 沒有 effective 的停用開關畫成關閉；設定檔裡的值不動。
  assert.strictEqual(S.itemEntry({ key: 'mixed-input', label: '中英混打', values: ['on', 'off'], value: 'on', disabled: true, disabled_reason: '倚天 41 鍵時關閉' }, 'mode', VALUE).value, '關閉');
  // 多選一停用時也照 effective。
  assert.strictEqual(S.itemEntry({ key: 'start-mode', label: '開始時的中英模式', values: ['zhuyin', 'english'], value: 'english', effective: 'zhuyin', disabled: true,
    disabled_reason: '不用 Shift 切換時固定從注音開始', value_labels: { zhuyin: '注音', english: '英文' } }, 'mode', VALUE).value, '注音');
  // 沒停用就沒有原因。
  assert.strictEqual(S.itemEntry(width, 'keyboard', VALUE).reason, '');
});

test('目前的值照設定頁顯示的名稱：value_labels、畫面自己的對照表、開關是開啟／關閉', () => {
  assert.strictEqual(S.itemEntry(width, 'keyboard', VALUE).value, '所有符號鍵打半形');
  assert.strictEqual(S.itemEntry(selection, 'candidates', VALUE).value, '神經選字');
  const toggle = { key: 'esc-clear', label: '按 Esc 清除整段組字', description: '關閉時 Esc 只清除還沒打完的注音，已組好的字保留。', values: ['on', 'off'], value: 'off' };
  assert.strictEqual(S.itemEntry(toggle, 'candidates', VALUE).value, '關閉');
  assert.strictEqual(S.itemEntry(Object.assign({}, toggle, { value: 'on' }), 'candidates', VALUE).value, '開啟');
  // 自動加空格（#365）是三選一，值照 value_labels。
  assert.strictEqual(S.itemEntry(space, 'keyboard', VALUE).value, '關閉');
  assert.strictEqual(S.itemEntry(Object.assign({}, space, { value: 'on' }), 'keyboard', VALUE).value, '中英文之間');
  assert.strictEqual(S.itemEntry(Object.assign({}, space, { value: 'all' }), 'keyboard', VALUE).value, '中英文與數字之間');
});

test('選項名稱用畫面的對照表也搜得到（契約沒給 value_labels 的「選字方式」）', () => {
  const found = S.search(entries, '神經', PAGES);
  assert.deepStrictEqual(keys(found), ['selection']);
  assert.deepStrictEqual(found.results[0].match.options.map((o) => o.name), ['神經選字']);
});

test('選項提示也搜得到：名稱與說明沒有、提示裡有時多一行那個選項的提示', () => {
  const found = S.search(entries, '大千', PAGES);
  assert.deepStrictEqual(keys(found), ['keyboard-layout']);
  const hit = found.results[0].match;
  assert.deepStrictEqual(hit.options, []);
  assert.deepStrictEqual(hit.hints.map((o) => [o.name, o.hint]), [['標準', '大千式，最常見的注音鍵盤。']]);
  assert.deepStrictEqual(hit.keywords, []);
  // 說明裡就有的詞，不必再列提示。
  assert.deepStrictEqual(S.search(entries, '排列', PAGES).results[0].match.hints, []);
});

test('開關的「開啟」「關閉」不算選項名稱：搜「關閉」不會把每個開關都列出來', () => {
  // 自動加空格（#365）是三選一的單選，「關閉」是它真的有的選項名稱，所以找得到；其他開關都不列。
  assert.deepStrictEqual(keys(S.search(entries, '關閉', PAGES)), ['cjk-latin-space']);
  assert.deepStrictEqual(S.search(entries, '關閉', PAGES).results[0].match.options.map((o) => o.name), ['關閉']);
  // 「開啟」只找到提示裡真的寫了的那一項（所有符號鍵打半形：「這時 Ctrl＋標點固定開啟」），不是每個開關。
  assert.deepStrictEqual(keys(S.search(entries, '開啟', PAGES)), ['shift-symbol-halfwidth']);
  assert.deepStrictEqual(S.itemEntry(ctrl, 'keyboard', VALUE).options, []);
  assert.deepStrictEqual(S.itemEntry(space, 'keyboard', VALUE).options.map((o) => o.name), ['關閉', '中英文之間', '中英文與數字之間']);
});

test('不分大小寫、全形英數當半形', () => {
  const want = ['toggle-key', 'style-shift-space', 'shift-symbol-halfwidth'].sort();
  for (const query of ['shift', 'SHIFT', 'Ｓｈｉｆｔ', ' Shift ']) assert.deepStrictEqual(keys(S.search(entries, query, PAGES)).sort(), want, query);
  // 半形的 + 找得到全形的 ＋。
  assert.deepStrictEqual(keys(S.search(entries, 'ctrl+標點', PAGES)), ['ctrl-punctuation', 'shift-symbol-halfwidth']);
  assert.deepStrictEqual(keys(S.search(entries, 'CTRL+標點打全形', PAGES)), ['ctrl-punctuation']);
  assert.deepStrictEqual(keys(S.search(entries, 'layout', PAGES)), ['keyboard-layout']);
});

test('多個詞用空白分開要全部命中，各自可以在不同欄位', () => {
  assert.deepStrictEqual(keys(S.search(entries, 'Shift 空白', PAGES)), ['style-shift-space']);
  // 「熱鍵」在關鍵字、「shift」在名稱與說明。
  const found = S.search(entries, '熱鍵 shift', PAGES);
  assert.deepStrictEqual(keys(found), ['toggle-key']);
  assert.deepStrictEqual(found.results[0].match.keywords, ['中文', '快速鍵', '熱鍵']);
  assert.deepStrictEqual(keys(S.search(entries, 'Shift 簡體', PAGES)), []);
  // 全形空白也能分開。
  assert.deepStrictEqual(keys(S.search(entries, 'Shift　空白', PAGES)), ['style-shift-space']);
});

test('分組照側邊欄的頁面順序，組內照傳進來的順序；不依名稱命中重排', () => {
  const found = S.search(entries, 'shift', PAGES);
  assert.deepStrictEqual(found.groups.map((g) => g.page), ['mode', 'keyboard']);
  assert.deepStrictEqual(found.groups.map((g) => g.results.map((r) => r.entry.key)), [['toggle-key'], ['shift-symbol-halfwidth', 'style-shift-space']]);
  // results 是畫面由上到下的順序（↑↓ 照它走）。
  assert.deepStrictEqual(keys(found), ['toggle-key', 'shift-symbol-halfwidth', 'style-shift-space']);
  assert.deepStrictEqual(found.counts, { mode: 1, keyboard: 2 });
  // 名稱命中的（Shift＋空白）不會排到只有選項命中的（標點寬度）前面。
  assert.ok(keys(found).indexOf('shift-symbol-halfwidth') < keys(found).indexOf('style-shift-space'));
  // 不在 pages 裡的頁面排在最後，不會被丟掉。
  const extra = S.search(entries.concat([{ id: 'x', kind: 'item', page: 'future', key: 'x', label: 'Shift 新功能', description: '', options: [], keywords: [] }]), 'shift', PAGES);
  assert.deepStrictEqual(extra.groups.map((g) => g.page), ['mode', 'keyboard', 'future']);
});

test('非設定項的頁面本身是一筆結果：頁面名稱與關鍵字', () => {
  assert.deepStrictEqual(keys(S.search(entries, '更新', PAGES)), ['page:update']);
  const found = S.search(entries, '升級', PAGES);
  assert.deepStrictEqual(keys(found), ['page:update']);
  assert.deepStrictEqual(found.results[0].match.keywords, ['版本', '自動更新', '升級']);
});

test('設定頁本身當成結果時只比對頁名：頁首那一句（note）不比對', () => {
  // 鍵盤與符號頁：名稱是頁名，頁首那一句放在 note，沒有 description 與關鍵字；排在這一頁各項的前面。
  const page = { id: 'page:keyboard', kind: 'page', page: 'keyboard', label: '鍵盤與符號', description: '', note: '注音的鍵盤排列，以及標點符號和 Emoji 怎麼打。', keywords: [], options: [], value: '' };
  const withPage = [page].concat(entries);
  // 搜頁名：那一頁本身是一筆，排在該頁分組的最前面，筆數照實算。
  let found = S.search(withPage, '鍵盤與符號', PAGES);
  assert.deepStrictEqual(keys(found), ['page:keyboard']);
  assert.deepStrictEqual(found.counts, { keyboard: 1 });
  assert.deepStrictEqual(found.results[0].match, { options: [], hints: [], keywords: [] });
  found = S.search(withPage, '鍵盤', PAGES);
  assert.deepStrictEqual(keys(found), ['page:keyboard', 'keyboard-layout', 'shift-symbol-halfwidth']);
  assert.deepStrictEqual(found.groups.map((g) => [g.page, g.results.map((r) => r.entry.key || r.entry.id)]), [['keyboard', ['page:keyboard', 'keyboard-layout', 'shift-symbol-halfwidth']]]);
  assert.deepStrictEqual(found.counts, { keyboard: 3 });
  // 頁首那一句裡的字（標點符號、Emoji、怎麼打）不會讓這一頁多出一筆。
  assert.ok(!keys(S.search(withPage, '標點符號', PAGES)).includes('page:keyboard'));
  assert.ok(!keys(S.search(withPage, 'emoji', PAGES)).includes('page:keyboard'));
  assert.deepStrictEqual(keys(S.search(withPage, '怎麼打', PAGES)), []);
  // 「半形」的結果跟沒有這一筆時一樣。
  assert.deepStrictEqual(keys(S.search(withPage, '半形', PAGES)), keys(S.search(entries, '半形', PAGES)));
  // 多個詞要全部在頁名裡。
  assert.deepStrictEqual(keys(S.search(withPage, '鍵盤 符號', PAGES)).filter((key) => key === 'page:keyboard'), ['page:keyboard']);
  assert.ok(!keys(S.search(withPage, '鍵盤 emoji', PAGES)).includes('page:keyboard'));
  // 頁面結果沒有停用這回事：預設選取可以是它。
  assert.strictEqual(S.firstEnabled(S.search(withPage, '鍵盤', PAGES).results), 0);
});

test('找不到、空的查詢：沒有結果', () => {
  for (const query of ['字體大小', '', '   ']) {
    const found = S.search(entries, query, PAGES);
    assert.deepStrictEqual(found.results, []);
    assert.deepStrictEqual(found.groups, []);
    assert.deepStrictEqual(found.counts, {});
  }
});

test('舊的 0c-update 沒有 keywords、value_hints：照樣能搜名稱、說明與選項', () => {
  const old = [width, ctrl, layout].map((item) => { const copy = Object.assign({}, item); delete copy.keywords; delete copy.value_hints; return S.itemEntry(copy, 'keyboard', VALUE); });
  assert.deepStrictEqual(keys(S.search(old, '半形', PAGES)), ['shift-symbol-halfwidth']); // Ctrl＋標點只有關鍵字有「半形」
  assert.deepStrictEqual(keys(S.search(old, '倚天', PAGES)), ['keyboard-layout']);
  assert.deepStrictEqual(keys(S.search(old, '大千', PAGES)), []); // 只寫在提示裡
  // 欄位型別不對（不是陣列、不是字串）當作沒有，不出錯。
  const odd = S.itemEntry({ key: 'x', label: '測試', values: ['on', 'off'], value: 'on', keywords: 'abc', value_hints: 'no' }, 'mode', VALUE);
  assert.deepStrictEqual(odd.keywords, []);
  assert.deepStrictEqual(S.itemEntry({ key: 'x', label: '測試', values: ['on', 'off'], value: 'on', keywords: ['好', 3, ''] }, 'mode', VALUE).keywords, ['好']);
});

test('標示命中的字：切成段，原文不變（全形、大小寫照原樣）', () => {
  assert.deepStrictEqual(S.pieces('注音模式的符號鍵打全形標點還是半形符號。', ['半形']),
    [{ text: '注音模式的符號鍵打全形標點還是', hit: false }, { text: '半形', hit: true }, { text: '符號。', hit: false }]);
  assert.deepStrictEqual(S.pieces('Shift＋空白', ['shift']), [{ text: 'Shift', hit: true }, { text: '＋空白', hit: false }]);
  assert.deepStrictEqual(S.pieces('Ｓｈｉｆｔ鍵', ['shift']), [{ text: 'Ｓｈｉｆｔ', hit: true }, { text: '鍵', hit: false }]);
  // 多個詞、重疊與相鄰的合併，每段都接得回原文。
  assert.deepStrictEqual(S.ranges('半形半形符號', ['半形', '形半']), [[0, 4]]);
  assert.deepStrictEqual(S.ranges('所有符號鍵打半形', ['符號', '半形']), [[2, 4], [6, 8]]);
  for (const [text, words] of [['Shift＋符號打半形', ['shift', '半形']], ['沒有命中', ['xyz']], ['', ['a']]]) {
    assert.strictEqual(S.pieces(text, words).map((p) => p.text).join(''), text);
  }
  assert.deepStrictEqual(S.pieces('沒有命中', ['xyz']), [{ text: '沒有命中', hit: false }]);
});

test('自動加空格（#365 三選一）：搜「數字」「空格」「中英文」「盤古」都找得到', () => {
  // 「數字」：說明與選項「中英文與數字之間」都有 → 多一行「選項：」。
  let hit = S.search(entries, '數字', PAGES).results.find((r) => r.entry.key === 'cjk-latin-space');
  assert.deepStrictEqual(hit.match.options.map((o) => o.name), ['中英文與數字之間']);
  // 「空格」：名稱裡就有。
  hit = S.search(entries, '空格', PAGES).results.find((r) => r.entry.key === 'cjk-latin-space');
  assert.deepStrictEqual(hit.match, { options: [], hints: [], keywords: [] });
  // 「中英文」：只有兩個選項的名稱有。
  hit = S.search(entries, '中英文', PAGES).results.find((r) => r.entry.key === 'cjk-latin-space');
  assert.deepStrictEqual(hit.match.options.map((o) => o.name), ['中英文之間', '中英文與數字之間']);
  // 「Python」：只有「中英文之間」的提示有。
  hit = S.search(entries, 'python', PAGES).results.find((r) => r.entry.key === 'cjk-latin-space');
  assert.deepStrictEqual(hit.match.hints.map((o) => o.name), ['中英文之間']);
  // 「盤古」：關鍵字。
  hit = S.search(entries, '盤古', PAGES).results.find((r) => r.entry.key === 'cjk-latin-space');
  assert.deepStrictEqual(hit.match.keywords, ['空白', '間距', '排版', '盤古']);
});

test('預設選取第一筆沒有停用的結果；全部停用時才選第一筆', () => {
  // 「半形」：第一筆是反灰的 Ctrl＋標點，預設選第二筆（標點寬度）；順序不變，反灰的還在第一筆。
  const found = S.search(entries, '半形', PAGES);
  assert.deepStrictEqual(keys(found), ['ctrl-punctuation', 'shift-symbol-halfwidth', 'cjk-latin-space', 'style-shift-space']);
  assert.strictEqual(found.results[0].entry.disabled, true);
  assert.strictEqual(S.firstEnabled(found.results), 1);
  // 第一筆本來就能改：選它。
  assert.strictEqual(S.firstEnabled(S.search(entries, 'shift', PAGES).results), 0);
  // 只找到反灰的項目：選第一筆。
  const only = S.search(entries, 'Control', PAGES);
  assert.deepStrictEqual(keys(only), ['ctrl-punctuation']);
  assert.strictEqual(S.firstEnabled(only.results), 0);
  const off = (key) => ({ entry: { key, disabled: true } });
  const on = (key) => ({ entry: { key, disabled: false } });
  assert.strictEqual(S.firstEnabled([off('a'), off('b'), off('c')]), 0);
  assert.strictEqual(S.firstEnabled([off('a'), off('b'), on('c'), on('d')]), 2);
  // 頁面本身的結果沒有 disabled 欄位，當作能前往。
  assert.strictEqual(S.firstEnabled([off('a'), { entry: { id: 'page:update', kind: 'page' } }]), 1);
  // 沒有結果。
  assert.strictEqual(S.firstEnabled([]), -1);
  assert.strictEqual(S.firstEnabled(S.search(entries, '字體大小', PAGES).results), -1);
});

test('↑↓ 移動選取：到頭就停', () => {
  assert.strictEqual(S.step(0, 1, 4), 1);
  assert.strictEqual(S.step(3, 1, 4), 3);
  assert.strictEqual(S.step(0, -1, 4), 0);
  assert.strictEqual(S.step(2, -1, 4), 1);
  assert.strictEqual(S.step(-1, 1, 4), 1);
  assert.strictEqual(S.step(0, 1, 0), -1);
  // 從預設選取的第二筆（第一筆是反灰的）按 ↑ 照樣走得到第一筆。
  assert.strictEqual(S.step(S.firstEnabled(S.search(entries, '半形', PAGES).results), -1, 4), 0);
});

test('index.html 在 app.js 之前載入 search.js，搜尋框在導覽之前', () => {
  const html = fs.readFileSync(path.join(__dirname, '../ui/index.html'), 'utf8');
  assert.ok(html.indexOf('<script src="search.js">') > 0 && html.indexOf('<script src="search.js">') < html.indexOf('<script src="app.js">'));
  // Tab 順序照文件順序：搜尋框排在第一個導覽按鈕之前；清除鈕不占 Tab 站。
  assert.ok(html.indexOf('id="search-input"') > 0 && html.indexOf('id="search-input"') < html.indexOf('class="nav"'));
  assert.match(html, /<input id="search-input"[^>]+type="search"[^>]+placeholder="搜尋設定"/);
  assert.match(html, /<label class="sr-only" for="search-input">搜尋設定<\/label>/);
  assert.match(html, /<button type="button" id="search-clear"[^>]+tabindex="-1"[^>]+aria-label="清除搜尋"[^>]+hidden>/);
});
