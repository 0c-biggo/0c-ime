/* qsel: the selection model (sequence-v2, q4 block 16) without onnxruntime.
 *
 * The weights stay in a read-only file mapping: nothing is copied, unpacked or
 * repacked at load time, so they are clean file pages that the system may drop
 * and read back. Each call allocates its activations and frees them before it
 * returns (qsel_workspace_bytes gives the size).
 *
 * Inputs and output are those of model.onnx (see the APP mobile/neural Inputs):
 * int64 ids, bases, tones, types, positions [L]; mask [L*L] (nonzero = may
 * attend, row = query); proj_pos [P]; target_node, target_ids [R*N]; scores [R].
 *
 * One model handle runs one call at a time (calls are serialized by a mutex).
 * Threads are created by qsel_open and sleep between calls.
 */
#ifndef QSEL_H
#define QSEL_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

enum {
    QSEL_OK = 0,
    QSEL_EIO = 1,      /* open, stat or mmap failed (errno is kept) */
    QSEL_EFORMAT = 2,  /* not a qsel file, or a version/shape this build does not know */
    QSEL_EINPUT = 3,   /* inputs out of range or inconsistent */
    QSEL_ENOMEM = 4,
    QSEL_ETHREAD = 5,
};

typedef struct qsel_model qsel_model;

typedef struct {
    int64_t L, P, R, N;
    const int64_t *ids, *bases, *tones, *types, *positions; /* [L] */
    const uint8_t *mask;                                     /* [L*L] */
    const int64_t *proj_pos;                                 /* [P] */
    const int64_t *target_node, *target_ids;                 /* [R*N] */
} qsel_inputs;

/* path: a qsel file. data_path: NULL when the data follows the header in the same
 * file; otherwise the unchanged model.onnx.data for a header-only file.
 * threads: 1 or more (the calling thread counts as one). */
int qsel_open(const char *path, const char *data_path, int threads, qsel_model **out);
void qsel_close(qsel_model *m);

/* Writes R scores. Returns QSEL_OK or an error; scores are untouched on error. */
int qsel_score(qsel_model *m, const qsel_inputs *in, float *scores);

/* The same inputs as the APP core packs them for the host (mobile/neural Pack, all
 * little-endian): int64 L, P, R, N; int64 ids, bases, tones, types, positions [L];
 * int64 proj_pos [P], target_node [R*N], target_ids [R*N]; byte mask [L*L].
 * Writes R scores (at most cap); *n gets R. The buffer may have any alignment. */
int qsel_score_packed(qsel_model *m, const void *buf, size_t len, float *scores, size_t cap, size_t *n);

/* Bytes one qsel_score call allocates for these shapes. */
size_t qsel_workspace_bytes(const qsel_model *m, int64_t L, int64_t P, int64_t R, int64_t N);

/* Drop the model's resident file pages (e.g. on a memory warning); the next call
 * reads them back from the file. The handle stays valid. */
void qsel_release_pages(qsel_model *m);

const char *qsel_strerror(int code);

#ifdef __cplusplus
}
#endif
#endif
