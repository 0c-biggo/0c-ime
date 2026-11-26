/* qsel: see qsel.h and README.md.
 *
 * Numerics follow model.onnx under onnxruntime's CPU provider with fp32 compute
 * (MatMulNBits accuracy_level 1): every 4-bit weight is dequantized as
 * (q - 8) * scale and multiplied in fp32; GELU (erf), softmax and the log softmax
 * are fp32 as well (on arm64 with vector exp and erf of libm's accuracy class).
 * LayerNorm statistics and the final sum of each score are taken in double.
 * Otherwise only summation order differs. Masked attention entries, which
 * onnxruntime adds as float32-lowest and which then contribute exactly zero, are
 * skipped. Against a float64 evaluation of the graph (qsel_reference.py) this
 * stays several times closer than onnxruntime does.
 */
#if defined(__linux__) || defined(__ANDROID__)
#define _GNU_SOURCE
#endif
#include "qsel.h"

#include <errno.h>
#include <fcntl.h>
#include <math.h>
#include <pthread.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

#if defined(__aarch64__)
#include <arm_neon.h>
#define QSEL_NEON 1
#else
#define QSEL_NEON 0
#endif

/* -DQSEL_ACCELERATE (Apple): the matrix products go to Accelerate's cblas_sgemm on
 * dequantized panels of ACC_NT rows instead of the kernel below. */
#ifdef QSEL_ACCELERATE
#include <Accelerate/Accelerate.h>
#define ACC_NT 32
#else
#define ACC_NT 8
#endif

#if defined(__BYTE_ORDER__) && __BYTE_ORDER__ != __ORDER_LITTLE_ENDIAN__
#error "qsel files are little-endian"
#endif

/* ---- file format (see qsel_convert.py) ---- */

#define QSEL_VERSION 1
#define HDR_FIXED 424
#define HDR_ENTRY 32
#define MAX_LAYERS 32
#define MAX_TENSORS (16 + MAX_LAYERS * 16)

enum { DT_F32 = 1, DT_Q4B16 = 2, DT_I64 = 3 };
enum {
    S_EMB_NORM, S_FINAL_NORM, S_HEAD_DENSE, S_HEAD_NORM, S_DEC_EMB, S_BASE_EMB, S_TONE_EMB,
    S_TYPE_EMB, S_OUT_IDS, S_OUT_BIAS, S_OUT_Q, S_OUT_S,
    S_LAYER0 = 16, S_LAYER_STRIDE = 16,
};
enum {
    L_ATTN_NORM, L_MLP_NORM, L_Q_Q, L_Q_S, L_K_Q, L_K_S, L_V_Q, L_V_S, L_O_Q, L_O_S,
    L_UP_Q, L_UP_S, L_DOWN_Q, L_DOWN_S,
};

/* ---- limits of one call ---- */

#define MAX_L 4096
#define MAX_TARGETS (1 << 20)

/* ---- tiling ---- */

#define NT 8         /* weight rows dequantized at a time (one panel) */
#define MLP_ROWS 96  /* at most this many rows of the MLP at a time (in even chunks) */
#define ATT_ROWS 16  /* query rows per attention task */
#define VCHUNK 256   /* vocabulary rows per output task (fixed, so results do not depend on threads) */

typedef struct {
    const uint8_t *q; /* [n][k/16][8]: element 2j of a block is the low nibble of byte j */
    const float *s;   /* [n][k/16] */
    int n, k;
} q4mat;

typedef struct {
    const float *attn_norm, *mlp_norm; /* attn_norm is NULL in layer 0 */
    q4mat wq, wk, wv, wo, up, down;
} layer_t;

typedef void (*task_fn)(void *ctx, int task, int thread);

struct qsel_model {
    uint8_t *map;
    size_t map_len;
    uint8_t *dmap; /* separate data mapping, or NULL */
    size_t dmap_len;
    const uint8_t *data;
    size_t data_len;

    int D, layers, heads, hd, inter, vocab, n_bases, n_tones, n_types, n_out, reading_type;
    float eps, scale, inv_freq[64];
    const float *emb_norm, *final_norm, *head_dense, *head_norm, *dec_emb, *base_emb, *tone_emb,
        *type_emb, *out_bias;
    const int64_t *out_ids;
    q4mat out;
    layer_t layer[MAX_LAYERS];

    /* threads */
    int threads;
    pthread_t *th;
    pthread_mutex_t mu;
    pthread_cond_t go, done;
    unsigned gen;
    int stop, active, started;
    task_fn fn;
    void *ctx;
    int ntasks;
    atomic_int next;
    pthread_mutex_t call;

    /* 前綴快取（qsel_set_prefix_cache），由 call 保護：上一次呼叫前 pc_rows 列每一層的
     * key、value（轉過位置之後），以及算出它們的輸入。pc_kv 是 [pc_alloc][layers][2][D]
     * （以列為主，長大時原有的列不動）；pc_tok [pc_rows][5]；pc_mask [pc_rows][pc_rows]；
     * pc_reach [pc_rows] 是那次呼叫每一列看到的最後一欄（全部欄，不只 pc_rows 以內）。 */
    int pc_max, pc_rows, pc_alloc, pc_reused;
    float *pc_kv;
    int64_t *pc_tok;
    uint8_t *pc_mask;
    int32_t *pc_reach;
};

const char *qsel_strerror(int code) {
    switch (code) {
    case QSEL_OK: return "ok";
    case QSEL_EIO: return "cannot open or map the model file";
    case QSEL_EFORMAT: return "not a qsel model this runtime knows";
    case QSEL_EINPUT: return "inputs out of range or inconsistent";
    case QSEL_ENOMEM: return "out of memory";
    case QSEL_ETHREAD: return "cannot start threads";
    }
    return "unknown error";
}

/* ---- thread pool ---- */

static void *worker(void *arg) {
    qsel_model *m = arg;
    int id;
    pthread_mutex_lock(&m->mu);
    id = ++m->started;
    unsigned seen = 0; /* gen starts at 0; a job started before this thread ran is still picked up */
    for (;;) {
        while (m->gen == seen && !m->stop) pthread_cond_wait(&m->go, &m->mu);
        if (m->stop) break;
        seen = m->gen;
        task_fn fn = m->fn;
        void *ctx = m->ctx;
        int n = m->ntasks;
        pthread_mutex_unlock(&m->mu);
        for (int t; (t = atomic_fetch_add(&m->next, 1)) < n;) fn(ctx, t, id);
        pthread_mutex_lock(&m->mu);
        if (--m->active == 0) pthread_cond_signal(&m->done);
    }
    pthread_mutex_unlock(&m->mu);
    return NULL;
}

static void run(qsel_model *m, task_fn fn, void *ctx, int ntasks) {
    if (m->threads <= 1 || ntasks <= 1) {
        for (int t = 0; t < ntasks; t++) fn(ctx, t, 0);
        return;
    }
    pthread_mutex_lock(&m->mu);
    m->fn = fn;
    m->ctx = ctx;
    m->ntasks = ntasks;
    atomic_store(&m->next, 0);
    m->active = m->threads - 1;
    m->gen++;
    pthread_cond_broadcast(&m->go);
    pthread_mutex_unlock(&m->mu);
    for (int t; (t = atomic_fetch_add(&m->next, 1)) < ntasks;) fn(ctx, t, 0);
    pthread_mutex_lock(&m->mu);
    while (m->active > 0) pthread_cond_wait(&m->done, &m->mu);
    pthread_mutex_unlock(&m->mu);
}

/* ---- loading ---- */

static uint32_t crc32(const uint8_t *p, size_t n) {
    uint32_t c = 0xffffffffu;
    for (size_t i = 0; i < n; i++) {
        c ^= p[i];
        for (int k = 0; k < 8; k++) c = (c >> 1) ^ (0xedb88320u & (0u - (c & 1)));
    }
    return ~c;
}

static uint32_t rd32(const uint8_t *p) { uint32_t v; memcpy(&v, p, 4); return v; }
static uint64_t rd64(const uint8_t *p) { uint64_t v; memcpy(&v, p, 8); return v; }

static int map_file(const char *path, uint8_t **out, size_t *len) {
    int fd = open(path, O_RDONLY | O_CLOEXEC);
    if (fd < 0) return QSEL_EIO;
    struct stat st;
    if (fstat(fd, &st) != 0 || st.st_size <= 0) {
        int e = errno;
        close(fd);
        errno = e;
        return QSEL_EIO;
    }
    void *p = mmap(NULL, (size_t)st.st_size, PROT_READ, MAP_SHARED, fd, 0);
    int e = errno;
    close(fd);
    if (p == MAP_FAILED) {
        errno = e;
        return QSEL_EIO;
    }
    *out = p;
    *len = (size_t)st.st_size;
    return QSEL_OK;
}

typedef struct {
    int present, dtype;
    uint32_t rows, cols;
    const uint8_t *ptr;
} entry_t;

