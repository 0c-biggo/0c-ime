"""Float64 evaluation of a qsel file, straight from the header (numpy only).

The graph is evaluated as model.onnx specifies it (weights dequantized exactly,
rotary angles formed in float32 as the graph does), but every other operation in
float64. qsel_parity.py uses it to tell which side of a difference is off;
qsel_test.py uses it as the oracle for random small models.
"""
import math
import struct

import numpy as np

S_LAYER0, S_LAYER_STRIDE = 16, 16
_erf = np.vectorize(math.erf, otypes=[np.float64])


def load(path, data_path=None):
    raw = np.memmap(path, np.uint8, mode='r')
    h = bytes(raw[:min(len(raw), 65536)])
    doff, = struct.unpack_from('<Q', h, 24)
    data = np.memmap(data_path, np.uint8, mode='r') if data_path else raw[doff:]
    cfg = dict(zip(('hidden', 'layers', 'heads', 'head_dim', 'inter', 'vocab', 'n_bases', 'n_tones', 'n_types',
                    'n_out', 'block', 'reading_type'), struct.unpack_from('<12I', h, 104)))
    cfg['eps'], cfg['scale'] = struct.unpack_from('<2f', h, 152)
    cfg['inv_freq'] = np.frombuffer(h[160:160 + 256], np.float32)[:cfg['head_dim'] // 2].copy()
    nt, = struct.unpack_from('<I', h, 416)
    raw_t = {}
    for i in range(nt):
        slot, dt, rows, cols, off, ln = struct.unpack_from('<4IQQ', h, 424 + 32 * i)
        b = np.asarray(data[off:off + ln])
        if dt == 1:
            raw_t[slot] = np.frombuffer(b, np.float32).reshape(rows, cols).astype(np.float64)
        elif dt == 3:
            raw_t[slot] = np.frombuffer(b, np.int64).copy()
        else:
            q = np.frombuffer(b, np.uint8).reshape(rows, cols // 2)
            lo, hi = (q & 15).astype(np.int64) - 8, (q >> 4).astype(np.int64) - 8
            raw_t[slot] = ('q4', np.stack([lo, hi], -1).reshape(rows, cols))
    T = {}
    for slot, v in raw_t.items():
        if isinstance(v, tuple):
            T[slot] = v[1] * np.repeat(raw_t[slot + 1], 16, axis=1)
        elif slot not in T:
            T[slot] = v
    return cfg, T


def _ln(x, w, eps):
    m = x.mean(-1, keepdims=True)
    v = ((x - m) ** 2).mean(-1, keepdims=True)
    return (x - m) / np.sqrt(v + eps) * w


def _gelu(x):
    return x * 0.5 * (1 + _erf(x / math.sqrt(2)))


def forward(model, x):
    """Scores (float64) for the nine inputs as numpy arrays."""
    cfg, T = model
    D, layers, H, hd, I = cfg['hidden'], cfg['layers'], cfg['heads'], cfg['head_dim'], cfg['inter']
    ids, bases, tones, types, pos = (np.asarray(x[n]).reshape(-1) for n in ('ids', 'bases', 'tones', 'types', 'positions'))
    L = len(ids)
    mask = np.asarray(x['mask']).reshape(L, L).astype(bool)
    reading = (types == cfg['reading_type'])[:, None]
    e = np.where(reading, T[5][bases] + T[6][tones], T[4][ids]) + T[7][types]
    h = _ln(e, T[0][0], cfg['eps'])
    f = np.outer(pos.astype(np.float32), cfg['inv_freq']).astype(np.float32).astype(np.float64)
    c, s = np.cos(np.concatenate([f, f], 1)), np.sin(np.concatenate([f, f], 1))

    def rope(t):
        t = t.reshape(L, H, hd)
        r = np.concatenate([-t[..., hd // 2:], t[..., :hd // 2]], -1)
        return t * c[:, None] + r * s[:, None]
    for l in range(layers):
        b = S_LAYER0 + S_LAYER_STRIDE * l
        a = h if l == 0 else _ln(h, T[b][0], cfg['eps'])
        q, k = rope(a @ T[b + 2].T), rope(a @ T[b + 4].T)
        v = (a @ T[b + 6].T).reshape(L, H, hd)
        sc = np.einsum('ihd,jhd->hij', q, k) * float(cfg['scale'])
        sc = np.where(mask[None], sc, -np.inf)
        empty = ~mask.any(1)
        sc[:, empty, :] = 0  # nothing allowed: onnxruntime weighs every key equally
        sc = np.exp(sc - sc.max(-1, keepdims=True))
        sc /= sc.sum(-1, keepdims=True)
        o = np.einsum('hij,jhd->ihd', sc, v).reshape(L, D)
        h = h + o @ T[b + 8].T
        u = _ln(h, T[b + 1][0], cfg['eps']) @ T[b + 10].T
        h = h + (_gelu(u[:, :I]) * u[:, I:]) @ T[b + 12].T
    g = _ln(h, T[1][0], cfg['eps'])[np.asarray(x['proj_pos'])]
    g = _ln(_gelu(g @ T[2].T), T[3][0], cfg['eps'])
    lg = g @ T[10].T + T[9][0]
    mx = lg.max(-1, keepdims=True)
    lsm = lg - mx - np.log(np.exp(lg - mx).sum(-1, keepdims=True))
    rows = T[8][np.asarray(x['target_ids'])]
    return lsm[np.asarray(x['target_node']), rows].sum(-1)
