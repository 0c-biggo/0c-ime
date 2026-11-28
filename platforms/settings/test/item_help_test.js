// 設定項目的選項提示與「說明」連結（#362，ui/item-help.js）：node --test platforms/settings/test/
// 由 scripts/test_release.py 在有 node 時執行。
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

globalThis.window = globalThis;
require('../ui/input-styles.js');
require('../ui/item-help.js');
const H = window.ZcItemHelp;
const state = window.ZcInputStylesLogic.controlState;

const layout = (over) => Object.assign({
  key: 'keyboard-layout', label: '鍵盤排列', values: ['standard', 'eten', 'hsu', 'pinyin'], value: 'standard',
  value_labels: { standard: '標準', eten: '倚天 41 鍵', hsu: '許氏', pinyin: '漢語拼音' },
  value_hints: { standard: '大千式，最常見的注音鍵盤。', eten: '倚天的提示。', hsu: '許氏的提示。', pinyin: '拼音的提示。' },
  help_url: 'https://0c.biggo.com/guide/settings/#keyboard-layout',
}, over);

test('只給目前選中那個選項的提示，換選項就換', () => {
  for (const value of ['standard', 'eten', 'hsu', 'pinyin']) {
    const item = layout({ value });
    const hint = H.hint(item, state(item, false).value);
    assert.strictEqual(hint.text, item.value_hints[value]);
    assert.strictEqual(hint.label, item.value_labels[value]);
  }
});

test('選中的選項沒有提示就不顯示', () => {
  const item = { key: 'shift-symbol-halfwidth', values: ['off', 'on', 'all'], value: 'off', value_hints: { on: 'a。', all: 'b。' } };
  assert.strictEqual(H.hint(item, 'off'), null);
  assert.strictEqual(H.hint(item, 'on').text, 'a。');
  // 不是自己的欄位（原型上的名字）、空字串、不是字串都不算提示。
  assert.strictEqual(H.hint({ value_hints: {} }, 'toString'), null);
  assert.strictEqual(H.hint({ value_hints: { on: '' } }, 'on'), null);
  assert.strictEqual(H.hint({ value_hints: { on: 1 } }, 'on'), null);
});

test('舊版 0c-update 沒有 value_hints、help_url：不顯示提示也不顯示連結', () => {
  const old = { key: 'keyboard-layout', values: ['standard', 'eten'], value: 'eten', description: '很長的舊說明' };
  assert.strictEqual(H.hint(old, 'eten'), null);
  assert.strictEqual(H.url(old), null);
  assert.strictEqual(H.hint(null, 'eten'), null);
  assert.strictEqual(H.hint({ value_hints: 'x' }, 'eten'), null);
});

test('沒有 value_labels 時用畫面自己的對照表，再沒有就不加名稱', () => {
  const item = { values: ['neural', 'statistical'], value: 'neural', value_hints: { neural: '提示。' } };
  assert.strictEqual(H.hint(item, 'neural', { neural: '神經選字' }).label, '神經選字');
  assert.strictEqual(H.hint(item, 'neural').label, '');
});

test('停用時照實際使用的值給提示（effective），不是設定檔裡的值', () => {
  const item = { values: ['zhuyin', 'english'], value: 'english', effective: 'zhuyin', disabled: true, value_hints: { english: 'e。', zhuyin: 'z。' } };
  assert.strictEqual(H.hint(item, state(item, false).value).text, 'z。');
});

test('說明連結只認官網的說明文件，錨點是設定的鍵名', () => {
  for (const ok of ['https://0c.biggo.com/guide/settings/#keyboard-layout', 'https://0c.biggo.com/guide/settings/#fuzzy-z-zh', 'https://0c.biggo.com/guide/settings/']) {
    assert.strictEqual(H.url({ help_url: ok }), ok);
  }
  for (const bad of [
    'http://0c.biggo.com/guide/settings/#keyboard-layout', 'https://0c.biggo.com.evil.example/guide/settings/#x',
    'https://0c.biggo.com/download/x.exe', 'https://0c.biggo.com/guide/../x', 'https://0c.biggo.com/guide//evil.example',
    'https://0c.biggo.com/guide/settings/?a=1', 'https://0c.biggo.com/guide/settings/#', 'https://0c.biggo.com/guide/settings/#A',
    'https://0c.biggo.com/guide/settings/#a#b', 'https://evil.example/#keyboard-layout', 'javascript:alert(1)', '', null, 1,
  ]) {
    assert.strictEqual(H.url({ help_url: bad }), null, String(bad));
  }
});

test('頁首的說明連結只有輸入習慣、更新、我的詞彙三頁有', () => {
  assert.strictEqual(H.pageUrl('styles'), 'https://0c.biggo.com/guide/settings/#input-styles');
  assert.strictEqual(H.pageUrl('update'), 'https://0c.biggo.com/guide/settings/#update-mode');
  assert.strictEqual(H.pageUrl('vocab'), 'https://0c.biggo.com/guide/settings/#vocab');
  for (const page of ['overview', 'mode', 'candidates', 'keyboard', 'output', 'models', 'troubleshoot', 'about', 'toString', '', null, undefined]) {
    assert.strictEqual(H.pageUrl(page), null, String(page));
  }
});

test('三頁的頁首接上連結，F1 開目前這一頁的說明', () => {
  const ui = path.join(__dirname, '..', 'ui');
  const app = fs.readFileSync(path.join(ui, 'app.js'), 'utf8');
  assert.match(app, /pageHelpLink\('update', '更新'\)/);
  assert.match(app, /helpLink: \(\) => pageHelpLink\('vocab', '我的詞彙'\)/);
  assert.match(app, /helpLink: \(\) => pageHelpLink\('styles', '輸入習慣'\)/);
  assert.match(app, /event\.key !== 'F1' \|\| event\.defaultPrevented/);
  assert.match(app, /window\.ZcItemHelp\.pageUrl\(page\)/);
  assert.match(fs.readFileSync(path.join(ui, 'input-styles.js'), 'utf8'), /helpLink \? helpLink\(\) : null/);
  assert.match(fs.readFileSync(path.join(ui, 'vocab.js'), 'utf8'), /ctx\.helpLink \? ctx\.helpLink\(\) : null/);
});

test('畫面載入 item-help.js，連結不在 Tab 順序裡、可以按 F1', () => {
  const ui = path.join(__dirname, '..', 'ui');
  const html = fs.readFileSync(path.join(ui, 'index.html'), 'utf8');
  assert.ok(html.indexOf('item-help.js') > 0 && html.indexOf('item-help.js') < html.indexOf('app.js'));
  const app = fs.readFileSync(path.join(ui, 'app.js'), 'utf8');
  assert.match(app, /class: 'link help', tabindex: '-1'/);
  assert.match(app, /event\.key === 'F1'/);
  assert.match(app, /invoke\('open_url', \{ url \}\)/);
});