static int get(const entry_t *e, int slot, int dtype, uint32_t rows, uint32_t cols, const void **out) {
    if (!e[slot].present || e[slot].dtype != dtype || e[slot].rows != rows || e[slot].cols != cols) return 0;
    *out = e[slot].ptr;
    return 1;
}

static int get_q4(const entry_t *e, int qslot, int n, int k, q4mat *w) {
    const void *q, *s;
    if (k % 32 || !get(e, qslot, DT_Q4B16, n, k, &q) || !get(e, qslot + 1, DT_F32, n, k / 16, &s)) return 0;
    w->q = q;
    w->s = s;
    w->n = n;
    w->k = k;
    return 1;
}

static int parse(qsel_model *m) {
    const uint8_t *h = m->map;
    if (m->map_len < HDR_FIXED || memcmp(h, "0CQSEL1\0", 8) != 0 || rd32(h + 8) != QSEL_VERSION) return QSEL_EFORMAT;
    uint32_t hbytes = rd32(h + 12);
    if (hbytes < HDR_FIXED || hbytes > m->map_len) return QSEL_EFORMAT;
    {
        uint8_t *copy = malloc(hbytes);
        if (!copy) return QSEL_ENOMEM;
        memcpy(copy, h, hbytes);
        memset(copy + 16, 0, 4);
        uint32_t c = crc32(copy, hbytes);
        free(copy);
        if (c != rd32(h + 16)) return QSEL_EFORMAT;
    }
    uint64_t doff = rd64(h + 24), dlen = rd64(h + 32);
    if (m->dmap) {
        if (doff != 0 || dlen != m->dmap_len) return QSEL_EFORMAT;
        m->data = m->dmap;
    } else {
        if (doff < hbytes || doff % 4096 || doff > m->map_len || m->map_len - doff != dlen) return QSEL_EFORMAT;
        m->data = m->map + doff;
    }
    m->data_len = (size_t)dlen;
    const uint8_t *c = h + 104;
    m->D = (int)rd32(c + 0);
    m->layers = (int)rd32(c + 4);
    m->heads = (int)rd32(c + 8);
    m->hd = (int)rd32(c + 12);
    m->inter = (int)rd32(c + 16);
    m->vocab = (int)rd32(c + 20);
    m->n_bases = (int)rd32(c + 24);
    m->n_tones = (int)rd32(c + 28);
    m->n_types = (int)rd32(c + 32);
    m->n_out = (int)rd32(c + 36);
    uint32_t block = rd32(c + 40);
    m->reading_type = (int)rd32(c + 44);
    memcpy(&m->eps, h + 152, 4);
    memcpy(&m->scale, h + 156, 4);
    memcpy(m->inv_freq, h + 160, sizeof m->inv_freq);
    if (block != 16 || m->D <= 0 || m->D > 4096 || m->D % 16 || m->layers < 1 || m->layers > MAX_LAYERS ||
        m->heads < 1 || m->hd < 8 || m->hd % 8 || m->hd > 128 || m->heads * m->hd != m->D || m->inter <= 0 ||
        m->inter % 16 || m->inter > 16384 || m->vocab <= 0 || m->vocab > (1 << 24) || m->n_bases <= 0 ||
        m->n_tones <= 0 || m->n_types <= 0 || m->n_out <= 0 || m->n_out > m->vocab ||
        !(m->eps > 0 && m->eps < 1) || !(m->scale > 0 && m->scale < 1e3f))
        return QSEL_EFORMAT;
    uint32_t nt = rd32(h + 416);
    if (nt > MAX_TENSORS || (uint64_t)HDR_FIXED + (uint64_t)nt * HDR_ENTRY != hbytes) return QSEL_EFORMAT;
    entry_t e[MAX_TENSORS];
    memset(e, 0, sizeof e);
    for (uint32_t i = 0; i < nt; i++) {
        const uint8_t *t = h + HDR_FIXED + (size_t)i * HDR_ENTRY;
        uint32_t slot = rd32(t), dtype = rd32(t + 4), rows = rd32(t + 8), cols = rd32(t + 12);
        uint64_t off = rd64(t + 16), len = rd64(t + 24);
        if (slot >= MAX_TENSORS || e[slot].present || off % 64 || off > dlen || len > dlen - off) return QSEL_EFORMAT;
        uint64_t want = dtype == DT_F32 ? (uint64_t)rows * cols * 4
                        : dtype == DT_I64 ? (uint64_t)rows * cols * 8
                        : dtype == DT_Q4B16 ? (uint64_t)rows * cols / 2 : 0;
        if (want == 0 || want != len || (dtype == DT_Q4B16 && cols % 16)) return QSEL_EFORMAT;
        e[slot] = (entry_t){1, (int)dtype, rows, cols, m->data + off};
    }
    const void *p;
    int D = m->D;
#define NEED(slot, dt, r, cl, dst) do { if (!get(e, slot, dt, r, cl, &p)) return QSEL_EFORMAT; dst = p; } while (0)
    NEED(S_EMB_NORM, DT_F32, 1, D, m->emb_norm);
    NEED(S_FINAL_NORM, DT_F32, 1, D, m->final_norm);
    NEED(S_HEAD_DENSE, DT_F32, D, D, m->head_dense);
    NEED(S_HEAD_NORM, DT_F32, 1, D, m->head_norm);
    NEED(S_DEC_EMB, DT_F32, m->vocab, D, m->dec_emb);
    NEED(S_BASE_EMB, DT_F32, m->n_bases, D, m->base_emb);
    NEED(S_TONE_EMB, DT_F32, m->n_tones, D, m->tone_emb);
    NEED(S_TYPE_EMB, DT_F32, m->n_types, D, m->type_emb);
    NEED(S_OUT_IDS, DT_I64, 1, m->n_out, m->out_ids);
    NEED(S_OUT_BIAS, DT_F32, 1, m->vocab, m->out_bias);
    if (!get_q4(e, S_OUT_Q, m->vocab, D, &m->out)) return QSEL_EFORMAT;
    for (int l = 0; l < m->layers; l++) {
        int b = S_LAYER0 + l * S_LAYER_STRIDE;
        layer_t *ly = &m->layer[l];
        if (l == 0) {
            if (e[b + L_ATTN_NORM].present) return QSEL_EFORMAT;
            ly->attn_norm = NULL;
        } else {
            NEED(b + L_ATTN_NORM, DT_F32, 1, D, ly->attn_norm);
        }
        NEED(b + L_MLP_NORM, DT_F32, 1, D, ly->mlp_norm);
        if (!get_q4(e, b + L_Q_Q, D, D, &ly->wq) || !get_q4(e, b + L_K_Q, D, D, &ly->wk) ||
            !get_q4(e, b + L_V_Q, D, D, &ly->wv) || !get_q4(e, b + L_O_Q, D, D, &ly->wo) ||
            !get_q4(e, b + L_UP_Q, 2 * m->inter, D, &ly->up) || !get_q4(e, b + L_DOWN_Q, D, m->inter, &ly->down))
            return QSEL_EFORMAT;
    }
#undef NEED
    for (int i = 0; i < m->n_out; i++)
        if (m->out_ids[i] < 0 || m->out_ids[i] >= m->vocab) return QSEL_EFORMAT;
    /* The embedding table is read a few rows at a time: no readahead around them. */
    {
        uintptr_t pg = (uintptr_t)sysconf(_SC_PAGESIZE);
        uintptr_t a = (uintptr_t)m->dec_emb, z = a + (uintptr_t)m->vocab * D * 4;
        a = (a + pg - 1) & ~(pg - 1);
        z &= ~(pg - 1);
        if (z > a) madvise((void *)a, z - a, MADV_RANDOM);
    }
    return QSEL_OK;
}

int qsel_open(const char *path, const char *data_path, int threads, qsel_model **out) {
    *out = NULL;
    if (threads < 1) threads = 1;
    if (threads > 64) threads = 64;
    qsel_model *m = calloc(1, sizeof *m);
    if (!m) return QSEL_ENOMEM;
    int rc = map_file(path, &m->map, &m->map_len);
    if (rc == QSEL_OK && data_path) rc = map_file(data_path, &m->dmap, &m->dmap_len);
    if (rc == QSEL_OK) rc = parse(m);
    if (rc != QSEL_OK) {
        int e = errno;
        if (m->map) munmap(m->map, m->map_len);
        if (m->dmap) munmap(m->dmap, m->dmap_len);
        free(m);
        errno = e;
        return rc;
    }
    pthread_mutex_init(&m->mu, NULL);
    pthread_mutex_init(&m->call, NULL);
    pthread_cond_init(&m->go, NULL);
    pthread_cond_init(&m->done, NULL);
    m->threads = 1;
    if (threads > 1) {
        m->th = calloc((size_t)threads - 1, sizeof *m->th);
        if (!m->th) {
            qsel_close(m);
            return QSEL_ENOMEM;
        }
        pthread_attr_t attr;
        pthread_attr_init(&attr);
        pthread_attr_setstacksize(&attr, 256 * 1024);
        for (int i = 0; i < threads - 1; i++) {
            if (pthread_create(&m->th[i], &attr, worker, m) != 0) {
                pthread_attr_destroy(&attr);
                qsel_close(m);
                return QSEL_ETHREAD;
            }
            m->threads++;
        }
        pthread_attr_destroy(&attr);
    }
    *out = m;
    return QSEL_OK;
}

