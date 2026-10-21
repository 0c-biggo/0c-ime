#!/usr/bin/env python3
"""設定程式 UI 測試用的 0c-update 替身（#125「我的詞彙」）。

vocab 與 api 兩個指令由這裡用一份狀態檔（FAKE_VOCAB）模擬，回應格式與 core/engine 的 VocabResponse、
cmd/0c-update/api.go 的 apiView 相同；其他指令（status、settings、set…）原樣交給真的 0c-update（REAL_UPDATER），
所以總覽、側欄與入口（features.vocab，0.2.0 起恆為 true）走的是真契約。每個詞彙指令記一行到 FAKE_LOG，
測試據此確認畫面確實呼叫了什麼。

測試另外直接執行：
  fake-update.py --init STATE empty|full|ai|english [old]  建立初始狀態（english：漢字詞夾英文詞，#146；
                                                 old：模擬還沒有 features.vocab 的舊版 0c-update，入口要藏起來）
  fake-update.py vocab add-as assistant 詞 讀音   模擬 AI 助手從 API 加詞（版本加一，watch 會看到）

「模型」頁（#180）：設了 FAKE_MODELS 時 models status|download|cancel|remove 由這裡依契約（schema 1）回應：
  fake-update.py --models STATE bundled|none|downloading|user|redownload|runtime|distribution|dictionary
downloading 的進度固定不動（截圖用）；從 none 按「下載模型」後，每讀一次 status 前進一步：下載 → 核對 → 配對 → 已安裝。
"""
import fcntl
import json
import os
import re
import sys
import time

STATE = os.environ.get('FAKE_VOCAB', '/tmp/fake-vocab.json')
LOG = os.environ.get('FAKE_LOG', '/tmp/fake-vocab.log')
MAX_WORDS = 10000

# 每個字的讀音（常見的在前）；AMBIGUOUS 是要使用者確認的破音字。不在表裡的字回報查不到。
CHARS = {
    '陳': ['ㄔㄣˊ'], '怡': ['ㄧˊ'], '君': ['ㄐㄩㄣ'], '曾': ['ㄗㄥ', 'ㄘㄥˊ'], '國': ['ㄍㄨㄛˊ'], '城': ['ㄔㄥˊ'],
    '靈': ['ㄌㄧㄥˊ'], '犀': ['ㄒㄧ'], '長': ['ㄔㄤˊ', 'ㄓㄤˇ'], '庚': ['ㄍㄥ'], '王': ['ㄨㄤˊ'], '小': ['ㄒㄧㄠˇ'],
    '明': ['ㄇㄧㄥˊ'], '張': ['ㄓㄤ'], '雅': ['ㄧㄚˇ'], '婷': ['ㄊㄧㄥˊ'], '樂': ['ㄌㄜˋ', 'ㄩㄝˋ'], '天': ['ㄊㄧㄢ'],
    '堂': ['ㄊㄤˊ'], '黃': ['ㄏㄨㄤˊ'], '偉': ['ㄨㄟˇ'], '倫': ['ㄌㄨㄣˊ'], '世': ['ㄕˋ'], '介': ['ㄐㄧㄝˋ'],
    '林': ['ㄌㄧㄣˊ'], '志': ['ㄓˋ'], '玲': ['ㄌㄧㄥˊ'], '阿': ['ㄚ'], '姆': ['ㄇㄨˇ'], '斯': ['ㄙ'], '特': ['ㄊㄜˋ'],
    '朗': ['ㄌㄤˇ'], '砲': ['ㄆㄠˋ'], '行': ['ㄒㄧㄥˊ', 'ㄏㄤˊ'], '銀': ['ㄧㄣˊ'], '大': ['ㄉㄚˋ'], '同': ['ㄊㄨㄥˊ'],
    '區': ['ㄑㄩ'], '研': ['ㄧㄢˊ'], '發': ['ㄈㄚ'], '部': ['ㄅㄨˋ'], '芳': ['ㄈㄤ'], '瑜': ['ㄩˊ'], '珊': ['ㄕㄢ'],
}
AMBIGUOUS = {'曾', '長', '樂', '行'}
SYSTEM = {'靈犀', '長庚', '世界'}  # 詞庫已有的詞
RANKS = {  # 只打這組注音時的排名；沒列的詞是首選
    '世介': {'first': False, 'page': 1, 'index': 2, 'ahead': '世界', 'output': '世界'},
    '曾國城': {'first': False, 'page': 1, 'index': 2, 'ahead': '曾國成', 'output': '曾國成'},
    '林芳瑜': {'first': False, 'page': 2, 'index': 12, 'ahead': '臨方餘', 'output': '林方於'},
}


