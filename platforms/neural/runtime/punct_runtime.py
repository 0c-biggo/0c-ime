#!/usr/bin/env python3
"""提交時補標點的 ONNX worker（zuin-punct-runtime-v1）。

協定（每行一個 JSON）：啟動後先輸出 ready；之後每行請求 {"id", "text"}，回覆 {"id", "edits": [{"offset", "text"}]}
或 {"id", "error"}。只插入標點、不改原字；offset 是原字串的字元（rune）索引，插在該字元之前。
插入規則：可插入位置由 eligible_boundaries 決定，段尾不補；各位置取標點機率最高者，
≥ 套件門檻才插入；開頭單個數字後不補頓號（見 ENUMERATORS）。長於一個窗口時依 manifest 的窗口與步長滑動，
每個位置取離窗口邊緣最遠的那一窗。manifest 的 input 決定模型輸入：echo（回聲輸入，窗口 64、步長 32；
舊套件沒有此欄位時的預設）或 bidirectional（punct-encoder-20260930 雙向 encoder，[BOS][任務] 原文，窗口 128、步長 64）。
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import sys

import numpy as np

SCHEMA = 'zuin-punct-runtime-v1'
LABELS = ['', '，', '。', '？', '！', '、', '；']
URL = re.compile(r'(?:https?://|www\.)[^\s，。！？、；「」『』（）【】]+', re.I)
MAX_TEXT = 256
# 新聞語料的「一、」「二、」條列標題讓模型在開頭數字後補頓號（「一本正經」→「一、本正經」）；輸入法提交的是片段，不補。
ENUMERATORS = set('一二三四五六七八九十壹貳參肆伍陸柒捌玖拾甲乙丙丁戊己庚辛壬癸')
MAX_FRAME = 1 << 16


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def is_han(char):
    return '㐀' <= char <= '鿿' or '\U00020000' <= char <= '\U000323af'


def eligible_boundaries(text):
    """與 punctuation_data.eligible_boundaries 逐字相同（worker 不依賴訓練程式）。"""
    protected = set()
    for match in URL.finditer(text):
        protected.update(range(match.start(), match.end()))
    allowed = []
    for i, char in enumerate(text):
        following = text[i + 1:]
        next_char = following[0] if following else ''
        next_visible = following.lstrip()[:1]
        allowed.append(is_han(char) and i not in protected
                       and (not next_char or is_han(next_char) or next_char.isspace())
                       and (not next_visible or is_han(next_visible)
                            or next_visible.isascii() and next_visible.isalnum()))
    return allowed


class Runtime:
    def __init__(self, package, threads=2):
        import onnxruntime as ort
        package = Path(package)
        self.manifest = m = json.loads((package / 'manifest.json').read_text())
        if m['schema'] != SCHEMA or m['labels'] != LABELS:
            raise ValueError('package_contract_mismatch')
        for name in ('model.onnx', 'vocab.json'):
            if sha(package / name) != m['files'][name]:
                raise ValueError('package_hash_mismatch')
        vocab = json.loads((package / 'vocab.json').read_text())
        self.chars = {c: i + 8 for i, c in enumerate(vocab['characters'])}
        self.threshold, self.window, self.stride = float(m['threshold']), int(m['window']), int(m['stride'])
        self.input = m.get('input', 'echo')
        limit = {'echo': 64, 'bidirectional': 128}.get(self.input)
        if limit is None or not 0 < self.threshold < 1 or not 8 <= self.window <= limit or not 0 < self.stride <= self.window:
            raise ValueError('package_contract_mismatch')
        options = ort.SessionOptions(); options.intra_op_num_threads = threads; options.inter_op_num_threads = 1
        options.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
        options.add_session_config_entry('session.intra_op.allow_spinning', '0')
        self.session = ort.InferenceSession(str(package / 'model.onnx'), sess_options=options, providers=['CPUExecutionProvider'])
        self.model_sha = m['files']['model.onnx']

    def window_probs(self, text):
        src = [self.chars.get(c, 4) for c in text]
        if self.input == 'bidirectional':
            ids = np.array([[1, 7] + src], dtype=np.int64)
            return self.session.run(['probs'], {'ids': ids})[0][2:]
        ids = np.array([[1, 7] + src + [2] + src], dtype=np.int64)
        types = np.array([[0] * (len(src) + 3) + [2] * len(src)], dtype=np.int64)
        probs = self.session.run(['probs'], {'ids': ids, 'types': types})[0]
        return probs[len(src) + 3:]

    def probabilities(self, text):
        n, W, S = len(text), self.window, self.stride
        starts = [0] if n <= W else list(range(0, n - W, S)) + [n - W]
        out, best = [None] * n, [-1] * n
        for s in starts:
            probs = self.window_probs(text[s:s + W]); m = len(probs)
            for j in range(m):
                margin = min(j, m - 1 - j) if n > W else 0
                if margin > best[s + j]:
                    best[s + j] = margin; out[s + j] = probs[j]
        return np.stack(out)

    def edits(self, text):
        if not isinstance(text, str) or not text or len(text) > MAX_TEXT:
            raise ValueError('unsupported_text')
        allowed = eligible_boundaries(text)
        if not any(allowed[:-1]):
            return []
        probs = self.probabilities(text)
        out = []
        for i in range(len(text) - 1):   # 段尾不補
            k = int(np.argmax(probs[i, 1:])) + 1
            if i == 0 and LABELS[k] == '、' and text[0] in ENUMERATORS:
                continue
            if allowed[i] and float(probs[i, k]) >= self.threshold:
                out.append({'offset': i + 1, 'text': LABELS[k]})
        return out


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--package', type=Path, required=True)
    p.add_argument('--threads', type=int, default=2)
    a = p.parse_args()
    if not 1 <= a.threads <= 8:
        p.error('threads must be between 1 and 8')
    runtime = Runtime(a.package, a.threads)
    runtime.edits('暖機只用固定字串不含使用者文字')
    print(json.dumps({'ready': True, 'schema': SCHEMA, 'model_sha256': runtime.model_sha}), flush=True)
    while True:
        line = sys.stdin.buffer.readline(MAX_FRAME + 1)
        if not line:
            return
        if len(line) > MAX_FRAME or not line.endswith(b'\n'):
            raise ValueError('request_frame_limit')
        r = json.loads(line)
        reply = {'id': r['id']}
        try:
            reply['edits'] = runtime.edits(r['text'])
        except (ValueError, KeyError, TypeError) as e:
            reply['error'] = str(e) if isinstance(e, ValueError) else 'bad_request'
        sys.stdout.write(json.dumps(reply, ensure_ascii=False) + '\n'); sys.stdout.flush()


if __name__ == '__main__':
    main()