static void prefix_free(qsel_model *m) {
    free(m->pc_kv);
    free(m->pc_tok);
    free(m->pc_mask);
    free(m->pc_reach);
    m->pc_kv = NULL, m->pc_tok = NULL, m->pc_mask = NULL, m->pc_reach = NULL;
    m->pc_rows = m->pc_alloc = 0;
}

void qsel_close(qsel_model *m) {
    if (!m) return;
    prefix_free(m);
    if (m->threads > 1) {
        pthread_mutex_lock(&m->mu);
        m->stop = 1;
        pthread_cond_broadcast(&m->go);
        pthread_mutex_unlock(&m->mu);
        for (int i = 0; i < m->threads - 1; i++) pthread_join(m->th[i], NULL);
    }
    free(m->th);
    pthread_mutex_destroy(&m->mu);
    pthread_mutex_destroy(&m->call);
    pthread_cond_destroy(&m->go);
    pthread_cond_destroy(&m->done);
    if (m->map) munmap(m->map, m->map_len);
    if (m->dmap) munmap(m->dmap, m->dmap_len);
    free(m);
}

void qsel_release_pages(qsel_model *m) {
    pthread_mutex_lock(&m->call);
    prefix_free(m); /* 快取是一般的 dirty 記憶體，最先還 */
    madvise(m->map, m->map_len, MADV_DONTNEED);
    if (m->dmap) madvise(m->dmap, m->dmap_len, MADV_DONTNEED);
    pthread_mutex_unlock(&m->call);
}

/* ---- kernels ---- */

/* Rows [n0, n0+nr) of w as fp32, row-major [nr][k]; with nr < NT, zero rows up to NT. */
static void dequant(const q4mat *w, int n0, int nr, float *panel) {
    int kb = w->k / 16;
    for (int r = 0; r < nr; r++) {
        const uint8_t *q = w->q + (size_t)(n0 + r) * kb * 8;
        const float *s = w->s + (size_t)(n0 + r) * kb;
        float *o = panel + (size_t)r * w->k;
#if QSEL_NEON
        /* two blocks (32 weights, 16 bytes) at a time; k % 32 == 0 */
        for (int b = 0; b < kb; b += 2, q += 16, o += 32) {
            uint8x16_t v = vld1q_u8(q);
            uint8x16x2_t z = vzipq_u8(vandq_u8(v, vdupq_n_u8(15)), vshrq_n_u8(v, 4));
            int8x16_t i0 = vsubq_s8(vreinterpretq_s8_u8(z.val[0]), vdupq_n_s8(8));
            int8x16_t i1 = vsubq_s8(vreinterpretq_s8_u8(z.val[1]), vdupq_n_s8(8));
            float32x2_t sc = vld1_f32(s + b);
            int16x8_t a0 = vmovl_s8(vget_low_s8(i0)), a1 = vmovl_high_s8(i0);
            int16x8_t a2 = vmovl_s8(vget_low_s8(i1)), a3 = vmovl_high_s8(i1);
            vst1q_f32(o + 0, vmulq_lane_f32(vcvtq_f32_s32(vmovl_s16(vget_low_s16(a0))), sc, 0));
            vst1q_f32(o + 4, vmulq_lane_f32(vcvtq_f32_s32(vmovl_high_s16(a0)), sc, 0));
            vst1q_f32(o + 8, vmulq_lane_f32(vcvtq_f32_s32(vmovl_s16(vget_low_s16(a1))), sc, 0));
            vst1q_f32(o + 12, vmulq_lane_f32(vcvtq_f32_s32(vmovl_high_s16(a1)), sc, 0));
            vst1q_f32(o + 16, vmulq_lane_f32(vcvtq_f32_s32(vmovl_s16(vget_low_s16(a2))), sc, 1));
            vst1q_f32(o + 20, vmulq_lane_f32(vcvtq_f32_s32(vmovl_high_s16(a2)), sc, 1));
            vst1q_f32(o + 24, vmulq_lane_f32(vcvtq_f32_s32(vmovl_s16(vget_low_s16(a3))), sc, 1));
            vst1q_f32(o + 28, vmulq_lane_f32(vcvtq_f32_s32(vmovl_high_s16(a3)), sc, 1));
        }
#else
        for (int b = 0; b < kb; b++, q += 8, o += 16) {
            float sc = s[b];
            for (int j = 0; j < 8; j++) {
                o[2 * j] = (float)((int)(q[j] & 15) - 8) * sc;
                o[2 * j + 1] = (float)((int)(q[j] >> 4) - 8) * sc;
            }
        }
#endif
    }
    if (nr < NT) memset(panel + (size_t)nr * w->k, 0, (size_t)(NT - nr) * w->k * 4);
}

static size_t xt_floats(int rows, int k) { return (size_t)((rows + 7) / 8) * 8 * (size_t)k; }

#ifndef QSEL_ACCELERATE
/* Activations go k-major in tiles of 8 rows: tile t is rows 8t..8t+7 as [k][8],
 * zero past the last row. Then a weight row is one vector per 4 k and is broadcast
 * lane by lane against 8 rows at once (outer product), which needs a quarter of the
 * loads of row-by-row dot products: that is what bounds an in-order core like A55. */

static void pack_x(const float *x, int ldx, int rows, int k, float *xt) {
    for (int t = 0; t < rows; t += 8) {
        float *o = xt + (size_t)t * k;
        int mw = rows - t < 8 ? rows - t : 8;
        for (int i = 0; i < mw; i++) {
            const float *r = x + (size_t)(t + i) * ldx;
            for (int p = 0; p < k; p++) o[(size_t)p * 8 + i] = r[p];
        }
        for (int i = mw; i < 8; i++)
            for (int p = 0; p < k; p++) o[(size_t)p * 8 + i] = 0;
    }
}

/* c[n*8 + m] = sum_p w[n][p] * xt[p][m], p < k, for 8 weight rows (stride ldw) and
 * the 8 (wide) or first 4 rows of one activation tile. k % 4 == 0. */
#if QSEL_NEON
#define LANE(acc, x, v, l) acc = vfmaq_laneq_f32(acc, x, v, l)
/* 8 weight rows x 8 activation rows: 16 accumulators, 8 weight vectors, 2 activation vectors */
__attribute__((noinline)) static void kernel8(const float *w, int ldw, const float *xt, int k, float *c) {
    float32x4_t z = vdupq_n_f32(0);
    float32x4_t a0 = z, a1 = z, a2 = z, a3 = z, a4 = z, a5 = z, a6 = z, a7 = z;
    float32x4_t b0 = z, b1 = z, b2 = z, b3 = z, b4 = z, b5 = z, b6 = z, b7 = z;
    for (int p = 0; p < k; p += 4, w += 4) {
        float32x4_t v0 = vld1q_f32(w), v1 = vld1q_f32(w + ldw), v2 = vld1q_f32(w + 2 * ldw), v3 = vld1q_f32(w + 3 * ldw);
        float32x4_t v4 = vld1q_f32(w + 4 * ldw), v5 = vld1q_f32(w + 5 * ldw), v6 = vld1q_f32(w + 6 * ldw),
                    v7 = vld1q_f32(w + 7 * ldw);
#define STEP(l)                                                                                 \
    do {                                                                                        \
        float32x4_t xl = vld1q_f32(xt + (size_t)(p + l) * 8), xh = vld1q_f32(xt + (size_t)(p + l) * 8 + 4); \
        LANE(a0, xl, v0, l); LANE(b0, xh, v0, l); LANE(a1, xl, v1, l); LANE(b1, xh, v1, l);     \
        LANE(a2, xl, v2, l); LANE(b2, xh, v2, l); LANE(a3, xl, v3, l); LANE(b3, xh, v3, l);     \
        LANE(a4, xl, v4, l); LANE(b4, xh, v4, l); LANE(a5, xl, v5, l); LANE(b5, xh, v5, l);     \
        LANE(a6, xl, v6, l); LANE(b6, xh, v6, l); LANE(a7, xl, v7, l); LANE(b7, xh, v7, l);     \
    } while (0)
        STEP(0);
        STEP(1);
        STEP(2);
        STEP(3);
#undef STEP
    }
    vst1q_f32(c + 0, a0); vst1q_f32(c + 8, a1); vst1q_f32(c + 16, a2); vst1q_f32(c + 24, a3);
    vst1q_f32(c + 32, a4); vst1q_f32(c + 40, a5); vst1q_f32(c + 48, a6); vst1q_f32(c + 56, a7);
    vst1q_f32(c + 4, b0); vst1q_f32(c + 12, b1); vst1q_f32(c + 20, b2); vst1q_f32(c + 28, b3);
    vst1q_f32(c + 36, b4); vst1q_f32(c + 44, b5); vst1q_f32(c + 52, b6); vst1q_f32(c + 60, b7);
}

