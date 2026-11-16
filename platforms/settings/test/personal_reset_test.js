// 「我的詞彙」頁「靈犀學到的」底下的「清除學習紀錄」（#301，ui/personal-reset.js）：node --test platforms/settings/test/
// 結果依 0c-update personal reset -json 的契約（docs/settings-contract.md）。
'use strict';
const test = require('node:test');
const assert = require('node:assert');

globalThis.window = globalThis;
require('../ui/personal-reset.js');
const L = window.ZcPersonalResetLogic;

test('確認文案：說明回到剛安裝時的樣子、無法復原，自訂詞彙不受影響', () => {
  assert.match(L.TEXT.ask, /剛安裝時的樣子/);
  assert.match(L.TEXT.ask, /無法復原/);
  assert.match(L.TEXT.ask, /你加的詞與片語不受影響/);
  assert.match(L.TEXT.desc, /不受影響/);
});

test('成功：不論引擎有沒有在跑都是同一句', () => {
  for (const engine of ['running', 'stopped', 'not_running']) {
    assert.deepStrictEqual(L.resultText({ schema: 1, ok: true, engine, backups_removed: 2 }), [L.TEXT.done, 'ok']);
  }
});

test('引擎正在切換（busy）：請稍後再試，不是錯誤', () => {
  assert.deepStrictEqual(L.resultText({ schema: 1, ok: false, error: { code: 'busy', message: '輸入法引擎正在切換，請稍後再試（…）' } }), [L.TEXT.busy, 'warn']);
});

test('其他錯誤：顯示 0c-update 的說明', () => {
  const [text, tone] = L.resultText({ schema: 1, ok: false, error: { code: 'io', message: '已清除目前的學習紀錄，但寫入 user.json 失敗：磁碟已滿' } });
  assert.strictEqual(tone, 'error');
  assert.match(text, /磁碟已滿/);
  assert.deepStrictEqual(L.resultText(null), ['沒有清除完成：發生未知的錯誤。', 'error']);
});

// 不碰真的 DOM：h() 做出只有 append／querySelector 的節點，按鈕的 onclick 直接呼叫。
function fakeDom() {
  const make = (tag, attrs, ...kids) => {
    const node = { tag, attrs: attrs || {}, kids: kids.flat().filter((k) => k != null), focused: false };
    node.append = (...more) => { node.kids.push(...more.flat().filter((k) => k != null)); };
    node.focus = () => { node.focused = true; };
    node.querySelector = (sel) => {
      const key = /data-focus="([^"]+)"/.exec(sel)[1];
      const walk = (n) => {
        if (!n || typeof n !== 'object') return null;
        if (n.attrs && n.attrs['data-focus'] === key) return n;
        for (const k of n.kids || []) { const f = walk(k); if (f) return f; }
        return null;
      };
      return walk(node);
    };
    return node;
  };
  return { h: make, clear: (node) => { node.kids = []; return node; } };
}

async function press(container, invokeResult) {
  const { h, clear } = fakeDom();
  const toasts = [];
  let done = 0;
  const ui = window.ZcPersonalReset({ h, clear, toast: (text, kind) => toasts.push([text, kind]), invoke: async () => invokeResult, onDone: () => { done += 1; } });
  const root = container || h('div', {});
  ui.mount(root);
  root.querySelector('[data-focus="personal-reset"]').attrs.onclick();
  assert.ok(root.querySelector('[data-focus="personal-reset-cancel"]').focused, '確認出現時焦點在「取消」');
  await root.querySelector('[data-focus="personal-reset-confirm"]').attrs.onclick();
  return { root, toasts, done };
}

test('清除成功：提示、卡片說明換成已清除，並通知「靈犀學到的」重新讀取', async () => {
  const { root, toasts, done } = await press(null, { schema: 1, ok: true, engine: 'running' });
  assert.deepStrictEqual(toasts, [[L.TEXT.done, 'ok']]);
  assert.strictEqual(done, 1);
  assert.ok(root.querySelector('[data-focus="personal-reset"]').focused, '完成後焦點回到「清除…」');
  assert.strictEqual(root.querySelector('[data-focus="personal-reset-confirm"]'), null, '確認收起來');
});

test('沒有清除完成（busy、錯誤）：不通知重新讀取', async () => {
  for (const result of [{ ok: false, error: { code: 'busy' } }, { ok: false, error: { code: 'io', message: 'x' } }]) {
    const { done } = await press(null, result);
    assert.strictEqual(done, 0);
  }
});
