#!/usr/bin/env python3
"""Turn a q4 selection model (model.onnx + model.onnx.data) into a qsel file.

The qsel file is a small header followed by model.onnx.data byte for byte, so the
data part keeps the manifest's SHA-256 and every weight keeps the 64 KiB-aligned
offset the exporter gave it. Nothing is requantized or reordered: the 4-bit blocks,
their scales, the fp32 embeddings and norms are the ones onnxruntime reads.

The converter only accepts the graph it knows: the D512 sequence-v2 decoder with
MatMulNBits (4 bits, block 16, symmetric, no zero points) on every linear layer and
on the tied output layer, fp32 input embeddings, a fp32 head dense layer, and the
tree mask, rotary positions and target gather of the sequence-v2 inputs. Anything
else (another exporter, another quantization, extra ops) is refused rather than
guessed, and the runtime is checked against onnxruntime on the fixtures
(qsel_parity.py) after conversion.

  python3 qsel_convert.py --model model.onnx --data model.onnx.data --out model.qsel
  python3 qsel_convert.py ... --header-only --out model.qselh   # header for the
                                                                # unchanged data file
Needs numpy and onnx (only to read the graph; nothing runs the model).
"""
import argparse
import collections
import hashlib
import os
import struct
import sys
import zlib

import numpy as np

try:  # only convert() needs onnx; header() is also used by qsel_test.py
    import onnx
    from onnx import numpy_helper
except ImportError:
    onnx = None

MAGIC = b'0CQSEL1\0'
VERSION = 1
DATA_ALIGN = 65536  # data offset in a combined file; also the largest page size we care about
MIN_TENSOR_ALIGN = 64

# dtypes in the tensor table
DT_F32, DT_Q4B16, DT_I64 = 1, 2, 3

# slots (keep in sync with qsel.c)
S_EMB_NORM, S_FINAL_NORM, S_HEAD_DENSE, S_HEAD_NORM, S_DEC_EMB, S_BASE_EMB, S_TONE_EMB, \
    S_TYPE_EMB, S_OUT_IDS, S_OUT_BIAS, S_OUT_Q, S_OUT_S = range(12)
S_LAYER0, S_LAYER_STRIDE = 16, 16
L_ATTN_NORM, L_MLP_NORM, L_Q_Q, L_Q_S, L_K_Q, L_K_S, L_V_Q, L_V_S, L_O_Q, L_O_S, \
    L_UP_Q, L_UP_S, L_DOWN_Q, L_DOWN_S = range(14)

# op histogram of the graph this converter understands (8 layers); checked exactly
EXPECTED_OPS = {
    'Mul': 66, 'Add': 52, 'MatMulNBits': 49, 'Transpose': 41, 'Reshape': 32, 'Slice': 32,
    'Concat': 19, 'LayerNormalization': 18, 'MatMul': 17, 'Neg': 16, 'Div': 9, 'Erf': 9,
    'Softmax': 8, 'Split': 8, 'Unsqueeze': 7, 'Gather': 6, 'Where': 2, 'GatherND': 2,
    'Shape': 1, 'Equal': 1, 'Expand': 1, 'Not': 1, 'Cast': 1, 'Cos': 1, 'Sin': 1, 'Gemm': 1,
    'LogSoftmax': 1, 'ReduceSum': 1,
}
INPUTS = ['ids', 'bases', 'tones', 'types', 'positions', 'mask', 'proj_pos', 'target_node', 'target_ids']


class Refuse(Exception):
    pass


def need(cond, what):
    if not cond:
        raise Refuse(what)


class Graph:
    def __init__(self, model):
        g = model.graph
        self.nodes = list(g.node)
        self.init = {t.name: t for t in g.initializer}
        self.producer = {}
        self.consumers = collections.defaultdict(list)
        for n in self.nodes:
            for o in n.output:
                self.producer[o] = n
            for i in n.input:
                self.consumers[i].append(n)

    def attrs(self, n):
        return {a.name: onnx.helper.get_attribute_value(a) for a in n.attribute}

    def const(self, name):
        t = self.init[name]
        need(not t.external_data, f'{name} should be an inline constant')
        return numpy_helper.to_array(t)

    def src(self, name):
        return self.producer.get(name)

    def only_consumer(self, name, op):
        cs = self.consumers[name]
        need(len(cs) == 1 and cs[0].op_type == op, f'{name} should feed exactly one {op}')
        return cs[0]