/* the same for the first 4 rows of a tile */
__attribute__((noinline)) static void kernel4(const float *w, int ldw, const float *xt, int k, float *c) {
    float32x4_t z = vdupq_n_f32(0);
    float32x4_t a0 = z, a1 = z, a2 = z, a3 = z, a4 = z, a5 = z, a6 = z, a7 = z;
    for (int p = 0; p < k; p += 4, w += 4) {
        float32x4_t v0 = vld1q_f32(w), v1 = vld1q_f32(w + ldw), v2 = vld1q_f32(w + 2 * ldw), v3 = vld1q_f32(w + 3 * ldw);
        float32x4_t v4 = vld1q_f32(w + 4 * ldw), v5 = vld1q_f32(w + 5 * ldw), v6 = vld1q_f32(w + 6 * ldw),
                    v7 = vld1q_f32(w + 7 * ldw);
#define STEP(l)                                                                                 \
    do {                                                                                        \
        float32x4_t xl = vld1q_f32(xt + (size_t)(p + l) * 8);                                   \
        LANE(a0, xl, v0, l); LANE(a1, xl, v1, l); LANE(a2, xl, v2, l); LANE(a3, xl, v3, l);     \
        LANE(a4, xl, v4, l); LANE(a5, xl, v5, l); LANE(a6, xl, v6, l); LANE(a7, xl, v7, l);     \
    } while (0)
        STEP(0);
        STEP(1);
        STEP(2);
        STEP(3);
#undef STEP
    }
    z = vdupq_n_f32(0);
    vst1q_f32(c + 0, a0); vst1q_f32(c + 8, a1); vst1q_f32(c + 16, a2); vst1q_f32(c + 24, a3);
    vst1q_f32(c + 32, a4); vst1q_f32(c + 40, a5); vst1q_f32(c + 48, a6); vst1q_f32(c + 56, a7);
    for (int n = 0; n < 8; n++) vst1q_f32(c + n * 8 + 4, z);
}
#undef LANE

static void kernel(const float *w, int ldw, const float *xt, int k, int wide, float *c) {
    if (wide) kernel8(w, ldw, xt, k, c);
    else kernel4(w, ldw, xt, k, c);
}
#else
static void kernel(const float *w, int ldw, const float *xt, int k, int wide, float *c) {
    /* portable (GCC/Clang vector extensions): the same order of operations */
    typedef float v8 __attribute__((vector_size(32)));
    v8 acc[8] = {{0}};
    for (int p = 0; p < k; p++) {
        v8 xv;
        memcpy(&xv, xt + (size_t)p * 8, sizeof xv);
        for (int n = 0; n < 8; n++) acc[n] += w[(size_t)n * ldw + p] * xv;
    }
    for (int n = 0; n < 8; n++) {
        memcpy(c + n * 8, &acc[n], sizeof acc[n]);
        if (!wide)
            for (int m = 4; m < 8; m++) c[n * 8 + m] = 0;
    }
}
#endif

/* Each dot product is summed in runs of KRUN and the runs added: a little closer to
 * exact than one running sum over all of k, for 64 adds per run. */
#define KRUN 128

/* y[m][n0+b] (+)= x[m] . panel[b] for all rows, b < nr, from packed xt */
static void panel_rows(const float *xt, int rows, const float *panel, int nr, int k, float *y, int ldy, int add) {
    float c[64], part[64];
    for (int t = 0; t < rows; t += 8) {
        int mw = rows - t < 8 ? rows - t : 8;
        const float *xs = xt + (size_t)t * k;
        kernel(panel, k, xs, k < KRUN ? k : KRUN, mw > 4, c);
        for (int p = KRUN; p < k; p += KRUN) {
            kernel(panel + p, k, xs + (size_t)p * 8, k - p < KRUN ? k - p : KRUN, mw > 4, part);
            for (int i = 0; i < 64; i++) c[i] += part[i];
        }
        for (int i = 0; i < mw; i++) {
            float *r = y + (size_t)(t + i) * ldy;
            if (add)
                for (int b = 0; b < nr; b++) r[b] += c[b * 8 + i];
            else
                for (int b = 0; b < nr; b++) r[b] = c[b * 8 + i];
        }
    }
}
#endif /* !QSEL_ACCELERATE */

static float dot(const float *a, const float *b, int n) {
#if QSEL_NEON
    float32x4_t s0 = vdupq_n_f32(0), s1 = s0;
    for (int i = 0; i < n; i += 8) {
        s0 = vfmaq_f32(s0, vld1q_f32(a + i), vld1q_f32(b + i));
        s1 = vfmaq_f32(s1, vld1q_f32(a + i + 4), vld1q_f32(b + i + 4));
    }
    return vaddvq_f32(vaddq_f32(s0, s1));
#else
    float s[4] = {0};
    for (int i = 0; i < n; i += 4)
        for (int l = 0; l < 4; l++) s[l] += a[i + l] * b[i + l];
    return (s[0] + s[2]) + (s[1] + s[3]);
#endif
}

/* LayerNormalization without bias. Mean and variance are taken in two passes in
 * double: against a float64 evaluation of the same graph this is about 3x closer
 * than onnxruntime is (and than float sums of x and x*x, which drift on long inputs). */
static void layernorm(const float *x, const float *w, float *y, int n, float eps) {
    double mean = 0, var = 0;
    for (int i = 0; i < n; i++) mean += x[i];
    mean /= n;
    for (int i = 0; i < n; i++) var += (x[i] - mean) * (x[i] - mean);
    float m = (float)mean, den = (float)sqrt(var / n + eps);
    for (int i = 0; i < n; i++) y[i] = (x[i] - m) / den * w[i];
}

static inline float gelu(float x) { return x * (0.5f * (erff(x / 1.41421354f) + 1.0f)); }

#if QSEL_NEON
/* libm's expf and erff cost 27 and 66 ns a call on a Cortex-A55; these take a few
 * ns per lane. exp: Cephes (within 1.4 ulp on the range used here, softmax inputs
 * <= 0); below -87.3 it is 0. erf: odd polynomial on |x| < 0.5 (fitted, within
 * 1e-7), else 1 - erfc with the Numerical Recipes erfcc fit (fractional error
 * 1.2e-7). GELU through it stays within 4.5e-7 of exact, as with libm's erff. */
static inline float32x4_t vexpq(float32x4_t x) {
    float32x4_t xc = vminq_f32(vmaxq_f32(x, vdupq_n_f32(-87.3f)), vdupq_n_f32(88.3f));
    float32x4_t n = vrndnq_f32(vmulq_f32(xc, vdupq_n_f32(1.44269504088896341f)));
    float32x4_t r = vfmsq_f32(xc, n, vdupq_n_f32(0.693359375f));
    r = vfmsq_f32(r, n, vdupq_n_f32(-2.12194440e-4f));
    float32x4_t p = vdupq_n_f32(1.9875691500e-4f);
    p = vfmaq_f32(vdupq_n_f32(1.3981999507e-3f), p, r);
    p = vfmaq_f32(vdupq_n_f32(8.3334519073e-3f), p, r);
    p = vfmaq_f32(vdupq_n_f32(4.1665795894e-2f), p, r);
    p = vfmaq_f32(vdupq_n_f32(1.6666665459e-1f), p, r);
    p = vfmaq_f32(vdupq_n_f32(5.0000001201e-1f), p, r);
    p = vaddq_f32(vfmaq_f32(r, p, vmulq_f32(r, r)), vdupq_n_f32(1.0f));
    int32x4_t e = vshlq_n_s32(vaddq_s32(vcvtq_s32_f32(n), vdupq_n_s32(127)), 23);
    float32x4_t y = vmulq_f32(p, vreinterpretq_f32_s32(e));
    return vbslq_f32(vcltq_f32(x, vdupq_n_f32(-87.3f)), vdupq_n_f32(0), y);
}

