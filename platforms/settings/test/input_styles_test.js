'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
globalThis.window = globalThis;
globalThis.document = { querySelector: () => null };
require('../ui/input-styles.js');
const logic = window.ZcInputStylesLogic;

test('側欄提供輸入習慣入口，頁面與腳本一併載入', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const html = fs.readFileSync(path.join(__dirname, '../ui/index.html'), 'utf8');
  assert.match(html, /<button[^>]+class="nav"[^>]+data-page="styles"[^>]*>輸入習慣<\/button>/);
  assert.match(html, /<section[^>]+id="page-styles"/);
  assert.ok(html.indexOf('src="input-styles.js"') < html.indexOf('src="app.js"'));
});

test('固定風格的開關仍顯示實際開啟；無效條件與唯讀各自保留語意', () => {
  const item = { key: 'mixed-input', values: ['off', 'on'], value: 'on' };
  assert.deepEqual(logic.controlState({ ...item, locked: true }), { value: 'on', disabled: true, toggle: true, checked: true });
  assert.equal(logic.controlState({ ...item, disabled: true }).checked, false);
  assert.equal(logic.controlState({ ...item, disabled: true, effective: 'on' }).checked, true);
  assert.equal(logic.controlState({ ...item, locked: true, effective: 'off' }).checked, false);
  assert.deepEqual(logic.controlState({ values: ['chinese', 'english'], value: 'english', effective: 'chinese', locked: true }),
    { value: 'chinese', disabled: true, toggle: false, checked: false });
});

test('中文與 Emoji 名稱依 Unicode 字數限制，不接受空白與控制字元', () => {
  assert.equal(logic.validName('  我的習慣  '), true);
  assert.equal(logic.validName('😀'.repeat(80)), true);
  assert.equal(logic.validName('字'.repeat(81)), false);
  assert.equal(logic.validName('  '), false);
  assert.equal(logic.validName('我的\t習慣'), false);
});

test('差異摘要的開關值顯示開啟／關閉，不暴露 equals 等儲存值', () => {
  assert.equal(logic.valueLabel({ values: ['off', 'equals'] }, 'equals'), '開啟');
  assert.equal(logic.valueLabel({ values: ['off', 'equals'] }, 'off'), '關閉');
  assert.equal(logic.valueLabel({ values: ['off', 'on'], value_labels: { on: '套用自訂標籤' } }, 'on'), '套用自訂標籤');
});

test('差異摘要只比較風格實際管理的設定，不混入鍵盤排列與模型', () => {
  const items = [{ key: 'toggle-key', value: 'shift' }, { key: 'esc-clear', value: 'off' },
    { key: 'keyboard-layout', value: 'hsu' }, { key: 'selection', value: 'neural' }];
  assert.deepEqual(logic.differences({ values: { 'toggle-key': 'shift', 'esc-clear': 'on' } }, items),
    [{ item: items[1], before: 'off', after: 'on' }]);
});

// 小型 DOM 介面：直接操作真正頁面產生的控制項與事件，檢查「選取→預覽→確認→橋接」流程。
function node(tag, attrs = {}, ...children) {
  return { tag, attrs, children: children.flat().filter((x) => x != null && x !== false),
    append(...kids) { this.children.push(...kids); },
    get disabled() { return !!this.attrs.disabled; }, set disabled(value) { this.attrs.disabled = value; } };
}
function walk(tree) { return [tree, ...(tree.children || []).flatMap((x) => typeof x === 'object' ? walk(x) : [])]; }
function text(tree) { return typeof tree === 'string' ? tree : (tree.children || []).map(text).join(''); }
function harness(overrides = {}) {
  const profiles = { active: 'custom-original', running: 'custom-original', pending: false,
    items: [
      { id: 'zc', name: '0c 預設', builtin: true, available: true, summary: ['保留靈犀注音操作'], values: { 'esc-clear': 'off' } },
      { id: 'ms', name: '微軟新注音習慣', builtin: true, available: true, summary: ['Shift＋空白切換全半形'], values: { 'esc-clear': 'on' } },
      { id: 'custom-original', name: '我的原有設定', builtin: false, available: true, values: { 'esc-clear': 'off' } },
      { id: 'custom-other', name: '其他自訂', builtin: false, available: true, values: { 'esc-clear': 'on' } },
    ], ...overrides.profiles };
  const settings = { file: { read_only: false }, profiles, items: [{ key: 'esc-clear', label: 'Esc 清除', value: 'off' }], ...overrides.settings };
  const calls = [];
  let root;
  const render = () => { root = node('section'); view.render(root); };
  const view = window.ZcInputStyles({ h: node, settings: () => settings, render, valueLabel: (_, v) => v, showKeyboard: () => {},
    mutate: overrides.mutate || (async (action, id, name) => {
      calls.push({ action, id, name });
      if (action === 'select') profiles.active = id;
      return { ok: true, profiles };
    }) });
  render();
  return { calls, profiles, settings, render,
    root: () => root,
    find: (focus) => walk(root).find((n) => n.attrs['data-focus'] === focus),
    choose(id) { this.find('style-choice').attrs.onchange({ target: { value: id } }); },
    click(focus) { const n = this.find(focus); assert.ok(n, focus); assert.equal(n.disabled, false, focus + '可操作'); return n.attrs.onclick(); },
    name(value) { this.find('style-name').attrs.oninput({ target: { value } }); } };
}

