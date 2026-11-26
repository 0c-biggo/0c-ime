#!/usr/bin/env python3
"""Tests of libqsel that need no model: a small random model in the same format.

  python3 -I qsel_test.py --lib build/libqsel.so

Checks scores against the float64 reference (qsel_reference.py) on random legal
inputs with odd sizes (vocabulary, rows and positions not multiples of the tiles),
that 1, 2 and 3 threads give the same bits, that the packed entry point matches,
that a row with nothing allowed is weighed as onnxruntime does, that the prefix
cache gives the same bits as scoring without it (typing sequences, and inputs where
the prefix must not be reused), and that bad inputs and damaged files are refused.
Needs numpy.
"""
import argparse
import ctypes
import os
import sys
import tempfile

import numpy as np

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import qsel_convert as conv  # noqa: E402
import qsel_reference as ref  # noqa: E402
from qsel_parity import NAMES, Inputs, Qsel, build_inputs, pack, random_inputs  # noqa: E402

CFG = dict(hidden=128, layers=2, heads=2, head_dim=64, inter=96, vocab=301, n_bases=20, n_tones=6, n_types=3,
           block=16, reading_type=1, eps=float(np.float32(1e-5)), attn_scale=0.125)


def build(path, seed=1):
    rng = np.random.default_rng(seed)
    D, I, V = CFG['hidden'], CFG['inter'], CFG['vocab']
    out_ids = np.concatenate([[4, 6], np.arange(8, V)]).astype(np.int64)
    cfg = dict(CFG, n_out=len(out_ids), inv_freq=(10000.0 ** (-np.arange(0, 64, 2) / 64)).astype(np.float32))
    blobs = []

    def f32(slot, a):
        a = np.ascontiguousarray(a, np.float32)
        blobs.append((slot, conv.DT_F32, a.shape[0], a.shape[1], a.tobytes()))

    def q4(slot, n, k, scale):
        q = rng.integers(0, 256, (n, k // 2), dtype=np.uint8)
        blobs.append((slot, conv.DT_Q4B16, n, k, q.tobytes()))
        f32(slot + 1, rng.uniform(0.2, 1.0, (n, k // 16)) * scale)
    norm = lambda: 1 + 0.1 * rng.standard_normal((1, D))
    f32(conv.S_EMB_NORM, norm())
    f32(conv.S_FINAL_NORM, norm())
    f32(conv.S_HEAD_DENSE, rng.standard_normal((D, D)) / np.sqrt(D))
    f32(conv.S_HEAD_NORM, norm())
    f32(conv.S_DEC_EMB, rng.standard_normal((V, D)))
    f32(conv.S_BASE_EMB, rng.standard_normal((CFG['n_bases'], D)))
    f32(conv.S_TONE_EMB, rng.standard_normal((CFG['n_tones'], D)))
    f32(conv.S_TYPE_EMB, rng.standard_normal((CFG['n_types'], D)))
    blobs.append((conv.S_OUT_IDS, conv.DT_I64, 1, len(out_ids), out_ids.tobytes()))
    bias = rng.standard_normal((1, V))
    bias[0, np.setdiff1d(np.arange(V), out_ids)] = -np.inf
    f32(conv.S_OUT_BIAS, bias)
    q4(conv.S_OUT_Q, V, D, 0.1)
    for l in range(CFG['layers']):
        b = conv.S_LAYER0 + l * conv.S_LAYER_STRIDE
        if l:
            f32(b + conv.L_ATTN_NORM, norm())
        f32(b + conv.L_MLP_NORM, norm())
        for s_, n, k in ((conv.L_Q_Q, D, D), (conv.L_K_Q, D, D), (conv.L_V_Q, D, D), (conv.L_O_Q, D, D),
                         (conv.L_UP_Q, 2 * I, D), (conv.L_DOWN_Q, D, I)):
            q4(b + s_, n, k, 0.6 / np.sqrt(k))
    data, table = b'', []
    for slot, dt, rows, cols, raw in sorted(blobs):
        data += b'\0' * (-len(data) % 64)  # the smallest alignment the runtime accepts
        table.append((slot, dt, rows, cols, len(data), len(raw)))
        data += raw
    import hashlib
    h = conv.header(cfg, table, conv.DATA_ALIGN, len(data), hashlib.sha256(data).digest(), b'\0' * 32)
    with open(path, 'wb') as f:
        f.write(h + b'\0' * (conv.DATA_ALIGN - len(h)) + data)


def typing(rng, steps):
    """同一句打字時的請求：前文不變，讀音一次多一個音節或換掉最後一個音節，候選每次都變。"""
    V = CFG['vocab']
    ctx = [int(rng.integers(8, V)) for _ in range(int(rng.choice([0, int(rng.integers(1, 30))])))]
    sounds = [(int(rng.integers(2, CFG['n_bases'])), int(rng.integers(0, 6)))]
    out = []
    while len(out) < steps:
        if len(sounds) < 2 or rng.random() < 0.6:
            sounds.append((int(rng.integers(2, CFG['n_bases'])), int(rng.integers(0, 6))))
        else:
            sounds[-1] = (int(rng.integers(2, CFG['n_bases'])), int(rng.integers(0, 6)))
        n = len(sounds)
        first = [int(rng.integers(8, V)) for _ in range(n)]
        cands = {tuple(first)}
        while len(cands) < int(rng.integers(2, 9)):
            cands.add(tuple(first[i] if rng.random() < 0.6 else int(rng.integers(8, V)) for i in range(n)))
        text = rng.random() < 0.1
        neutral = not text and sounds[-1][1] not in (0, 5) and rng.random() < 0.3
        out.append(build_inputs(ctx, sounds, [list(c) for c in sorted(cands)], text, neutral))
    return out


def prefix_tests(lib, path, check):
    """前綴快取的分數跟不開快取逐位元相同，而且只在確定相同的地方沿用。"""
    plain = Qsel(lib, path, None, 1)
    cached = [Qsel(lib, path, None, t) for t in (1, 3)]
    check(cached[0].prefix_cache(-1) == 3, 'negative prefix rows accepted')
    check(cached[0].prefix_cache(1000) == 0 and cached[1].prefix_cache(24) == 0, 'prefix cache refused')
    rng = np.random.default_rng(336)
    reused = rows = calls = 0
    seqs = [typing(rng, int(rng.integers(3, 9))) for _ in range(12)]
    # 中間插入：重複上一個請求、另一句的請求、候選樹有一列整列遮掉的請求
    seqs.insert(3, [seqs[2][-1]])
    y = {k: v.copy() for k, v in seqs[5][-1].items()}
    y['mask'][0, 0, -1, :] = False
    seqs.insert(6, [y])
    for seq in seqs:
        for x in seq:
            want = plain.score(x).view(np.uint32)
            for q in cached:
                check(np.array_equal(q.score(x).view(np.uint32), want), f'prefix cache changed the scores (call {calls})')
            r, kept, nbytes = cached[0].prefix_info()
            check(kept <= x['ids'].shape[-1] and nbytes > 0, f'call {calls}: kept {kept} rows, {nbytes} bytes')
            check(cached[1].prefix_info()[1] <= 24, 'the cache kept more rows than allowed')
            reused, rows, calls = reused + r, rows + x['ids'].shape[-1], calls + 1
    check(reused > rows // 4, f'typing reused only {reused} of {rows} rows')
    print(f'prefix cache: {calls} calls in {len(seqs)} sentences, same bits, {reused} of {rows} rows reused')

    # 輸入大多相同、但不能全部沿用的情況：沿用的話快取的列會算錯。
    base = typing(np.random.default_rng(5), 3)[-1]
    L = base['ids'].shape[-1]
    S = int(np.nonzero(base['ids'][0] == 3)[0][0]) - 1  # 最後一個音節分支之前的列數
    proj0 = int(base['proj_pos'].min())

    def edited(name, fn, want_reused):
        y = {k: v.copy() for k, v in base.items()}
        fn(y)
        want = plain.score(y).view(np.uint32)
        q = cached[0]
        q.score(base)
        got = q.score(y).view(np.uint32)
        r = q.prefix_info()[0]
        check(np.array_equal(got, want), f'{name}: scores differ from scoring without the cache')
        check(r == want_reused, f'{name}: reused {r} rows, want {want_reused}')
        q.score(y)  # 反過來：快取 y，再算 base
        check(np.array_equal(q.score(base).view(np.uint32), plain.score(base).view(np.uint32)), f'{name}: back to base differs')
    r = S // 2
    edited('same inputs', lambda y: None, proj0)
    edited('a tone changed', lambda y: y['tones'].__setitem__((0, r), (y['tones'][0, r] + 1) % 6), r)
    edited('a position changed', lambda y: y['positions'].__setitem__((0, r), y['positions'][0, r] + 1), r)
    edited('a prefix row attends past the prefix', lambda y: y['mask'].__setitem__((0, 0, r, L - 1), True), r)
    edited('a prefix row attends less', lambda y: y['mask'].__setitem__((0, 0, r, 0), False), r)
    edited('a prefix row attends nothing', lambda y: y['mask'].__setitem__((0, 0, r, slice(None)), False), r)
    edited('a projected row in the prefix', lambda y: y.__setitem__('proj_pos', np.concatenate([[r], y['proj_pos']])), r)

    # 記憶體要還得回來：關掉快取時，以及記憶體警告（release_pages）時。
    q = cached[0]
    q.score(base)
    check(q.prefix_info()[2] > 0, 'nothing kept')
    q.release_pages()
    check(q.prefix_info()[1:] == (0, 0), 'release_pages kept the prefix')
    q.score(base)
    q.score(base)
    check(q.prefix_info()[0] > 0, 'no reuse after release_pages')
    check(q.prefix_cache(0) == 0 and q.prefix_info()[1:] == (0, 0), 'turning the cache off kept it')
    q.score(base)
    check(q.prefix_info() == (0, 0, 0), 'a cache that is off kept rows')
    for h in [plain] + cached:
        h.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--lib', required=True)
    ap.add_argument('--cases', type=int, default=60)
    args = ap.parse_args()
    failures = []

    def check(cond, what):
        if not cond:
            failures.append(what)
            print('FAIL', what)
    with tempfile.TemporaryDirectory() as tmp:
        path = os.path.join(tmp, 'tiny.qsel')
        build(path)
        model = ref.load(path)
        q1, q2, q3 = (Qsel(args.lib, path, None, t) for t in (1, 2, 3))
        rng = np.random.default_rng(7)
        worst = 0.0
        for i in range(args.cases):
            x, _ = random_inputs(rng, vocab=CFG['vocab'], n_bases=CFG['n_bases'], max_syllables=12)
            if i % 10 == 9:  # a row with nothing allowed
                x['mask'] = x['mask'].copy()
                x['mask'][0, 0, x['mask'].shape[-1] // 2, :] = False
            want = ref.forward(model, x)
            got = [q.score(x) for q in (q1, q2, q3)]
            d = float(np.max(np.abs(got[0] - want) / np.maximum(1.0, np.abs(want))))
            worst = max(worst, d)
            check(d < 2e-5, f'case {i}: relative difference {d:.2e} from float64')
            check(all(np.array_equal(got[0], g) for g in got[1:]), f'case {i}: thread counts differ')
            buf = pack(x)
            for shift in (0, 3):  # aligned and unaligned buffers
                b = (ctypes.c_char * (len(buf) + shift)).from_buffer_copy(b'\0' * shift + buf)
                out = np.zeros(want.size, np.float32)
                n = ctypes.c_size_t()
                q1.lib.qsel_score_packed.argtypes = [ctypes.c_void_p, ctypes.c_void_p, ctypes.c_size_t, ctypes.c_void_p,
                                                     ctypes.c_size_t, ctypes.POINTER(ctypes.c_size_t)]
                rc = q1.lib.qsel_score_packed(q1.h, ctypes.addressof(b) + shift, len(buf), out.ctypes.data, out.size,
                                              ctypes.byref(n))
                check(rc == 0 and n.value == want.size and np.array_equal(out, got[0]), f'case {i}: packed (shift {shift})')
        print(f'{args.cases} random cases, largest relative difference from float64 {worst:.2e}')
        prefix_tests(args.lib, path, check)

        # refused inputs
        x, _ = random_inputs(rng, vocab=CFG['vocab'], n_bases=CFG['n_bases'], max_syllables=6)
        for name, edit in (('id', lambda y: y['ids'].__setitem__((0, 0), CFG['vocab'])),
                           ('base', lambda y: y['bases'].__setitem__((0, 0), -1)),
                           ('proj_pos', lambda y: y['proj_pos'].__setitem__(0, y['ids'].shape[1])),
                           ('target id', lambda y: y['target_ids'].__setitem__((0, 0), 10 ** 6))):
            y = {k: v.copy() for k, v in x.items()}
            edit(y)
            arrs = {n: np.ascontiguousarray(y[n], dtype=np.uint8 if n == 'mask' else np.int64) for n in NAMES}
            R, N = arrs['target_ids'].shape
            inp = Inputs(arrs['ids'].shape[-1], arrs['proj_pos'].shape[0], R, N, *[arrs[n].ctypes.data for n in NAMES])
            out = np.zeros(R, np.float32)
            check(q1.lib.qsel_score(q1.h, ctypes.byref(inp), out.ctypes.data) == 3, f'bad {name} accepted')
        buf = pack(x)
        out = np.zeros(64, np.float32)
        short = ctypes.create_string_buffer(buf[:-1], len(buf) - 1)
        check(q1.lib.qsel_score_packed(q1.h, ctypes.addressof(short), len(buf) - 1, out.ctypes.data, 64, None) == 3,
              'short packed buffer accepted')
        for q in (q1, q2, q3):
            q.close()

        # damaged files
        raw = open(path, 'rb').read()
        for name, bad in (('header byte', raw[:200] + bytes([raw[200] ^ 1]) + raw[201:]),
                          ('truncated data', raw[:-100]),
                          ('magic', b'X' + raw[1:])):
            p = os.path.join(tmp, 'bad.qsel')
            open(p, 'wb').write(bad)
            try:
                Qsel(args.lib, p, None, 1)
                check(False, f'{name} accepted')
            except RuntimeError:
                pass
        # header-only file with the data beside it
        hdr_len = int.from_bytes(raw[12:16], 'little')
        cfg_tab = raw[:hdr_len]
        import struct
        import zlib
        h = bytearray(cfg_tab)
        struct.pack_into('<Q', h, 24, 0)
        struct.pack_into('<I', h, 16, 0)
        struct.pack_into('<I', h, 16, zlib.crc32(bytes(h)) & 0xffffffff)
        hp, dp = os.path.join(tmp, 'tiny.qselh'), os.path.join(tmp, 'tiny.data')
        open(hp, 'wb').write(h)
        open(dp, 'wb').write(raw[conv.DATA_ALIGN:])
        qs = Qsel(args.lib, hp, dp, 2)
        x, _ = random_inputs(rng, vocab=CFG['vocab'], n_bases=CFG['n_bases'], max_syllables=8)
        qa = Qsel(args.lib, path, None, 2)
        check(np.array_equal(qs.score(x), qa.score(x)), 'header-only file differs')
        qs.close()
        qa.close()
    print('ok' if not failures else f'{len(failures)} failures')
    sys.exit(1 if failures else 0)


if __name__ == '__main__':
    main()
