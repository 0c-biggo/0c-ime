#!/usr/bin/env python3
"""Compare qsel with onnxruntime on the same model, value by value.

Runs every accepted case of the models sequence-prepare-v2 fixtures plus random
legal requests (built the way APP mobile/neural prepare.go builds them) through
model.onnx under onnxruntime (the fixtures' session settings) and through libqsel,
and checks |qsel - ort| <= max(1e-4, 1e-6 * |ort|) and the same candidate order.

  python3 -I qsel_parity.py --lib libqsel.so --qsel model.qsel \
      --onnx model.onnx --prepare .../sequence-prepare-v2/fixtures.json --random 200

Needs numpy and onnxruntime (platforms/neural/requirements.txt versions).
"""
import argparse
import base64
import ctypes
import json
import os
import sys

import numpy as np

NAMES = ['ids', 'bases', 'tones', 'types', 'positions', 'mask', 'proj_pos', 'target_node', 'target_ids']


class Inputs(ctypes.Structure):
    _fields_ = [('L', ctypes.c_int64), ('P', ctypes.c_int64), ('R', ctypes.c_int64), ('N', ctypes.c_int64)] + [
        (n, ctypes.c_void_p) for n in NAMES]


class Qsel:
    def __init__(self, lib, path, data_path=None, threads=2):
        self.lib = ctypes.CDLL(lib)
        self.lib.qsel_open.argtypes = [ctypes.c_char_p, ctypes.c_char_p, ctypes.c_int, ctypes.POINTER(ctypes.c_void_p)]
        self.lib.qsel_score.argtypes = [ctypes.c_void_p, ctypes.POINTER(Inputs), ctypes.c_void_p]
        self.lib.qsel_strerror.restype = ctypes.c_char_p
        self.lib.qsel_close.argtypes = [ctypes.c_void_p]
        self.h = ctypes.c_void_p()
        rc = self.lib.qsel_open(path.encode(), data_path.encode() if data_path else None, threads, ctypes.byref(self.h))
        if rc:
            raise RuntimeError(f'qsel_open: {self.lib.qsel_strerror(rc).decode()}')

    def score(self, x):
        arrs = {n: np.ascontiguousarray(x[n], dtype=np.uint8 if n == 'mask' else np.int64) for n in NAMES}
        L = arrs['ids'].shape[-1]
        R, N = arrs['target_ids'].shape
        inp = Inputs(L, arrs['proj_pos'].shape[0], R, N, *[arrs[n].ctypes.data for n in NAMES])
        out = np.zeros(R, np.float32)
        rc = self.lib.qsel_score(self.h, ctypes.byref(inp), out.ctypes.data)
        if rc:
            raise RuntimeError(f'qsel_score: {self.lib.qsel_strerror(rc).decode()}')
        return out

    def close(self):
        self.lib.qsel_close(self.h)


def session(path, threads=2):
    import onnxruntime as ort
    o = ort.SessionOptions()
    o.intra_op_num_threads = threads
    o.inter_op_num_threads = 1
    o.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
    o.add_session_config_entry('session.intra_op.allow_spinning', '0')
    return ort.InferenceSession(path, o, providers=['CPUExecutionProvider'])


def fixture_inputs(path):
    d = json.load(open(path))
    for c in d['cases']:
        e = c['expected']
        if 'inputs' not in e:
            continue
        x = {}
        for n in NAMES:
            t = e['inputs'][n]
            if n == 'mask':
                assert t['encoding'] == 'packbits-big-rowmajor-base64'
                size = int(np.prod(t['shape']))
                bits = np.unpackbits(np.frombuffer(base64.b64decode(t['data']), np.uint8), bitorder='big')[:size]
                x[n] = bits.astype(np.bool_).reshape(t['shape'])
                assert int(x[n].sum()) == t['true_count']
            else:
                x[n] = np.array(t['data'], dtype=np.int64).reshape(t['shape'])
        yield c['name'], x, len(c['request']['candidates'])