static inline float32x4_t verfq(float32x4_t x) {
    float32x4_t z = vabsq_f32(x), z2 = vmulq_f32(z, z);
    float32x4_t s = vdupq_n_f32(0.004730028100311756f);
    s = vfmaq_f32(vdupq_n_f32(-0.02676357887685299f), s, z2);
    s = vfmaq_f32(vdupq_n_f32(0.11282932758331299f), s, z2);
    s = vfmaq_f32(vdupq_n_f32(-0.37612614035606384f), s, z2);
    s = vfmaq_f32(vdupq_n_f32(1.128379225730896f), s, z2);
    s = vmulq_f32(s, z);
    float32x4_t t = vdivq_f32(vdupq_n_f32(1.0f), vfmaq_f32(vdupq_n_f32(1.0f), z, vdupq_n_f32(0.5f)));
    static const float c[10] = {0.17087277f, -0.82215223f, 1.48851587f, -1.13520398f, 0.27886807f,
                                -0.18628806f, 0.09678418f, 0.37409196f, 1.00002368f, -1.26551223f};
    float32x4_t q = vdupq_n_f32(c[0]);
    for (int i = 1; i < 10; i++) q = vfmaq_f32(vdupq_n_f32(c[i]), q, t);
    float32x4_t big = vsubq_f32(vdupq_n_f32(1.0f), vmulq_f32(t, vexpq(vsubq_f32(q, z2))));
    float32x4_t r = vbslq_f32(vcltq_f32(z, vdupq_n_f32(0.5f)), s, big);
    return vbslq_f32(vcltq_f32(x, vdupq_n_f32(0)), vnegq_f32(r), r);
}

static inline float32x4_t vgeluq(float32x4_t x) {
    float32x4_t e = verfq(vdivq_f32(x, vdupq_n_f32(1.41421354f)));
    return vmulq_f32(x, vmulq_f32(vdupq_n_f32(0.5f), vaddq_f32(e, vdupq_n_f32(1.0f))));
}
#endif

/* x[i] = gelu(x[i]) */
static void gelu_n(float *x, int n) {
    int i = 0;
#if QSEL_NEON
    for (; i + 4 <= n; i += 4) vst1q_f32(x + i, vgeluq(vld1q_f32(x + i)));
#endif
    for (; i < n; i++) x[i] = gelu(x[i]);
}

/* g[j] = gelu(u[j]) * u[n + j] */
static void geglu(const float *u, float *g, int n) {
    int j = 0;
#if QSEL_NEON
    for (; j + 4 <= n; j += 4) vst1q_f32(g + j, vmulq_f32(vgeluq(vld1q_f32(u + j)), vld1q_f32(u + n + j)));
#endif
    for (; j < n; j++) g[j] = gelu(u[j]) * u[n + j];
}

static float maxf_n(const float *x, int n) {
    float top = -INFINITY;
    int i = 0;
#if QSEL_NEON
    if (n >= 4) {
        float32x4_t v = vld1q_f32(x);
        for (i = 4; i + 4 <= n; i += 4) v = vmaxq_f32(v, vld1q_f32(x + i));
        top = vmaxvq_f32(v);
    }
#endif
    for (; i < n; i++)
        if (x[i] > top) top = x[i];
    return top;
}

/* sum of exp(x[i] - top); with store, x[i] becomes exp(x[i] - top) */
static float sumexp_n(float *x, int n, float top, int store) {
    float sum = 0;
    int i = 0;
#if QSEL_NEON
    float32x4_t acc = vdupq_n_f32(0), t = vdupq_n_f32(top);
    for (; i + 4 <= n; i += 4) {
        float32x4_t e = vexpq(vsubq_f32(vld1q_f32(x + i), t));
        if (store) vst1q_f32(x + i, e);
        acc = vaddq_f32(acc, e);
    }
    sum = vaddvq_f32(acc);
    for (; i < n; i++) {
        float e = vgetq_lane_f32(vexpq(vdupq_n_f32(x[i] - top)), 0);
        if (store) x[i] = e;
        sum += e;
    }
#else
    for (; i < n; i++) {
        float e = expf(x[i] - top);
        if (store) x[i] = e;
        sum += e;
    }
#endif
    return sum;
}

/* ---- matmul over a thread pool ---- */

typedef struct {
    const float *x, *xt; /* activations, row-major and packed */
    int rows, ldx;
    const q4mat *w;  /* or */
    const float *wf; /* fp32 [n][k], n % NT == 0 */
    int n, k;
    float *y;
    int ldy, add;
    float *panels; /* per thread ACC_NT*kmax */
    int kmax;
} mm_t;

#ifdef QSEL_ACCELERATE
static void mm_task(void *ctx, int t, int thread) {
    mm_t *j = ctx;
    int n0 = t * ACC_NT, nr = j->n - n0 < ACC_NT ? j->n - n0 : ACC_NT;
    const float *panel = j->wf + (size_t)n0 * j->k;
    if (j->w) {
        float *p = j->panels + (size_t)thread * ACC_NT * j->kmax;
        for (int r = 0; r < nr; r += NT) dequant(j->w, n0 + r, nr - r < NT ? nr - r : NT, p + (size_t)r * j->k);
        panel = p;
    }
    cblas_sgemm(CblasRowMajor, CblasNoTrans, CblasTrans, j->rows, nr, j->k, 1.0f, j->x, j->ldx, panel, j->k,
                j->add ? 1.0f : 0.0f, j->y + n0, j->ldy);
}
#else
static void mm_task(void *ctx, int t, int thread) {
    mm_t *j = ctx;
    int n0 = t * NT, nr = j->n - n0 < NT ? j->n - n0 : NT;
    const float *panel;
    if (j->w) {
        float *p = j->panels + (size_t)thread * NT * j->kmax;
        dequant(j->w, n0, nr, p);
        panel = p;
    } else {
        panel = j->wf + (size_t)n0 * j->k;
    }
    panel_rows(j->xt, j->rows, panel, nr, j->k, j->y + n0, j->ldy, j->add);
}
#endif

typedef struct {
    float *panels;
    int kmax;
    float *xt;
} scratch_t;

enum { REPACK, SAME_X }; /* SAME_X: x is what the previous matmul packed, unchanged */

/* y[rows][n] (+)= x[rows][k] . w^T */
static void matmul(qsel_model *m, const scratch_t *s, int same, const float *x, int ldx, int rows, const q4mat *w,
                   const float *wf, int n, int k, float *y, int ldy, int add) {
#ifndef QSEL_ACCELERATE
    if (same != SAME_X) pack_x(x, ldx, rows, k, s->xt);
#else
    (void)same;
#endif
    mm_t j = {x, s->xt, rows, ldx, w, wf, n, k, y, ldy, add, s->panels, s->kmax};
    run(m, mm_task, &j, (n + ACC_NT - 1) / ACC_NT);
}

/* ---- attention ---- */

typedef struct {
    qsel_model *m;
    const float *q, *k, *v;
    float *o;
    int L, first; /* 算 first..L-1 列的 query；key、value 是全部 L 列 */
    const int32_t *rowptr, *cols;
    float *rowbuf; /* per thread L */
} att_t;

static void att_task(void *ctx, int t, int thread) {
    att_t *a = ctx;
    qsel_model *m = a->m;
    int D = m->D, hd = m->hd, L = a->L;
    int blocks = (L - a->first + ATT_ROWS - 1) / ATT_ROWS;
    int h = t / blocks, i0 = a->first + (t % blocks) * ATT_ROWS;
    int i1 = i0 + ATT_ROWS < L ? i0 + ATT_ROWS : L;
    float *sc = a->rowbuf + (size_t)thread * L;
    for (int i = i0; i < i1; i++) {
        const float *qi = a->q + (size_t)i * D + h * hd;
        float *oi = a->o + (size_t)i * D + h * hd;
        int b = a->rowptr[i], e = a->rowptr[i + 1];
        for (int d = 0; d < hd; d++) oi[d] = 0;
        if (b == e) {
            /* Nothing allowed: onnxruntime then sees every score as float32-lowest
             * and weighs all keys equally. */
            for (int j = 0; j < L; j++) {
                const float *vj = a->v + (size_t)j * D + h * hd;
                for (int d = 0; d < hd; d++) oi[d] += vj[d] * (1.0f / (float)L);
            }
            continue;
        }
#if QSEL_NEON
        if (hd == 64) { /* the query and the output row stay in registers */
            float32x4_t qv[16];
            for (int d = 0; d < 16; d++) qv[d] = vld1q_f32(qi + 4 * d);
            for (int p = b; p < e; p++) {
                const float *kj = a->k + (size_t)a->cols[p] * D + h * hd;
                float32x4_t s0 = vmulq_f32(qv[0], vld1q_f32(kj)), s1 = vmulq_f32(qv[1], vld1q_f32(kj + 4));
                float32x4_t s2 = vmulq_f32(qv[2], vld1q_f32(kj + 8)), s3 = vmulq_f32(qv[3], vld1q_f32(kj + 12));
                for (int d = 4; d < 16; d += 4) {
                    s0 = vfmaq_f32(s0, qv[d], vld1q_f32(kj + 4 * d));
                    s1 = vfmaq_f32(s1, qv[d + 1], vld1q_f32(kj + 4 * d + 4));
                    s2 = vfmaq_f32(s2, qv[d + 2], vld1q_f32(kj + 4 * d + 8));
                    s3 = vfmaq_f32(s3, qv[d + 3], vld1q_f32(kj + 4 * d + 12));
                }
                sc[p - b] = vaddvq_f32(vaddq_f32(vaddq_f32(s0, s1), vaddq_f32(s2, s3))) * m->scale;
            }
            float mx = maxf_n(sc, e - b);
            float inv = 1.0f / sumexp_n(sc, e - b, mx, 1);
            float32x4_t ov[16];
            for (int d = 0; d < 16; d++) ov[d] = vdupq_n_f32(0);
            for (int p = b; p < e; p++) {
                float32x4_t wv = vdupq_n_f32(sc[p - b] * inv);
                const float *vj = a->v + (size_t)a->cols[p] * D + h * hd;
                for (int d = 0; d < 16; d++) ov[d] = vfmaq_f32(ov[d], vld1q_f32(vj + 4 * d), wv);
            }
            for (int d = 0; d < 16; d++) vst1q_f32(oi + 4 * d, ov[d]);
            continue;
        }
#endif
        for (int p = b; p < e; p++) sc[p - b] = dot(qi, a->k + (size_t)a->cols[p] * D + h * hd, hd) * m->scale;
        float mx = maxf_n(sc, e - b);
        float inv = 1.0f / sumexp_n(sc, e - b, mx, 1);
        for (int p = b; p < e; p++) {
            float w = sc[p - b] * inv;
            const float *vj = a->v + (size_t)a->cols[p] * D + h * hd;
            for (int d = 0; d < hd; d++) oi[d] += w * vj[d];
        }
    }
}

