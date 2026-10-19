#!/usr/bin/env python3
"""序列模型（PF）的 ONNX 本機 stdio worker；協定同 encoder_runtime，另收外殼確認的前文。

以前綴樹一次評分全部候選（共用前綴與候選共同開頭），分數等同逐候選評分。

無 Torch、網路、學習或背景改字。前文只用最後 64 字；字表外的前文字元視為 UNK（同訓練），
候選含字表外字元或讀音不支援時整例回退。

mode="text"（單音節聲調容錯）：只用前文續寫評分 logP(候選｜前文)，不放讀音 token，
讀音只用來核對長度；必須有前文。規則同 SequenceModel 的 text task。
套件宣告 neutral_tolerant_last（一聲音節清單）時，讀音最後一個音節若在清單內，另以輕聲讀音評分，
逐候選取較大者（同一次 ONNX 呼叫的分支）。
"""
import argparse
import hashlib
import json
import math
from pathlib import Path
import sys

import numpy as np

MAX_FRAME = 65536
SCHEMA = 'zuin-sequence-runtime-v1'
CONTEXT_POLICY = 'confirmed_left_64'
TONES = {'ˊ': 2, 'ˇ': 3, 'ˋ': 4, '˙': 5}
MAX_CANDIDATES = 16   # 同 ime rerank.MaxCandidates：打出讀音 8 個＋輕聲擴充最多 8 個


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def syllable(y, bases):
    if not isinstance(y, str) or not y:
        raise ValueError('invalid_reading')
    tone = TONES.get(y[-1], 1)
    base = y[:-1] if y[-1] in TONES else y
    if base not in bases:
        raise ValueError('unknown_base')
    return bases[base], tone


def prepare(context, reading, candidates, maps, allow_partial=False, text_only=False, neutral_last=False):
    """前綴樹輸入：[BOS] 前文 [SEP] 讀音 [OUT] 只出現一次，候選共同開頭共用節點。

    每個節點只注意前綴與自己的祖先，位置編號同單獨評分，因此分數等同
    SequenceModel.task_logits（phonetic）逐候選評分；proj_pos 是需要輸出分布的唯一位置。
    text_only=True 時省略讀音 token（text task），只支援單音節且需有前文。
    neutral_last=True 且最後一個音節不是輕聲時，另加一支「最後音節改輕聲」的分支（共用前文與前面讀音）；
    target 列依分支排列（每支 len(candidates) 列），由呼叫端逐候選取最大值。
    """
    bases, chars = maps
    n = len(reading)
    if not (n == 1 if text_only else 2 <= n <= 32) or not 2 <= len(candidates) <= MAX_CANDIDATES:
        raise ValueError('unsupported_shape')
    if len({c['text'] for c in candidates}) != len(candidates):
        raise ValueError('duplicate_candidates')
    if not isinstance(context, str) or (text_only and not context):
        raise ValueError('invalid_context')
    encoded = []
    for candidate in candidates:
        text = candidate['text']
        if len(text) != n or not math.isfinite(candidate['log_score']):
            raise ValueError('candidate_alignment_or_score')
        end = 0
        for seg in candidate['segments']:
            if seg['Start'] != end or not end < seg['End'] <= n or (seg['Partial'] and not allow_partial) \
                    or seg['Correction'] or seg['Word'] != text[end:seg['End']]:
                raise ValueError('unsupported_segment')
            end = seg['End']
        if end != n:
            raise ValueError('incomplete_segments')
        try:
            encoded.append([chars[c] for c in text])
        except KeyError as e:
            raise ValueError('unknown_character') from e
    sounds = [] if text_only else [syllable(y, bases) for y in reading]
    # 讀音最後一個音節之前共用；最後一個音節（與 OUT）依變體分支，各自接一棵候選樹。
    branches = [[(0, 0)]] if text_only else [[sounds[-1], (0, 0)]]
    if neutral_last and not text_only and sounds[-1][1] != 5:
        branches.append([(sounds[-1][0], 5), (0, 0)])
    head = [1] + [chars.get(c, 4) for c in context[-64:]] + [2]
    shared = sounds[:-1]
    ids = head + [0] * len(shared)
    base_ids = [0] * len(head) + [b for b, _ in shared]
    tones = [0] * len(head) + [x for _, x in shared]
    types = [0] * len(head) + [1] * len(shared)
    S = len(ids); positions = list(range(S)); parent = {}; nodes = []
    for branch in branches:
        prev = None
        for k, (b, t) in enumerate(branch):
            if prev is not None:
                parent[len(ids)] = prev
            prev = len(ids)
            last = k == len(branch) - 1
            ids.append(3 if last else 0); base_ids.append(b); tones.append(t); types.append(0 if last else 1); positions.append(S + k)
        P = S + len(branch)   # OUT 之後第一個候選字的位置
        node = {(): prev}
        for cids in encoded:
            for i in range(1, n):
                key = tuple(cids[:i])
                if key not in node:
                    node[key] = len(ids); parent[len(ids)] = node[key[:-1]]
                    ids.append(cids[i - 1]); base_ids.append(0); tones.append(0); types.append(2); positions.append(P + i - 1)
        nodes.append(node)
    L = len(ids)
    mask = np.zeros((L, L), dtype=bool); mask[:S, :S] = np.tril(np.ones((S, S), dtype=bool))
    for q in range(S, L):
        mask[q, :S] = True; a = q
        while a is not None:
            mask[q, a] = True; a = parent.get(a)
    proj = sorted({v for node in nodes for v in node.values()}); index = {p: i for i, p in enumerate(proj)}
    target_node = np.array([[index[node[tuple(c[:i])]] for i in range(n)] for node in nodes for c in encoded], dtype=np.int64)
    target_ids = np.array([[x - 6 for x in c] for _ in nodes for c in encoded], dtype=np.int64)
    row = lambda x: np.array([x], dtype=np.int64)
    return {'ids': row(ids), 'bases': row(base_ids), 'tones': row(tones), 'types': row(types), 'positions': row(positions),
            'mask': mask[None, None], 'proj_pos': np.array(proj, dtype=np.int64), 'target_node': target_node, 'target_ids': target_ids}


