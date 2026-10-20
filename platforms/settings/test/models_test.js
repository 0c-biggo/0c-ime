// 「模型」頁（#180，ui/models.js）的判斷：node --test platforms/settings/test/
// 狀態依 0c-update models status -json（schema 1）的欄位。
'use strict';
const test = require('node:test');
const assert = require('node:assert');

globalThis.window = globalThis;
require('../ui/models.js');
const L = window.ZcModelsLogic;

const LOCK = [
  { role: 'select', label: '選字', state: 'not_installed', version: 'd512-pf512-20260926', path: '', sha256: '', size: 130723134,
    repository: 'Funmula/0c-sequence-d512', revision: 'df4b9f04aa11', url: 'https://huggingface.co/Funmula/0c-sequence-d512/tree/df4b9f04aa11', license: 'MIT' },
  { role: 'punct', label: '補標點', state: 'not_installed', version: 'punct-d512', path: '', sha256: '', size: 131276866,
    repository: 'Funmula/0c-punct-d512', revision: '4db3ed40bb22', url: 'https://huggingface.co/Funmula/0c-punct-d512/tree/4db3ed40bb22', license: 'MIT' },
];
function status(over) {
  return Object.assign({ schema: 1, source: 'none', can_download: true, disabled_reason: '', runtime: { ok: true, python: '/usr/bin/python3', missing: [] },
    need: 'download', download_size: 262000000, models: LOCK, operation: null, last_error: null }, over);
}
const bundled = status({ source: 'bundled', can_download: false, disabled_reason: 'bundled', need: 'none',
  models: [{ role: 'select', label: '選字', state: 'ready', version: 'v', path: '/x/neural/select', sha256: 'c'.repeat(64), size: 1 }] });

test('內建模型：只顯示資訊，沒有任何按鈕', () => {
  assert.deepStrictEqual(L.summary(bundled), { text: '隨安裝包內附。', tone: 'ok' });
  assert.deepStrictEqual(L.actions(bundled), { download: false, redownload: false, cancel: false, remove: false });
  assert.strictEqual(L.runtimeMissing(bundled), null);
});

test('未安裝、可下載：只有「下載模型」，下載前寫明大小、來源與授權', () => {
  const s = status();
  assert.strictEqual(L.summary(s).text, '未安裝，目前使用統計選字。');
  assert.deepStrictEqual(L.actions(s), { download: true, redownload: false, cancel: false, remove: false });
  assert.strictEqual(L.downloadNote(s), '約 262 MB，從 Hugging Face 下載：Funmula/0c-sequence-d512（df4b9f04）、Funmula/0c-punct-d512（4db3ed40），授權 MIT。');
});

test('下載中：顯示百分比，只能取消', () => {
  const s = status({ operation: { phase: 'downloading', done_bytes: 131000000, total_bytes: 262000000, started_at: '2026-10-21T00:00:00Z' } });
  assert.strictEqual(L.summary(s).text, '正在下載 50%');
  assert.deepStrictEqual(L.actions(s), { download: false, redownload: false, cancel: true, remove: false });
  assert.strictEqual(L.mb(131000000), '131 MB');
  assert.strictEqual(L.operationText({ phase: 'verifying' }), '核對中…');
  assert.strictEqual(L.operationText({ phase: 'pairing' }), '配對中…');
  assert.strictEqual(L.percent({ phase: 'downloading', done_bytes: 1, total_bytes: 0 }), null);
});

test('已安裝（個人目錄）：可以刪除，不能下載', () => {
  const s = status({ source: 'user', need: 'none' });
  assert.deepStrictEqual(L.summary(s), { text: '已安裝。', tone: 'ok' });
  assert.deepStrictEqual(L.actions(s), { download: false, redownload: false, cancel: false, remove: true });
});

test('上游模型換了：提示重新下載；字典換了只重新配對，不給按鈕', () => {
  const re = status({ source: 'user', need: 'redownload' });
  assert.match(L.summary(re).text, /需要重新下載/);
  assert.deepStrictEqual(L.actions(re), { download: true, redownload: true, cancel: false, remove: true });
  const repair = status({ source: 'user', need: 'repair' });
  assert.strictEqual(L.summary(repair).text, '字典已更新，正在重新配對，不必重新下載。');
  assert.strictEqual(L.actions(repair).download, false);
});