def log(entry):
    with open(LOG, 'a', encoding='utf-8') as f:
        f.write(json.dumps(dict(entry, t=round(time.time(), 3)), ensure_ascii=False) + '\n')


def load():
    with open(STATE, encoding='utf-8') as f:
        return json.load(f)


def locked():
    """畫面會同時送好幾個指令（清單、最近變更、排名）：讀、改、存整段上鎖，跟真的引擎一樣一次一個。"""
    f = open(STATE + '.lock', 'w')
    fcntl.flock(f, fcntl.LOCK_EX)
    return f


def save(state):
    tmp = '%s.%d.tmp' % (STATE, os.getpid())
    with open(tmp, 'w', encoding='utf-8') as f:
        json.dump(state, f, ensure_ascii=False, indent=1)
    os.replace(tmp, STATE)


def init(kind):
    now = int(time.time())
    state = {'version': 1, 'words': [], 'phrases': [], 'batches': [], 'learned': [], 'next_batch': 1, 'next_phrase': 1,
             'engine': True, 'tombstones': [],
             'api': {'enabled': False, 'port': 47100, 'token': '', 'created': 0, 'last_used': 0, 'listening': True}}
    if kind in ('full',):
        def add(word, reading, source, age):
            state['words'].append({'word': word, 'readings': [reading], 'source': source, 'added': now - age, 'batch': 0})
        add('陳怡君', 'ㄔㄣˊ ㄧˊ ㄐㄩㄣ', 'manual', 86400 * 2)
        add('曾國城', 'ㄗㄥ ㄍㄨㄛˊ ㄔㄥˊ', 'manual', 86400 * 2 + 600)
        add('世介', 'ㄕˋ ㄐㄧㄝˋ', 'compose', 86400)
        add('林芳瑜', 'ㄌㄧㄣˊ ㄈㄤ ㄩˊ', 'manual', 7200)
        add('長庚', 'ㄔㄤˊ ㄍㄥ', 'learned', 3600)
        add('靈犀', 'ㄌㄧㄥˊ ㄒㄧ', 'assistant', 30)
        state['batches'] = [
            {'id': 1, 'time': now - 86400 * 2, 'source': 'manual', 'op': 'add',
             'changes': [{'after': dict(state['words'][0])}, {'after': dict(state['words'][1])}]},
            {'id': 2, 'time': now - 4000, 'source': 'manual', 'op': 'remove',
             'changes': [{'before': {'word': '王小明', 'readings': ['ㄨㄤˊ ㄒㄧㄠˇ ㄇㄧㄥˊ'], 'source': 'manual', 'added': now - 90000}}]},
            {'id': 3, 'time': now - 30, 'source': 'assistant', 'op': 'add', 'changes': [{'after': dict(state['words'][5])}]},
        ]
        state['next_batch'] = 4
        state['version'] = 7
        state['learned'] = [
            {'word': '大同區', 'reading': 'ㄉㄚˋ ㄊㄨㄥˊ ㄑㄩ', 'kind': 'phrase', 'uses': 4},
            {'word': '研發部', 'reading': 'ㄧㄢˊ ㄈㄚ ㄅㄨˋ', 'kind': 'phrase', 'uses': 2},
            {'word': '銀行', 'reading': 'ㄧㄣˊ ㄏㄤˊ', 'kind': 'reading', 'uses': 0},
            {'word': '長庚', 'reading': 'ㄔㄤˊ ㄍㄥ', 'kind': 'reading', 'uses': 0},
        ]
        state['phrases'] = [
            {'id': 1, 'text': '台北市大同區承德路一段 70 號 5 樓', 'added': now - 86400},
            {'id': 2, 'text': 'chen.yijun@example.com', 'added': now - 86000},
            {'id': 3, 'text': '陳怡君 敬上\n研發部', 'added': now - 80000},
        ]
        state['next_phrase'] = 4
    if kind == 'english':
        for word, reading, age in (('陳怡君', 'ㄔㄣˊ ㄧˊ ㄐㄩㄣ', 86400), ('ls', None, 7200), ('kubectl', None, 3600),
                                   ('git-lfs', None, 1800), ('曾國城', 'ㄗㄥ ㄍㄨㄛˊ ㄔㄥˊ', 600)):
            state['words'].append({'word': word, 'readings': [reading] if reading else [], 'source': 'manual', 'added': now - age, 'batch': 0})
        state['batches'] = [{'id': 1, 'time': now - 600, 'source': 'manual', 'op': 'add', 'changes': [{'after': dict(w)} for w in state['words']]}]
        state['next_batch'] = 2
        state['version'] = 2
    if kind == 'ai':
        state['api'].update(enabled=True, token='0c_3f9a1c27d84e4b6a9e0b52c1d7f8a6e2', created=now - 86400 * 3, last_used=now - 300)
    save(state)


