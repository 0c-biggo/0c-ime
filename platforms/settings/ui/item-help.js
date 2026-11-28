// 設定項目的「選項提示」與「說明」連結（#362）：只看 0c-update settings -json 的項目，不碰畫面，node 測試直接載入
// （platforms/settings/test/item_help_test.js）。
//
// - value_hints（{值: 提示}，可省略）：說明下面只顯示目前選中那個值的提示，換選項就跟著換；那個值沒有提示就不顯示。
// - help_url（可省略）：官網設定說明頁的那一節，交給系統瀏覽器開（Rust 的 open_url 再核對一次）。
// 兩個欄位舊版 0c-update 都沒有：沒有就不顯示，畫面照舊。
// - 頁首的「說明」連結只有三頁有（輸入習慣、更新、我的詞彙）：這三頁的細節這次搬到設定說明頁，頁面上只剩一句。
//   錨點是設定說明頁那一節的錨點，不是設定的鍵名，也不來自 0c-update。
(function () {
  'use strict';

  // 與 platforms/settings/src/updater.rs 的 browser_url 相同：只開官網說明文件，錨點是設定的鍵名。
  const GUIDE = /^https:\/\/0c\.biggo\.com\/guide\/[A-Za-z0-9/._-]*(#[a-z0-9-]+)?$/;

  // 目前顯示的那個值（controlState 的 value：停用時是實際使用的值）的提示：{ label, text }；沒有就回 null。
  function hint(item, shown, labels = {}) {
    const hints = item && item.value_hints;
    if (!hints || typeof hints !== 'object' || shown == null) return null;
    const text = Object.prototype.hasOwnProperty.call(hints, shown) ? hints[shown] : null;
    if (typeof text !== 'string' || !text) return null;
    const label = (item.value_labels && item.value_labels[shown]) || labels[shown] || '';
    return { label, text };
  }

  // 這一項的說明連結；沒有、或不是官網說明文件的網址時回 null（不顯示連結）。
  function url(item) {
    const value = item && item.help_url;
    return typeof value === 'string' && value.length <= 300 && GUIDE.test(value) && !value.includes('..') && !value.includes('//', 8) ? value : null;
  }

  // 頁首有「說明」連結的頁面（app.js 的頁面名稱 → 設定說明頁的錨點）。模型、問題排解、總覽、關於本來就短，不放。
  const PAGE_ANCHORS = { styles: 'input-styles', update: 'update-mode', vocab: 'vocab' };

  // 這一頁的說明連結；沒有的頁面回 null。
  function pageUrl(page) {
    return Object.prototype.hasOwnProperty.call(PAGE_ANCHORS, page) ? url({ help_url: 'https://0c.biggo.com/guide/settings/#' + PAGE_ANCHORS[page] }) : null;
  }

  window.ZcItemHelp = { hint, url, pageUrl };
})();
