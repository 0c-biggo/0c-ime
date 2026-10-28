// 以真的 0c-server 測試 C++ 用戶端：打字、補全、提交、斷線重播。
// 用法：session_test <socket> <0c-server 路徑> <資料目錄> <個人資料檔>
#include <cstdio>
#include <cstdlib>
#include <filesystem>
#include <fstream>
#include <string>
#include <sys/stat.h>
#include <unistd.h>

#include "../src/session.h"
#include "../src/shift.h"
#include "../src/context.h"

static int failures = 0;
#define CHECK(cond, ...)                                                                                               \
    do {                                                                                                               \
        if (!(cond)) {                                                                                                 \
            std::fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__);                                                  \
            std::fprintf(stderr, __VA_ARGS__);                                                                         \
            std::fprintf(stderr, "\n");                                                                                \
            failures++;                                                                                                \
        }                                                                                                              \
    } while (0)

int main(int argc, char **argv) {
    {
        // #112：鍵標照核心給的 key；舊核心沒有時依位置 1–0（第 10 項是 0），英文模式的補字候選不畫。
        using nlohmann::json;
        CHECK(zuin::candidateKey(json{{"key", "q"}}, 4, false) == "q", "核心給的選字鍵");
        CHECK(zuin::candidateKey(json{{"key", ";"}}, 9, true) == ";", "有 key 時照畫");
        CHECK(zuin::candidateKey(json{{"text", "是"}}, 0, false) == "1" && zuin::candidateKey(json{{"text", "是"}}, 9, false) == "0",
              "舊核心依位置 1–0");
        CHECK(zuin::candidateKey(json{{"text", "hello"}}, 0, true).empty(), "舊核心的英文補字不畫鍵標");
    }
    {
        zuin::ShiftTap tap, other;
        CHECK(!tap.key(false, false, false) && !tap.key(false, true, false), "左 Shift 不應切換");
        CHECK(!tap.key(true, false, false) && tap.key(true, true, false), "右 Shift 單按應切換");
        CHECK(!tap.key(true, true, false), "重複放開不應切換");
        tap.key(true, false, false);
        tap.key(false, true, false);
        CHECK(tap.key(true, true, false), "額外左 Shift 放開不能取消右 Shift 單按");
        tap.key(true, false, false);
        tap.key(false, false, false);
        CHECK(!tap.key(true, true, false), "Shift 加字母／標點／另一個 Shift 不應切換");
        tap.key(true, false, true);
        CHECK(!tap.key(true, true, false), "有修飾鍵的右 Shift 不應切換");
        // #151：經 RDP＋GTK_IM_MODULE=fcitx 時每個 Shift 按下都送兩次；Shift 已按著時的重複按下仍算同一次單按。
        tap.key(true, false, false);
        tap.key(true, false, false);
        CHECK(tap.key(true, true, false), "重複按下後放開仍應切換");
        tap.key(true, false, false);
        tap.key(true, false, false);
        tap.key(true, false, false);
        CHECK(tap.key(true, true, false), "連續多次重複按下後放開仍應切換");
        // 重複按下不能把已取消的單按重新算進來：Shift＋字母、另一側 Shift 按下、帶修飾鍵的重複按下都不切換。
        tap.key(true, false, false);
        tap.key(false, false, false);
        tap.key(true, false, false);
        CHECK(!tap.key(true, true, false), "按下、字母、重複按下、放開不應切換");
        tap.key(true, false, false);
        tap.key(true, false, false);
        tap.key(false, false, false);
        CHECK(!tap.key(true, true, false), "重複按下後按了字母或另一側 Shift 不應切換");
        tap.key(true, false, false);
        tap.key(true, false, true);
        CHECK(!tap.key(true, true, false), "重複按下時帶 Ctrl／Alt／Super 不應切換");
        tap.key(true, false, false);
        CHECK(!other.key(true, true, false), "不能跨欄位完成右 Shift");
        tap.reset();
        CHECK(!tap.key(true, true, false), "重新啟用／失焦／重設後不能沿用按下狀態");
        // #117：按住超過門檻（預設 300 ms）不算單按；剛好 300 ms 仍算。
        tap.key(true, false, false, 1000);
        CHECK(tap.key(true, true, false, 1300) && tap.within(300), "按住 300 ms 以內放開應切換");
        tap.key(true, false, false, 2000);
        CHECK(tap.key(true, true, false, 2500) && !tap.within(300), "按住 0.5 秒放開不應切換");
        CHECK(tap.within(0), "門檻 0 表示不限時");
        // 重複按下（RDP 的重複事件、長按的自動重複）不改變起算時間；按住超過門檻才放開仍不切換（#151）。
        tap.key(true, false, false, 3000);
        CHECK(tap.down(), "按下後應記得 Shift 按著");
        tap.key(true, false, false, 3200);
        tap.key(true, false, false, 3350);
        CHECK(!(tap.key(true, true, false, 3400) && tap.within(300)), "重複按下但總共按住 0.4 秒不應切換");
        CHECK(!tap.within(350), "按住時間從第一次按下起算");
        tap.key(true, false, false, 4000);
        tap.key(true, false, false, 4005);
        CHECK(tap.key(true, true, false, 4080) && tap.within(300), "重複按下的快速單按應切換");
        CHECK(!tap.within(79) && tap.within(80), "重複按下不重設按下時間");
        // 左右 Shift 一起按不算單按（#151，同 IBus 外殼）。照 engine.cpp 的順序餵事件：PreInputMethod 的 leftShiftKey
        // 先看到每個事件；接手的左 Shift 按下被 filter、keyEvent 收不到，所以由 leftShiftKey 替右側記一次另一個按下。
        zuin::ShiftTap leftTap, rightTap;
        auto shiftEvent = [&](bool left, bool release) {
            bool toggled = leftTap.key(left, release, rightTap.down());
            if (left) {
                if (!release) { rightTap.key(false, false, false); }
                return toggled;
            }
            return rightTap.key(true, release, leftTap.down());
        };
        shiftEvent(true, false);
        CHECK(shiftEvent(true, true), "左 Shift 單按應切換");
        shiftEvent(false, false);
        CHECK(shiftEvent(false, true), "右 Shift 單按應切換");
        shiftEvent(true, false);
        shiftEvent(false, false);
        CHECK(!shiftEvent(false, true), "按住左 Shift 時單按右 Shift 不應切換");
        CHECK(!shiftEvent(true, true), "按住左 Shift、按過右 Shift 後放開左 Shift 不應切換");
        shiftEvent(false, false);
        shiftEvent(true, false);
        CHECK(!shiftEvent(true, true), "按住右 Shift 時單按左 Shift 不應切換");
        CHECK(!shiftEvent(false, true), "按住右 Shift、按過左 Shift 後放開右 Shift 不應切換");
    }
    {
        // #117：health 的 left_shift／right_shift／shift_tap_ms。舊引擎沒有 right_shift 時右 Shift 照舊切換、門檻 300 ms。
        auto old = zuin::ShiftKeys::from({{"left_shift", "true"}});
        CHECK(old.left && old.right && old.tapMs == 300, "舊引擎：左=%d 右=%d 門檻=%d", old.left, old.right, old.tapMs);
        auto none = zuin::ShiftKeys::from({{"left_shift", "false"}, {"right_shift", "false"}, {"shift_tap_ms", "450"}});
        CHECK(!none.left && !none.right && none.tapMs == 450, "toggle-key=none：左=%d 右=%d 門檻=%d", none.left, none.right, none.tapMs);
        auto bad = zuin::ShiftKeys::from({{"shift_tap_ms", "abc"}});
        CHECK(!bad.left && bad.right && bad.tapMs == 300, "看不懂的門檻退回 300 ms：%d", bad.tapMs);
        auto empty = zuin::ShiftKeys::from(nlohmann::json());
        CHECK(!empty.left && empty.right && empty.tapMs == 300, "引擎不可用時左 Shift 照 Fcitx5、右 Shift 照舊");
    }
    {
        using D = zuin::DocumentContext;
        D d;
        D::Text before{"甲😀乙", 2, 2, true}, after{"甲😀風乙", 3, 3, true};
        d.committed(before, "風", 17);
        CHECK(!d.takeAction(), "未收到文件事件不能啟用前文");
        d.observed(after);
        CHECK(d.confirmed() && d.takeAction() == 17, "中文字元及 emoji 游標需確認插入位置");
        d.observed(after);
        CHECK(!d.takeAction(), "重複事件不重複確認");
        d.validate({"甲😀風乙", 2, 2, true});
        CHECK(d.takeAction() == 0 && !d.confirmed(), "游標移動需清除");
        d.committed(before, "風", 18); d.validate(after);
        CHECK(d.takeAction() == 0, "只有快取相符但沒有更新事件不可確認");
        d.committed(before, "風", 19); d.observed({"甲😀風乙", 3, 2, true});
        CHECK(d.takeAction() == 0, "選取文字需清除");
        d.committed(before, "風", 20); d.observed({"甲😀風丙", 3, 3, true});
        CHECK(d.takeAction() == 0, "外部修改需清除");
        d.committed(before, "風", 21); d.observed(after); d.takeAction(); d.clear();
        CHECK(d.takeAction() == 0 && !d.confirmed(), "失焦／隱私／重連清除");
        D other; other.observed(after);
        CHECK(!other.takeAction(), "另一欄位不能沿用確認");
        d.committed({"甲", 0, 1, true}, "風", 22); d.observed({"風", 1, 1, true});
        CHECK(!d.confirmed(), "替換選取不推測連續性");
        d.committed({std::string(20000, 'x'), 0, 0, true}, "風", 23);
        CHECK(!d.confirmed(), "大文件有界保留");
    }
    if (argc != 5) {
        std::fprintf(stderr, "usage: %s socket server data user\n", argv[0]);
        return 2;
    }
    // 用包裝腳本啟動 server，以便帶入測試用的資料目錄與 socket
    std::string wrapper = std::string(argv[1]) + ".sh";
    FILE *f = std::fopen(wrapper.c_str(), "w");
    // 設定檔與個人詞庫同目錄；用專屬目錄，不在 /tmp 留下會影響其他測試的 settings.json。
    std::string home = std::string(argv[1]) + ".home";
    std::filesystem::create_directories(home);
    std::string user = home + "/" + std::filesystem::path(argv[4]).filename().string();
    std::fprintf(f, "#!/bin/sh\nexec %s -socket %s -data %s -user %s\n", argv[2], argv[1], argv[3], user.c_str());
    std::fclose(f);
    chmod(wrapper.c_str(), 0755);

    // 只開 ㄘ／ㄔ 的模糊音：驗證近音候選的附註一路傳到外殼。
    std::ofstream(home + "/settings.json") << "{\"fuzzy\":{\"c-ch\":\"select\"}}\n";
    zuin::Client client(argv[1], wrapper);
    std::string traceDir = std::string(argv[1]) + ".trace";
    {
        zuin::Trace trace(traceDir);
        zuin::Session s(client, &trace);
        zuin::Snapshot snap;
        for (const char *k : {"a", "8", "6", "q", "i", "6"}) { // ㄇㄚˊ ㄆㄛˊ（第一次呼叫會自動啟動 server）
            snap = s.key(k);
        }
        CHECK(snap.ok, "server 不可用：%s", client.lastError().c_str());
        CHECK(snap.text() == "麻婆", "組字 %s", snap.text().c_str());
        CHECK(snap.suggestion == "豆腐", "補全 %s", snap.suggestion.c_str());
        CHECK(snap.cursorByte() == 6, "游標位元組 %d", snap.cursorByte());

        // 斷線重播：強迫下一次請求重新連線；組字狀態必須還在
        unsigned gen = client.generation();
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        snap = s.key("Tab");
        CHECK(client.generation() > gen, "應已重新連線");
        CHECK(snap.text() == "麻婆豆腐", "重播後接受補全 %s", snap.text().c_str());
        snap = s.key("Enter");
        CHECK(snap.commit == "麻婆豆腐", "提交 %s", snap.commit.c_str());
        CHECK(s.key("Enter").handled == false, "沒有組字時 Enter 應交還應用程式");

        // #106：組字中 Home／End 移到組字區頭尾並吃掉；沒在組字時交還應用程式。
        CHECK(!s.key("Home").handled && !s.key("End").handled, "沒有組字時 Home／End 應交還應用程式");
        for (const char *k : {"a", "8", "6", "q", "i", "6"}) { snap = s.key(k); }
        snap = s.key("Home");
        CHECK(snap.handled && snap.text() == "麻婆" && snap.cursorRune == 0, "Home 應移到組字區最前：%d", snap.cursorRune);
        snap = s.key("End");
        CHECK(snap.handled && snap.cursorRune == 2, "End 應移到組字區最後：%d", snap.cursorRune);
        for (const auto *k : {"Up", "PageUp", "PageDown", "BackTab"}) {
            snap = s.key(k);
            CHECK(snap.handled && snap.text() == "麻婆" && snap.cursorRune == 2 && snap.candidates.empty(),
                  "組字中 %s 應吃掉不動作", k);
        }
        snap = s.key("Down");
        CHECK(!snap.candidates.empty(), "↓ 應開候選");
        snap = s.key("Backspace");
        CHECK(snap.handled && snap.candidates.empty() && snap.text() == "麻婆", "候選中 Backspace 應關窗保留組字");
        // 英文模式保留的中文組字：Ctrl＋Home 走 bypassEnglish 交還應用程式，不得移動組字游標。
        CHECK(s.toggle().english, "右 Shift 應切到英文");
        CHECK(!s.bypassEnglish("Home").ok, "保留中文組字時 Ctrl＋Home 不得送進核心");
        snap = s.key("Home");
        CHECK(snap.handled && snap.cursorRune == 0, "英文模式保留的組字 Home 仍在組字區移動");
        s.key("Escape");
        s.key("Escape");
        CHECK(s.handle({{"type", "cancel"}}).text().empty(), "清掉 Home／End 測試的組字");
        s.handle({{"type", "mode"}, {"on", false}});
        // #106：組字中的 Ctrl／Alt／Super 組合鍵先送出組字；沒有組字時不送請求。
        CHECK(!s.commitForShortcut().ok, "沒有組字時組合鍵不送請求");
        for (const char *k : {"a", "8", "6", "q", "i", "6"}) { snap = s.key(k); }
        snap = s.commitForShortcut();
        CHECK(snap.ok && snap.commit == "麻婆" && snap.text().empty(), "組合鍵前應先送出組字：%s", snap.commit.c_str());

        // 同音補全先預覽；斷線重播後仍需提交相同的完整詞。
        for (const char *k : {"v", "u", "l", "3", "x", "j", "/", "6"}) {
            snap = s.key(k); // ㄒㄧㄠˇ ㄌㄨㄥˊ
        }
        CHECK(snap.text() == "小龍" && snap.suggestion.empty(), "原組字應保持小龍");
        snap = s.key("Tab");
        CHECK(snap.text() == "小龍" && snap.suggestion == "小龍 → 小籠包" && snap.commit.empty(),
              "第一次 Tab 應清楚預覽同音替換：%s", snap.suggestion.c_str());
        gen = client.generation();
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        snap = s.key("Tab");
        CHECK(client.generation() > gen && snap.text() == "小籠包" && snap.commit.empty(),
              "預覽重播後第二次 Tab 應接受補全");
        CHECK(s.key("Enter").commit == "小籠包", "提交同音補全");

        // 標點與中英切換
        snap = s.key("<");
        CHECK(snap.handled && snap.commit.empty() && snap.text() == "，", "全形逗號應留在組字區");
        snap = s.key("Enter");
        CHECK(snap.commit == "，", "Enter 才提交標點");
        s.key("]");
        snap = s.key("Down");
        CHECK(snap.candidates.size() >= 2 && snap.candidates[1].second == "』", "右引號應提供變體");
        snap = s.key("2");
        CHECK(snap.text() == "』" && snap.commit.empty(), "選符號不應提交");
        CHECK(s.key("Enter").commit == "』", "確認右引號");

        // 空白組字區的符號選單在 server 重啟後仍可選取。
        snap = s.key("`");
        CHECK(!snap.candidates.empty(), "反引號應開啟符號選單");
        gen = client.generation();
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        snap = s.key("2");
        CHECK(client.generation() > gen && snap.text() == "『" && snap.commit.empty(), "空白選單也必須可重播");
        s.key("Escape");
        s.key(".");
        snap = s.key("Down");
        CHECK(snap.candidates.size() == 1 && snap.candidates[0].second == "ㄡ", "單獨注音可選");
        snap = s.key("Enter");
        CHECK(snap.text() == "ㄡ" && snap.commit.empty(), "選注音不應提交");
        CHECK(s.key("Enter").commit == "ㄡ", "第二次 Enter 才提交注音");

        // 模糊音（只開 ㄘ／ㄔ）：ㄘ 沒有精確的字、近音 ㄔ 可成詞。顯示維持原樣注音，
        // 近音候選帶「近音 ㄔ」附註一路傳到外殼，原樣注音仍在最後；選取即鎖定。
        s.key("h");
        snap = s.key(" ");
        CHECK(snap.text() == "ㄘ", "近音不得自動換字：%s", snap.text().c_str());
        snap = s.key("Down");
        CHECK(snap.candidates.size() == snap.candidateNotes.size(), "附註數 %zu 應與候選數 %zu 相同", snap.candidateNotes.size(),
              snap.candidates.size());
        CHECK(snap.candidates.size() >= 2 && snap.candidates[0].second == "吃" && snap.candidateNotes[0] == "近音 ㄔ",
              "近音候選與附註：%s／%s", snap.candidates.empty() ? "" : snap.candidates[0].second.c_str(),
              snap.candidateNotes.empty() ? "" : snap.candidateNotes[0].c_str());
        CHECK(!snap.candidates.empty() && snap.candidates.back().second == "ㄘ" && snap.candidateNotes.back().empty(),
              "原樣注音應在最後且沒有附註");
        CHECK(snap.candidateKeys.size() == snap.candidates.size() && snap.candidateKeys[0] == "1" && snap.candidateKeys[1] == "2",
              "鍵標由核心給，預設 1–0（#112）");
        snap = s.key("1");
        CHECK(snap.text() == "吃" && snap.commit.empty(), "選近音不應提交：%s", snap.text().c_str());
        CHECK(s.key("Enter").commit == "吃", "確認近音選字");
        s.key("j");
        zuin::ShiftTap rightShift;
        auto tapRightShift = [&]() {
            rightShift.key(true, false, false);
            return rightShift.key(true, true, false) ? s.toggle() : zuin::Snapshot{};
        };
        snap = tapRightShift();
        CHECK(snap.english && snap.commit.empty() && snap.text().empty(), "切到英文應清掉未打完的注音（#90）");
        snap = tapRightShift();
        CHECK(snap.ok && !snap.english && snap.text() == "ㄨ", "立即再按右 Shift 應放回剛清掉的注音");
        s.key("i");
        CHECK(s.key("3").text() == "我", "放回後可直接補聲調");
        snap = tapRightShift();
        CHECK(snap.english && snap.commit.empty() && snap.text() == "我", "切到英文應保留已組好的字");
        snap = s.key("j");
        CHECK(snap.handled && snap.text() == "我j" && snap.commit.empty(), "英文應留在組字區");
        CHECK(s.key("Backspace").text() == "我", "英文可逐字刪除");
        snap = tapRightShift();
        CHECK(snap.ok && !snap.english && snap.text() == "我", "再次右 Shift 應恢復注音並保留內容");
        snap = tapRightShift();
        CHECK(snap.english && snap.commit.empty() && snap.text() == "我", "右 Shift 切換不應提交");
        snap = s.activate();
        CHECK(snap.ok && !snap.english && snap.text() == "我", "重新啟用應清除英文模式但保留組字");
        // 切到英文後有過交還應用程式的按鍵（快捷鍵）或其他請求，再切回都不放回注音。
        s.key("j");
        s.toggle();
        s.bypassed();
        CHECK(s.toggle().text() == "我", "快捷鍵之後切回不放回注音");
        s.key("j");
        s.toggle();
        s.activate();
        CHECK(s.toggle().text() == "我", "其他請求之後切換不放回注音");
        // 只有未打完的注音時，切到英文結束組字、立即切回重新開始；斷線重播後仍是放回的注音與注音模式。
        s.handle({{"type", "cancel"}});
        s.handle({{"type", "mode"}, {"on", false}});
        s.key("j");
        CHECK(s.toggle().text().empty() && s.toggle().text() == "ㄨ", "空白組字的誤觸也能放回");
        gen = client.generation();
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        snap = s.key("i");
        CHECK(client.generation() > gen && !snap.english && snap.text() == "ㄨㄛ" && snap.notices.empty(),
              "重播後放回的注音仍在：%s", snap.text().c_str());
        s.handle({{"type", "cancel"}});
        s.handle({{"type", "mode"}, {"on", true}});
        CHECK(!s.key("j").handled, "無組字時英文應放行按鍵");
        gen = client.generation();
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        snap = s.key("j");
        CHECK(client.generation() > gen && snap.handled && !snap.english && snap.text() == "ㄨ",
              "空白組字重連後應恢復注音");
        snap = s.activate();
        CHECK(!snap.ok, "已是注音時啟用不得額外送出模式切換");
        CHECK(s.key("Enter").commit == "ㄨ", "重複啟用不得提前提交或丟掉組字");

        // 落單聲調鍵與中英混合文字都留在組字區；重連後可繼續編輯。
        for (const char *k : {"j", "i", "3"}) { s.key(k); } // 我
        snap = s.key("4");
        CHECK(snap.handled && snap.text() == "我4" && snap.commit.empty(), "落單聲調鍵不可漏到句首");
        CHECK(s.key("Backspace").text() == "我", "誤按數字可單獨刪除");
        // #135：沒開中英混打時，單按聲調鍵緊接空白是聲調符號；外殼照常把按鍵交給核心即可。
        CHECK(s.key("3").text() == "我3", "聲調鍵先以數字顯示");
        snap = s.key(" ");
        CHECK(snap.handled && snap.text() == "我ˇ" && snap.commit.empty(), "單按聲調鍵＋空白應為聲調符號：%s", snap.text().c_str());
        CHECK(s.key("Backspace").text() == "我", "聲調符號可單獨刪除");
        s.handle({{"type", "toggle"}});
        for (char k : std::string("ENGLISH_SECRET_TOKEN1234")) { s.key(std::string(1, k)); }
        gen = client.generation();
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        snap = s.key("Backspace");
        CHECK(client.generation() > gen && snap.english && snap.text() == "我ENGLISH_SECRET_TOKEN123" && snap.commit.empty(),
              "混合組字重連應保留英文模式、字面文字與可編輯性");
        s.handle({{"type", "toggle"}});
        CHECK(s.key("Enter").commit == "我ENGLISH_SECRET_TOKEN123", "切回注音後提交所見混合內容");
        s.traceEvent("public_after_english");

        // 注音未完成時誤按數字鍵盤；重連後撤銷仍可接續原本聲調。
        s.key("j"); s.key("i");
        CHECK(s.key("KP1").text() == "ㄨㄛ1", "數字應跟在未完成注音後");
        gen = client.generation();
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        snap = s.key("Backspace");
        CHECK(client.generation() > gen && snap.text() == "ㄨㄛ" && snap.cursorRune == 2,
              "重播後刪掉數字應保留原注音與游標");
        CHECK(s.key("3").text() == "我", "可直接補聲調，不必重打");
        CHECK(s.key("Enter").commit == "我", "恢復組音後正常提交");
        // 同一欄位重複往返，與另一欄位的模式各自獨立。
        zuin::Session other(client, &trace);
        for (int i = 0; i < 3; ++i) {
            s.handle({{"type", "mode"}, {"on", true}});
            snap = s.activate();
            CHECK(snap.ok && !snap.english, "第 %d 次啟用未恢復注音", i);
            CHECK(!other.english() && other.key("j").handled, "另一欄位不應被切成英文");
            other.key("Escape");
        }

        // 候選
        for (const char *k : {"r", "u", "/", "3"}) { // ㄐㄧㄥˇ
            snap = s.key(k);
        }
        snap = s.key("Down");
        CHECK(!snap.candidates.empty() && snap.candidates[0].second == "景", "候選 %zu", snap.candidates.size());
        CHECK(snap.candidateTitle == "修改「景」 · Tab 切換詞長", "修改範圍提示 %s", snap.candidateTitle.c_str());
        snap = s.handle({{"type", "cancel"}});
        CHECK(snap.text().empty(), "放棄後應清空");

        // 敏感欄位的操作及組字不得落盤，離開後殘留到提交的文字也不得洩漏。
        s.setPrivate(true);
        s.key("PRIVATE_SECRET_TOKEN");
        for (const char *k : {"s", "u", ".", "6"}) { snap = s.key(k); } // 牛
        CHECK(snap.text() == "牛", "敏感欄位測試需有實際組字");
        s.setPrivate(false);
        s.key("Enter");
        s.traceEvent("public_after_private");

        s.handle({{"type", "pause"}, {"on", true}});
        s.key("PAUSED_SECRET_TOKEN");
        for (const char *k : {"s", "u", ".", "6"}) { s.key(k); }
        s.handle({{"type", "pause"}, {"on", false}});
        s.key("Enter");
        s.traceEvent("public_after_pause");
    }
    {
        std::ifstream in(traceDir + "/trace.jsonl");
        std::string line, all;
        bool request = false, redactedEnglish = false, reconnect = false;
        while (std::getline(in, line)) {
            all += line;
            auto j = nlohmann::json::parse(line);
            if (j.value("event", "") != "request") { continue; }
            request = true;
            CHECK(j.contains("before") && j.contains("after") && j.contains("engine_us"), "需保存前後狀態與耗時");
            reconnect = reconnect || j.value("reconnected", false);
            if (j["request"].value("key_class", "") == "text") { redactedEnglish = true; }
        }
        CHECK(request && redactedEnglish && reconnect, "應記錄請求、重連及英文遮罩");
        CHECK(all.find("PRIVATE_SECRET_TOKEN") == std::string::npos &&
              all.find("PAUSED_SECRET_TOKEN") == std::string::npos &&
              all.find("ENGLISH_SECRET_TOKEN") == std::string::npos && all.find("牛") == std::string::npos,
              "敏感欄位／暫停模式不得留在請求或前後快照");
        CHECK(all.find("public_after_private") != std::string::npos && all.find("public_after_pause") != std::string::npos &&
              all.find("public_after_english") != std::string::npos,
              "安全邊界後應恢復紀錄");
    }
    {
        zuin::Session s(client, nullptr, true);
        for (const char *k : {"z", "/", " ", "r", "u", "/", "3"}) { s.key(k); }
        auto committed = s.key("Enter");
        CHECK(committed.commit == "風景" && committed.contextID != 0, "opt-in 真實 server 應提供提交代號");
        s.documentContext(committed.contextID);
        auto idle = s.handle({{"type", "context"}, {"on", false}});
        CHECK(idle.commit.empty() && idle.text().empty(), "確認／失效不能重複提交文字");
        s.setPrivate(true);
        for (const char *k : {"z", "/", " ", "r", "u", "/", "3"}) { s.key(k); }
        CHECK(s.key("Enter").contextID == 0, "敏感提交不提供前文代號");
        s.setPrivate(false);
        s.handle({{"type", "pause"}, {"on", true}});
        for (const char *k : {"z", "/", " ", "r", "u", "/", "3"}) { s.key(k); }
        CHECK(s.key("Enter").contextID == 0, "暫停模式不保留前文");
        s.handle({{"type", "pause"}, {"on", false}});
        for (const char *k : {"z", "/", " ", "r", "u", "/", "3"}) { s.key(k); }
        committed = s.key("Enter");
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        s.documentContext(committed.contextID); // 舊 token 不得重播進新 server
        auto snap = s.key("z");
        CHECK(snap.ok && snap.commit.empty(), "重連後舊確認不能造成提交或卡住輸入");
    }
    {
        // 模糊音 auto：設定只在 server 啟動時讀取，改成 auto 後重啟（設定檔 → server → 快照 → 外殼整條路）。
        // ㄘ 沒有精確的字、近音 ㄔ 讀得出來，auto 直接顯示「吃」並以已修正樣式標示；提交的文字等於畫面上的字。
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << "{\"fuzzy\":{\"c-ch\":\"auto\"}}\n";
        zuin::Session s(client, nullptr);
        s.key("h");
        auto snap = s.key(" ");
        CHECK(snap.ok && snap.text() == "吃", "auto 應直接顯示近音讀得出的字：%s", snap.text().c_str());
        bool corrected = false;
        for (const auto &seg : snap.preedit) { corrected = corrected || seg.style == "corrected"; }
        CHECK(corrected, "自動修正的字要以已修正樣式標示");
        CHECK(s.key("Enter").commit == "吃", "提交的文字應等於畫面上的字");
    }
    {
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({"english_autocomplete":true,"selection":"statistical","punctuation":false})";
        zuin::Session s(client, nullptr);
        s.handle({{"type", "mode"}, {"on", true}});
        for (const auto *boundary : {"Enter", " ", "Left", "Home", "End", "Delete", "."}) {
            for (const auto *k : {"h", "e", "l"}) { s.key(k); }
            CHECK(s.englishPending(), "English word ownership must reach shell");
            auto snap = s.key(boundary);
            // #235：Enter 只送出單字、不交給程式；其他邊界鍵送出後交給程式。
            bool enter = std::string(boundary) == "Enter";
            CHECK(snap.ok && snap.handled == enter && snap.commit == "hel" && !snap.englishPending && !s.englishPending(),
                  "boundary %s must commit raw prefix (native host key except Enter)", boundary);
            s.key(" ");
        }
        for (const auto *k : {"h", "e", "l"}) { s.key(k); }
        auto chosen = s.key("Tab");
        CHECK(chosen.handled && chosen.text() != "hel" && chosen.englishPending, "Tab must explicitly accept completion");
        auto snap = s.key("Enter");
        CHECK(snap.handled && snap.commit == chosen.text(), "Enter commits the chosen completion only (#235)");
        for (const auto *k : {"r", "e", "c", "i", "e", "v", "e"}) { snap = s.key(k); }
        CHECK(snap.ok && snap.text() == "recieve" && !snap.candidates.empty() && snap.candidates[0].second == "receive",
              "English typo must retain raw spelling and expose correction over IPC");
        CHECK(snap.candidateTitle == "按 Tab 補全或修正拼字 · Esc 關閉",
              "English candidate hint must describe Tab completion and correction");
        auto corrected = s.key("Tab");
        CHECK(corrected.handled && corrected.text() == "receive" && corrected.englishPending,
              "Tab must explicitly accept spelling correction");
        auto restored = s.key("Backspace");
        CHECK(restored.handled && restored.text() == "recieve", "Backspace must undo spelling correction");
        auto literal = s.key("Enter");
        CHECK(literal.handled && literal.commit == "recieve", "Enter commits unaccepted spelling only (#235)");
        for (const auto *k : {"r", "e", "c", "i", "e", "v", "e"}) { s.key(k); }
        s.key("Tab");
        auto committed = s.key("Enter");
        CHECK(committed.handled && committed.commit == "receive", "Enter commits accepted spelling only (#235)");
        for (const auto *boundary : {"Home", "End", "BackTab"}) {
            for (const auto *k : {"h", "e", "l"}) { s.key(k); }
            auto word = s.bypassEnglish(boundary);
            CHECK(word.ok && !word.handled && word.commit == "hel" && !s.englishPending(),
                  "native %s must deliver pending English without taking the host key", boundary);
            s.key(".");
            CHECK(!s.key("h").handled && !s.englishPending(), "period must block a new English token");
            auto moved = s.bypassEnglish(boundary);
            CHECK(moved.ok && !moved.handled && moved.commit.empty(),
                  "native %s must notify core even without pending composition", boundary);
            auto next = s.key("h");
            CHECK(next.handled && s.englishPending(), "native %s must allow a new English word", boundary);
            s.key("Enter");
        }
    }
    {
        // #123：vocab_keys 來自 open 回覆。0.2.0 起一律開放，只有自訂詞彙不能改時（這裡用較新版本寫成的
        // vocab.json）為 false，外殼送原本的鍵。舊設定檔的 preview_0_2=false 不再關掉它。
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({"preview_0_2":false})";
        std::ofstream(home + "/vocab.json") << R"({"format":99,"words":[]})";
        zuin::Session off(client, nullptr);
        CHECK(!off.vocabKeys(), "詞彙檔由較新版本寫成時引擎不應回報 vocab_keys");
        // 移掉詞彙檔後重啟引擎：重開 session 時快取更新。
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::filesystem::remove(home + "/vocab.json");
        // 斷線要等下一個請求才發現；發現後重開 session，快取跟著更新。
        off.handle({{"type", "cancel"}});
        CHECK(off.vocabKeys(), "引擎重啟後重開的 session 應讀到新的 vocab_keys");
        zuin::Session s(client, nullptr);
        CHECK(s.vocabKeys(), "舊設定檔寫著 preview_0_2=false 時引擎仍應回報 vocab_keys");
        CHECK(!s.key("ShiftLeft").handled, "沒在組字時 Shift＋← 應交還應用程式（選取文字）");
        zuin::Snapshot snap;
        for (const char *k : {"a", "8", "6", "q", "i", "6"}) { snap = s.key(k); } // ㄇㄚˊ ㄆㄛˊ
        CHECK(snap.text() == "麻婆", "組字 %s", snap.text().c_str());
        s.key("ShiftLeft");
        snap = s.key("ShiftLeft");
        std::string marked;
        for (const auto &seg : snap.preedit) { if (seg.style == "marked") { marked += seg.text; } }
        CHECK(snap.handled && marked == "麻婆", "Shift＋← 兩次應標示「麻婆」：%s", marked.c_str());
        snap = s.key("Enter");
        CHECK(snap.handled && snap.commit.empty() && snap.text() == "麻婆" && !snap.notices.empty() &&
              snap.notices.front() == "已加入詞庫：麻婆", "Enter 應加入詞庫並留著組字：%s",
              snap.notices.empty() ? "（沒有通知）" : snap.notices.front().c_str());
        // 加入後游標回到開始標示的位置（句尾）；移到最前，Shift＋Delete 在沒有候選窗時等同 Delete（刪掉游標後的字，不漏給應用程式）。
        s.key("Home");
        snap = s.key("ShiftDelete");
        CHECK(snap.handled && snap.text() == "婆", "沒有候選窗時 Shift＋Delete 應等同 Delete：%d %s", snap.handled, snap.text().c_str());
        snap = s.key("Escape");
        // 候選窗開著時 Shift＋Delete 刪掉反白的自訂詞，通知說明，候選重開。
        for (const char *k : {"a", "8", "6", "q", "i", "6"}) { snap = s.key(k); }
        CHECK(snap.text() == "麻婆", "組字 %s", snap.text().c_str());
        snap = s.key("Down");
        for (int i = 0; i < 4 && !snap.candidates.empty() && snap.candidates[snap.highlight].second != "麻婆"; i++) {
            snap = s.key("Tab"); // 候選窗內 Tab 切換詞長
        }
        CHECK(!snap.candidates.empty() && snap.candidates[snap.highlight].second == "麻婆", "候選應能反白「麻婆」");
        snap = s.key("ShiftDelete");
        CHECK(snap.handled && !snap.notices.empty() && snap.notices.front().rfind("已從詞庫刪除：麻婆", 0) == 0,
              "Shift＋Delete 應刪掉反白的自訂詞：%s", snap.notices.empty() ? "（沒有通知）" : snap.notices.front().c_str());
        s.key("Escape");
        s.key("Escape");
    }
    {
        // #168：ctrl_punctuation 來自 open 回覆；預設開，Ctrl+, 在中文模式插進組字區。設定關閉後重啟引擎，
        // 重開的 session 讀到 false，外殼照舊把 Ctrl＋標點交給應用程式；引擎收到也回未處理。
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({})";
        zuin::Session s(client, nullptr);
        s.handle({{"type", "cancel"}}); // 斷線要等下一個請求才發現：先重連到重啟的引擎
        CHECK(s.ctrlPunct("Ctrl+,"), "預設應回報 ctrl_punctuation");
        CHECK(s.ctrlPunct("Ctrl+("), "引擎應回報認得 Ctrl+(（ctrl_punctuation_keys，#231）");
        CHECK(!s.ctrlPunct("Ctrl+["), "Ctrl+[ 不是 Ctrl＋標點");
        auto snap = s.key("Ctrl+(");
        CHECK(snap.handled && snap.text() == "（", "Ctrl+( 應插進組字區：%d %s", snap.handled, snap.text().c_str());
        s.key("Escape");
        snap = s.key("Ctrl+,");
        CHECK(snap.handled && snap.text() == "，", "Ctrl+, 應插進組字區：%d %s", snap.handled, snap.text().c_str());
        snap = s.key("Enter");
        CHECK(snap.commit == "，", "Enter 送出全形逗號：%s", snap.commit.c_str());
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({"ctrl_punctuation":false})";
        s.handle({{"type", "cancel"}});
        CHECK(!s.ctrlPunct("Ctrl+,"), "設定關閉後重開的 session 不應回報 ctrl_punctuation");
        CHECK(!s.ctrlPunct("Ctrl+)"), "設定關閉時新加的鍵也不送");
        CHECK(!s.key("Ctrl+.").handled, "設定關閉時引擎不處理 Ctrl+.");
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({})";
    }
    {
        // #173：caps_lock_english 來自 open 回覆；預設關，外殼不送 caps_lock（注音模式不理 Caps Lock，#168）。
        // 開啟後：組字中開 Caps Lock 先送出組字、切到英文；同樣的狀態不重送；關掉回到注音。
        zuin::Session s(client, nullptr);
        s.handle({{"type", "cancel"}});
        CHECK(!s.capsLockEnglish(true) && !s.capsNeedsSync(true, true), "預設不應回報 caps_lock_english");
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({"caps_lock_english":true})";
        // 設定程式打開這一項、引擎重啟後，Caps Lock 鍵本身（spawn=false）以 health 重新確認，不用 open 的舊快取。
        CHECK(s.capsLockEnglish(false), "引擎重啟後 Caps Lock 鍵應重新確認到 caps_lock_english");
        CHECK(s.capsLockEnglish(true), "設定開啟後重開的 session 應回報 caps_lock_english");
        s.key("j"); s.key("i"); s.key("3");
        CHECK(s.capsNeedsSync(true, true) && !s.capsNeedsSync(false, true), "開 Caps Lock 才需要同步");
        auto snap = s.capsLock(true);
        CHECK(snap.commit == "我" && snap.english && snap.capsLock && s.english(), "開 Caps Lock 應先送出組字再切英文：%s %d", snap.commit.c_str(), snap.english);
        CHECK(!s.capsNeedsSync(true, true), "引擎已記得開啟，不重送");
        CHECK(!s.key("A").handled, "暫時英文的 A 交給應用程式");
        snap = s.capsLock(false);
        CHECK(!snap.english && !snap.capsLock && !s.english(), "關 Caps Lock 應回到注音");
        // 連線還在時 Caps Lock 鍵沿用 open 的值、不問引擎：引擎暫停（SIGSTOP）時問 health 會逾時而回 false。
        CHECK(client.alive(), "連線還在");
        (void)!system((std::string("pkill -STOP -f '[0]c-server -socket ") + argv[1] + "'").c_str());
        CHECK(s.capsLockEnglish(false), "連線還在時 Caps Lock 鍵不問引擎，沿用 open 的值");
        (void)!system((std::string("pkill -CONT -f '[0]c-server -socket ") + argv[1] + "'").c_str());
        // 引擎在 Caps Lock 英文中重啟（例如在設定程式改了別的設定）：重開 session 先還原，第一個字母就是英文。
        snap = s.capsLock(true);
        CHECK(snap.english && snap.capsLock, "再開 Caps Lock");
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        CHECK(!client.alive() && !client.connected(), "引擎退出後 alive 發現斷線並關掉連線");
        snap = s.key("A");
        CHECK(snap.ok && snap.english && !snap.handled && snap.capsLock, "重開 session 後 Caps Lock 還開著，A 照英文：english=%d handled=%d", snap.english, snap.handled);
        snap = s.capsLock(false);
        CHECK(!snap.english && !s.english(), "重開後關 Caps Lock 也回到注音");
        // Caps Lock 英文中關掉這一項（引擎以新設定重啟）：照注音重開，不送 caps_lock。
        s.capsLock(true);
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({})";
        snap = s.key("j");
        CHECK(snap.ok && !snap.english && snap.handled && snap.text() == "ㄨ", "設定關掉後照注音：%s", snap.text().c_str());
        CHECK(!s.capsNeedsSync(false, true) && !s.capsNeedsSync(true, true), "設定關掉後不送 caps_lock");
        s.key("Escape");
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({})";
    }
    {
        // #169：選單切換「輸出簡體字」即時生效：另一個輸入框已經在組字區的字照新的設定送出，設定檔跟著寫；
        // 切換不記進重播紀錄（斷線重連後不會再切一次）。
        auto typeScenery = [](zuin::Session &s) {
            zuin::Snapshot snap;
            for (const char *k : {"z", "/", " ", "r", "u", "/", "3"}) { snap = s.key(k); } // ㄈㄥ ㄐㄧㄥˇ
            return snap;
        };
        auto stored = [&] {
            std::ifstream in(home + "/settings.json");
            auto j = nlohmann::json::parse(in, nullptr, false);
            return j.is_object() && j.value("output_simplified", false);
        };
        zuin::Session menu(client, nullptr), editor(client, nullptr);
        CHECK(menu.ensureLive() && !menu.simplifiedAtOpen(), "引擎應回報 output_simplified_live、目前是繁體");
        auto snap = typeScenery(editor);
        CHECK(snap.text() == "風景" && !snap.outputSimplified, "組字：%s", snap.text().c_str());
        auto toggled = menu.outputSimplified(true);
        CHECK(toggled.ok && toggled.handled && toggled.outputSimplified && !toggled.notices.empty() &&
              toggled.notices.front() == "已切換為輸出簡體字", "切換：%d %d", toggled.handled, toggled.outputSimplified);
        CHECK(stored(), "settings.json 應寫入 output_simplified=true");
        snap = editor.key("Enter");
        CHECK(snap.commit == "风景" && snap.outputSimplified, "組字中切換後送出 %s", snap.commit.c_str());
        // 組字中切換：組字不動，也不進重播紀錄。引擎重啟後設定檔改回繁體，重播不能再把它切成簡體。
        snap = typeScenery(menu);
        toggled = menu.outputSimplified(true);
        CHECK(toggled.text() == "風景" && toggled.commit.empty(), "切換不動組字：%s", toggled.text().c_str());
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({"output_simplified":false})";
        snap = menu.key("Enter"); // 重連、重播組字後送出
        CHECK(snap.commit == "風景" && !stored(), "重播不能再切換：送出 %s，設定 %d", snap.commit.c_str(), stored());
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({})";
    }
    {
        // #192：「用快捷鍵切換簡體字」預設關，外殼不攔 Ctrl＋Shift＋F11。在設定程式打開（引擎以新設定重啟）後，
        // 已經開著的 session 第一次按就重新確認到新值；關掉後也一樣。
        zuin::Session s(client, nullptr);
        CHECK(!s.simplifiedHotkey(), "預設不應回報 simplified_hotkey");
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({"simplified_hotkey":true})";
        CHECK(s.simplifiedHotkey(), "引擎重啟後按下快捷鍵應重新確認到 simplified_hotkey");
        auto toggled = s.outputSimplified(true);
        CHECK(toggled.ok && toggled.handled && toggled.outputSimplified, "快捷鍵送的是選單同一個請求");
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({"simplified_hotkey":false})";
        CHECK(!s.simplifiedHotkey(), "設定關掉、引擎重啟後第一次按就不攔");
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({})";
    }
    {
        // #117：預設（右 Shift）時 health 回報右 Shift 切換、左 Shift 不切換。
        zuin::Session s(client, nullptr);
        auto keys = s.shiftKeys();
        CHECK(!keys.left && keys.right && keys.tapMs == 300, "預設：左=%d 右=%d 門檻=%d", keys.left, keys.right, keys.tapMs);
        // toggle-key=left、mode-memory=shared：右 Shift 交給應用程式；一個輸入框切換後，換到另一個輸入框時
        // activate 立刻回報共用的英文模式（#115），不必等第一個按鍵。
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({"toggle_key":"left","mode_memory":"shared"})";
        keys = s.shiftKeys();
        CHECK(keys.left && !keys.right, "toggle-key=left：左=%d 右=%d", keys.left, keys.right);
        zuin::Session a(client, nullptr), b(client, nullptr);
        CHECK(!b.activate().english, "共用模式一開始是注音");
        CHECK(a.toggle().english, "第一個輸入框切到英文");
        auto snap = b.activate();
        CHECK(snap.ok && snap.english && b.english(), "共用模式換輸入框應立刻是英文");
        CHECK(b.toggle().ok && !b.english(), "第二個輸入框切回注音");
        snap = a.activate();
        CHECK(snap.ok && !snap.english && !a.english(), "回到第一個輸入框也跟著是注音");
        // 換回 native：換輸入框回到注音。
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({"mode_memory":"native"})";
        zuin::Session c(client, nullptr), d(client, nullptr);
        c.handle({{"type", "cancel"}});
        CHECK(c.toggle().english, "native：第一個輸入框切到英文");
        snap = d.activate();
        CHECK(!snap.ok && !d.english(), "native：另一個輸入框不送請求、維持注音");
        CHECK(c.activate().ok && !c.english(), "native：回到第一個輸入框回到注音");
        // #160：開始時是英文（native）。新的輸入框一取得焦點就是英文；切到注音後換輸入框回到英文。
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({"mode_memory":"native","start_mode":"english"})";
        zuin::Session e(client, nullptr), f(client, nullptr);
        snap = e.key("t");
        CHECK(snap.ok && !snap.handled && snap.english && e.english(), "開始時英文：第一個按鍵就是英文、交還應用程式");
        snap = f.activate();
        CHECK(snap.ok && snap.english && f.english(), "開始時英文：新的輸入框一取得焦點就是英文");
        CHECK(e.toggle().ok && !e.english(), "切到注音");
        snap = e.activate();
        CHECK(snap.ok && snap.english && e.english(), "native：換輸入框回到開始時的英文");
        CHECK(!f.activate().ok && f.english(), "已經是開始時的模式就不送請求");
        // 開始時是英文（shared）：一開始英文；切到注音後換輸入框維持注音。
        (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
        usleep(300 * 1000);
        std::ofstream(home + "/settings.json") << R"({"mode_memory":"shared","start_mode":"english"})";
        s.shiftKeys(); // 重連到新引擎（換輸入框不為此啟動引擎）
        zuin::Session g(client, nullptr), k(client, nullptr);
        snap = g.activate();
        CHECK(snap.ok && snap.english, "shared：登入後一開始是英文");
        CHECK(g.toggle().ok && !g.english(), "shared：切到注音");
        snap = k.activate();
        CHECK(snap.ok && !snap.english && !k.english(), "shared：換輸入框維持共用的注音");
    }
    std::filesystem::remove_all(traceDir);
    (void)!system((std::string("pkill -f '0c-server -socket ") + argv[1] + "'").c_str());
    usleep(800 * 1000); // server 結束時會存檔，等它寫完再刪目錄
    std::filesystem::remove_all(home);
    std::printf(failures ? "FAIL (%d)\n" : "PASS\n", failures);
    return failures ? 1 : 0;
}