def word_view(w):
    return {k: w[k] for k in ('word', 'readings', 'source', 'added', 'batch') if w.get(k) not in (None, '')}


def status_fields(state):
    return {'version': state['version'], 'count': len(state['words']), 'limit': MAX_WORDS}


def err(code, message, item=-1):
    return {'code': code, 'item': item, 'message': message}


def is_english(text):
    return bool(re.fullmatch(r'(?=.*[A-Za-z])[A-Za-z0-9._-]{1,32}', text))


def is_han(text):
    return bool(text) and all('㐀' <= c <= '鿿' or '豈' <= c <= '﫿' for c in text)


def commit(state, op, source, changes, **extra):
    batch = {'id': state['next_batch'], 'time': int(time.time()), 'source': source, 'op': op, 'changes': changes}
    batch.update(extra)
    state['next_batch'] += 1
    state['version'] += 1
    state['batches'].append(batch)
    state['batches'] = state['batches'][-50:]
    return batch


def summary(b):
    preview = []
    for c in b['changes']:
        if len(preview) == 5:
            break
        x = c.get('after') or c.get('before')
        if x:
            preview.append(x['word'])
            continue
        p = c.get('phrase_after') or c.get('phrase_before')
        if p:
            preview.append(p['text'][:20] + ('…' if len(p['text']) > 20 else ''))
    s = {'id': b['id'], 'time': b['time'], 'source': b['source'], 'op': b['op'], 'count': len(b['changes']), 'preview': preview}
    for k in ('undo_of', 'undone'):
        if b.get(k):
            s[k] = b[k]
    return s


def annotate(text):
    if is_english(text):
        return {'word': text, 'reading': '', 'chars': [], 'english': True}
    a = {'word': text, 'reading': '', 'chars': []}
    readings = []
    for c in text:
        options = CHARS.get(c, [])
        entry = {'char': c}
        if options:
            entry.update(reading=options[0], options=options)
            if c in AMBIGUOUS:
                entry['ambiguous'] = True
            readings.append(options[0])
        else:
            a['unknown'] = True
        a['chars'].append(entry)
    if not a.get('unknown'):
        a['reading'] = ' '.join(readings)
    if text in SYSTEM:
        a['exists'] = True
    return a


def vocab(req):
    with locked():
        return vocab_locked(req)