/* ---- output layer: log softmax over the vocabulary, streamed ---- */

typedef struct {
    qsel_model *m;
    const float *x, *xt; /* head output, row-major and packed */
    int P;
    float *mx, *sm;   /* [chunks][P] running max and sum of exp */
    const int32_t *tgt;    /* (vocabulary row, proj row, index in the target list), sorted */
    const int32_t *tstart; /* [chunks+1] */
    float *tlogit;         /* [R*N] */
    float *panels;
    float *logits; /* per thread P*VCHUNK */
    int kmax;
} out_t;

static void out_task(void *ctx, int c, int thread) {
    out_t *o = ctx;
    qsel_model *m = o->m;
    int P = o->P;
    int v0 = c * VCHUNK, v1 = v0 + VCHUNK < m->vocab ? v0 + VCHUNK : m->vocab;
    float *panel = o->panels + (size_t)thread * ACC_NT * o->kmax;
    float *lg = o->logits + (size_t)thread * P * VCHUNK;
    float *mx = o->mx + (size_t)c * P, *sm = o->sm + (size_t)c * P;
    int nv = v1 - v0;
    /* logits of the whole chunk for every row, then one max and one pass of exp per row */
#ifdef QSEL_ACCELERATE
    for (int n0 = v0; n0 < v1; n0 += ACC_NT) {
        int nr = v1 - n0 < ACC_NT ? v1 - n0 : ACC_NT;
        for (int r = 0; r < nr; r += NT) dequant(&m->out, n0 + r, nr - r < NT ? nr - r : NT, panel + (size_t)r * m->D);
        cblas_sgemm(CblasRowMajor, CblasNoTrans, CblasTrans, P, nr, m->D, 1.0f, o->x, m->D, panel, m->D, 0.0f,
                    lg + (n0 - v0), VCHUNK);
    }
#else
    for (int n0 = v0; n0 < v1; n0 += NT) {
        int nr = v1 - n0 < NT ? v1 - n0 : NT;
        dequant(&m->out, n0, nr, panel);
        panel_rows(o->xt, P, panel, nr, m->D, lg + (n0 - v0), VCHUNK, 0);
    }
#endif
    const float *bias = m->out_bias + v0;
    for (int p = 0; p < P; p++) {
        float *r = lg + (size_t)p * VCHUNK;
        for (int b = 0; b < nv; b++) r[b] += bias[b];
        float top = maxf_n(r, nv);
        mx[p] = top;
        sm[p] = top == -INFINITY ? 0 : sumexp_n(r, nv, top, 0);
    }
    for (int t = o->tstart[c]; t < o->tstart[c + 1]; t++) {
        const int32_t *e = o->tgt + 3 * t;
        o->tlogit[e[2]] = lg[(size_t)e[1] * VCHUNK + (e[0] - v0)];
    }
}

/* ---- workspace ---- */

typedef struct {
    size_t x, h, q, k, v, xt, mlp_up, mlp_gate, panels, rowbuf, cosv, sinv, rowptr, cols, tsort, tstart, tlogit, mx,
        sm, logits, total;
} layout_t;

static size_t al(size_t n) { return (n + 63) & ~(size_t)63; }

static void plan(const qsel_model *m, int64_t L, int64_t P, int64_t RN, int64_t nnz, layout_t *w) {
    size_t D = (size_t)m->D, T = (size_t)m->threads, half = (size_t)m->hd / 2;
    size_t kmax = (size_t)(m->inter > m->D ? m->inter : m->D);
    size_t chunks = ((size_t)m->vocab + VCHUNK - 1) / VCHUNK;
    size_t mrows = (size_t)(L < MLP_ROWS ? L : MLP_ROWS);
    size_t off = 0;
#define AT(f, bytes) do { w->f = off; off += al(bytes); } while (0)
    AT(x, (size_t)L * D * 4);
    AT(h, (size_t)L * D * 4);
    AT(q, (size_t)L * D * 4);
    AT(k, (size_t)L * D * 4);
    AT(v, (size_t)L * D * 4);
    {
        size_t a = xt_floats((int)L, (int)D), b = xt_floats((int)mrows, m->inter);
        AT(xt, (a > b ? a : b) * 4);
    }
    AT(mlp_up, mrows * 2 * (size_t)m->inter * 4);
    AT(mlp_gate, mrows * (size_t)m->inter * 4);
    AT(panels, T * ACC_NT * kmax * 4);
    AT(rowbuf, T * (size_t)L * 4);
    AT(cosv, (size_t)L * half * 4);
    AT(sinv, (size_t)L * half * 4);
    AT(rowptr, ((size_t)L + 1) * 4);
    AT(cols, (size_t)nnz * 4);
    AT(tsort, (size_t)RN * 3 * 4);
    AT(tstart, (chunks + 1) * 4);
    AT(tlogit, (size_t)RN * 4);
    AT(mx, chunks * (size_t)P * 4);
    AT(sm, chunks * (size_t)P * 4);
    AT(logits, T * (size_t)P * VCHUNK * 4);
#undef AT
    w->total = off;
}

size_t qsel_workspace_bytes(const qsel_model *m, int64_t L, int64_t P, int64_t R, int64_t N) {
    layout_t w;
    plan(m, L, P, R * N, L * L, &w); /* upper bound: a full mask */
    return w.total;
}

/* ---- prefix cache ---- */

#define PC_TOK 5 /* id、base、tone、type、position */

/* 快取現在佔的 bytes。 */
static size_t prefix_bytes(const qsel_model *m) {
    size_t r = (size_t)m->pc_rows;
    return (size_t)m->pc_alloc * m->layers * 2 * m->D * 4 + r * (PC_TOK * 8 + 4) + r * r;
}

/* 這次呼叫前面幾列可以直接拿快取的 key、value。每一層只把一列跟遮罩允許的列混在一起，
 * 所以 [0, c) 這幾列在兩次呼叫算出來一樣的條件是：每一列在兩邊的輸入相同、遮罩在
 * [0, c) 內相同、兩次呼叫裡都不看 c 以後的列（前綴之後的列從沒影響過它們），而且至少
 * 看一列（整列遮掉的列要平均全部 L 列）。APP 的輸入（BOS、前文、SEP、最後一個音節之前
 * 的讀音，接著是候選樹，每個節點排在父節點後面）每一列只看自己和前面的列，所以這就是
 * 兩次輸入的共同前綴。 */
static int prefix_reuse(const qsel_model *m, const qsel_inputs *in, const int32_t *rowptr, const int32_t *cols) {
    int C = m->pc_rows, L = (int)in->L, lim = C < L ? C : L;
    int best = 0, reach = -1, diff = lim;
    for (int i = 0; i < lim; i++) {
        const int64_t *t = m->pc_tok + (size_t)i * PC_TOK;
        if (t[0] != in->ids[i] || t[1] != in->bases[i] || t[2] != in->tones[i] || t[3] != in->types[i] ||
            t[4] != in->positions[i] || rowptr[i] == rowptr[i + 1])
            break;
        int r = cols[rowptr[i + 1] - 1]; /* 欄照順序排：允許的最後一欄 */
        if (r > reach) reach = r;
        if (m->pc_reach[i] > reach) reach = m->pc_reach[i];
        const uint8_t *a = in->mask + (size_t)i * L, *b = m->pc_mask + (size_t)i * C;
        for (int j = 0; j < diff; j++)
            if ((a[j] != 0) != b[j]) {
                diff = j;
                break;
            }
        if (diff <= i) break; /* 遮罩在 [0, i] 內就不同：更長的前綴都不會相符 */
        if (reach <= i) best = i + 1;
    }
    return best;
}

