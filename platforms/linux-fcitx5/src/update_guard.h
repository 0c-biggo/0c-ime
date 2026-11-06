// 更新器與 Fcitx5 的交接入口；只傳狀態，不傳組字文字。
#pragma once

#include <chrono>
#include <memory>
#include <fcitx-utils/dbus/bus.h>
#include <fcitx-utils/dbus/objectvtable.h>
#include <fcitx/inputcontextmanager.h>
#include <fcitx/inputcontext.h>
#include <fcitx/inputpanel.h>
#include <fcitx/instance.h>
#include <nlohmann/json.hpp>

#ifndef ZC_SHELL_VERSION
#define ZC_SHELL_VERSION "development"
#endif

namespace zuin {
class UpdateGuard : public fcitx::dbus::ObjectVTable<UpdateGuard> {
public:
    explicit UpdateGuard(fcitx::Instance *instance) : instance_(instance) {
        keyWatcher_ = instance_->watchEvent(fcitx::EventType::InputContextKeyEvent,
            fcitx::EventWatcherPhase::PreInputMethod, [this](fcitx::Event &) {
                lastKey_ = std::chrono::steady_clock::now();
                restorePending();
            });
        focusWatcher_ = instance_->watchEvent(fcitx::EventType::InputContextFocusIn,
            fcitx::EventWatcherPhase::PostInputMethod, [this](fcitx::Event &) { restorePending(); });
        bus_ = std::make_unique<fcitx::dbus::Bus>(fcitx::dbus::BusType::Session);
        // Empty flags also work on Fcitx 5.0, before RequestNameFlag::None.
        if (!bus_->isOpen() || !bus_->requestName("org.biggo.ime.Update", fcitx::Flags<fcitx::dbus::RequestNameFlag>{})) {
            bus_.reset(); return; // 更新器無法取得保護時不自動重啟。
        }
        bus_->attachEventLoop(&instance_->eventLoop());
        bus_->addObjectVTable("/update", "org.biggo.ime.Update1", *this);
    }

    std::string status() {
        bool busy = false;
        instance_->inputContextManager().foreach([&busy](fcitx::InputContext *ic) {
            // 包含其他輸入法、失焦欄位與 client-side preedit。
            const auto &panel = ic->inputPanel();
            auto candidates = panel.candidateList();
            if (!panel.preedit().empty() || !panel.clientPreedit().empty() ||
                (candidates && !candidates->empty())) { busy = true; }
            return true;
        });
        auto idle = std::chrono::duration_cast<std::chrono::seconds>(
            std::chrono::steady_clock::now() - lastKey_).count();
        return nlohmann::json{{"busy", busy}, {"idle_seconds", idle},
                              {"version", ZC_SHELL_VERSION}, {"restore_supported", true},
                              {"program", instance_->mostRecentInputContext() ? instance_->mostRecentInputContext()->program() : std::string()}}.dump();
    }

    std::string exitForUpdate(uint64_t idleNeeded) {
        auto s = nlohmann::json::parse(status());
        if (s["busy"].get<bool>()) { return "composing"; }
        if (s["idle_seconds"].get<uint64_t>() < idleNeeded) { return "typing"; }
        // 檢查與退出同在 Fcitx 事件迴圈，兩者間不插入新的組字事件。
        instance_->exit();
        return "ready";
    }
    // 重啟時 GTK／IBus 可能尚未重新建立原欄位。只恢復原程式下一次焦點／按鍵，
    // 不將其他 App 或敏感欄位的輸入來源一起改掉。
    void restoreInputMethod(const std::string &program, const std::string &im) {
        restoreProgram_ = program;
        restoreIM_ = im;
        restoreDeadline_ = std::chrono::steady_clock::now() + std::chrono::minutes(2);
        restorePending();
    }
    FCITX_OBJECT_VTABLE_METHOD(restoreInputMethod, "RestoreInputMethod", "ss", "");
    FCITX_OBJECT_VTABLE_METHOD(status, "Status", "", "s");
    FCITX_OBJECT_VTABLE_METHOD(exitForUpdate, "ExitForUpdate", "t", "s");

private:
    void restorePending() {
        if (restoreProgram_.empty() || restoreIM_.empty()) { return; }
        if (std::chrono::steady_clock::now() > restoreDeadline_) {
            restoreProgram_.clear(); restoreIM_.clear(); return;
        }
        auto *ic = instance_->mostRecentInputContext();
        if (!ic || !ic->hasFocus() || ic->program() != restoreProgram_ ||
            ic->capabilityFlags().testAny(fcitx::CapabilityFlag::PasswordOrSensitive) ||
            // Fcitx 5.0 (Ubuntu 22.04) predates the Disable enumerator. Its
            // ABI bit is stable in 5.1; keep checking it when loaded there.
            ic->capabilityFlags().test(static_cast<fcitx::CapabilityFlag>(1ULL << 40))) { return; }
        auto im = std::move(restoreIM_);
        restoreProgram_.clear();
        instance_->setCurrentInputMethod(ic, im, true);
    }
    std::string restoreProgram_, restoreIM_;
    std::chrono::steady_clock::time_point restoreDeadline_;
    std::unique_ptr<fcitx::HandlerTableEntry<fcitx::EventHandler>> focusWatcher_;
    fcitx::Instance *instance_;
    std::chrono::steady_clock::time_point lastKey_ = std::chrono::steady_clock::now();
    std::unique_ptr<fcitx::HandlerTableEntry<fcitx::EventHandler>> keyWatcher_;
    std::unique_ptr<fcitx::dbus::Bus> bus_;
};
} // namespace zuin