def vocab_locked(req):
    state = load()
    op = req.get('op')
    source = req.get('source') or 'manual'
    log({'op': op, 'req': req})
    resp = {'ok': True}
    # 同 0c-update：引擎沒執行時在寫入者鎖內直接改 vocab.json，只有學到的詞要交給引擎。
    if not state.get('engine', True) and op in ('learned', 'keep', 'forget', 'unlearn', 'forgotten', 'relearn', 'learned-undo'):
        return {'ok': False, 'error': err('engine', '引擎沒有在執行，學到的詞要在輸入法執行時才能管理；切換到靈犀注音打一個字後再試'), 'version': 0, 'count': 0, 'limit': 0}
    words = state['words']
    find = lambda t: next((w for w in words if w['word'] == t), None)
    changed = None
    if op == 'status':
        pass
    elif op == 'list':
        q = (req.get('query') or '').strip()
        hits = [w for w in words if not q or q in w['word'] or any(r.startswith(q) for r in w['readings'])]
        off, lim = max(0, req.get('offset') or 0), req.get('limit') or 1000
        resp['words'] = [word_view(w) for w in hits[off:off + lim]]
        resp['total'] = len(hits)
    elif op == 'add':
        items = req.get('words') or []
        if not items:
            resp = {'ok': False, 'error': err('empty', '沒有要加入的詞')}
        changes = []
        for i, item in enumerate(items):
            text, rs = item.get('word', '').strip(), item.get('readings') or []
            if is_english(text):
                if rs:
                    resp = {'ok': False, 'error': err('invalid_reading', '「' + text + '」是英文詞，不需要讀音', i)}
                    break
            elif not is_han(text):
                resp = {'ok': False, 'error': err('invalid_word', '「' + text + '」不是 1～32 個漢字', i)}
                break
            elif not rs or any(len(r.split()) != len(text) for r in rs):
                resp = {'ok': False, 'error': err('invalid_reading', '「' + text + '」的讀音音節數與字數不同', i)}
                break
            old = find(text)
            if old:
                before = dict(old, readings=list(old['readings']))
                for r in rs:
                    if r not in old['readings']:
                        old['readings'].append(r)
                changes.append({'before': word_view(before), 'after': word_view(old)})
            else:
                w = {'word': text, 'readings': rs, 'source': source, 'added': int(time.time())}
                words.append(w)
                changes.append({'after': word_view(w)})
        if resp['ok'] and changes:
            changed = commit(state, 'add', source, changes)
    elif op == 'remove':
        changes = []
        for i, t in enumerate(req.get('texts') or []):
            w = find(t)
            if not w:
                resp = {'ok': False, 'error': err('not_found', '沒有「' + t + '」', i)}
                break
            words.remove(w)
            changes.append({'before': word_view(w)})
        if resp['ok']:
            changed = commit(state, 'remove', source, changes)
    elif op == 'readings':
        w = find(req.get('text', ''))
        if not w:
            resp = {'ok': False, 'error': err('not_found', '沒有這個詞')}
        elif req.get('expect') is not None and req['expect'] != w['readings']:
            resp = {'ok': False, 'error': err('conflict', '「' + w['word'] + '」剛被修改過（可能是 AI 助手或另一個視窗），已換成最新的內容，請再改一次', 0)}
        else:
            before = word_view(dict(w, readings=list(w['readings'])))
            w['readings'] = req.get('readings') or w['readings']
            changed = commit(state, 'readings', source, [{'before': before, 'after': word_view(w)}])
    elif op == 'undo':
        b = next((x for x in state['batches'] if x['id'] == req.get('batch')), None)
        if not b or b.get('undone') or b['op'] == 'phrase-move':
            resp = {'ok': False, 'error': err('conflict', '這一批不能復原')}
        else:
            changes = []
            for c in reversed(b['changes']):
                if 'after' in c or 'before' in c:
                    if c.get('after'):
                        x = find(c['after']['word'])
                        if x:
                            words.remove(x)
                    if c.get('before'):
                        words.append(dict(c['before']))
                    changes.append({k2: c[k1] for k1, k2 in (('after', 'before'), ('before', 'after')) if c.get(k1)})
                else:
                    if c.get('phrase_after'):
                        state['phrases'] = [p for p in state['phrases'] if p['id'] != c['phrase_after']['id']]
                    if c.get('phrase_before'):
                        state['phrases'].append(dict(c['phrase_before']))
                    changes.append({k2: c[k1] for k1, k2 in (('phrase_after', 'phrase_before'), ('phrase_before', 'phrase_after')) if c.get(k1)})
            b['undone'] = True
            changed = commit(state, 'undo', source, changes, undo_of=b['id'])
    elif op == 'batches':
        if req.get('batch'):
            b = next((x for x in state['batches'] if x['id'] == req['batch']), None)
            resp = {'ok': True, 'batch': b} if b else {'ok': False, 'error': err('not_found', '找不到這個批次')}
        else:
            resp['batches'] = [summary(b) for b in reversed(state['batches'])]
    elif op == 'rank':
        texts = req.get('texts') or [w['word'] for w in words]
        resp['ranks'] = []
        for t in texts:
            w = find(t)
            if not w:
                continue
            for r in w['readings']:
                rank = {'word': t, 'reading': r, 'first': True, 'page': 1, 'index': 1, 'output': t, 'learned': False}
                rank.update(RANKS.get(t, {}))
                resp['ranks'].append(rank)
    elif op == 'annotate':
        resp['annotations'] = [annotate(t.strip()) for t in req.get('texts') or []]
    elif op == 'learned':
        resp['learned'] = [dict(l, vocab=bool(find(l['word']))) for l in state['learned']]
        resp['forgotten_count'] = len(set(state['tombstones']))
    elif op == 'keep':
        changes = []
        for i, t in enumerate(req.get('texts') or []):
            l = next((x for x in state['learned'] if x['word'] == t), None)
            if not l:
                resp = {'ok': False, 'error': err('not_found', '「' + t + '」不是學到的詞', i)}
                break
            w = {'word': t, 'readings': [l['reading']], 'source': 'learned', 'added': int(time.time())}
            words.append(w)
            changes.append({'after': word_view(w)})
        if resp['ok']:
            changed = commit(state, 'add', 'learned', changes)
    elif op in ('forget', 'unlearn', 'relearn'):
        # 同引擎（#182）：不再學留 tombstone、移除不留、恢復學習刪 tombstone；操作前留一份複本給 learned-undo。
        texts = [t.strip() for t in req.get('texts') or [] if t.strip()]
        if not texts:
            return {'ok': False, 'error': err('empty', '沒有選任何詞'), **status_fields(state)}
        before = {'learned': list(state['learned']), 'tombstones': list(state['tombstones'])}
        if op == 'relearn':
            state['tombstones'] = [t for t in state['tombstones'] if t not in texts]
        else:
            state['learned'] = [l for l in state['learned'] if l['word'] not in texts]
            if op == 'forget':
                state['tombstones'] += [t for t in texts if t not in state['tombstones']]
        if before['learned'] != state['learned'] or before['tombstones'] != state['tombstones']:
            state['next_undo'] = state.get('next_undo', 0) + 1
            state.setdefault('undos', {})[str(state['next_undo'])] = before
            resp['undo'] = state['next_undo']
            resp['changed'] = True
        resp['forgotten_count'] = len(set(state['tombstones']))
    elif op == 'forgotten':
        resp['forgotten'] = sorted(set(state['tombstones']))
        resp['forgotten_count'] = len(resp['forgotten'])
    elif op == 'learned-undo':
        before = state.get('undos', {}).pop(str(req.get('undo')), None)
        if not before:
            resp = {'ok': False, 'error': err('not_found', '已經無法復原（太久以前的操作，或輸入法引擎重新啟動過）')}
        else:
            state['learned'], state['tombstones'] = before['learned'], before['tombstones']
            resp['changed'] = True
            resp['forgotten_count'] = len(set(state['tombstones']))
    elif op in ('phrases', 'phrase-add', 'phrase-remove', 'phrase-move'):
        if op == 'phrase-add':
            changes = []
            for t in req.get('texts') or []:
                p = {'id': state['next_phrase'], 'text': t, 'added': int(time.time())}
                state['next_phrase'] += 1
                state['phrases'].append(p)
                changes.append({'phrase_after': p})
            changed = commit(state, 'phrase-add', source, changes)
        elif op == 'phrase-remove':
            ids = req.get('ids') or []
            gone = [p for p in state['phrases'] if p['id'] in ids]
            state['phrases'] = [p for p in state['phrases'] if p['id'] not in ids]
            changed = commit(state, 'phrase-remove', source, [{'phrase_before': p} for p in gone])
        elif op == 'phrase-move':
            ps = state['phrases']
            k = next((i for i, p in enumerate(ps) if p['id'] == req.get('id')), -1)
            to = min(max(req.get('to') or 0, 0), len(ps) - 1)
            if k >= 0 and to != k:
                ps.insert(to, ps.pop(k))
                changed = commit(state, 'phrase-move', source, [])
        resp['phrases'] = state['phrases']
    else:
        resp = {'ok': False, 'error': err('unknown_op', '未知的詞彙指令：' + str(op))}
    if changed:
        resp['batch'] = changed
        resp['changed'] = True
    save(state)
    resp.update(status_fields(state))
    return resp