/* 這次呼叫前面幾列（最多 max）留給下一次：每列至少看一列，且都不看最後一列之後。 */
static int prefix_keep(int max, int L, const int32_t *rowptr, const int32_t *cols) {
    int lim = max < L ? max : L, best = 0, reach = -1;
    for (int i = 0; i < lim; i++) {
        if (rowptr[i] == rowptr[i + 1]) break;
        int r = cols[rowptr[i + 1] - 1];
        if (r > reach) reach = r;
        if (reach <= i) best = i + 1;
    }
    return best;
}

int qsel_set_prefix_cache(qsel_model *m, int max_rows) {
    if (max_rows < 0 || max_rows > MAX_L) return QSEL_EINPUT;
    pthread_mutex_lock(&m->call);
    m->pc_max = max_rows;
    if (m->pc_rows > max_rows || max_rows == 0) prefix_free(m);
    pthread_mutex_unlock(&m->call);
    return QSEL_OK;
}

void qsel_prefix_info(qsel_model *m, int *reused, int *kept, size_t *bytes) {
    pthread_mutex_lock(&m->call);
    if (reused) *reused = m->pc_reused;
    if (kept) *kept = m->pc_rows;
    if (bytes) *bytes = prefix_bytes(m);
    pthread_mutex_unlock(&m->call);
}

/* ---- the forward pass ---- */

static int check(const qsel_model *m, const qsel_inputs *in) {
    int64_t L = in->L, P = in->P, R = in->R, N = in->N;
    if (L < 1 || L > MAX_L || P < 1 || P > L || R < 1 || N < 1 || R > MAX_TARGETS || N > MAX_TARGETS ||
        R * N > MAX_TARGETS)
        return QSEL_EINPUT;
    if (!in->ids || !in->bases || !in->tones || !in->types || !in->positions || !in->mask || !in->proj_pos ||
        !in->target_node || !in->target_ids)
        return QSEL_EINPUT;
    for (int64_t i = 0; i < L; i++)
        if (in->ids[i] < 0 || in->ids[i] >= m->vocab || in->bases[i] < 0 || in->bases[i] >= m->n_bases ||
            in->tones[i] < 0 || in->tones[i] >= m->n_tones || in->types[i] < 0 || in->types[i] >= m->n_types)
            return QSEL_EINPUT;
    for (int64_t i = 0; i < P; i++)
        if (in->proj_pos[i] < 0 || in->proj_pos[i] >= L) return QSEL_EINPUT;
    for (int64_t i = 0; i < R * N; i++)
        if (in->target_node[i] < 0 || in->target_node[i] >= P || in->target_ids[i] < 0 ||
            in->target_ids[i] >= m->n_out)
            return QSEL_EINPUT;
    return QSEL_OK;
}

static int cmp_target(const void *a, const void *b) {
    const int32_t *x = a, *y = b;
    if (x[0] != y[0]) return x[0] < y[0] ? -1 : 1;
    return x[2] < y[2] ? -1 : x[2] > y[2];
}

static void rope(float *x, int L, int D, int hd, const float *cs, const float *sn) {
    int half = hd / 2;
    for (int i = 0; i < L; i++)
        for (int h = 0; h < D; h += hd) {
            float *r = x + (size_t)i * D + h;
            const float *c = cs + (size_t)i * half, *s = sn + (size_t)i * half;
            for (int j = 0; j < half; j++) {
                float a = r[j], b = r[j + half];
                r[j] = a * c[j] + -b * s[j];
                r[j + half] = b * c[j] + a * s[j];
            }
        }
}

