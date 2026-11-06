// 「我的詞彙」匯出與匯入的文字格式（#255，ui/vocab-text.js）：node --test platforms/settings/test/
// 由 scripts/test_release.py 在有 node 時執行。
'use strict';
const test = require('node:test');
const assert = require('node:assert');

globalThis.window = globalThis;
require('../ui/vocab-text.js');
const T = window.ZcVocabText;

// 匯出的詞照 list 指令的樣子：破音字的自訂讀音、多個讀音、英文詞、輕聲。
const WORDS = [
  { word: '陳怡君', readings: ['ㄔㄣˊ ㄧˊ ㄐㄩㄣ'] },
  { word: '曾國城', readings: ['ㄗㄥ ㄍㄨㄛˊ ㄔㄥˊ'] },          // 曾 讀 ㄗㄥ（查讀音預設是 ㄘㄥˊ 也一樣保住）
  { word: '銀行', readings: ['ㄧㄣˊ ㄏㄤˊ', 'ㄧㄣˊ ㄒㄧㄥˊ'] },     // 兩個讀音
  { word: '朋友們', readings: ['ㄆㄥˊ ㄧㄡˇ ㄇㄣ˙'] },
  { word: 'kubectl', readings: [] },
  { word: 'git-lfs', readings: [] },
  { word: 'v1.2_rc', readings: [] },
];
const PHRASES = [
  '台北市大同區承德路一段 70 號 5 樓',
  'chen.yijun@example.com',
  '陳怡君 敬上\n研發部',                 // 換行
  '姓名\t電話\r\n王小明\t0912',          // Tab、Windows 換行
  '  前後有空白  ',
  '# 不是說明',
  '"引號"開頭',
  '[我的片語]',
  '[我加的詞]',
  'C:\\Users\\chen\\Desktop',           // 反斜線照字面
  '第一段\u2028第二段\u0085第三段',      // 有些編輯器當成換行的字元
  '表情 😀 與 ㄅㄆㄇ',
  '\\n 不是換行',
  '"',
];

function roundTrip(words, phrases) {
  const text = T.format({ words, phrases: phrases.map((text) => ({ text })), date: new Date(2026, 10, 4) });
  return { text, parsed: T.parse(text) };
}

test('匯出再匯入：詞、讀音（含破音字與多個讀音）、英文詞、片語都相同', () => {
  const { parsed } = roundTrip(WORDS, PHRASES);
  assert.deepStrictEqual(parsed.words.map((w) => ({ word: w.word, readings: w.readings })), WORDS);
  assert.deepStrictEqual(parsed.phrases.map((p) => p.text), PHRASES);
});

test('匯出再匯入到空的詞彙：每個詞照檔案的讀音加入、不重查，片語全部加入', () => {
  const { parsed } = roundTrip(WORDS, PHRASES);
  const p = T.plan(parsed, { words: [], phrases: [], count: 0, limit: 10000 });
  assert.strictEqual(p.same, 0);
  assert.deepStrictEqual(p.rows.map((r) => [r.word, r.readings, r.given, r.problem]), WORDS.map((w) => [w.word, w.readings, true, null]));
  assert.deepStrictEqual(p.phrases.map((x) => x.text), PHRASES);
  assert.ok(p.phrases.every((x) => !x.problem));
});

test('匯入到同一份詞彙：全部已經有了，什麼都不加', () => {
  const { parsed } = roundTrip(WORDS, PHRASES);
  const p = T.plan(parsed, { words: WORDS, phrases: PHRASES, count: WORDS.length, limit: 10000 });
  assert.deepStrictEqual(p.rows, []);
  assert.deepStrictEqual(p.phrases, []);
  assert.strictEqual(p.same, WORDS.length);
  assert.strictEqual(p.samePhrases, PHRASES.length);
});