test('挑選內建風格只預覽，明確套用才送出一次動作', async () => {
  const ui = harness();
  ui.choose('ms');
  assert.deepEqual(ui.calls, []);
  assert.match(text(ui.root()), /off → on/);
  assert.match(text(ui.root()), /Shift＋空白/);
  await ui.click('style-apply');
  assert.deepEqual(ui.calls, [{ action: 'select', id: 'ms', name: null }]);
  assert.equal(ui.find('style-apply').disabled, true);
});

test('副本名稱保留中文、可修改且空白名稱無法送出；取消不變更', async () => {
  const ui = harness();
  ui.choose('ms');
  ui.click('style-copy');
  ui.name('  ');
  assert.equal(ui.find('style-save').disabled, true);
  ui.name('  我的微軟習慣  ');
  await ui.click('style-save');
  assert.deepEqual(ui.calls, [{ action: 'copy', id: 'ms', name: '我的微軟習慣' }]);
  ui.click('style-copy');
  ui.click('style-cancel');
  assert.equal(ui.find('style-name'), undefined);
  assert.equal(ui.calls.length, 1);
});

test('自訂可改名，使用中的自訂不能刪除，其他自訂需要再次確認', async () => {
  const ui = harness();
  assert.equal(ui.find('style-delete').disabled, true);
  ui.click('style-rename'); ui.name('我的設定'); await ui.click('style-save');
  assert.deepEqual(ui.calls[0], { action: 'rename', id: 'custom-original', name: '我的設定' });
  ui.choose('custom-other'); ui.click('style-rename'); ui.name('其他設定'); await ui.click('style-save');
  assert.deepEqual(ui.calls[1], { action: 'rename', id: 'custom-other', name: '其他設定' });
  assert.equal(ui.find('style-delete').disabled, false, '改名後仍停在選中的自訂');
  ui.click('style-delete');
  assert.equal(ui.calls.length, 2);
  await ui.click('style-confirm-delete');
  assert.deepEqual(ui.calls[2], { action: 'delete', id: 'custom-other', name: null });
});

test('組字延後、不可用與唯讀原因可見，不能套用不可用風格', () => {
  const ui = harness({ profiles: { active: 'ms', running: 'custom-original', pending: true } });
  assert.match(text(ui.root()), /完成目前輸入後套用；目前仍使用「我的原有設定」/);
  ui.profiles.items[0].available = false; ui.profiles.items[0].reason = '此排列不支援'; ui.choose('zc');
  assert.match(text(ui.root()), /此排列不支援/);
  assert.equal(ui.find('style-apply').disabled, true);
  assert.equal(ui.find('style-copy').disabled, true);
  ui.settings.file.read_only = true; ui.choose('custom-other');
  assert.equal(ui.find('style-apply').disabled, true);
  assert.equal(ui.find('style-rename').disabled, true);
  assert.equal(ui.find('style-delete').disabled, true);
});

test('儲存期間不可重複提交，失敗保留草稿與可讀錯誤', async () => {
  let finish;
  let count = 0;
  const ui = harness({ mutate: () => { count++; return new Promise((resolve) => { finish = resolve; }); } });
  ui.choose('ms'); ui.click('style-copy'); ui.name('保留這個名稱');
  const save = ui.find('style-save');
  const promise = save.attrs.onclick();
  assert.equal(ui.find('style-save').disabled, true);
  save.attrs.onclick();
  assert.equal(count, 1);
  finish({ ok: false, error: { message: '設定已由其他程式修改，請重試。' } });
  await promise;
  assert.equal(ui.find('style-name').attrs.value, '保留這個名稱');
  assert.match(text(ui.root()), /設定已由其他程式修改/);
});
