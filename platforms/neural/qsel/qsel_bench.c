/* qsel_bench: time qsel on recorded inputs and check them against reference scores.
 *
 *   qsel_bench [-t threads] [-r rounds] [-d model.onnx.data] [-o scores.bin] model.qsel cases.bin
 *
 * cases.bin is a sequence of records: uint64 n, n bytes of packed inputs (APP
 * mobile/neural Pack), uint64 R, R float32 reference scores (NaN = none); all
 * little-endian. qsel_parity.py --dump writes one with onnxruntime's scores.
 *
 * Prints the open time, the largest difference from the references and the
 * tolerance used (max(1e-4, 1e-6 |ref|)), call latency percentiles, and on Linux
 * and Android the process memory from /proc/self/smaps_rollup after open, after
 * the first round and at the end (VmHWM is the peak resident size). -o writes the
 * first round's scores (float32, case after case) for comparing builds or threads.
 */
#define _GNU_SOURCE
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

#include "qsel.h"

static double now_ms(void) {
    struct timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return t.tv_sec * 1e3 + t.tv_nsec / 1e6;
}

/* peak Private_Dirty and Rss, sampled from smaps_rollup every 2 ms while cases run */
#include <pthread.h>
static volatile int sampling;
static long peak_dirty_kb, peak_rss_kb;
static long rollup(const char *key) {
    FILE *f = fopen("/proc/self/smaps_rollup", "r");
    char line[256];
    long v = -1;
    if (!f) return -1;
    while (fgets(line, sizeof line, f))
        if (!strncmp(line, key, strlen(key))) v = atol(line + strlen(key));
    fclose(f);
    return v;
}
static void *sampler(void *arg) {
    (void)arg;
    struct timespec d = {0, 2000000};
    while (sampling) {
        long pd = rollup("Private_Dirty:"), rs = rollup("Rss:");
        if (pd > peak_dirty_kb) peak_dirty_kb = pd;
        if (rs > peak_rss_kb) peak_rss_kb = rs;
        nanosleep(&d, NULL);
    }
    return NULL;
}

static void memory(const char *when) {
    FILE *f = fopen("/proc/self/smaps_rollup", "r");
    char line[256];
    printf("memory %-12s", when);
    if (f) {
        while (fgets(line, sizeof line, f)) {
            static const char *keys[] = {"Rss:", "Pss:", "Shared_Clean:", "Private_Clean:", "Private_Dirty:", "Anonymous:"};
            for (size_t i = 0; i < sizeof keys / sizeof *keys; i++)
                if (!strncmp(line, keys[i], strlen(keys[i]))) {
                    long kb = atol(line + strlen(keys[i]));
                    printf(" %s%.1fMB", keys[i], kb / 1024.0);
                }
        }
        fclose(f);
    }
    f = fopen("/proc/self/status", "r");
    if (f) {
        while (fgets(line, sizeof line, f))
            if (!strncmp(line, "VmHWM:", 6)) printf(" VmHWM:%.1fMB", atol(line + 6) / 1024.0);
        fclose(f);
    }
    printf("\n");
}

typedef struct {
    uint8_t *in;
    size_t len;
    uint64_t R;
    float *ref;
} rec_t;

static int cmpd(const void *a, const void *b) {
    double x = *(const double *)a, y = *(const double *)b;
    return x < y ? -1 : x > y;
}

