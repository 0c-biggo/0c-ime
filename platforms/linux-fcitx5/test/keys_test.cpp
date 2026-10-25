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

    // #168：中文模式不理 Caps Lock；Shift＋字母照舊大寫。
    EXPECT(zuin::foldStyleCaps('A', true, false, true, true) == 'a');
    EXPECT(zuin::foldStyleCaps('a', true, true, true, true) == 'A');
    EXPECT(zuin::foldStyleCaps('a', true, true, true, false) == 'a');
    EXPECT(zuin::foldStyleCaps('A', true, false, true, false) == 'A');
    EXPECT(zuin::foldStyleCaps('A', false, true, true, true) == 'A');
    EXPECT(zuin::foldStyleCaps('a', false, false, true, true) == 'a');
    EXPECT(zuin::foldStyleCaps('A', true, false, false, false) == 'a');
    EXPECT(zuin::foldStyleCaps('a', true, true, false, false) == 'A');
    EXPECT(zuin::foldCaps('S', true, false) == 's');
    EXPECT(zuin::foldCaps('s', true, true) == 'S');
    EXPECT(zuin::foldCaps('S', false, true) == 'S');
    EXPECT(zuin::foldCaps('s', false, false) == 's');
    EXPECT(zuin::foldCaps('3', true, false) == '3');
    EXPECT(zuin::foldCaps(',', true, false) == ',');
    EXPECT(zuin::foldCaps(0xff0d, true, false) == 0xff0d); // Return

    // #168：Ctrl＋標點的鍵名；其他 Ctrl 組合鍵、Ctrl+Alt、沒按 Ctrl 都不換。
    EXPECT(zuin::ctrlPunctKey(',', true, false) == "Ctrl+,");
    EXPECT(zuin::ctrlPunctKey('.', true, false) == "Ctrl+.");
    EXPECT(zuin::ctrlPunctKey('\'', true, false) == "Ctrl+'");
    EXPECT(zuin::ctrlPunctKey(';', true, false) == "Ctrl+;");
    EXPECT(zuin::ctrlPunctKey('?', true, false) == "Ctrl+?");
    EXPECT(zuin::ctrlPunctKey('!', true, false) == "Ctrl+!");
    EXPECT(zuin::ctrlPunctKey(',', false, false).empty());
    EXPECT(zuin::ctrlPunctKey(',', true, true).empty());
    for (uint32_t c : {'/', '1', '<', '[', ']', 'A', 'v'}) { EXPECT(zuin::ctrlPunctKey(c, true, false).empty()); }

    // #192：切換簡體字的 Ctrl＋Shift＋F11；少一個或多一個修飾鍵、別的 F 鍵都不是。
    EXPECT(zuin::simplifiedHotkey(zuin::keyF11, true, true, false));
    EXPECT(!zuin::simplifiedHotkey(zuin::keyF11, true, false, false));  // Ctrl＋F11
    EXPECT(!zuin::simplifiedHotkey(zuin::keyF11, false, true, false));  // Shift＋F11
    EXPECT(!zuin::simplifiedHotkey(zuin::keyF11, false, false, false)); // F11（全螢幕）
    EXPECT(!zuin::simplifiedHotkey(zuin::keyF11, true, true, true));    // 多按 Alt／Super
    EXPECT(!zuin::simplifiedHotkey(0xffc9, true, true, false));         // Ctrl＋Shift＋F12
    EXPECT(!zuin::simplifiedHotkey('F', true, true, false));            // Ctrl＋Shift＋F

    asked = 0;
    EXPECT(zuin::styleKey(" ", true, yes) == "ShiftSpace");
    EXPECT(zuin::styleKey(" ", true, no) == " ");
    EXPECT(zuin::styleKey(" ", false, yes) == " ");
    EXPECT(zuin::styleKey("BackTab", true, yes) == "BackTab");
    EXPECT(asked == 2);

    if (failures) { std::fprintf(stderr, "%d failure(s)\n", failures); return 1; }
    std::puts("keys_test ok");
    return 0;
}
