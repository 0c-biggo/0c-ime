// 按鍵名稱轉換（#123 的新鍵名）：不需要 Fcitx 與 0c-server。
#include "../src/keys.h"

#include <cstdio>

static int failures = 0;
#define EXPECT(cond) do { if (!(cond)) { std::fprintf(stderr, "FAIL %s:%d %s\n", __FILE__, __LINE__, #cond); ++failures; } } while (0)

int main() {
    int asked = 0;
    auto yes = [&] { ++asked; return true; };
    auto no = [&] { ++asked; return false; };

    // 引擎回報支援、只按著 Shift：四個鍵換成新鍵名。
    EXPECT(zuin::vocabKey("Left", true, yes) == "ShiftLeft");
    EXPECT(zuin::vocabKey("Right", true, yes) == "ShiftRight");
    EXPECT(zuin::vocabKey("Delete", true, yes) == "ShiftDelete");
    EXPECT(zuin::vocabKey("Backspace", true, yes) == "ShiftBackspace");
    EXPECT(asked == 4);

    // 舊引擎（沒有 vocab_keys 或為 false）不認得新鍵名：照舊送原本的鍵。
    asked = 0;
    for (const char *k : {"Left", "Right", "Delete", "Backspace"}) { EXPECT(zuin::vocabKey(k, true, no) == k); }
    EXPECT(asked == 4);

    // 沒按 Shift：原本的鍵，而且不必問引擎。
    asked = 0;
    for (const char *k : {"Left", "Right", "Delete", "Backspace"}) { EXPECT(zuin::vocabKey(k, false, yes) == k); }
    // 其他按鍵（含 Shift＋字母、Shift+Tab 已是 BackTab、Home／End）不換名，也不問引擎。
    for (const char *k : {"A", "BackTab", "Home", "End", "Up", "Down", "Enter", " ", "KP4"}) { EXPECT(zuin::vocabKey(k, true, yes) == k); }
    EXPECT(asked == 0);

    if (failures) { std::fprintf(stderr, "%d failure(s)\n", failures); return 1; }
    std::puts("keys_test ok");
    return 0;
}