int main(int argc, char **argv) {
    int threads = 1, rounds = 3, opt;
    const char *data = NULL, *dump = NULL;
    while ((opt = getopt(argc, argv, "t:r:d:o:")) != -1) {
        if (opt == 't') threads = atoi(optarg);
        else if (opt == 'r') rounds = atoi(optarg);
        else if (opt == 'd') data = optarg;
        else if (opt == 'o') dump = optarg;
        else return 2;
    }
    if (argc - optind != 2) {
        fprintf(stderr, "usage: qsel_bench [-t threads] [-r rounds] [-d data] model.qsel cases.bin\n");
        return 2;
    }
    FILE *f = fopen(argv[optind + 1], "rb");
    if (!f) {
        perror("cases");
        return 1;
    }
    rec_t *recs = NULL;
    size_t n = 0;
    for (;;) {
        uint64_t len;
        if (fread(&len, 8, 1, f) != 1) break;
        recs = realloc(recs, (n + 1) * sizeof *recs);
        rec_t *r = &recs[n++];
        r->len = len;
        r->in = malloc(len);
        if (fread(r->in, 1, len, f) != len || fread(&r->R, 8, 1, f) != 1) return fprintf(stderr, "short file\n"), 1;
        r->ref = malloc(r->R * 4);
        if (fread(r->ref, 4, r->R, f) != r->R) return fprintf(stderr, "short file\n"), 1;
    }
    fclose(f);
    memory("before-open");
    double t0 = now_ms();
    qsel_model *m;
    int rc = qsel_open(argv[optind], data, threads, &m);
    if (rc) {
        fprintf(stderr, "qsel_open: %s\n", qsel_strerror(rc));
        return 1;
    }
    printf("open %.2f ms, threads %d, cases %zu, rounds %d\n", now_ms() - t0, threads, n, rounds);
    memory("after-open");
    FILE *df = dump ? fopen(dump, "wb") : NULL;
    double *ms = malloc(sizeof(double) * n * (rounds > 0 ? rounds : 1));
    size_t k = 0;
    double worst = 0, worst_used = 0, total = 0;
    size_t worst_case = 0, fails = 0;
    size_t maxws = 0;
    pthread_t smp;
    sampling = 1;
    pthread_create(&smp, NULL, sampler, NULL);
    for (int round = 0; round < rounds; round++) {
        for (size_t i = 0; i < n; i++) {
            rec_t *r = &recs[i];
            float *out = malloc(r->R * 4);
            size_t got;
            int64_t hdr[4];
            memcpy(hdr, r->in, 32);
            /* the bound assumes a full mask; the call itself keeps one int per allowed pair */
            size_t ws = qsel_workspace_bytes(m, hdr[0], hdr[1], hdr[2], hdr[3]), nnz = 0;
            for (const uint8_t *q = r->in + r->len - (size_t)(hdr[0] * hdr[0]); q < r->in + r->len; q++) nnz += *q != 0;
            ws -= 4 * ((size_t)(hdr[0] * hdr[0]) - nnz);
            if (ws > maxws) maxws = ws;
            double a = now_ms();
            rc = qsel_score_packed(m, r->in, r->len, out, r->R, &got);
            double b = now_ms();
            if (rc) {
                fprintf(stderr, "case %zu: %s\n", i, qsel_strerror(rc));
                return 1;
            }
            ms[k++] = b - a;
            total += b - a;
            if (round == 0 && df) fwrite(out, 4, r->R, df);
            if (round == 0)
                for (uint64_t j = 0; j < r->R; j++) {
                    if (isnan(r->ref[j])) continue;
                    double d = fabs((double)out[j] - r->ref[j]);
                    double tol = fmax(1e-4, 1e-6 * fabs(r->ref[j]));
                    if (!(d <= tol)) fails++;
                    if (d > worst) worst = d, worst_case = i;
                    if (d / tol > worst_used) worst_used = d / tol;
                }
            free(out);
        }
        if (round == 0) memory("after-round1");
    }
    if (df) fclose(df);
    sampling = 0;
    pthread_join(smp, NULL);
    memory("end");
    printf("sampled peak while running: Private_Dirty %.1fMB Rss %.1fMB\n", peak_dirty_kb / 1024.0, peak_rss_kb / 1024.0);
    qsort(ms, k, sizeof *ms, cmpd);
    if (k)
        printf("latency ms: p50 %.2f p90 %.2f p95 %.2f max %.2f mean %.2f (n=%zu)\n", ms[k / 2], ms[(size_t)(k * 0.9)],
               ms[(size_t)(k * 0.95)], ms[k - 1], total / k, k);
    printf("parity: max|d| %.3e (case %zu) tolerance used %.3f, %zu over tolerance\n", worst, worst_case, worst_used, fails);
    printf("workspace: largest call %.2f MB\n", maxws / 1048576.0);
    qsel_close(m);
    for (size_t i = 0; i < n; i++) free(recs[i].in), free(recs[i].ref);
    free(recs);
    free(ms);
    return fails ? 1 : 0;
}
