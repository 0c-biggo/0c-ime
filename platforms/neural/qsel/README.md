# qsel：手機選字模型的自寫推論（#336）

手機的選字模型 q4x-b16（sequence-v2、D512、8 層）不經 onnxruntime，直接用 C 跑。目的是 iOS 鍵盤 77 MB 的上限：onnxruntime 載入時要多 34–45 MB 的 dirty 記憶體（MLAS prepack、arena），qsel 的權重全留在唯讀 mmap 的乾淨檔案頁，dirty 只剩每次呼叫的活化值，呼叫結束就釋放。

數值照 `model.onnx` 在 onnxruntime CPU（MatMulNBits accuracy_level 1，也就是 fp32 計算）的定義：每個 4-bit 權重還原成 `(q − 8) × scale` 再用 fp32 乘加。不換模型、不重新量化，品質評估過的就是這份權重。

## 檔案

| 檔案 | 用途 |
|---|---|
| `qsel.h`、`qsel.c` | 推論函式庫（C11＋pthreads；arm64 用 NEON，Apple 可選 Accelerate） |
| `qsel_convert.py` | `model.onnx`＋`model.onnx.data` → qsel 檔（需要 numpy、onnx；只讀圖，不跑模型） |
| `qsel_parity.py` | 跟 onnxruntime 逐值比對（fixtures、隨機合法輸入、錄下的手機請求）；`--prefix` 另外要求開前綴快取時逐位元相同 |
| `qsel_reference.py` | 從 qsel 檔做 float64 的參考計算，判斷差異是哪一邊造成的 |
| `qsel_test.py` | 不需要模型的測試：隨機小模型對 float64、執行緒數、打包輸入、前綴快取、壞檔與壞輸入 |
| `qsel_bench.c` | 量延遲、記憶體（smaps_rollup）與分數差的命令列程式，Linux 與 Android 都能跑；`-p` 開前綴快取 |

```sh
make -C platforms/neural/qsel test            # 輸出在 build/qsel/
make -C platforms/neural/qsel android NDK=~/Android/Sdk/ndk/27.1.12297006
make -C platforms/neural/qsel parity MODEL=<套件目錄> PREPARE=<models>/data/fixtures/sequence-prepare-v2/fixtures.json
```

## 檔案格式（qsel v1，little-endian）

一個 qsel 檔是 64 KiB 的檔頭區，後面**逐位元組接上 `model.onnx.data`**。所以資料段的 SHA-256 就是 manifest 裡 `model.onnx.data` 的雜湊，每個張量也保留匯出器給的 64 KiB 對齊位置。也可以只寫檔頭（`--header-only`，`data_offset` = 0），執行時另外 mmap 原本的 `model.onnx.data`。

| offset | 型別 | 內容 |
|---:|---|---|
| 0 | char[8] | `0CQSEL1\0` |
| 8 | u32 | 版本 1 |
| 12 | u32 | 檔頭位元組數（424 + 32 × 張量數） |
| 16 | u32 | 檔頭 CRC-32（計算時這 4 bytes 當 0） |
| 20 | u32 | 保留 0 |
| 24 | u64 | 資料段 offset（合併檔 65536；只有檔頭時 0） |
| 32 | u64 | 資料段長度 |
| 40 | u8[32] | 資料段 SHA-256（轉檔時記下，執行時不驗；雜湊由宿主照 manifest 驗） |
| 72 | u8[32] | `model.onnx` 的 SHA-256 |
| 104 | u32×12 | hidden、layers、heads、head_dim、inter、vocab、bases、tones、types、out_ids 數、block（16）、讀音的 type 值（1） |
| 152 | f32×2 | LayerNorm eps、注意力縮放（1/√64） |
| 160 | f32×64 | 旋轉位置的 inv_freq（用前 head_dim/2 個） |
| 416 | u32×2 | 張量數、保留 |
| 424 | 32 B × n | 張量表：u32 slot、u32 dtype（1 = f32、2 = 4-bit block 16、3 = i64）、u32 rows、u32 cols、u64 資料段內 offset、u64 長度 |

slot：0 輸入嵌入 norm、1 final norm、2 head dense（f32 [512,512]）、3 head norm、4 輸入嵌入（f32 [27865,512]）、5 base、6 tone、7 type 嵌入、8 out_ids、9 輸出 bias、10／11 輸出層 4-bit／scale；第 l 層從 16 + 16l 起：0 attn norm（第 0 層沒有）、1 mlp norm、2–13 為 q、k、v、o、up（gate 與 up 合併，[1536,512]）、down 的 4-bit 與 scale。

- 4-bit 矩陣照 MatMulNBits 原樣：`[N][K/16][8 bytes]`，第 j 個 byte 的低 4 位是區塊第 2j 個元素、高 4 位是第 2j+1 個；scale 另存 `[N][K/16]` f32；沒有 zero point（固定 8）。每一列在檔案裡連續，kernel 一次讀 8 列還原成 fp32，所以不需要重排。K 必須是 32 的倍數（NEON 一次還原兩個區塊）。
- 轉檔工具只接受它認得的圖：比對整張圖的 op 數量、每層的接法（pre-norm、第 0 層沒有 attn norm、只有 q／k 做旋轉、GEGLU、殘差）、常數（eps、縮放、√2、遮罩填值）與 MatMulNBits 屬性（4 bits、block 16、沒有 zero point、accuracy_level 0／1）。不認得就拒絕，不猜。
- 權重為什麼是乾淨頁：整個檔案 `mmap(PROT_READ, MAP_SHARED)`，程式從不寫入，也不複製或 prepack；每次呼叫把需要的 8 列 4-bit 權重還原到每個執行緒 24 KB 的暫存，用完即丟。輸入嵌入表 57 MB 只讀到用到的列（`MADV_RANDOM`，不預讀）。這些頁不算 iOS 的 phys_footprint，記憶體吃緊時系統可以直接丟掉再讀回。

