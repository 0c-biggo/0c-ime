#pragma once

#include <cstdint>

namespace zuin {

// 每個輸入欄位分別追蹤一個 Shift 鍵（右 Shift，以及設定開啟時的左 Shift）。只有完整的單鍵按放才切換模式。
class ShiftTap {
public:
    // target：這個事件是否為追蹤的那一個 Shift 鍵；nowMs：事件時間（毫秒，單調時鐘），用來算按住多久。
    // 回傳 true 表示這是一次單按的放開；按住多久要另外用 within() 比對門檻（門檻來自引擎設定，見 #117）。
    bool key(bool target, bool release, bool modified, int64_t nowMs = 0) {
        if (!target) {
            // 只有另一個按下事件構成組合鍵；桌面／RDP 補送的另一側 Shift
            // 放開事件不能取消已觀察到的單按。
            if (!release) { armed_ = false; }
            return false;
        }
        if (!release) {
            // 經 RDP 的 GTK_IM_MODULE=fcitx 等路徑把每個 Shift 按下送兩次（#151）。Shift 已按著時的重複按下
            // 維持原本的狀態，不取消這次單按、也不重設按下時間；按住太久由 within() 的時限擋（#117），
            // 中間按了別的鍵仍由上面的 !target 取消。同 IBus 外殼與 macOS（#71）。
            if (!down_) {
                down_ = true;
                armed_ = !modified;
                pressedAt_ = nowMs;
            } else if (modified) {
                armed_ = false;
            }
            return false;
        }
        bool toggle = down_ && armed_ && !modified;
        held_ = nowMs - pressedAt_;
        reset();
        return toggle;
    }
    // 上一次放開時按住的時間是否不超過 limitMs（#117：預設 300 ms，按著 Shift 猶豫後放開不算切換）；limitMs <= 0 不限時。
    bool within(int64_t limitMs) const { return limitMs <= 0 || held_ <= limitMs; }
    bool down() const { return down_; }
    void reset() { down_ = armed_ = false; }

private:
    bool down_ = false, armed_ = false;
    int64_t pressedAt_ = 0, held_ = 0;
};

} // namespace zuin
