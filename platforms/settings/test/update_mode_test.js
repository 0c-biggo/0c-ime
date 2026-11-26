// 更新方式的名稱與說明（#244、#340，ui/update-mode.js）：node --test platforms/settings/test/
// 由 scripts/test_release.py 在有 node 時執行。
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

globalThis.window = globalThis;
require('../ui/update-mode.js');
const mode = window.ZcUpdateMode;

const PLATFORMS = ['linux/x86_64', 'windows/x86_64', 'macos/aarch64'];

test('三種方式的名稱在各平台相同，依序是下拉選單的三個選項', () => {
  for (const platform of PLATFORMS) {
    assert.strictEqual(mode('auto', platform).label, '自動更新（建議）');
    assert.strictEqual(mode('ask', platform).label, '下載後詢問');
    assert.strictEqual(mode('manual', platform).label, '手動更新');
    assert.deepStrictEqual(mode('ask', platform).options, [
      { value: 'auto', label: '自動更新（建議）' },
      { value: 'ask', label: '下載後詢問' },
      { value: 'manual', label: '手動更新' },
    ]);
  }
});

test('Windows 的「自動更新」照實說改輸入法註冊的更新要確認系統管理員權限；其他平台不提', () => {
  const windows = mode('auto', 'windows/x86_64').note;
  assert.match(windows, /閒置時完成/);
  assert.match(windows, /系統管理員權限/);
  assert.match(windows, /確認/);
  assert.doesNotMatch(windows, /登出再登入|重新登入/);
  for (const platform of ['linux/x86_64', 'macos/aarch64']) {
    assert.doesNotMatch(mode('auto', platform).note, /系統管理員/);
  }
});

// #340：三種方式都會通知新版本；差別在下載與安裝要不要你按。
test('下載後詢問：背景下載，通知後按立即安裝', () => {
  for (const platform of PLATFORMS) {
    const ask = mode('ask', platform);
    assert.match(ask.note, /通知你/);
    assert.match(ask.note, /立即安裝/);
    assert.strictEqual(ask.scheduled, true);
    assert.strictEqual(ask.background(), true);
  }
});

test('手動更新：照排程檢查並通知，不自己下載；按立即下載、再按立即安裝', () => {
  for (const platform of PLATFORMS) {
    const manual = mode('manual', platform);
    assert.match(manual.note, /通知你/);
    assert.match(manual.note, /不會自己下載/);
    assert.match(manual.note, /立即下載.*立即安裝/);
    assert.strictEqual(manual.scheduled, true);
    assert.strictEqual(manual.background(), false);
  }
});

test('舊版 0c-update（沒有 features.update_mode）的手動：背景不檢查，照實說', () => {
  const old = mode('manual', 'windows/x86_64', false);
  assert.strictEqual(old.label, '手動更新');
  assert.strictEqual(old.scheduled, false);
  assert.strictEqual(old.background(), false);
  assert.match(old.note, /不檢查也不下載/);
  assert.strictEqual(mode('auto', 'linux/x86_64', false).scheduled, true);
});

test('自動與下載後詢問會在背景下載，暫停中不會', () => {
  for (const value of ['auto', 'ask']) {
    assert.strictEqual(mode(value, 'linux/x86_64').scheduled, true);
    assert.strictEqual(mode(value, 'linux/x86_64').background(), true);
    assert.strictEqual(mode(value, 'linux/x86_64').background('2026-11-07T00:00:00Z'), false);
  }
});

test('不認得的值（新版寫的、或沒有回報）照實顯示，不猜說明，不算選項之一', () => {
  const future = mode('future', 'windows/x86_64');
  assert.deepStrictEqual([future.label, future.note, future.known], ['future', '', false]);
  assert.deepStrictEqual([mode(undefined, 'windows/x86_64').label, mode(undefined, 'windows/x86_64').note], ['未知', '']);
  assert.strictEqual(mode('auto', 'linux/x86_64').known, true);
});

test('說明都簡短：一句話', () => {
  for (const platform of PLATFORMS) {
    for (const value of ['auto', 'ask', 'manual']) {
      const note = mode(value, platform).note;
      assert.ok(note.length <= 80, value + ' ' + platform + ' ' + note.length);
    }
  }
});

test('index.html 在 app.js 之前載入 update-mode.js', () => {
  const html = fs.readFileSync(path.join(__dirname, '../ui/index.html'), 'utf8');
  assert.ok(html.indexOf('src="update-mode.js"') > 0 && html.indexOf('src="update-mode.js"') < html.indexOf('src="app.js"'));
});