## C API

```c
int  qsel_open(const char *path, const char *data_path /* NULL＝合併檔 */, int threads, qsel_model **out);
void qsel_close(qsel_model *m);
int  qsel_score(qsel_model *m, const qsel_inputs *in, float *scores);            /* 九個張量 → R 個分數 */
int  qsel_score_packed(qsel_model *m, const void *buf, size_t len,                /* APP mobile/neural Pack 的位元組 */
                       float *scores, size_t cap, size_t *n);
size_t qsel_workspace_bytes(const qsel_model *m, int64_t L, int64_t P, int64_t R, int64_t N);
void qsel_release_pages(qsel_model *m);                                           /* 記憶體警告時丟掉常駐的檔案頁與前綴快取 */
int  qsel_set_prefix_cache(qsel_model *m, int max_rows);                          /* 沿用上一次呼叫的共同前綴；0＝關（預設） */
void qsel_prefix_info(qsel_model *m, int *reused, int *kept, size_t *bytes);      /* 上一次沿用幾列、快取幾列、幾 bytes */
const char *qsel_strerror(int code);
```

- 輸入、輸出跟 `model.onnx` 相同；`qsel_score_packed` 直接吃 Go 核心交給宿主的位元組（`neural.Inputs.Pack`），Swift／Kotlin 不用再拆成張量。
- 每次呼叫自己配置活化值、結束前釋放；大小見 `qsel_workspace_bytes`（上界，假設遮罩全開）。
- `qsel_open` 不讀權重，只驗檔頭與張量表，毫秒內完成；所以記憶體不足時可以直接 `qsel_close`，之後再開。
- 同一個 handle 一次跑一個呼叫（內部有 mutex）；執行緒在 `qsel_open` 建立，呼叫之間睡著。分數與執行緒數無關（逐位元相同）。
- 輸入超出範圍（id、base、tone、type、proj_pos、target）回 `QSEL_EINPUT`，不讀界外。

## 前綴快取

打字時同一句的請求，開頭的「BOS、前文、SEP、最後一個音節之前的讀音」都一樣，只有最後一個音節與候選樹在變。模型每一列只看遮罩允許的列，APP 的遮罩又只讓每一列看自己和前面的列，所以開頭這段每一層的 key／value 跟後面無關，可以留給下一次用。

- `qsel_set_prefix_cache(m, max_rows)` 打開後，每次呼叫留下前面最多 `max_rows` 列每一層的 key、value（轉過位置之後），連同這些列的輸入與遮罩；下一次呼叫只算共同前綴之後的列。
- 沿用的條件只看輸入，不靠呼叫端保證：前綴每一列的五個輸入相同、遮罩在前綴內相同、兩次呼叫裡都不看前綴之後的列、而且至少看一列（整列遮掉的列要平均全部 L 列）。要投影的列不沿用（快取不存輸出），所以沿用的列數不超過最小的 `proj_pos`。
- 分數跟不開快取逐位元相同：每一列的運算順序不因同一塊裡有哪些列而變（矩陣乘法每列各佔一個 lane，注意力、LayerNorm 各列獨立）。Accelerate 的 sgemm 不保證列數不同時分塊一樣，那邊只保證在捨入誤差內。
- 記憶體：每列 層數 × 2 × hidden × 4 bytes（q4x-b16 是 32 KiB），加上輸入與遮罩。呼叫之間一直佔著，下一次呼叫會縮到這次留下的列數；`qsel_release_pages` 與 `max_rows` 0 會釋放。
- 快取放在 handle 裡，由同一個 mutex 保護；兩個執行緒在同一個 handle 上交錯算不同句子只是互相換掉前綴，結果仍正確。
- 量測（#336，2026-11-25）：APP 核心照手機節奏打字錄下的 740 次請求（10 句連打、前文累積到 64 字的 146 次，與 Common Voice 60 句每 3 句清前文的 594 次），相鄰請求共同前綴占全部列的 57%。M127F 2 threads：p50 131→83 ms、p95 347→238 ms、平均 161→102 ms；`max_rows` 100 時快取最多 3.1 MB，取樣的 Private Dirty 尖峰 6.3→9.0 MB。不開快取的分數跟改動前逐位元相同，開了也相同（x86、M127F、M1 NEON）；Accelerate 版相差最多 1.1e-5（相對 9.4e-7），對 float64 的最大差不變。

## 跟 onnxruntime 的差別

- 計算順序不同：矩陣乘法每 128 個 k 分段累加；LayerNorm 的平均與變異數用 double 兩段算；每個分數的最後加總用 double。對 float64 參考計算，qsel 的誤差比 onnxruntime 小（量測見 #336）。
- 遮罩外的注意力不算（onnxruntime 加上 float32 最小值後 exp 為 0，結果相同）；整列都遮掉時照 onnxruntime 平均所有位置。
- arm64 的 exp 與 erf 用向量近似（exp 1.4 ulp 內、erf 1.8e-7 內），GELU 的誤差和 libm 相同等級；其他平台用 libm。
- 結果與執行緒數無關；onnxruntime 也是如此。