def watch(since):
    first = since is None
    parent = os.getppid()
    while True:
        if os.getppid() != parent:  # 與真的 watch 相同：設定程式被強制結束就跟著結束
            return
        try:
            state = load()
        except (OSError, ValueError):
            time.sleep(0.2)
            continue
        if first or state['version'] != since:
            first, since = False, state['version']
            print(json.dumps({'version': state['version'], 'count': len(state['words'])}), flush=True)
        time.sleep(0.3)


def api(args):
    with locked():
        return api_locked(args)


def api_locked(args):
    state = load()
    a = state['api']
    action = args[0] if args else ''
    log({'op': 'api-' + action, 'args': args})
    view_apply = None
    if action in ('enable', 'disable'):
        a['enabled'] = action == 'enable'
        if '-port' in args:
            a['port'] = int(args[args.index('-port') + 1])
            a['listening'] = True
        if a['enabled'] and not a['token']:
            a['token'], a['created'] = '0c_' + os.urandom(16).hex(), int(time.time())
        view_apply = {'requested': True, 'state': 'applied'}
    elif action == 'token':
        a['token'], a['created'] = '0c_' + os.urandom(16).hex(), int(time.time())
    save(state)
    addr = '127.0.0.1:%d' % a['port']
    st = 'off'
    if a['enabled']:
        st = 'listening' if a.get('listening', True) else 'port_in_use'
    view = {'schema': 1, 'ok': True, 'enabled': a['enabled'], 'addr': addr, 'state': st,
            'mcp_url': 'http://%s/mcp' % addr, 'openapi_url': 'http://%s/openapi.json' % addr}
    if a['token']:
        view.update(token=a['token'], token_created=a['created'])
    if a.get('last_used'):
        view['last_used'] = a['last_used']
    if view_apply:
        view['apply'] = view_apply
    if action == 'test':
        if not a['enabled']:
            view['test'] = {'ok': False, 'reason': 'off', 'detail': '還沒開啟「讓 AI 幫你加詞」'}
        else:
            view['test'] = {'ok': True, 'count': len(state['words'])}
    return view


