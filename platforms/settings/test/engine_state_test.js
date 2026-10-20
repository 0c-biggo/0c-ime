// 總覽「輸入法仍未全部切換」的判斷（ui/engine-state.js）：node --test platforms/settings/test/
// 由 scripts/test_release.py 在有 node 時執行。狀態依 0c-update status -json 的實際欄位：
// installed 是完整安裝的收據版本，engine 是核心更新的引擎指標（沒做過核心更新是空字串），
// runtime.version 是執行中引擎回報的版本。
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

globalThis.window = globalThis;
require('../ui/engine-state.js');
const state = window.ZcEngineState;

const PLATFORMS = ['linux/x86_64', 'windows/x86_64', 'macos/aarch64'];
const idle = { stage: 'idle', state: 'up_to_date' };
function status({ installed, engine = '', running, update = idle }) {
  const runtime = running ? { state: 'running', version: running, selection_state: 'ready' } : { state: 'not_running' };
  return { schema: 1, installed, engine, runtime, update, mode: 'auto' };
}

test('0.2.0 實際的 status.json（沒做過核心更新，engine 為空字串）不提示', () => {
  for (const platform of PLATFORMS) {
    const got = state(status({ installed: '0.2.0', running: '0.2.0' }), platform);
    assert.deepStrictEqual([got.runningOld, got.shellPending, got.relogin], [false, false, false], platform);
    assert.strictEqual(got.expected, '0.2.0');
  }
});

test('核心更新生效後（外殼收據仍是舊版）不誤報、不叫人登出', () => {
  for (const platform of PLATFORMS) {
    const got = state(status({ installed: '0.2.0', engine: '0.2.1', running: '0.2.1' }), platform);
    assert.deepStrictEqual([got.runningOld, got.shellPending, got.relogin], [false, false, false], platform);
    assert.strictEqual(got.expected, '0.2.1');
  }
});

test('核心已切換、舊引擎還在組字：提示等引擎重新啟動，任何平台都不用登出', () => {
  const update = { stage: 'pending', state: 'update_available', target: { version: '0.2.1', kind: 'core' },
    blocked: { reason: 'engine_busy', detail: '仍在組字' } };
  for (const platform of PLATFORMS) {
    const got = state(status({ installed: '0.2.0', engine: '0.2.1', running: '0.2.0', update }), platform);
    assert.deepStrictEqual([got.runningOld, got.shellPending, got.relogin], [true, false, false], platform);
    assert.deepStrictEqual([got.expected, got.running], ['0.2.1', '0.2.0']);
  }
});

test('外殼更新等重新登入：提示登出再登入', () => {
  const update = { stage: 'pending', state: 'update_available', target: { version: '0.2.1', kind: 'shell' },
    blocked: { reason: 'needs_relogin', detail: '已安裝，重新登入後生效' } };
  for (const platform of ['windows/x86_64', 'macos/aarch64']) {
    let got = state(status({ installed: '0.2.1', running: '0.2.0', update }), platform);
    assert.deepStrictEqual([got.runningOld, got.shellPending, got.relogin], [true, true, true], platform);
    assert.strictEqual(got.pendingDetail, '已安裝，重新登入後生效');
    // Windows 等重新登入時常駐引擎先交接成新版：引擎版本對了，外殼仍要重新登入。
    got = state(status({ installed: '0.2.1', running: '0.2.1', update }), platform);
    assert.deepStrictEqual([got.runningOld, got.shellPending, got.relogin], [false, true, true], platform);
  }
});

test('macOS 舊的輸入法程式等使用者離開時才換：不提示登出（#170、#178）', () => {
  for (const detail of ['已安裝，離開電腦（螢幕鎖定或關閉）時自動換成新版，不必登出', '已安裝，停止打字後自動換成新版，不必登出']) {
    const update = { stage: 'pending', state: 'update_available', target: { version: '0.2.1', kind: 'shell' },
      blocked: { reason: 'waiting_idle', detail } };
    const got = state(status({ installed: '0.2.1', running: '0.2.1', update }), 'macos/aarch64');
    assert.deepStrictEqual([got.shellPending, got.retiring, got.relogin], [true, true, false], detail);
    assert.strictEqual(got.pendingDetail, detail);
  }
});

test('macOS 延後到登入才安裝：保留待辦原因，提示登出再登入', () => {
  const update = { stage: 'pending', state: 'update_available', target: { version: '0.2.1', kind: 'shell' },
    blocked: { reason: 'needs_relogin', detail: '登出再登入時自動完成安裝' } };
  const got = state(status({ installed: '0.2.0', running: '0.2.0', update }), 'macos/aarch64');
  assert.deepStrictEqual([got.runningOld, got.shellPending, got.relogin], [false, true, true]);
  assert.strictEqual(got.pendingDetail, '登出再登入時自動完成安裝');
});

test('完整安裝後還在跑舊引擎：Linux 等引擎交接，Windows／macOS 要重新登入', () => {
  const s = status({ installed: '0.2.1', running: '0.2.0' });
  assert.deepStrictEqual([state(s, 'linux/x86_64').runningOld, state(s, 'linux/x86_64').relogin], [true, false]);
  for (const platform of ['windows/x86_64', 'macos/aarch64']) {
    assert.deepStrictEqual([state(s, platform).runningOld, state(s, platform).relogin], [true, true], platform);
  }
});

test('全部已切換、引擎沒在執行、舊版 0c-update 沒有 engine 欄位：都不提示', () => {
  for (const platform of PLATFORMS) {
    assert.strictEqual(state(status({ installed: '0.2.1', running: '0.2.1' }), platform).runningOld, false);
    assert.strictEqual(state(status({ installed: '0.2.1', engine: '0.2.1', running: '0.2.1' }), platform).runningOld, false);
    assert.strictEqual(state(status({ installed: '0.2.0', engine: '0.2.1' }), platform).runningOld, false);
    const old = status({ installed: '0.2.0', running: '0.2.0' });
    delete old.engine;
    assert.strictEqual(state(old, platform).runningOld, false);
    assert.strictEqual(state(old, platform).expected, '0.2.0');
  }
});

test('畫面使用這份判斷，不再拿收據版本直接比', () => {
  const ui = path.join(__dirname, '..', 'ui');
  const app = fs.readFileSync(path.join(ui, 'app.js'), 'utf8');
  const html = fs.readFileSync(path.join(ui, 'index.html'), 'utf8');
  assert.match(app, /window\.ZcEngineState\(/);
  assert.doesNotMatch(app, /runtime\.version !== status\.installed/);
  assert.ok(html.indexOf('engine-state.js') > 0 && html.indexOf('engine-state.js') < html.indexOf('app.js'), 'index.html 要先載入 engine-state.js');
});