class Runtime:
    def __init__(self, package, dictionary, threads=2):
        import onnxruntime as ort
        package = Path(package)
        self.manifest = m = json.loads((package / 'manifest.json').read_text())
        if m['schema'] != SCHEMA or m['context_policy'] != CONTEXT_POLICY or m['dictionary_sha256'] != dictionary:
            raise ValueError('package_contract_mismatch')
        for name in ('model.onnx', 'vocab.json'):
            if sha(package / name) != m['files'][name]:
                raise ValueError('package_hash_mismatch')
        vocab = json.loads((package / 'vocab.json').read_text())
        if vocab['dictionary_sha256'] != dictionary:
            raise ValueError('vocabulary_dictionary_mismatch')
        self.maps = ({x: i + 2 for i, x in enumerate(vocab['bases'])}, {x: i + 8 for i, x in enumerate(vocab['characters'])})
        self.vocab = vocab
        options = ort.SessionOptions(); options.intra_op_num_threads = threads; options.inter_op_num_threads = 1
        options.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
        options.add_session_config_entry('session.intra_op.allow_spinning', '0')
        self.session = ort.InferenceSession(str(package / 'model.onnx'), sess_options=options, providers=['CPUExecutionProvider'])
        self.model_sha = m['files']['model.onnx']; self.dictionary = dictionary
        self.allow_partial = bool(m.get('allow_partial', False))
        self.text_mode = float(m.get('text_weight', 0)) > 0
        # 句尾助詞常被打成一聲：最後一個音節是清單中的一聲音節時，同時以輕聲評分取較大者。
        # 套件宣告 neutral_expand_penalty 時，宿主會併入輕聲讀音的候選，清單基底的任何非輕聲聲調都另以輕聲評分。
        self.neutral_tolerant = frozenset(m.get('neutral_tolerant_last', []))
        self.neutral_expand = float(m.get('neutral_expand_penalty', 0)) > 0
        self.neutral_bases = frozenset(y[:-1] if y and y[-1] in TONES else y for y in self.neutral_tolerant)

    def score(self, request):
        if request['dictionary_sha256'] != self.dictionary or request['model_sha256'] != self.model_sha:
            raise ValueError('request_version_mismatch')
        if bool(request.get('allow_partial', False)) != self.allow_partial:
            raise ValueError('candidate_policy_mismatch')
        mode = request.get('mode', '')
        if mode not in ('', 'text') or (mode == 'text' and not self.text_mode):
            raise ValueError('unsupported_mode')
        reading, count = request['reading'], len(request['candidates'])
        last = reading[-1] if reading and isinstance(reading[-1], str) else ''
        if self.neutral_expand:
            neutral = mode == '' and bool(last) and last[-1] != '˙' and (last[:-1] if last[-1] in TONES else last) in self.neutral_bases
        else:
            neutral = mode == '' and last in self.neutral_tolerant
        inputs = prepare(request.get('context', ''), reading, request['candidates'], self.maps, self.allow_partial,
                         text_only=mode == 'text', neutral_last=neutral)
        raw = self.session.run(['scores'], inputs)[0].astype(float)
        if raw.shape[0] % count or not np.isfinite(raw).all():
            raise ValueError('nonfinite_neural_score')
        return raw.reshape(-1, count).max(0).tolist()   # 各讀音分支逐候選取最大


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--package', type=Path, required=True); p.add_argument('--dictionary', required=True)
    p.add_argument('--threads', type=int, default=2)
    a = p.parse_args()
    if not 1 <= a.threads <= 8:
        p.error('threads must be between 1 and 8')
    runtime = Runtime(a.package, a.dictionary, a.threads)
    # 暖機只用字表，不含新聞、私人輸入或評測資料。
    chars = runtime.vocab['characters'][8:12]; base = runtime.vocab['bases'][0]
    seg = lambda t: {'text': t, 'log_score': 0., 'segments': [{'Start': 0, 'End': 2, 'Word': t, 'Partial': False, 'Correction': False}]}
    warm = {'dictionary_sha256': a.dictionary, 'model_sha256': runtime.model_sha, 'context': ''.join(chars),
            'reading': [base] * 2, 'candidates': [seg(chars[0] + chars[1]), seg(chars[2] + chars[3])],
            'allow_partial': runtime.allow_partial}
    runtime.score(warm)
    print(json.dumps({'ready': True, 'schema': SCHEMA, 'model_sha256': runtime.model_sha, 'dictionary_sha256': a.dictionary,
                      'allow_partial': runtime.allow_partial}), flush=True)
    while True:
        line = sys.stdin.buffer.readline(MAX_FRAME + 1)
        if not line:
            return
        if len(line) > MAX_FRAME or not line.endswith(b'\n'):
            raise ValueError('request_frame_limit')
        r = json.loads(line)
        reply = {'id': r['id'], 'revision': r['revision'], 'model_sha256': runtime.model_sha, 'dictionary_sha256': a.dictionary}
        try:
            reply['scores'] = runtime.score(r)
        except (ValueError, KeyError, TypeError):
            reply['fallback'] = 'unsupported_request'
        print(json.dumps(reply, allow_nan=False), flush=True)


if __name__ == '__main__':
    main()