MODELS = os.environ.get('FAKE_MODELS', '')
MODEL_LOCK = [
    {'role': 'select', 'label': '選字', 'version': 'd512-pf512-20260926', 'size': 130723134,
     'repository': 'Funmula/0c-sequence-d512', 'revision': 'df4b9f04f8aee1dd34dcb246ac2b83c4ab3d14a9',
     'url': 'https://huggingface.co/Funmula/0c-sequence-d512/tree/df4b9f04f8aee1dd34dcb246ac2b83c4ab3d14a9', 'license': 'MIT'},
    {'role': 'punct', 'label': '補標點', 'version': 'd512-e60k-20260930', 'size': 130584075,
     'repository': 'Funmula/0c-punct-d512', 'revision': '105c2770e0b7cf4ce44a792b3b186000dd1727d9',
     'url': 'https://huggingface.co/Funmula/0c-punct-d512/tree/105c2770e0b7cf4ce44a792b3b186000dd1727d9', 'license': 'MIT'},
]
# 0.2.2 以前官網內建的匯出（models/equivalent.json）
OLD_SHA = {'select': 'cef689f1e706537e54eaed4241a776f1ea41b4cad2b504061d1e27cd0c1ca342',
           'punct': '2d98c7c6c75dc9d46081e967dbd2fccef67f73a1ed9b2da44d7e02002e760cf5'}
