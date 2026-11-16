// 「我的詞彙」頁「靈犀學到的」清單底下的「清除學習紀錄」（#301）。不是開關：按了先確認，確認後呼叫 0c-update personal reset -json
// （Rust 的 personal_command）；引擎在跑時由它清空並存檔，沒在跑時直接刪檔，升級前保存的個人詞庫備份一起刪。
// 自訂詞彙不動。只在 settings -json 的 features.personal_reset 為 true（0.2.12 起的 0c-update）時出現（vocab.js 決定）。
// 「我的詞彙」的 DOM 只建一次（vocab.js），這張卡畫在 vocab.js 給的固定容器裡、自己重畫，不靠整頁重畫。
(function () {
  'use strict';

  const TEXT = {
    label: '清除學習紀錄',
    desc: '清掉靈犀從你打字學到的選字習慣。你加的詞與片語不受影響。',
    ask: '清除後，選字會回到剛安裝時的樣子：學到的詞、你選過的寫法、「不再學」的詞與最近用過的符號都會清掉，升級前保存的備份也一併刪除，無法復原。你加的詞與片語不受影響。',
    done: '已清除學習紀錄，選字回到剛安裝時的樣子。',
    busy: '輸入法正在切換，請稍後再試。',
  };

  // 0c-update personal reset -json 的結果 → 提示文字與語氣。
  function resultText(result) {
    if (result && result.ok) return [TEXT.done, 'ok'];
    const error = (result && result.error) || {};
    if (error.code === 'busy') return [TEXT.busy, 'warn'];
    return ['沒有清除完成：' + (error.message || '發生未知的錯誤。'), 'error'];
  }

  window.ZcPersonalResetLogic = { TEXT, resultText };

  // ctx：h、clear、invoke、toast；onDone() 在清除成功後呼叫（vocab.js 重新讀「靈犀學到的」與「不再學的詞」）。
  window.ZcPersonalReset = function (ctx) {
    const { h, clear, invoke, toast } = ctx;
    const st = { ask: false, busy: false, done: false };
    let root = null;

    function focus(key) {
      const node = root && root.querySelector('[data-focus="' + key + '"]');
      if (node) node.focus();
    }

    async function reset() {
      if (st.busy) return;
      st.busy = true;
      st.ask = false;
      draw();
      let tone = 'error';
      try {
        const [text, kind] = resultText(await invoke('personal_command', { action: 'reset' }));
        tone = kind;
        toast(text, kind);
      } catch (error) {
        toast('沒有清除完成：' + error, 'error');
      } finally {
        st.busy = false;
        st.done = tone === 'ok';
        draw();
        focus('personal-reset');
      }
      if (tone === 'ok' && ctx.onDone) ctx.onDone();
    }

    // data-k 讓 vocab.js 重畫區塊時（keepFocus）認得這幾個控制項。
    function draw() {
      if (!root) return;
      clear(root);
      const button = h('button', {
        type: 'button', class: 'secondary', 'data-focus': 'personal-reset', 'data-k': 'personal-reset', disabled: st.busy || st.ask, 'aria-labelledby': 'label-personal-reset',
        onclick: () => { st.ask = true; st.done = false; draw(); focus('personal-reset-cancel'); },
      }, st.busy ? '清除中…' : '清除…');
      root.append(h('div', { class: 'card' }, h('div', { class: 'row' },
        h('div', {}, h('div', { class: 'label', id: 'label-personal-reset' }, TEXT.label),
          h('div', { class: 'desc' }, st.done ? TEXT.done : TEXT.desc)),
        button)));
      // 確認出現時焦點先放在「取消」（連按兩下不會誤清），取消後回到「清除…」。
      if (st.ask) {
        root.append(h('div', { class: 'notice', role: 'alertdialog', 'aria-labelledby': 'label-personal-reset', 'aria-describedby': 'personal-reset-ask' },
          h('p', { id: 'personal-reset-ask' }, TEXT.ask),
          h('div', { class: 'actions' },
            h('button', { type: 'button', class: 'primary', 'data-focus': 'personal-reset-confirm', 'data-k': 'personal-reset-confirm', onclick: reset }, '確認清除'),
            h('button', { type: 'button', class: 'secondary', 'data-focus': 'personal-reset-cancel', 'data-k': 'personal-reset-cancel', onclick: () => { st.ask = false; draw(); focus('personal-reset'); } }, '取消'))));
      }
    }

    // container 在頁面存在期間不變；只在第一次（或換了容器）時畫。
    function mount(container) {
      if (root === container) return;
      root = container;
      draw();
    }

    return { mount };
  };
})();
