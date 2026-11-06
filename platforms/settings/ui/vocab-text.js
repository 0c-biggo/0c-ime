// 「我的詞彙」的文字檔格式（#255）：匯出成文字檔，到另一台電腦「匯入」或整份貼到新增框就能加回去。
// 只處理文字，不碰畫面，node 測試直接載入（platforms/settings/test/vocab_text_test.js）。
//
// 格式（人看得懂、也能直接貼回）：
//   # 開頭的行是說明，空行略過。
//   [我加的詞] 以下一行一個詞：詞、Tab、讀音（音節以空白分隔）；同一個詞有幾個讀音就寫幾行，英文詞只有詞。
//   [我的片語] 以下一行一則片語；有換行、Tab，開頭結尾有空白，或以 " # [ 開頭的片語寫成 JSON 字串（"第一行\n第二行"）。
// 讀入時也認得：沒有讀音的一行一詞（要查讀音）、Tab 分隔的多個詞（從試算表複製的一列）、詞和讀音以空白分隔、
// 讀音以 - 分隔音節、同一格以「／」分隔的多個讀音（設定程式清單上的顯示方式），以及 Windows 換行與 BOM。
window.ZcVocabText = (function () {
  'use strict';

  const WORDS = '[我加的詞]';
  const PHRASES = '[我的片語]';
  const MAX_READINGS = 4;
  const MAX_RUNES = 32;

  const HAN = /^\p{Script=Han}+$/u;
  // 英文詞（#146）：終端機指令、工具名。與 core/vocab.IsEnglish 相同。
  const ENGLISH = /^(?=.*[A-Za-z])[A-Za-z0-9._-]{1,32}$/;
  const ASCII = /^[\x21-\x7e]+$/;
  const isEnglish = (text) => ENGLISH.test(text);
  // 注音符號（含擴充）、聲調、空白，以及音節分隔 - 與讀音分隔 ／ /。
  const BOPOMOFO = /^[ㄅ-ㄯㆠ-ㆿˉˊˇˋ˙\s　\-／/]+$/;
  const HAS_BOPOMOFO = /[ㄅ-ㄯㆠ-ㆿ]/;
  const isReading = (field) => BOPOMOFO.test(field) && HAS_BOPOMOFO.test(field);
  // 片語要寫成 JSON 字串的情況：讀回時會被當成換行、說明、標題或被去掉的內容。
  const NEEDS_QUOTE = /[\u0000-\u001f\u007f\u0085\u2028\u2029]/;

  function wordProblem(text) {
    if (isEnglish(text)) return null;
    if (ASCII.test(text)) {
      if (text.length > 32) return '英文詞最多 32 個字元；比較長的內容請放到「我的片語」。';
      if (!/[A-Za-z]/.test(text)) return '英文詞至少要有一個英文字母。';
      return '英文詞只能有英文字母、數字與 - _ .；其他符號請放到「我的片語」。';
    }
    if (!HAN.test(text)) return '只能加漢字，或英文字母、數字與 - _ . 組成的英文詞（例如 ls）；符號、假名或句子請放到「我的片語」。';
    if ([...text].length > MAX_RUNES) return '一個詞最多 32 個字；比較長的句子請放到「我的片語」。';
    return null;
  }

  // 讀音與詞對不上的原因；沒問題回傳 null。音節合不合法由引擎檢查（core/vocab.Normalize）。
  function readingProblem(word, readings) {
    if (!readings.length) return null;
    if (isEnglish(word)) return '英文詞不需要讀音。';
    const n = [...word].length;
    for (const r of readings) {
      const syls = r.split(' ').length;
      if (syls !== n) return '「' + word + '」有 ' + n + ' 個字，讀音「' + r + '」卻有 ' + syls + ' 個音節。';
    }
    if (readings.length > MAX_READINGS) return '同一個詞最多 ' + MAX_READINGS + ' 個讀音，這裡有 ' + readings.length + ' 個。';
    return null;
  }

  // 一格讀音：可能用「／」分隔多個讀音、用 - 或全形空白分隔音節。回傳整理好的讀音（音節以一個空白分隔）。
  function splitReadings(field) {
    return field.split(/[／/]/).map((r) => r.replace(/-/g, ' ').trim().split(/[\s　]+/).filter(Boolean).join(' ')).filter(Boolean);
  }

  function quotePhrase(text) {
    const first = text.charAt(0);
    if (NEEDS_QUOTE.test(text) || text.trim() !== text || first === '"' || first === '#' || first === '[' || text === '') {
      // JSON.stringify 不跳脫 U+0085、U+2028、U+2029；有些編輯器把它們當換行，這裡自己跳脫。
      return JSON.stringify(text).replace(/[\u0085\u2028\u2029]/g, (c) => '\\u' + c.charCodeAt(0).toString(16).padStart(4, '0'));
    }
    return text;
  }

  function unquotePhrase(line) {
    if (line.charAt(0) === '"') {
      try {
        const text = JSON.parse(line);
        if (typeof text === 'string') return text;
      } catch (_) { /* 不是 JSON 字串：照字面 */ }
    }
    return line;
  }

  function pad(n) { return String(n).padStart(2, '0'); }

  // 匯出：words 是 list 指令回傳的詞（{word, readings}），phrases 是片語（{text} 或字串）。
  function format({ words = [], phrases = [], date = new Date() } = {}) {
    const day = date.getFullYear() + '-' + pad(date.getMonth() + 1) + '-' + pad(date.getDate());
    const lines = [
      '# 靈犀注音「我的詞彙」（' + day + ' 匯出，' + words.length + ' 個詞、' + phrases.length + ' 則片語）',
      '# 到另一台電腦開「靈犀注音設定」→「我的詞彙」，按「匯入」選這個檔案，就能加回去；原本有的詞不會被刪掉。',
      '# 一行一個詞：詞、Tab、讀音。同一個詞有幾個讀音就寫幾行；英文詞只有詞。',
      '# 「' + PHRASES + '」以下一行一則；有換行的片語寫成 "第一行\\n第二行"。',
      '',
      WORDS,
    ];
    for (const w of words) {
      const readings = w.readings || [];
      if (!readings.length) lines.push(w.word);
      for (const r of readings) lines.push(w.word + '\t' + r);
    }
    lines.push('', PHRASES);
    for (const p of phrases) lines.push(quotePhrase(typeof p === 'string' ? p : p.text));
    return lines.join('\n') + '\n';
  }

  // 讀入：匯出的檔案、貼上的文字都走這裡。回傳
  //   words：[{word, readings, line}]，同一個詞合併成一筆，readings 依出現順序、不重複；沒有讀音的要查讀音
  //   phrases：[{text, line}]（依出現順序，含重複；重複由 plan 處理）
  // line 是第幾行（從 1 起），給錯誤訊息用。
  function parse(text) {
    const out = { words: [], phrases: [] };
    const byWord = new Map();
    const addWord = (word, readings, line) => {
      let w = byWord.get(word);
      if (!w) { w = { word, readings: [], line }; byWord.set(word, w); out.words.push(w); }
      for (const r of readings) if (!w.readings.includes(r)) w.readings.push(r);
    };
    let section = 'words';
    String(text || '').replace(/^﻿/, '').split(/\r\n|\r|\n/).forEach((raw, i) => {
      const line = raw.trim();
      const n = i + 1;
      if (!line || line.charAt(0) === '#') return;
      if (line === WORDS) { section = 'words'; return; }
      if (line === PHRASES) { section = 'phrases'; return; }
      if (section === 'phrases') { out.phrases.push({ text: unquotePhrase(line), line: n }); return; }
      if (line.includes('\t')) {
        const fields = line.split('\t').map((x) => x.trim()).filter(Boolean);
        const rest = fields.slice(1);
        if (rest.length && rest.every(isReading)) { addWord(fields[0], rest.flatMap(splitReadings), n); return; }
        fields.forEach((f) => addWord(f, [], n)); // 從試算表複製的一列：每格一個詞
        return;
      }
      const m = /^(\S+)[ 　]+(.+)$/.exec(line);
      if (m && isReading(m[2])) { addWord(m[1], splitReadings(m[2]), n); return; }
      addWord(line, [], n);
    });
    return out;
  }

  // 對照現有的詞彙，決定要加哪些（「加入」語意：不刪除這邊有、檔案沒有的詞）。
  //   current：{ words: [{word, readings}], phrases: [字串], count, limit, phraseLimit }
  // 回傳
  //   rows：要加的詞 [{word, readings, given, english, more, problem}]；given 表示檔案帶了讀音（不重查），
  //         more 表示詞已經有了、這次多加讀音；readings 只含這次要加的讀音。沒帶讀音的要先查讀音。
  //   phrases：要加的片語 [{text, problem}]
  //   same、samePhrases：已經有了、略過的詞與片語數
  //   room、phraseRoom：還能加幾個詞、幾則片語
  function plan(parsed, current) {
    const have = new Map((current.words || []).map((w) => [w.word, w.readings || []]));
    const rows = [];
    let same = 0;
    for (const w of parsed.words) {
      const english = isEnglish(w.word);
      const problem = wordProblem(w.word) || readingProblem(w.word, w.readings);
      const old = have.get(w.word);
      if (!problem && old) {
        const fresh = w.readings.filter((r) => !old.includes(r));
        if (!fresh.length) { same++; continue; } // 已經有了（沒帶讀音的也算：不替它多查一個讀音）
        const row = { word: w.word, readings: fresh, given: true, english, more: true, line: w.line, problem: null };
        if (old.length + fresh.length > MAX_READINGS) {
          row.problem = '「' + w.word + '」已經有 ' + old.length + ' 個讀音，再加 ' + fresh.length + ' 個會超過 ' + MAX_READINGS + ' 個。';
        }
        rows.push(row);
        continue;
      }
      rows.push({ word: w.word, readings: w.readings.slice(), given: english || w.readings.length > 0, english, more: false, line: w.line, problem });
    }
    const seen = new Set(current.phrases || []);
    const phrases = [];
    let samePhrases = 0;
    for (const p of parsed.phrases) {
      if (seen.has(p.text)) { samePhrases++; continue; }
      seen.add(p.text);
      const n = [...p.text].length;
      phrases.push({ text: p.text, line: p.line, problem: n > 1000 ? '片語最多 1,000 字，這則有 ' + n + ' 字。' : null });
    }
    const limit = current.limit || 10000;
    const phraseLimit = current.phraseLimit || 200;
    return {
      rows, phrases, same, samePhrases,
      room: Math.max(0, limit - (current.count != null ? current.count : have.size)),
      phraseRoom: Math.max(0, phraseLimit - (current.phrases || []).length),
    };
  }

  // 把要送的項目切成一次一批：最多 size 筆，而且每批的 JSON 不超過 bytes（設定程式一次送出最多 1 MiB）。
  function chunks(items, size, bytes = 512 * 1024) {
    const out = [];
    let cur = [];
    let used = 0;
    for (const item of items) {
      const len = new TextEncoder().encode(JSON.stringify(item)).length + 1;
      if (cur.length && (cur.length >= size || used + len > bytes)) { out.push(cur); cur = []; used = 0; }
      cur.push(item);
      used += len;
    }
    if (cur.length) out.push(cur);
    return out;
  }

  return { format, parse, plan, chunks, wordProblem, readingProblem, isEnglish, isReading, WORDS, PHRASES };
})();
