// 面板提示列的配置：不需要 Fcitx 與 0c-server。
#include "../src/panel.h"

#include <cstdio>
#include <string>

static int failures = 0;
#define EXPECT(cond) do { if (!(cond)) { std::fprintf(stderr, "FAIL %s:%d %s\n", __FILE__, __LINE__, #cond); ++failures; } } while (0)

int main() {
    zuin::Snapshot tab;
    tab.preedit = {{"側刺", "normal"}};
    tab.suggestion = "側刺 → 冊次";

    // 應用程式自己畫組字：面板第一列只有提示，照舊放 auxUp。
    auto client = zuin::panelAux(tab, true);
    EXPECT(client.up == "⇥ 側刺 → 冊次" && client.down.empty());

    // 組字畫在面板上（#136）：Fcitx5 classicui／kimpanel 把 auxUp 接在組字前面，同一列擠成「⇥ 側刺 → 冊次側刺」；
    // 提示改放 auxDown，自成一列。
    auto panel = zuin::panelAux(tab, false);
    EXPECT(panel.up.empty() && panel.down == "⇥ 側刺 → 冊次");

    // 通知同理，而且優先於補全提示。
    auto notice = tab;
    notice.notices = {"已撤銷與「冊次」有關的學習"};
    EXPECT(zuin::panelAux(notice, true).up == "已撤銷與「冊次」有關的學習");
    auto noticePanel = zuin::panelAux(notice, false);
    EXPECT(noticePanel.up.empty() && noticePanel.down == "已撤銷與「冊次」有關的學習");

    // 開候選：標題取代提示，頁碼在 auxDown。
    auto list = tab;
    list.candidates = {{1, "側刺"}, {2, "冊次"}};
    list.candidateTitle = "修改「側刺」 · Tab 切換詞長";
    list.pages = 3;
    list.page = 1;
    auto listClient = zuin::panelAux(list, true);
    EXPECT(listClient.up == "修改「側刺」 · Tab 切換詞長" && listClient.down == "2/3");
    // 組字在面板上時，標題與頁碼同在組字下面那一列。
    auto listPanel = zuin::panelAux(list, false);
    EXPECT(listPanel.up.empty() && listPanel.down == "修改「側刺」 · Tab 切換詞長　2/3");

    // 沒有標題的候選照舊顯示提示；只有一頁時不顯示頁碼。
    auto plain = tab;
    plain.candidates = {{1, "側刺"}};
    plain.pages = 1;
    auto plainClient = zuin::panelAux(plain, true);
    EXPECT(plainClient.up == "⇥ 側刺 → 冊次" && plainClient.down.empty());
    auto plainPanel = zuin::panelAux(plain, false);
    EXPECT(plainPanel.up.empty() && plainPanel.down == "⇥ 側刺 → 冊次");
    auto pagesOnly = plain;
    pagesOnly.suggestion.clear();
    pagesOnly.pages = 2;
    EXPECT(zuin::panelAux(pagesOnly, false).down == "1/2" && zuin::panelAux(pagesOnly, true).down == "1/2");

    // 通知與候選標題同時存在（候選窗的 Shift＋Delete 刪除後候選重開，#123）：通知是剛做的動作的回饋，優先；
    // 頁碼照舊。下一個按鍵沒有通知，標題就回來。
    auto forgot = list;
    forgot.notices = {"已從詞庫刪除：世傑（可在設定程式復原）"};
    auto forgotClient = zuin::panelAux(forgot, true);
    EXPECT(forgotClient.up == "已從詞庫刪除：世傑（可在設定程式復原）" && forgotClient.down == "2/3");
    EXPECT(zuin::panelAux(forgot, false).down == "已從詞庫刪除：世傑（可在設定程式復原）　2/3");

    // 組字樣式：一般與待定是底線；選過的字加粗；自動修正斜體；加詞的標示範圍反白（#123）。
    auto normal = zuin::preeditStyle("normal");
    EXPECT(normal.underline && !normal.bold && !normal.italic && !normal.highlight);
    EXPECT(zuin::preeditStyle("pending").underline && !zuin::preeditStyle("pending").highlight);
    EXPECT(zuin::preeditStyle("locked").bold && !zuin::preeditStyle("locked").highlight);
    EXPECT(zuin::preeditStyle("corrected").italic && !zuin::preeditStyle("corrected").highlight);
    auto marked = zuin::preeditStyle("marked");
    EXPECT(marked.highlight && marked.underline && !marked.bold && !marked.italic);
    // 加詞標示前後加【】，只顯示不提交；游標停在範圍外側（左端在【之前，右端在】之後）。
    const std::vector<zuin::Segment> mark = {{"我和", "locked"}, {"世傑", "marked"}};
    const int left = static_cast<int>(std::string("我和").size()); // Shift＋← 兩次：游標在範圍左端
    auto layout = zuin::preeditLayout(mark, left);
    EXPECT(layout.runs.size() == 4);
    std::string joined;
    for (const auto &r : layout.runs) { joined += r.text; }
    EXPECT(joined == "我和【世傑】");
    EXPECT(!layout.runs[0].display && layout.runs[1].display && !layout.runs[2].display && layout.runs[3].display);
    EXPECT(layout.runs[2].style.highlight && !layout.runs[1].style.highlight);
    EXPECT(layout.cursor == left);
    const int right = static_cast<int>(std::string("我和世傑").size()); // Shift＋→：游標在範圍右端
    EXPECT(zuin::preeditLayout(mark, right).cursor == static_cast<int>(std::string("我和【世傑】").size()));
    // 標示跨好幾段時只在整段前後各加一個；沒有標示時與原本的分段、游標相同。
    const std::vector<zuin::Segment> split = {{"世", "marked"}, {"傑", "marked"}, {"好", "normal"}};
    auto splitLayout = zuin::preeditLayout(split, static_cast<int>(std::string("世傑好").size()));
    std::string splitText;
    for (const auto &r : splitLayout.runs) { splitText += r.text; }
    EXPECT(splitText == "【世傑】好" && splitLayout.cursor == static_cast<int>(splitText.size()));
    auto unmarkedLayout = zuin::preeditLayout({{"世傑", "normal"}}, 3);
    EXPECT(unmarkedLayout.runs.size() == 1 && unmarkedLayout.cursor == 3);

    // 不認得的樣式（未來的引擎）照一般組字畫，不會不見。
    auto unknown = zuin::preeditStyle("future");
    EXPECT(unknown.underline && !unknown.highlight);

    // 讀音提示（#162）：中英混打打 20 時在提示列顯示 ㄉㄢ（按空白出「單」、Enter 留 20）；組字畫在面板上時放 auxDown。
    // 數字照一般組字畫底線（Fcitx5 沒有顏色格式）。通知優先。
    zuin::Snapshot digits;
    digits.preedit = {{"20", "reading"}};
    digits.readingHint = "ㄉㄢ";
    EXPECT(zuin::panelAux(digits, true).up == "ㄉㄢ" && zuin::panelAux(digits, true).down.empty());
    EXPECT(zuin::panelAux(digits, false).up.empty() && zuin::panelAux(digits, false).down == "ㄉㄢ");
    auto readingStyle = zuin::preeditStyle("reading");
    EXPECT(readingStyle.underline && !readingStyle.bold && !readingStyle.italic && !readingStyle.highlight);
    auto digitsNotice = digits;
    digitsNotice.notices = {"已加入詞庫：世傑"};
    EXPECT(zuin::panelAux(digitsNotice, true).up == "已加入詞庫：世傑");

    // 什麼都沒有時兩列都空，面板不多出空白列。
    zuin::Snapshot empty;
    EXPECT(zuin::panelAux(empty, false).up.empty() && zuin::panelAux(empty, false).down.empty());

    if (failures) { std::fprintf(stderr, "%d failure(s)\n", failures); return 1; }
    std::puts("panel_test ok");
    return 0;
}