test('缺推論元件：列出 apt 套件，奇怪的名稱不會進到指令裡', () => {
  const s = status({ runtime: { ok: false, python: '/usr/bin/python3', missing: ['python3-numpy', 'python3-onnxruntime', '; rm -rf /'] } });
  assert.deepStrictEqual(L.runtimeMissing(s), ['python3-numpy', 'python3-onnxruntime']);
  assert.strictEqual(L.runtimeMissing(status()), null);
  // 發行版關掉下載又沒有模型：不提示裝推論元件（裝了也沒有模型可用）。
  assert.strictEqual(L.runtimeMissing(status({ can_download: false, disabled_reason: 'distribution', runtime: { ok: false, missing: ['python3-numpy'] } })), null);
});

test('不能下載又沒有模型：說明原因，不給按鈕', () => {
  const dist = status({ can_download: false, disabled_reason: 'distribution' });
  assert.match(L.summary(dist).text, /發行版/);
  assert.deepStrictEqual(L.actions(dist), { download: false, redownload: false, cancel: false, remove: false });
  assert.match(L.summary(status({ can_download: false, disabled_reason: 'platform' })).text, /平台/);
});

test('字典不在配對清單上（發行版自己重建字典）：說明不能用下載的模型，不給下載', () => {
  const none = status({ can_download: false, disabled_reason: 'dictionary_mismatch' });
  assert.deepStrictEqual(L.summary(none), { text: '目前安裝的字典沒有對應的模型配對清單，不能使用下載的模型。', tone: 'warn' });
  assert.deepStrictEqual(L.actions(none), { download: false, redownload: false, cancel: false, remove: false });
  // 之前下載的權重還在：同樣說明，不說「已安裝」；仍可刪除。
  const user = status({ source: 'user', need: 'none', can_download: false, disabled_reason: 'dictionary_mismatch' });
  assert.match(L.summary(user).text, /配對清單/);
  assert.deepStrictEqual(L.actions(user), { download: false, redownload: false, cancel: false, remove: true });
});

test('SHA-256 截斷顯示，網址只放行 Hugging Face', () => {
  const sha = '0123456789abcdef'.repeat(4);
  assert.strictEqual(L.shortSha(sha), '0123456789ab…89abcdef');
  assert.ok(L.safeUrl(LOCK[0].url));
  for (const bad of ['http://huggingface.co/x', 'https://huggingface.co.evil.example/x', 'https://huggingface.co/x?a=1', 'javascript:alert(1)', 'https://huggingface.co/../x', null]) {
    assert.ok(!L.safeUrl(bad), String(bad));
  }
});

test('引擎狀態沿用總覽的文字；引擎沒在跑時說明', () => {
  const label = (v, fallback) => v + '/' + fallback;
  assert.strictEqual(L.stateText({ role: 'select', state: 'ready' }, label), 'ready/統計引擎');
  assert.strictEqual(L.stateText({ role: 'punct', state: 'not_installed' }, label), 'not_installed/原文提交');
  assert.strictEqual(L.stateText({ role: 'select', state: '' }, label), '輸入法未執行');
});

test('0.2.2 以前內建的模型（equivalent）：照樣顯示 Hugging Face 來源，另加一行小字', () => {
  const old = Object.assign({}, LOCK[0], { state: 'ready', source: 'bundled', path: '/x/neural/select', sha256: 'c'.repeat(64), equivalent: true });
  assert.strictEqual(L.equivalentNote(old), '權重與公開版相同，僅匯出資訊不同。');
  assert.strictEqual(L.equivalentNote(Object.assign({}, old, { equivalent: false })), null);
  assert.strictEqual(L.equivalentNote(Object.assign({}, old, { url: '' })), null); // 沒有上游資訊時不說
  assert.strictEqual(L.equivalentNote(LOCK[0]), null);
});