def ext(t):
    d = {e.key: e.value for e in t.external_data}
    need(d.get('location') == 'model.onnx.data', f'{t.name} is not in model.onnx.data')
    return int(d.get('offset', 0)), int(d['length'])


def shape_of(t):
    return [int(x) for x in t.dims]


def scalar(g, name, value, what):
    v = g.const(name)
    need(v.size == 1 and abs(float(v.ravel()[0]) - value) <= abs(value) * 1e-7, f'{what} should be {value}, got {v.ravel()[:1]}')


def convert(model_path, data_path):
    if onnx is None:
        raise Refuse('the onnx package is needed to read the graph')
    model = onnx.load(model_path, load_external_data=False)
    g = Graph(model)
    hist = collections.Counter(n.op_type for n in g.nodes)
    need(dict(hist) == EXPECTED_OPS, f'op histogram differs: {dict(hist)}')
    need([i.name for i in model.graph.input] == INPUTS, 'inputs differ')
    need([o.name for o in model.graph.output] == ['scores'], 'outputs differ')
    need(all(n.domain in ('', 'com.microsoft') for n in g.nodes), 'unknown op domain')

    tensors = {}  # slot -> (initializer, dtype, rows, cols)

    def put(slot, name, dtype, rows, cols):
        need(slot not in tensors, f'slot {slot} twice')
        t = g.init[name]
        need(t.external_data, f'{name} should be external')
        want = {DT_F32: 1, DT_I64: 7, DT_Q4B16: 2}[dtype]
        need(t.data_type == want, f'{name} dtype {t.data_type}')
        tensors[slot] = (t, dtype, rows, cols)

    lns = [n for n in g.nodes if n.op_type == 'LayerNormalization']
    for n in lns:
        a = g.attrs(n)
        need(len(n.input) == 2, f'{n.name}: LayerNormalization with bias')
        need(a.get('axis') == -1 and a.get('stash_type', 1) == 1, f'{n.name} attrs')
        need(abs(a['epsilon'] - 1e-5) < 1e-12, f'{n.name} epsilon')
    eps = float(np.float32(g.attrs(lns[0])['epsilon']))

    # embeddings: where(types == 1, base[bases] + tone[tones], decoder[ids]) + type[types]
    gathers = {n.input[0]: n for n in g.nodes if n.op_type == 'Gather' and n.input[0] in g.init and n.input[1] in ('ids', 'bases', 'tones', 'types')}
    names = {k: shape_of(g.init[k]) for k in gathers}
    by_input = {gathers[k].input[1]: k for k in gathers}
    need(set(by_input) == {'bases', 'tones', 'ids', 'types'}, 'embedding gathers')
    dec, base, tone, typ = by_input['ids'], by_input['bases'], by_input['tones'], by_input['types']
    vocab, hidden = names[dec]
    need(hidden == 512, 'hidden size')
    where = [n for n in g.nodes if n.op_type == 'Where' and n.input[1] != '' and g.src(n.input[2]) is gathers[dec]]
    need(len(where) == 1, 'embedding Where')
    eq = g.src(g.src(where[0].input[0]).input[0])
    need(eq.op_type == 'Equal' and eq.input[0] == 'types', 'reading positions are types == k')
    reading_type = int(g.const(eq.input[1]).ravel()[0])
    add_rt = g.src(where[0].input[1])
    need(add_rt.op_type == 'Add' and sorted(g.src(x).output[0] for x in add_rt.input) == sorted([gathers[base].output[0], gathers[tone].output[0]]), 'base + tone')
    add_ty = g.only_consumer(where[0].output[0], 'Add')
    need(gathers[typ].output[0] in add_ty.input, 'type embedding')
    need(g.only_consumer(add_ty.output[0], 'LayerNormalization') is lns[0], 'embedding norm')

    put(S_DEC_EMB, dec, DT_F32, vocab, hidden)
    put(S_BASE_EMB, base, DT_F32, *names[base])
    put(S_TONE_EMB, tone, DT_F32, *names[tone])
    put(S_TYPE_EMB, typ, DT_F32, *names[typ])

    # mask: additive, 0 where mask is true and float32 lowest where false
    mf = [n for n in g.nodes if n.op_type == 'Where' and n is not where[0]][0]
    need(g.src(mf.input[0]).op_type == 'Not' and g.src(mf.input[0]).input[0] == 'mask', 'mask fill')
    scalar(g, mf.input[1], float(np.finfo(np.float32).min), 'mask fill value')

    # rotary: freqs = inv_freq * positions, emb = cat(freqs, freqs)
    mm = [n for n in g.nodes if n.op_type == 'MatMul' and n.input[0] in g.init]
    need(len(mm) == 1, 'rotary frequencies')
    inv_freq = g.const(mm[0].input[0]).astype(np.float32).ravel()
    need(inv_freq.size == 32, 'rotary dimension')
    cast = g.src(mm[0].input[1])
    need(cast.op_type == 'Cast' and g.src(cast.input[0]).input[0] == 'positions', 'rotary positions')

    mnb = [n for n in g.nodes if n.op_type == 'MatMulNBits']
    for n in mnb:
        a = g.attrs(n)
        need(len(n.input) == 3, f'{n.name}: zero points or extra inputs are not supported')
        need(a.get('bits') == 4 and a.get('block_size') == 16, f'{n.name}: only 4-bit block 16')
        need(a.get('accuracy_level', 0) in (0, 1), f'{n.name}: accuracy_level {a.get("accuracy_level")} (fp32 compute only)')
        K, N = a['K'], a['N']
        need(K % 32 == 0, f'{n.name}: K {K} is not a multiple of 32 (the runtime reads two blocks at a time)')
        need(shape_of(g.init[n.input[1]]) == [N, K // 16, 8] and shape_of(g.init[n.input[2]]) == [N, K // 16], f'{n.name} shapes')

    softmaxes = [n for n in g.nodes if n.op_type == 'Softmax']
    layers = 8
    need(len(mnb) == 6 * layers + 1 and len(lns) == 2 * layers + 2, 'layer count')
    heads, head_dim, inter = 8, 64, None
    attn_scale = None
    x_in = lns[0].output[0]  # residual stream (layer 0 starts from the normed embedding)
    for l in range(layers):
        base_slot = S_LAYER0 + l * S_LAYER_STRIDE
        q, k, v, o, up, down = mnb[6 * l: 6 * l + 6]
        if l == 0:
            attn_in = x_in
        else:
            ln = lns[2 * l]
            need(ln.input[0] == x_in, f'layer {l} attn norm input')
            attn_in = ln.output[0]
            need(g.init[ln.input[1]].dims == [hidden], 'norm size')
            put(base_slot + L_ATTN_NORM, ln.input[1], DT_F32, 1, hidden)
        need(q.input[0] == k.input[0] == v.input[0] == attn_in, f'layer {l} qkv input')
        for m_, nn_ in ((q, 'q'), (k, 'k'), (v, 'v'), (o, 'o')):
            a = g.attrs(m_)
            need(a['K'] == hidden and a['N'] == hidden, f'layer {l} {nn_} shape')
        # q and k are rotated, v is not
        sm = softmaxes[l]
        av = g.only_consumer(sm.output[0], 'MatMul')
        vt = g.src(av.input[1])
        need(vt.op_type == 'Transpose' and g.src(g.src(vt.input[0]).input[0]) is v, f'layer {l} value path')
        add_mask = g.src(sm.input[0])
        need(add_mask.op_type == 'Add' and add_mask.input[1] == mf.output[0], f'layer {l} mask add')
        mul_scale = g.src(add_mask.input[0])
        need(mul_scale.op_type == 'Mul', f'layer {l} scale')
        s = float(g.const(mul_scale.input[1]).ravel()[0])
        need(attn_scale is None or s == attn_scale, 'attention scale')
        attn_scale = s
        qk = g.src(mul_scale.input[0])
        need(qk.op_type == 'MatMul', f'layer {l} qk')
        for side, lin in ((qk.input[0], q), (g.src(qk.input[1]).input[0], k)):
            rot = g.src(side)  # add(x * cos, rotate_half(x) * sin)
            need(rot.op_type == 'Add', f'layer {l} rotary add')
            m1, m2 = (g.src(x) for x in rot.input)
            tr = g.src(m1.input[0])
            need(tr.op_type == 'Transpose' and g.src(g.src(tr.input[0]).input[0]) is lin, f'layer {l} rotary input')
            cat = g.src(m2.input[0])
            need(cat.op_type == 'Concat' and g.src(cat.input[0]).op_type == 'Neg', f'layer {l} rotate_half')
        need(g.src(g.src(g.src(o.input[0]).input[0]).input[0]) is av, f'layer {l} attention output')
        res1 = g.only_consumer(o.output[0], 'Add')
        need(x_in in res1.input, f'layer {l} attention residual')
        mln = lns[2 * l + 1]
        need(mln.input[0] == res1.output[0] and up.input[0] == mln.output[0], f'layer {l} mlp norm')
        put(base_slot + L_MLP_NORM, mln.input[1], DT_F32, 1, hidden)
        au, ad = g.attrs(up), g.attrs(down)
        inter = au['N'] // 2
        need(au['K'] == hidden and ad['K'] == inter and ad['N'] == hidden, f'layer {l} mlp shape')
        sp = g.only_consumer(up.output[0], 'Split')
        need(len(sp.output) == 2 and g.attrs(sp).get('axis') == 2 and len(sp.input) == 1, f'layer {l} split')
        gmul = g.src(down.input[0])
        need(gmul.op_type == 'Mul' and gmul.input[1] == sp.output[1], f'layer {l} gate * up')
        gelu = g.src(gmul.input[0])
        need(gelu.op_type == 'Mul' and gelu.input[0] == sp.output[0], f'layer {l} gelu(gate)')
        res2 = g.only_consumer(down.output[0], 'Add')
        need(res1.output[0] in res2.input, f'layer {l} mlp residual')
        x_in = res2.output[0]
        for slot, m_ in ((L_Q_Q, q), (L_K_Q, k), (L_V_Q, v), (L_O_Q, o), (L_UP_Q, up), (L_DOWN_Q, down)):
            a = g.attrs(m_)
            put(base_slot + slot, m_.input[1], DT_Q4B16, a['N'], a['K'])
            put(base_slot + slot + 1, m_.input[2], DT_F32, a['N'], a['K'] // 16)
    need(attn_scale == 0.125, 'attention scale 1/sqrt(64)')

    # GELU constants (erf form) on every gelu
    for n in g.nodes:
        if n.op_type == 'Div':
            scalar(g, n.input[1], 1.4142135381698608, 'gelu sqrt2')

    fln = lns[16]
    need(fln.input[0] == x_in, 'final norm input')
    put(S_EMB_NORM, lns[0].input[1], DT_F32, 1, hidden)
    put(S_FINAL_NORM, fln.input[1], DT_F32, 1, hidden)
    gemm = [n for n in g.nodes if n.op_type == 'Gemm'][0]
    a = g.attrs(gemm)
    need(len(gemm.input) == 2 and a.get('transB') == 1 and a.get('transA', 0) == 0 and a.get('alpha', 1) == 1 and a.get('beta', 1) == 1, 'head dense')
    gnd = g.src(gemm.input[0])
    need(gnd.op_type == 'GatherND' and g.src(gnd.input[0]).op_type == 'Gather' and g.src(gnd.input[0]).input[0] == fln.output[0], 'head gathers proj_pos rows')
    need(shape_of(g.init[gemm.input[1]]) == [hidden, hidden], 'head dense shape')
    put(S_HEAD_DENSE, gemm.input[1], DT_F32, hidden, hidden)
    hln = lns[17]
    need(g.src(hln.input[0]).op_type == 'Mul', 'head gelu')
    put(S_HEAD_NORM, hln.input[1], DT_F32, 1, hidden)
    out = mnb[-1]
    need(out.input[0] == hln.output[0], 'output layer input')
    a = g.attrs(out)
    need(a['K'] == hidden and a['N'] == vocab, 'output layer shape (tied)')
    put(S_OUT_Q, out.input[1], DT_Q4B16, vocab, hidden)
    put(S_OUT_S, out.input[2], DT_F32, vocab, hidden // 16)
    badd = g.only_consumer(out.output[0], 'Add')
    bias = badd.input[1]
    need(shape_of(g.init[bias]) == [vocab], 'output bias')
    put(S_OUT_BIAS, bias, DT_F32, 1, vocab)
    lsm = g.only_consumer(badd.output[0], 'LogSoftmax')
    need(g.attrs(lsm).get('axis', -1) == -1, 'log softmax axis')
    tg = [n for n in g.nodes if n.op_type == 'Gather' and n.input[1] == 'target_ids']
    need(len(tg) == 1, 'target id gather')
    out_ids = tg[0].input[0]
    n_out = shape_of(g.init[out_ids])[0]
    put(S_OUT_IDS, out_ids, DT_I64, 1, n_out)
    rs = [n for n in g.nodes if n.op_type == 'ReduceSum'][0]
    need(rs.output[0] == 'scores' and g.src(rs.input[0]).op_type == 'GatherND' and g.src(rs.input[0]).input[0] == lsm.output[0], 'scores sum the target log probabilities')

    # every external tensor is used exactly once
    ext_names = {t.name for t in model.graph.initializer if t.external_data}
    used = {t.name for t, *_ in tensors.values()}
    need(ext_names == used, f'unused external tensors: {sorted(ext_names - used)}')

    data_size = os.path.getsize(data_path)
    table = []
    for slot in sorted(tensors):
        t, dtype, rows, cols = tensors[slot]
        off, length = ext(t)
        want = {DT_F32: rows * cols * 4, DT_I64: rows * cols * 8, DT_Q4B16: rows * cols // 2}[dtype]
        need(length == want, f'{t.name}: {length} bytes, want {want}')
        need(off % MIN_TENSOR_ALIGN == 0, f'{t.name}: offset {off} is not {MIN_TENSOR_ALIGN}-byte aligned')
        need(off + length <= data_size, f'{t.name} past the end of the data file')
        table.append((slot, dtype, rows, cols, off, length))

    # values the runtime relies on
    with open(data_path, 'rb') as f:
        def read(slot, dtype):
            _, _, rows, cols, off, length = next(e for e in table if e[0] == slot)
            f.seek(off)
            return np.frombuffer(f.read(length), dtype=dtype)
        oi = read(S_OUT_IDS, np.int64)
        need(((oi >= 0) & (oi < vocab)).all(), 'out_ids in range')
        need(np.isfinite(read(S_DEC_EMB, np.float32)).all(), 'finite embeddings')
        b = read(S_OUT_BIAS, np.float32)
        need(not np.isnan(b).any() and not np.isposinf(b).any(), 'output bias')
        need(np.isfinite(b[oi]).all(), 'output bias is finite on every target row')
    cfg = dict(hidden=hidden, layers=layers, heads=heads, head_dim=head_dim, inter=inter, vocab=vocab,
               n_bases=names[base][0], n_tones=names[tone][0], n_types=names[typ][0], n_out=n_out,
               block=16, reading_type=reading_type, eps=eps, attn_scale=attn_scale, inv_freq=inv_freq)
    return cfg, table


def sha256(path):
    h = hashlib.sha256()
    with open(path, 'rb') as f:
        for chunk in iter(lambda: f.read(1 << 20), b''):
            h.update(chunk)
    return h.digest()


def header(cfg, table, data_offset, data_bytes, data_sha, graph_sha):
    inv = np.zeros(64, np.float32)
    inv[:cfg['inv_freq'].size] = cfg['inv_freq']
    body = struct.pack('<QQ32s32s', data_offset, data_bytes, data_sha, graph_sha)
    body += struct.pack('<12I', cfg['hidden'], cfg['layers'], cfg['heads'], cfg['head_dim'], cfg['inter'],
                        cfg['vocab'], cfg['n_bases'], cfg['n_tones'], cfg['n_types'], cfg['n_out'],
                        cfg['block'], cfg['reading_type'])
    body += struct.pack('<2f', cfg['eps'], cfg['attn_scale']) + inv.tobytes()
    body += struct.pack('<II', len(table), 0)
    for e in table:
        body += struct.pack('<4IQQ', *e)
    size = 24 + len(body)
    head = MAGIC + struct.pack('<IIII', VERSION, size, 0, 0)
    blob = head + body
    crc = zlib.crc32(blob) & 0xffffffff
    return MAGIC + struct.pack('<IIII', VERSION, size, crc, 0) + body


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument('--model', required=True)
    ap.add_argument('--data', required=True)
    ap.add_argument('--out', required=True)
    ap.add_argument('--header-only', action='store_true', help='write only the header; the runtime then opens model.onnx.data next to it')
    args = ap.parse_args()
    try:
        cfg, table = convert(args.model, args.data)
    except Refuse as e:
        sys.exit(f'qsel_convert: refusing {args.model}: {e}')
    data_bytes = os.path.getsize(args.data)
    data_sha, graph_sha = sha256(args.data), sha256(args.model)
    offset = 0 if args.header_only else DATA_ALIGN
    h = header(cfg, table, offset, data_bytes, data_sha, graph_sha)
    assert len(h) <= DATA_ALIGN
    tmp = args.out + '.tmp'
    with open(tmp, 'wb') as f:
        f.write(h)
        if not args.header_only:
            f.write(b'\0' * (DATA_ALIGN - len(h)))
            with open(args.data, 'rb') as src:
                for chunk in iter(lambda: src.read(1 << 20), b''):
                    f.write(chunk)
    os.replace(tmp, args.out)
    print(f'{args.out}: {os.path.getsize(args.out)} bytes, header {len(h)} bytes, {len(table)} tensors, '
          f'data sha256 {data_sha.hex()}')


if __name__ == '__main__':
    main()