int qsel_score(qsel_model *m, const qsel_inputs *in, float *scores) {
    int rc = check(m, in);
    if (rc != QSEL_OK) return rc;
    int L = (int)in->L, P = (int)in->P, RN = (int)(in->R * in->N), D = m->D, hd = m->hd, NI = m->inter;
    int64_t nnz = 0;
    for (int64_t i = 0; i < (int64_t)L * L; i++) nnz += in->mask[i] != 0;
    layout_t w;
    plan(m, L, P, RN, nnz, &w);
    pthread_mutex_lock(&m->call);
    uint8_t *ws = malloc(w.total);
    if (!ws) {
        pthread_mutex_unlock(&m->call);
        return QSEL_ENOMEM;
    }
    float *x = (float *)(ws + w.x), *hb = (float *)(ws + w.h), *q = (float *)(ws + w.q), *k = (float *)(ws + w.k),
          *v = (float *)(ws + w.v), *up = (float *)(ws + w.mlp_up), *gate = (float *)(ws + w.mlp_gate);
    float *cs = (float *)(ws + w.cosv), *sn = (float *)(ws + w.sinv);
    int32_t *rowptr = (int32_t *)(ws + w.rowptr), *cols = (int32_t *)(ws + w.cols);
    int kmax = NI > D ? NI : D;
    scratch_t s = {(float *)(ws + w.panels), kmax, (float *)(ws + w.xt)};

    /* sparse mask rows */
    {
        int32_t n = 0;
        for (int i = 0; i < L; i++) {
            rowptr[i] = n;
            const uint8_t *r = in->mask + (size_t)i * L;
            for (int j = 0; j < L; j++)
                if (r[j]) cols[n++] = j;
        }
        rowptr[L] = n;
    }
    /* [0, c) 列用上一次呼叫留下的 key、value（qsel_set_prefix_cache）；[0, keep) 列留給下一次。 */
    int c = 0, keep = 0;
    int64_t *ntok = NULL;
    uint8_t *nmask = NULL;
    int32_t *nreach = NULL;
    if (m->pc_max > 0) {
        if (m->pc_rows > 0) {
            c = prefix_reuse(m, in, rowptr, cols);
            /* 只留 key、value，不留各列的輸出：要投影的列照算 */
            for (int p = 0; p < P; p++)
                if (in->proj_pos[p] < c) c = (int)in->proj_pos[p];
        }
        keep = prefix_keep(m->pc_max, L, rowptr, cols); /* >= c：c 以前的列都不看 c 以後 */
        if (keep > m->pc_alloc) {
            float *kv = realloc(m->pc_kv, (size_t)keep * m->layers * 2 * D * 4);
            if (kv) m->pc_kv = kv, m->pc_alloc = keep;
            else keep = prefix_keep(m->pc_alloc, L, rowptr, cols); /* 放得下的部分，仍 >= c */
        }
        if (keep > 0) {
            ntok = malloc((size_t)keep * PC_TOK * 8);
            nmask = malloc((size_t)keep * keep);
            nreach = malloc((size_t)keep * 4);
            if (!ntok || !nmask || !nreach) {
                free(ntok), free(nmask), free(nreach);
                ntok = NULL, nmask = NULL, nreach = NULL;
                keep = 0;
            }
        }
    }
    const size_t kvrow = (size_t)m->layers * 2 * D;
    int Lr = L - c; /* 要算的列數；>= 1，因為要投影的列一定算 */

    /* rotary tables */
    {
        int half = hd / 2;
        for (int i = c; i < L; i++) {
            float pos = (float)in->positions[i];
            for (int j = 0; j < half; j++) {
                float f = m->inv_freq[j] * pos;
                cs[(size_t)i * half + j] = cosf(f);
                sn[(size_t)i * half + j] = sinf(f);
            }
        }
    }
    /* embeddings */
    for (int i = c; i < L; i++) {
        float *e = hb + (size_t)i * D;
        int64_t ty = in->types[i];
        if (ty == m->reading_type) {
            const float *b = m->base_emb + (size_t)in->bases[i] * D, *t = m->tone_emb + (size_t)in->tones[i] * D;
            for (int d = 0; d < D; d++) e[d] = b[d] + t[d];
        } else {
            memcpy(e, m->dec_emb + (size_t)in->ids[i] * D, (size_t)D * 4);
        }
        const float *te = m->type_emb + (size_t)ty * D;
        for (int d = 0; d < D; d++) e[d] += te[d];
        layernorm(e, m->emb_norm, x + (size_t)i * D, D, m->eps);
    }

    /* 每一列的運算不因同一塊、同一段裡有哪些列而變，所以只算 [c, L) 列跟算全部 L 列，
     * 這些列得到的位元相同。 */
    att_t at = {m, q, k, v, hb, L, c, rowptr, cols, (float *)(ws + w.rowbuf)};
    int att_tasks = m->heads * ((Lr + ATT_ROWS - 1) / ATT_ROWS);
    float *xc = x + (size_t)c * D, *hc = hb + (size_t)c * D;
    int half = hd / 2;
    for (int l = 0; l < m->layers; l++) {
        const layer_t *ly = &m->layer[l];
        const float *ain = xc;
        if (ly->attn_norm) {
            for (int i = c; i < L; i++) layernorm(x + (size_t)i * D, ly->attn_norm, hb + (size_t)i * D, D, m->eps);
            ain = hc;
        }
        matmul(m, &s, REPACK, ain, D, Lr, &ly->wq, NULL, D, D, q + (size_t)c * D, D, 0);
        matmul(m, &s, SAME_X, ain, D, Lr, &ly->wk, NULL, D, D, k + (size_t)c * D, D, 0);
        matmul(m, &s, SAME_X, ain, D, Lr, &ly->wv, NULL, D, D, v + (size_t)c * D, D, 0);
        rope(q + (size_t)c * D, Lr, D, hd, cs + (size_t)c * half, sn + (size_t)c * half);
        rope(k + (size_t)c * D, Lr, D, hd, cs + (size_t)c * half, sn + (size_t)c * half);
        for (int i = 0; i < c; i++) {
            const float *r = m->pc_kv + i * kvrow + (size_t)2 * l * D;
            memcpy(k + (size_t)i * D, r, (size_t)D * 4);
            memcpy(v + (size_t)i * D, r + D, (size_t)D * 4);
        }
        for (int i = c; i < keep; i++) {
            float *r = m->pc_kv + i * kvrow + (size_t)2 * l * D;
            memcpy(r, k + (size_t)i * D, (size_t)D * 4);
            memcpy(r + D, v + (size_t)i * D, (size_t)D * 4);
        }
        run(m, att_task, &at, att_tasks); /* 寫 hb 的 [c, L) 列 */
        matmul(m, &s, REPACK, hc, D, Lr, &ly->wo, NULL, D, D, xc, D, 1);
        for (int i = c; i < L; i++) layernorm(x + (size_t)i * D, ly->mlp_norm, hb + (size_t)i * D, D, m->eps);
        /* even chunks, so that no chunk of a few rows dequantizes the MLP once more */
        int chunk = (Lr + (Lr + MLP_ROWS - 1) / MLP_ROWS - 1) / ((Lr + MLP_ROWS - 1) / MLP_ROWS);
        chunk = (chunk + 7) / 8 * 8 < MLP_ROWS ? (chunk + 7) / 8 * 8 : MLP_ROWS;
        for (int r0 = c; r0 < L; r0 += chunk) {
            int nr = L - r0 < chunk ? L - r0 : chunk;
            matmul(m, &s, REPACK, hb + (size_t)r0 * D, D, nr, &ly->up, NULL, 2 * NI, D, up, 2 * NI, 0);
            for (int i = 0; i < nr; i++) {
                const float *u = up + (size_t)i * 2 * NI;
                float *g = gate + (size_t)i * NI;
                geglu(u, g, NI);
            }
            matmul(m, &s, REPACK, gate, NI, nr, &ly->down, NULL, D, NI, x + (size_t)r0 * D, D, 1);
        }
    }
    if (m->pc_max > 0) {
        for (int i = 0; i < keep; i++) {
            int64_t *t = ntok + (size_t)i * PC_TOK;
            t[0] = in->ids[i], t[1] = in->bases[i], t[2] = in->tones[i], t[3] = in->types[i], t[4] = in->positions[i];
            for (int j = 0; j < keep; j++) nmask[(size_t)i * keep + j] = in->mask[(size_t)i * L + j] != 0;
            nreach[i] = cols[rowptr[i + 1] - 1];
        }
        free(m->pc_tok), free(m->pc_mask), free(m->pc_reach);
        m->pc_tok = ntok, m->pc_mask = nmask, m->pc_reach = nreach;
        m->pc_rows = keep;
        if (keep == 0) {
            free(m->pc_kv);
            m->pc_kv = NULL, m->pc_alloc = 0;
        } else if (keep < m->pc_alloc) { /* 這次沒留的部分還回去 */
            float *kv = realloc(m->pc_kv, (size_t)keep * kvrow * 4);
            if (kv) m->pc_kv = kv, m->pc_alloc = keep;
        }
    }
    m->pc_reused = c;

    /* head on the projected rows: q = final norm, k = dense, v = gelu + norm */
    for (int p = 0; p < P; p++) layernorm(x + (size_t)in->proj_pos[p] * D, m->final_norm, q + (size_t)p * D, D, m->eps);
    matmul(m, &s, REPACK, q, D, P, NULL, m->head_dense, D, D, k, D, 0);
    for (int p = 0; p < P; p++) {
        float *r = k + (size_t)p * D;
        gelu_n(r, D);
        layernorm(r, m->head_norm, v + (size_t)p * D, D, m->eps);
    }

    /* targets sorted by vocabulary row, then streamed with the vocabulary */
    int chunks = (m->vocab + VCHUNK - 1) / VCHUNK;
    int32_t *ts = (int32_t *)(ws + w.tsort), *tstart = (int32_t *)(ws + w.tstart);
    for (int i = 0; i < RN; i++) {
        ts[3 * i] = (int32_t)m->out_ids[in->target_ids[i]];
        ts[3 * i + 1] = (int32_t)in->target_node[i];
        ts[3 * i + 2] = i;
    }
    qsort(ts, (size_t)RN, 12, cmp_target);
    for (int c = 0, t = 0; c <= chunks; c++) {
        while (t < RN && ts[3 * t] < c * VCHUNK) t++;
        tstart[c] = t;
    }
    tstart[chunks] = RN;
    float *tlogit = (float *)(ws + w.tlogit), *mx = (float *)(ws + w.mx), *sm = (float *)(ws + w.sm);
#ifndef QSEL_ACCELERATE
    pack_x(v, D, P, D, s.xt);
#endif
    out_t ot = {m, v, s.xt, P, mx, sm, ts, tstart, tlogit, s.panels, (float *)(ws + w.logits), kmax};
    run(m, out_task, &ot, chunks);

    /* merge the chunks in a fixed order: log p = (x - max) - log(sum exp(x - max)) */
    float *lse_max = q, *lse_log = q + P; /* q is free now (P <= L) */
    for (int p = 0; p < P; p++) {
        float top = -INFINITY;
        for (int c = 0; c < chunks; c++)
            if (mx[(size_t)c * P + p] > top) top = mx[(size_t)c * P + p];
        float sum = 0;
        for (int c = 0; c < chunks; c++)
            if (mx[(size_t)c * P + p] != -INFINITY) sum += sm[(size_t)c * P + p] * expf(mx[(size_t)c * P + p] - top);
        lse_max[p] = top;
        lse_log[p] = logf(sum);
    }
    int N = (int)in->N;
    for (int r = 0; r < (int)in->R; r++) {
        double acc = 0; /* up to 32 terms of a score near -400: sum in double */
        for (int j = 0; j < N; j++) {
            int i = r * N + j;
            int p = (int)in->target_node[i];
            acc += ((double)tlogit[i] - lse_max[p]) - lse_log[p];
        }
        scores[r] = (float)acc;
    }
    free(ws);
    pthread_mutex_unlock(&m->call);
    return QSEL_OK;
}

int qsel_score_packed(qsel_model *m, const void *buf, size_t len, float *scores, size_t cap, size_t *n) {
    int64_t hdr[4];
    if (!buf || len < sizeof hdr) return QSEL_EINPUT;
    memcpy(hdr, buf, sizeof hdr);
    int64_t L = hdr[0], P = hdr[1], R = hdr[2], N = hdr[3];
    if (L < 1 || L > MAX_L || P < 1 || P > L || R < 1 || N < 1 || R > MAX_TARGETS || N > MAX_TARGETS ||
        R * N > MAX_TARGETS)
        return QSEL_EINPUT;
    uint64_t words = 4 + 5 * (uint64_t)L + (uint64_t)P + 2 * (uint64_t)(R * N);
    if (len != words * 8 + (uint64_t)L * L) return QSEL_EINPUT;
    if (n) *n = (size_t)R;
    if ((uint64_t)R > cap) return QSEL_EINPUT;
    const uint8_t *b = buf;
    int64_t *w = NULL;
    const int64_t *v;
    if ((uintptr_t)b % 8) { /* int64 loads need alignment in C: copy the words */
        w = malloc(words * 8);
        if (!w) return QSEL_ENOMEM;
        memcpy(w, b, words * 8);
        v = w;
    } else {
        v = (const int64_t *)(const void *)b;
    }
    v += 4;
    qsel_inputs in = {.L = L, .P = P, .R = R, .N = N};
    in.ids = v, v += L;
    in.bases = v, v += L;
    in.tones = v, v += L;
    in.types = v, v += L;
    in.positions = v, v += L;
    in.proj_pos = v, v += P;
    in.target_node = v, v += R * N;
    in.target_ids = v;
    in.mask = b + words * 8;
    int rc = qsel_score(m, &in, scores);
    free(w);
    return rc;
}
