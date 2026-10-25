# 注／英狀態圖示

自有 SVG 筆畫，不依賴中文字型。以 16 × 16 為基準，使用一致的圓角筆畫。

- `0c-zhuyin.svg`：深綠底白色「注」。
- `0c-english.svg`：淡綠底深綠色「英」。

配色取自 [官網](https://0c.biggo.com/) CSS（2026-10-07）：主色 `#075D53`、淡綠 `#E5ECE8`、白色 `#FFFFFF`。底色使用約 3 px 圓角，字形維持相同尺寸與筆畫；另以細的灰綠外框區隔亮／暗面板。底色和字色一起固定，在亮／暗面板均保留品牌配色，不依賴 SVG 的 CSS media query。

不再附 `0c-*-symbolic.svg`。GNOME Kimpanel 的 `createIcon` 使用 `name + '-symbolic-hack'` 及預設名稱退回鏈；同時存在單色版時會優先選它，綠底就消失。只有彩色版時，這條鏈及桌面的單色偏好都會退回彩色 SVG。回歸核對實際選檔與亮／暗前景下不變的完整像素及底色。

- 「注」：靈犀注音的注音模式。
- 「英」：右 Shift 切換的內部英文模式。

Fcitx5 引擎透過 `InputMethodEngineV2::subModeIconImpl` 回報目前欄位的圖示；輸入法項目的預設圖示也改為「注」。切到 `keyboard-us` 等系統鍵盤時，圖示仍由 Fcitx5 的鍵盤引擎決定。

`make install`、`make upgrade` 與 Linux 套件安裝均帶入兩張彩色圖，並移除舊單色檔。GNOME Shell 只在應用程式清單變動時、依 `hicolor` 目錄本身的時間決定是否重讀圖示主題，只換 `scalable/status/` 裡的檔案不會生效；`make upgrade` 與套件的安裝、回復、移除在自有圖示有變動時，會更新 `hicolor` 與 `applications` 兩個目錄的時間，讓登入中的桌面改用新圖（否則會一直畫已刪除的單色圖：黑色狀態列上的暗灰「注」）。安裝位置為 `~/.local/share/icons/hicolor/scalable/status/`。套件驗證要求注／英彩色圖成對存在；舊包可以都不帶，也持續接受帶四張圖的舊包。完整更新備份舊單色檔，回復／降版可放回，移除沿用檔案交易及收據。

KDE 的 KIconLoader 另外記住載入主題時存在的目錄；第一次安裝新增 `scalable/status` 時，只更新目錄時間還不足以讓已執行的載入器找到圖示（#227）。上述共用更新流程也會送出 KDE 的 `iconChanged` 工作階段廣播，讓載入器重新掃描。只通知安裝對象就是目前使用者、且已有 D-Bus 工作階段的情況；不啟動服務、不改主題，通知失敗也不撤回安裝。已驗證真實 KIconLoader 的正反對照，Plasma Wayland 完整桌面驗收仍待補。

如果 `PreferTextIcon=True`，Fcitx5 會優先自行畫「注／英」文字，不使用 SVG。完整套件安裝及 `make upgrade` 會將 Classic UI／Kimpanel 的這個偏好設為 `False`，保留其他設定、區段與註解；回復也保留使用者之後的主題變更。`make install` 沿用使用者設定，需自行關閉「偏好文字圖示」。

圖示本身為正方形。GNOME／Yaru 的 `system-status-icon` 左右各有 6 px padding 與 4 px margin，讓 Kimpanel 按鈕比內建文字指示器寬。`kimpanel-spacing.js` 只在目前圖示為 `0c-zhuyin` 或 `0c-english` 時移除這層留白，切到其他輸入法會還原原本的 inline style；不縮小 SVG，也不修改主題。安裝與回復界線見 [Linux 外殼說明](../README.md)。

隔離整合測試（需要 Fcitx5 SDK、GTK3 前端、bubblewrap、Xvfb 及 Python GI）：

```sh
python3 platforms/linux-fcitx5/test/desktop_test.py \
  --bundle /path/to/unpacked-preview --icons --result /path/to/new-result
```

測試會在私有家目錄掛載、PID namespace、D-Bus 與 Xvfb 裡，驗證彩色圖示能以 16 px 載入、Kimpanel 名稱鏈及單色偏好仍保留綠底、注／英雙向切換、焦點返回與切換輸入法，並確認 GTK 實際輸入及安裝／回復／移除。焦點返回沿用既有重新啟用時回到注音的行為，圖示跟隨實際模式。測試不操作日常桌面。

Fcitx5 介面依據：[引擎的子模式圖示](https://github.com/fcitx/fcitx5/blob/5.1.19/src/lib/fcitx/inputmethodengine.h)、[系統匣的文字圖示偏好](https://github.com/fcitx/fcitx5/blob/5.1.19/src/modules/notificationitem/notificationitem.cpp)。