def random_inputs(rng, vocab=27865, n_bases=418, max_syllables=32):
    """One legal request, as prepare.go builds it from token ids."""
    text = rng.random() < 0.1
    n = int(rng.integers(1 if text else 2, max_syllables + 1))
    if text and n == 1:
        ctx_len = int(rng.integers(1, 65))
    else:
        ctx_len = int(rng.choice([0, int(rng.integers(1, 65))]))
    ncand = int(rng.integers(2, 17))
    ctx = [4 if rng.random() < 0.02 else int(rng.integers(8, vocab)) for _ in range(ctx_len)]
    ids = [1] + ctx + [2]
    bases, tones, types = [0] * len(ids), [0] * len(ids), [0] * len(ids)
    unknown = rng.random() < 0.5
    sounds = [(int(rng.integers(2, n_bases)), 0 if unknown and rng.random() < 0.7 else int(rng.integers(1, 6)))
              for _ in range(n)]
    # candidates share prefixes, as engine candidates do
    first = [int(rng.integers(8, vocab)) for _ in range(n)]
    cands, seen = [], set()
    while len(cands) < ncand:
        c = list(first)
        for i in range(n):
            if rng.random() < (0.5 if i >= n // 2 else 0.15):
                c[i] = int(rng.integers(8, vocab))
        if tuple(c) not in seen:
            seen.add(tuple(c))
            cands.append(c)
    if text:
        branches = [[(0, 0)]]
        shared = []
    else:
        last = sounds[-1]
        branches = [[last, (0, 0)]]
        if last[1] not in (0, 5) and rng.random() < 0.3:
            branches.append([(last[0], 5), (0, 0)])
        shared = sounds[:-1]
    for b, t in shared:
        ids.append(0); bases.append(b); tones.append(t); types.append(1)
    S = len(ids)
    positions = list(range(S))
    parent = {}

    def push(i, b, t, k, pos):
        ids.append(i); bases.append(b); tones.append(t); types.append(k); positions.append(pos)
        return len(ids) - 1
    nodes = []
    for branch in branches:
        prev = -1
        for k, (b, t) in enumerate(branch):
            lastk = k == len(branch) - 1
            at = push(3 if lastk else 0, b, t, 0 if lastk else 1, S + k)
            if prev >= 0:
                parent[at] = prev
            prev = at
        P0 = S + len(branch)
        node = {(): prev}
        for c in cands:
            for i in range(1, n):
                key = tuple(c[:i])
                if key in node:
                    continue
                at = push(c[i - 1], 0, 0, 2, P0 + i - 1)
                parent[at] = node[tuple(c[:i - 1])]
                node[key] = at
        nodes.append(node)
    L = len(ids)
    mask = np.zeros((L, L), np.bool_)
    for q in range(S):
        mask[q, :q + 1] = True
    for q in range(S, L):
        mask[q, :S] = True
        a = q
        while True:
            mask[q, a] = True
            if a not in parent:
                break
            a = parent[a]
    proj = sorted({v for node in nodes for v in node.values()})
    index = {v: i for i, v in enumerate(proj)}
    tn, ti = [], []
    for node in nodes:
        for c in cands:
            tn.append([index[node[tuple(c[:i])]] for i in range(n)])
            ti.append([x - 6 for x in c])
    a = lambda v: np.array(v, np.int64).reshape(1, -1)
    return {'ids': a(ids), 'bases': a(bases), 'tones': a(tones), 'types': a(types), 'positions': a(positions),
            'mask': mask.reshape(1, 1, L, L), 'proj_pos': np.array(proj, np.int64),
            'target_node': np.array(tn, np.int64), 'target_ids': np.array(ti, np.int64)}, ncand


def unpack(buf):
    """APP mobile/neural Pack layout back to the nine tensors."""
    L, P, R, N = np.frombuffer(buf[:32], '<i8')
    words = 4 + 5 * L + P + 2 * R * N
    v = np.frombuffer(buf[:8 * words], '<i8')[4:]
    x, at = {}, 0
    for n, size in (('ids', L), ('bases', L), ('tones', L), ('types', L), ('positions', L)):
        x[n] = v[at:at + size].reshape(1, L).copy(); at += size
    x['proj_pos'] = v[at:at + P].copy(); at += P
    x['target_node'] = v[at:at + R * N].reshape(R, N).copy(); at += R * N
    x['target_ids'] = v[at:at + R * N].reshape(R, N).copy()
    x['mask'] = np.frombuffer(buf[8 * words:], np.uint8).astype(np.bool_).reshape(1, 1, L, L)
    return x


def pack(x):
    L = x['ids'].shape[-1]
    R, N = x['target_ids'].shape
    head = np.array([L, x['proj_pos'].shape[0], R, N], '<i8')
    parts = [head] + [np.asarray(x[n], '<i8').ravel() for n in ('ids', 'bases', 'tones', 'types', 'positions', 'proj_pos', 'target_node', 'target_ids')]
    return b''.join(p.tobytes() for p in parts) + np.asarray(x['mask'], np.uint8).ravel().tobytes()


def read_packed(path):
    """Records of uint64 length + packed inputs (the capture tool's output)."""
    data = open(path, 'rb').read()
    at = 0
    while at < len(data):
        n = int(np.frombuffer(data[at:at + 8], '<u8')[0])
        yield data[at + 8:at + 8 + n]
        at += 8 + n


def order(scores, ncand):
    best = scores.reshape(-1, ncand).max(axis=0)
    return sorted(range(ncand), key=lambda i: -best[i]), best


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--lib')
    ap.add_argument('--qsel')
    ap.add_argument('--data', help='model.onnx.data when --qsel is a header-only file')
    ap.add_argument('--onnx', required=True)
    ap.add_argument('--prepare', help='models data/fixtures/sequence-prepare-v2/fixtures.json')
    ap.add_argument('--random', type=int, default=0)
    ap.add_argument('--seed', type=int, default=336)
    ap.add_argument('--threads', type=int, default=2)
    ap.add_argument('--packed', action='append', default=[], help='file of uint64 length + packed inputs records (repeatable)')
    ap.add_argument('--dump', help='write every case with the onnxruntime scores as a qsel_bench cases file')
    ap.add_argument('--no-qsel', action='store_true', help='only run onnxruntime (with --dump)')
    ap.add_argument('--f64', action='store_true', help='also report each side against the float64 reference (qsel_reference.py)')
    args = ap.parse_args()
    sess = session(args.onnx)
    q = None if args.no_qsel else Qsel(args.lib, args.qsel, args.data, args.threads)
    cases = []
    if args.prepare:
        cases += [(f'fixture:{n}', x, k) for n, x, k in fixture_inputs(args.prepare)]
    rng = np.random.default_rng(args.seed)
    for i in range(args.random):
        x, k = random_inputs(rng)
        cases.append((f'random:{i}', x, k))
    for path in args.packed:
        for i, buf in enumerate(read_packed(path)):
            x = unpack(buf)
            # candidates per branch: rows are branch-major; recover from repeated target ids
            R = x['target_ids'].shape[0]
            k = R
            for b in (2,):
                if R % b == 0 and (x['target_ids'][:R // b] == x['target_ids'][R // b:]).all():
                    k = R // b
            cases.append((f'{path.rsplit("/", 1)[-1]}:{i}', x, k))
    dump = open(args.dump, 'wb') if args.dump else None
    worst, worst_ratio, bad = (0.0, ''), (0.0, ''), 0
    f64 = None
    if args.f64:
        sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # -I leaves the script directory out
        import qsel_reference
        f64 = qsel_reference.load(args.qsel, args.data)
    far = {'ort': 0.0, 'qsel': 0.0}
    for name, x, k in cases:
        ref = sess.run(['scores'], {n: x[n] for n in NAMES})[0]
        if dump:
            buf = pack(x)
            dump.write(np.array([len(buf)], '<u8').tobytes() + buf + np.array([ref.size], '<u8').tobytes() + ref.astype('<f4').tobytes())
        if q is None:
            continue
        got = q.score(x)
        diff = np.abs(got.astype(np.float64) - ref)
        tol = np.maximum(1e-4, 1e-6 * np.abs(ref.astype(np.float64)))
        (oref, bref), (ogot, _) = order(ref, k), order(got, k)
        # The fixtures keep candidates at least 0.11 apart; recorded requests can hold
        # near ties, which may swap within the tolerance without being an error.
        same_order = all(abs(bref[a] - bref[b]) <= 2 * tol.max() for a, b in zip(oref, ogot))
        ok = bool((diff <= tol).all()) and same_order and np.isfinite(got).all()
        md = float(diff.max())
        ratio = float((diff / tol).max())
        if md > worst[0]:
            worst = (md, name)
        if ratio > worst_ratio[0]:
            worst_ratio = (ratio, name)
        L = x['ids'].shape[-1]
        extra = ''
        if f64 is not None:
            exact = qsel_reference.forward(f64, x)
            do, dq = float(np.abs(ref - exact).max()), float(np.abs(got - exact).max())
            far['ort'], far['qsel'] = max(far['ort'], do), max(far['qsel'], dq)
            extra = f' |ort-f64|={do:.2e} |qsel-f64|={dq:.2e}'
        print(f'{name:45s} L={L:4d} P={x["proj_pos"].shape[0]:4d} R={ref.size:3d} max|d|={md:.3e} '
              f'tol_used={ratio:.3f} range=[{ref.min():.3f},{ref.max():.3f}]{extra} {"ok" if ok else "FAIL"}')
        bad += not ok
    if q:
        q.close()
    if dump:
        dump.close()
    print(f'cases={len(cases)} fail={bad} max|d|={worst[0]:.3e} ({worst[1]}) max tol_used={worst_ratio[0]:.3f} ({worst_ratio[1]})')
    if f64 is not None:
        print(f'against float64: onnxruntime max {far["ort"]:.3e}, qsel max {far["qsel"]:.3e}')
    sys.exit(1 if bad else 0)


if __name__ == '__main__':
    main()