MODEL_SHA = {'select': '71af1f0e77b2752cc71cbc04bf19c8e8f2e765662246f766e65a2822bab4e66e', 'punct': '4db3ed40df777347d9c455da01285b01de192d4c210c5f7cd9f0646c727880d8'}  # models.lock 的 model.onnx


def models_installed(source, state='ready'):
    base = '/usr/share/0c/neural' if source == 'bundled' else '/home/u/.local/share/zuin/models/5f1c0a9e'
    out = []
    for m in MODEL_LOCK:
        item = dict(m, state=state, source=source, path=base + '/' + m['role'], sha256=MODEL_SHA[m['role']], equivalent=False)
        if source == 'bundled':  # 0.2.2 以前官網內建的內部匯出：權重與 Hugging Face 版相同（models/equivalent.json），上游資訊照附
            item.update(sha256=OLD_SHA[m['role']], equivalent=True)
        out.append(item)
    return out


def models_missing():
    return [dict(m, state='not_installed', source='', path='', sha256='', equivalent=False) for m in MODEL_LOCK]


def models_init(path, kind):
    total = sum(m['size'] for m in MODEL_LOCK)
    st = {'schema': 1, 'ok': True, 'dictionary_sha256': '5f1c0a9e' + '7d2b' * 14, 'source': 'none', 'can_download': True, 'disabled_reason': '',
          'runtime': {'ok': True, 'python': '/usr/bin/python3', 'missing': []},
          'need': 'download', 'download_size': total, 'models': models_missing(), 'operation': None, 'last_error': None}
    frozen = False
    if kind == 'bundled':
        st.update(source='bundled', can_download=False, disabled_reason='bundled', need='none', models=models_installed('bundled'))
    elif kind == 'downloading':
        st['operation'] = {'phase': 'downloading', 'done_bytes': 98566144, 'total_bytes': total, 'started_at': '2026-10-21T08:00:00Z'}
        frozen = True
    elif kind == 'user':
        st.update(source='user', need='none', models=models_installed('user'))
    elif kind == 'redownload':
        st.update(source='user', need='redownload', models=[dict(m, state='fallback') for m in models_installed('user')])
    elif kind == 'runtime':
        st['runtime'] = {'ok': False, 'python': '/usr/bin/python3', 'missing': ['python3-numpy', 'python3-onnxruntime']}
    elif kind == 'distribution':
        st.update(can_download=False, disabled_reason='distribution')
    elif kind == 'dictionary':  # 發行版自己重建字典，不在 pairing.lock 上
        st.update(can_download=False, disabled_reason='dictionary_mismatch')
    with open(path, 'w', encoding='utf-8') as f:
        json.dump({'status': st, 'frozen': frozen}, f, ensure_ascii=False)


