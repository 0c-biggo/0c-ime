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
 * reads them back from the file. The handle stays valid.
 * 也清空前綴快取。 */
void qsel_release_pages(qsel_model *m);

/* 前綴快取：每次呼叫把前面最多 max_rows 列每一層的 key、value 留給同一個 handle 的
 * 下一次呼叫，下一次只算兩次共同前綴之後的列。一列能沿用的條件是輸入與遮罩列相同、
 * 前綴裡沒有列看前綴之後（見 qsel.c prefix_reuse）；這時分數跟不開快取逐位元相同
 * （Accelerate 版因 sgemm 分塊，只在捨入誤差內相同）。APP 的輸入把前文與讀音放在前面，
 * 每一列只看前面的列，同一句繼續打字時這段就是共同前綴。
 *
 * 呼叫之間每列佔 層數 × 2 × hidden × 4 bytes（q4x-b16 是 32 KiB），加上這些列的輸入與
 * 遮罩；下一次呼叫或 qsel_release_pages 會還回不需要的部分。max_rows 0（預設）關掉並
 * 釋放。同一個 handle 的呼叫本來就一個一個跑，快取不需要另外上鎖；兩個執行緒在同一個
 * handle 上算不同句子，只會互相換掉對方的前綴，分數仍正確。
 * max_rows 超出 0..4096 回 QSEL_EINPUT。 */
int qsel_set_prefix_cache(qsel_model *m, int max_rows);

/* 上一次呼叫沿用幾列、快取現在留幾列、佔幾 bytes（指標可為 NULL）。 */
void qsel_prefix_info(qsel_model *m, int *reused, int *kept, size_t *bytes);

const char *qsel_strerror(int code);

#ifdef __cplusplus
}
#endif
#endif