test('檔案是人看得懂的文字：一行一詞、詞與讀音以 Tab 分隔，片語另一段', () => {
  const { text } = roundTrip(WORDS.slice(0, 3), ['台北市', '陳怡君 敬上\n研發部']);
  const lines = text.split('\n');
  assert.match(lines[0], /^# 靈犀注音「我的詞彙」（2026-11-04 匯出，3 個詞、2 則片語）$/);
  assert.ok(lines.includes('[我加的詞]'));
  assert.ok(lines.includes('陳怡君\tㄔㄣˊ ㄧˊ ㄐㄩㄣ'));
  assert.ok(lines.includes('銀行\tㄧㄣˊ ㄏㄤˊ') && lines.includes('銀行\tㄧㄣˊ ㄒㄧㄥˊ'), '多個讀音一行一個');
  assert.ok(lines.indexOf('[我的片語]') > lines.indexOf('銀行\tㄧㄣˊ ㄒㄧㄥˊ'));
  assert.ok(lines.includes('台北市'), '一般的片語照字面');
  assert.ok(lines.includes('"陳怡君 敬上\\n研發部"'), '有換行的片語寫成 JSON 字串');
  assert.ok(text.endsWith('\n'));
});

test('英文詞只有詞面，沒有 Tab', () => {
  const { text } = roundTrip([{ word: 'kubectl', readings: [] }], []);
  assert.ok(text.split('\n').includes('kubectl'));
  assert.ok(!text.includes('kubectl\t'));
});

test('貼上：沒有讀音的一行一詞，照舊要查讀音', () => {
  const p = T.parse('陳怡君\n林志玲\r\nkubectl\n\n');
  assert.deepStrictEqual(p.words.map((w) => [w.word, w.readings]), [['陳怡君', []], ['林志玲', []], ['kubectl', []]]);
  const plan = T.plan(p, { words: [], phrases: [] });
  assert.deepStrictEqual(plan.rows.map((r) => r.given), [false, false, true], '漢字詞要查讀音，英文詞不用');
});

test('貼上：詞＋Tab＋讀音是一個詞，讀音不會被當成另一行', () => {
  const p = T.parse('陳怡君\tㄔㄣˊ ㄧˊ ㄐㄩㄣ\n曾國城\tㄗㄥ ㄍㄨㄛˊ ㄔㄥˊ');
  assert.deepStrictEqual(p.words.map((w) => [w.word, w.readings]), [['陳怡君', ['ㄔㄣˊ ㄧˊ ㄐㄩㄣ']], ['曾國城', ['ㄗㄥ ㄍㄨㄛˊ ㄔㄥˊ']]]);
});

test('貼上：從試算表複製的一列（Tab 分隔的幾個詞）仍是幾個詞', () => {
  const p = T.parse('陳怡君\t王小明\t林志玲');
  assert.deepStrictEqual(p.words.map((w) => [w.word, w.readings]), [['陳怡君', []], ['王小明', []], ['林志玲', []]]);
});

test('貼上：詞和讀音以空白分隔、音節以 - 分隔、「／」分隔多個讀音都認得', () => {
  const p = T.parse('陳怡君 ㄔㄣˊ ㄧˊ ㄐㄩㄣ\n林志玲　ㄌㄧㄣˊ-ㄓˋ-ㄌㄧㄥˊ\n銀行\tㄧㄣˊ ㄏㄤˊ／ㄧㄣˊ ㄒㄧㄥˊ\n銀行\tㄧㄣˊ ㄏㄤˊ');
  assert.deepStrictEqual(p.words.map((w) => [w.word, w.readings]), [
    ['陳怡君', ['ㄔㄣˊ ㄧˊ ㄐㄩㄣ']], ['林志玲', ['ㄌㄧㄣˊ ㄓˋ ㄌㄧㄥˊ']], ['銀行', ['ㄧㄣˊ ㄏㄤˊ', 'ㄧㄣˊ ㄒㄧㄥˊ']]]);
});

test('BOM、Windows 換行、說明行與空行都略過', () => {
  const p = T.parse('\ufeff# 說明\r\n\r\n[我加的詞]\r\n陳怡君\tㄔㄣˊ ㄧˊ ㄐㄩㄣ\r\n[我的片語]\r\n# 說明\r\n台北市\r\n');
  assert.deepStrictEqual(p.words.map((w) => w.word), ['陳怡君']);
  assert.deepStrictEqual(p.phrases.map((x) => x.text), ['台北市']);
});

test('有空白的英文是句子，不是詞：標出原因', () => {
  const plan = T.plan(T.parse('Hello world\n陳怡君'), { words: [], phrases: [] });
  assert.match(plan.rows[0].problem, /我的片語/);
  assert.strictEqual(plan.rows[1].problem, null);
});

test('讀音和字數對不上、英文詞帶讀音、超過四個讀音：標出原因，不送出', () => {
  const plan = T.plan(T.parse('陳怡君\tㄔㄣˊ ㄧˊ\nls\tㄌ\n行\tㄒㄧㄥˊ／ㄏㄤˊ／ㄏㄥˊ／ㄏㄤˋ／ㄒㄧㄥˋ'), { words: [], phrases: [] });
  assert.match(plan.rows[0].problem, /3 個字，讀音「ㄔㄣˊ ㄧˊ」卻有 2 個音節/);
  assert.match(plan.rows[1].problem, /英文詞不需要讀音/);
  assert.match(plan.rows[2].problem, /最多 4 個讀音/);
});

test('合併：已經有的詞只多加檔案裡新的讀音，不刪除這邊有、檔案沒有的詞', () => {
  const current = { words: [{ word: '銀行', readings: ['ㄧㄣˊ ㄏㄤˊ'] }, { word: '只在這台', readings: ['ㄓˇ ㄗㄞˋ ㄓㄜˋ ㄊㄞˊ'] }], phrases: ['這台的片語'], count: 2 };
  const plan = T.plan(T.parse('銀行\tㄧㄣˊ ㄏㄤˊ\n銀行\tㄧㄣˊ ㄒㄧㄥˊ\n陳怡君\n[我的片語]\n這台的片語\n新片語\n新片語'), current);
  assert.deepStrictEqual(plan.rows.map((r) => [r.word, r.readings, r.more]), [['銀行', ['ㄧㄣˊ ㄒㄧㄥˊ'], true], ['陳怡君', [], false]]);
  assert.deepStrictEqual(plan.phrases.map((x) => x.text), ['新片語'], '已經有的片語與檔案裡重複的片語只加一次');
  assert.strictEqual(plan.samePhrases, 2);
  assert.strictEqual(plan.room, 9998);
  assert.strictEqual(plan.phraseRoom, 199);
});

test('沒帶讀音的詞已經有了就略過，不替它多查一個讀音', () => {
  const plan = T.plan(T.parse('曾國城\n林志玲'), { words: [{ word: '曾國城', readings: ['ㄗㄥ ㄍㄨㄛˊ ㄔㄥˊ'] }], phrases: [] });
  assert.deepStrictEqual(plan.rows.map((r) => r.word), ['林志玲']);
  assert.strictEqual(plan.same, 1);
});

test('已經有四個讀音的詞再加讀音：標出原因', () => {
  const four = ['ㄒㄧㄥˊ', 'ㄏㄤˊ', 'ㄏㄥˊ', 'ㄏㄤˋ'];
  const plan = T.plan(T.parse('行\tㄒㄧㄥˋ'), { words: [{ word: '行', readings: four }], phrases: [] });
  assert.match(plan.rows[0].problem, /超過 4 個/);
});

test('上限：還能加幾個詞、幾則片語', () => {
  const plan = T.plan(T.parse('陳怡君'), { words: [], phrases: Array.from({ length: 195 }, (_, i) => 'p' + i), count: 9990, limit: 10000, phraseLimit: 200 });
  assert.strictEqual(plan.room, 10);
  assert.strictEqual(plan.phraseRoom, 5);
});

test('一萬個詞的檔案：讀得完、合併成一萬筆', () => {
  const words = Array.from({ length: 10000 }, (_, i) => ({ word: 'w' + i, readings: [] }));
  const { parsed } = roundTrip(words, []);
  assert.strictEqual(parsed.words.length, 10000);
  const plan = T.plan(parsed, { words: [], phrases: [], count: 0 });
  assert.strictEqual(plan.rows.length, 10000);
});

test('分批：一批最多 500 筆，而且不超過大小上限', () => {
  const items = Array.from({ length: 1234 }, (_, i) => ({ word: 'w' + i }));
  assert.deepStrictEqual(T.chunks(items, 500).map((c) => c.length), [500, 500, 234]);
  const big = Array.from({ length: 200 }, () => '字'.repeat(1000)); // 200 則 1,000 字的片語約 600 KB
  const parts = T.chunks(big, 500);
  assert.ok(parts.length >= 2);
  assert.strictEqual(parts.flat().length, 200);
  for (const part of parts) assert.ok(new TextEncoder().encode(JSON.stringify(part)).length <= 512 * 1024 + 4096);
});