def models(args):
    action = args[0] if args else ''
    log({'op': 'models-' + action})
    lock = open(MODELS + '.lock', 'w')
    fcntl.flock(lock, fcntl.LOCK_EX)
    with open(MODELS, encoding='utf-8') as f:
        doc = json.load(f)
    st = doc['status']
    op = st.get('operation')
    if action == 'status' and op and not doc.get('frozen'):
        if op['phase'] == 'downloading':
            op['done_bytes'] = min(op['total_bytes'], op['done_bytes'] + op['total_bytes'] // 3 + 1)
            if op['done_bytes'] >= op['total_bytes']:
                op['phase'] = 'verifying'
        elif op['phase'] == 'verifying':
            op['phase'] = 'pairing'
        else:
            st.update(operation=None, source='user', need='none', models=models_installed('user'))
    elif action == 'download':
        if not st['can_download']:
            print(json.dumps({'ok': False, 'error': {'code': 'unavailable', 'message': '這個安裝不能下載模型。'}}, ensure_ascii=False))
            return 1
        if op:
            print(json.dumps({'ok': False, 'error': {'code': 'in_progress', 'message': '模型正在下載。'}}, ensure_ascii=False))
            return 1
        st['operation'] = {'phase': 'downloading', 'done_bytes': 0, 'total_bytes': st['download_size'], 'started_at': '2026-10-21T08:00:00Z'}
        st['last_error'] = None
    elif action == 'cancel':
        st['operation'] = None
        st['last_error'] = {'code': 'canceled', 'message': '已取消下載，沒有留下檔案。', 'at': '2026-10-21T08:00:00Z'}
    elif action == 'remove':
        if op:
            print(json.dumps({'ok': False, 'error': {'code': 'in_progress', 'message': '模型正在下載，請先取消。'}}, ensure_ascii=False))
            return 1
        if st['source'] != 'user':
            print(json.dumps({'ok': False, 'error': {'code': 'bundled', 'message': '內建的模型不能刪除。'}}, ensure_ascii=False))
            return 1
        st.update(source='none', need='download', models=models_missing(), last_error=None)
    elif action != 'status':
        print(json.dumps({'ok': False, 'error': {'code': 'usage', 'message': '未知的 models 動作'}}, ensure_ascii=False))
        return 2
    tmp = MODELS + '.tmp'
    with open(tmp, 'w', encoding='utf-8') as f:
        json.dump(doc, f, ensure_ascii=False)
    os.replace(tmp, MODELS)
    print(json.dumps(st, ensure_ascii=False))
    return 0


def main():
    argv = sys.argv[1:]
    if argv[:1] == ['--models']:
        models_init(argv[1], argv[2])
        return 0
    if argv[:1] == ['models'] and MODELS:
        return models([x for x in argv[1:] if x not in ('-json', '--json')])
    if argv[:1] == ['--init']:
        global STATE
        STATE = argv[1]
        init(argv[2])
        if argv[3:4] == ['old']:
            state = load()
            state['old_updater'] = True
            save(state)
        return 0
    if argv[:1] == ['vocab']:
        rest = [x for x in argv[1:] if x not in ('-json', '--json')]
        if rest[:1] == ['watch']:
            since = int(rest[rest.index('-since') + 1]) if '-since' in rest else None
            try:
                watch(since)
            except BrokenPipeError:
                return 0
        if rest[:1] == ['request']:
            resp = vocab(json.load(sys.stdin))
            print(json.dumps(resp, ensure_ascii=False))
            return 0 if resp.get('ok') else 1
        if rest[:2] == ['add-as', 'assistant'] or rest[:1] == ['add-as']:
            source = rest[1]
            words = [{'word': rest[i], 'readings': [rest[i + 1]]} for i in range(2, len(rest), 2)]
            print(json.dumps(vocab({'op': 'add', 'source': source, 'words': words}), ensure_ascii=False))
            return 0
        print('fake-update: 不支援的 vocab 參數 %r' % rest, file=sys.stderr)
        return 2
    if argv[:1] == ['api']:
        print(json.dumps(api([x for x in argv[1:] if x not in ('-json', '--json')]), ensure_ascii=False))
        return 0
    real = os.environ['REAL_UPDATER']
    if argv[:1] in (['status'], ['settings']) and ('-json' in argv or '--json' in argv):
        import subprocess
        out = json.loads(subprocess.run([real] + argv, capture_output=True, text=True).stdout)
        if load().get('old_updater'):  # 舊版 0c-update 沒有 features 欄位
            out.pop('features', None)
        if argv[0] == 'settings':
            print(json.dumps(out, ensure_ascii=False))
            return 0
        # 假引擎「在執行」時，總覽與「我的詞彙」也要看到引擎在執行（真的 0c-update 找不到引擎）。
        if load().get('engine', True):
            out['runtime'] = dict(out.get('runtime') or {}, state='running', version='0.2.0-dev.125',
                                  selection_state='ready', punctuation_state='disabled_by_setting')
        print(json.dumps(out, ensure_ascii=False))
        return 0
    os.execv(real, [real] + argv)


if __name__ == '__main__':
    sys.exit(main())
