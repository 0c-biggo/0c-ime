// 輸入習慣頁：風格內容與適用條件由 0c-update profiles 契約提供；這裡只預覽與送出整份操作。
(function () {
  'use strict';

  function controlState(item, busy) {
    const values = item.values || [];
    const toggle = values.length === 2 && values.includes('off');
    // locked 是唯讀，不代表關閉；disabled 才是目前條件讓設定不起作用。
    const value = item.effective != null && item.effective !== '' ? item.effective
      : item.disabled && toggle ? 'off' : item.value;
    return { value, disabled: !!(busy || item.locked || item.disabled), toggle,
      checked: toggle && value === values.find((v) => v !== 'off') };
  }

  function differences(profile, items) {
    const values = profile?.values || {};
    // 只列服務端承諾由風格管理、且不同的設定；排列等獨立設定不在 values 裡。
    return (items || []).filter((item) => Object.prototype.hasOwnProperty.call(values, item.key) && values[item.key] !== item.value)
      .map((item) => ({ item, before: item.value, after: values[item.key] }));
  }

  function valueLabel(item, value, labels = {}) {
    if (item.value_labels?.[value]) return item.value_labels[value];
    if (item.values?.length === 2 && item.values.includes('off')) return value === 'off' ? '關閉' : '開啟';
    return labels[value] || value;
  }

  const validName = (name) => {
    const value = name.trim();
    return Array.from(value).length >= 1 && Array.from(value).length <= 80 && !/[\u0000-\u001f\u007f-\u009f]/.test(value);
  };

  window.ZcInputStylesLogic = { controlState, differences, validName, valueLabel };

  window.ZcInputStyles = function ({ h, settings, render, mutate, valueLabel, showKeyboard }) {
    let selected = null;
    let editing = null;
    let draft = '';
    let busy = false;
    let error = '';
    let composing = false;
    const button = (label, action, disabled, focus, primary) => h('button', {
      type: 'button', class: primary ? 'primary' : 'secondary', disabled: busy || disabled,
      'data-focus': focus, onclick: action,
    }, label);

    function begin(action, profile) {
      editing = action;
      draft = action === 'rename' ? profile.name : profile.name + '（自訂）';
      error = '';
      render();
      document.querySelector('[data-focus="style-name"]')?.focus();
    }

    async function run(action, profile) {
      if (busy) return;
      busy = true;
      error = '';
      render();
      const name = ['copy', 'rename'].includes(action) ? draft.trim() : null;
      try {
        const result = await mutate(action, profile.id, name);
        if (result.ok) {
          editing = null;
          selected = action === 'rename' ? profile.id : result.profiles?.active || null;
        } else error = result.error?.message || '無法儲存輸入習慣，請再試一次。';
      } catch (e) { error = String(e); }
      busy = false;
      render();
    }

    function view(root) {
      const s = settings();
      root.append(h('h1', {}, '輸入習慣'), h('p', { class: 'lead' },
        '選擇熟悉的操作習慣，查看差異後一次套用。想微調時，可複製成自己的自訂習慣。'));
      const profiles = s?.profiles;
      if (!profiles) {
        root.append(h('p', { class: 'muted' }, '目前版本尚未提供輸入習慣選擇。'));
        return;
      }
      const list = profiles.items || [];
      const active = list.find((p) => p.id === profiles.active);
      if (!list.some((p) => p.id === selected)) { selected = profiles.active; editing = null; }
      const target = list.find((p) => p.id === selected);
      const readOnly = !!s.file?.read_only;
      root.append(h('div', { class: 'notice info' },
        h('p', {}, '目前選用：', h('strong', {}, active?.name || profiles.active)),
        profiles.pending ? h('p', { role: 'status' }, '完成目前輸入後套用',
          profiles.running && profiles.running !== profiles.active
            ? '；目前仍使用「' + (list.find((p) => p.id === profiles.running)?.name || profiles.running) + '」。' : '。') : null));
      if (readOnly) root.append(h('p', { class: 'notice', role: 'status' }, '設定檔目前無法寫入，請到「問題排解」查看原因。'));
      if (error) root.append(h('p', { class: 'notice', role: 'alert' }, error));
      const chooser = h('select', { id: 'style-choice', class: 'style-select', 'data-focus': 'style-choice', disabled: busy,
        onchange: (event) => { selected = event.target.value; editing = null; error = ''; render(); } });
      for (const builtin of [true, false]) {
        const entries = list.filter((p) => !!p.builtin === builtin);
        if (entries.length) chooser.append(h('optgroup', { label: builtin ? '內建習慣' : '我的自訂習慣' },
          ...entries.map((p) => h('option', { value: p.id, selected: p.id === selected }, p.name))));
      }
      root.append(h('div', { class: 'card style-card' }, h('label', { for: 'style-choice', class: 'label' }, '選用風格'), chooser));
      if (!target) return;
      const unavailable = target.available === false;
      const locked = readOnly || unavailable;
      const diff = differences(target, s.items);
      const preview = h('div', { class: 'card style-card' }, h('h2', {}, target.name),
        target.id === 'legacy' ? h('p', {}, '保留升級前的設定，可直接到各設定頁修改。')
          : target.description ? h('p', {}, target.description) : null,
        (target.summary || []).length ? h('ul', { class: 'style-summary' }, ...target.summary.map((line) => h('li', {}, line))) : null,
        h('p', { class: 'muted small' }, target.builtin
          ? '內建習慣的相關設定固定。複製成自訂後可微調；不相容的功能仍會停用。'
          : '不相容的功能仍會停用，各設定頁會顯示原因。'));
      if (unavailable) preview.append(h('p', { class: 'notice' }, target.reason || '目前的鍵盤排列不適用這個習慣。'));
      if (target.id !== profiles.active) {
        preview.append(h('h3', {}, '套用時的變更'));
        if (diff.length) preview.append(h('ul', { class: 'style-diff' }, ...diff.map(({ item, before, after }) => h('li', {},
          h('span', { class: 'label' }, item.label || item.key),
          h('span', {}, valueLabel(item, before), ' → ', h('strong', {}, valueLabel(item, after)))))));
        else preview.append(h('p', {}, '切換至上方所列的操作習慣；目前各項設定值相同。'));
      }
      preview.append(h('p', { class: 'muted small' }, '鍵盤排列獨立設定。', ' ',
        h('button', { type: 'button', class: 'link goto', 'data-focus': 'style-keyboard', onclick: showKeyboard }, '查看鍵盤排列')),
      h('div', { class: 'actions' },
        button(target.id === profiles.active ? '目前選用' : '套用這個習慣', () => run('select', target), locked || target.id === profiles.active, 'style-apply', true),
        button('複製成自訂', () => begin('copy', target), locked, 'style-copy'),
        !target.builtin ? button('重新命名', () => begin('rename', target), readOnly, 'style-rename') : null,
        !target.builtin ? button('刪除', () => { editing = 'delete'; error = ''; render(); }, readOnly || target.id === profiles.active, 'style-delete') : null));
      if (!target.builtin && target.id === profiles.active) preview.append(h('p', { class: 'muted small' }, '要刪除此自訂習慣，請先套用另一個習慣。'));
      root.append(preview);
      if (editing === 'copy' || editing === 'rename') {
        const editLocked = readOnly || (editing === 'copy' && unavailable);
        const save = button(editing === 'copy' ? '建立並套用' : '儲存名稱', () => run(editing, target), editLocked || !validName(draft), 'style-save', true);
        const input = h('input', { type: 'text', id: 'style-name', class: 'style-name', value: draft, 'aria-describedby': 'style-name-help',
          'data-focus': 'style-name', disabled: busy || readOnly,
          oninput: (event) => { draft = event.target.value; save.disabled = busy || editLocked || !validName(draft); },
          oncompositionstart: () => { composing = true; },
          oncompositionend: (event) => { composing = false; draft = event.target.value; render(); },
          onkeydown: (event) => { if (event.key === 'Enter' && !event.isComposing && !save.disabled) { event.preventDefault(); run(editing, target); } },
        });
        root.append(h('div', { class: 'card style-card' },
          h('label', { for: 'style-name', class: 'label' }, editing === 'copy' ? '自訂習慣名稱' : '新的名稱'), input,
          h('p', { id: 'style-name-help', class: 'muted small' }, '名稱最多 80 個字。'),
          editing === 'copy' ? h('p', { class: 'muted small' }, '建立副本並套用後，可以到中英切換、選字或鍵盤與符號微調；不相容的功能仍會停用。') : null,
          h('div', { class: 'actions' }, save, button('取消', () => { editing = null; render(); }, false, 'style-cancel'))));
      } else if (editing === 'delete') {
        root.append(h('div', { class: 'card style-card' }, h('p', {}, '刪除「' + target.name + '」？這個自訂習慣的設定將一併移除。'),
          h('div', { class: 'actions' }, button('確認刪除', () => run('delete', target), readOnly || target.id === profiles.active, 'style-confirm-delete', true),
            button('取消', () => { editing = null; render(); }, false, 'style-cancel'))));
      }
    }
    return { render: view, composing: () => composing };
  };
})();
