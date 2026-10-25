// 0c-update checks, downloads, verifies and installs complete release bundles.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/0c-biggo/0c-ime/core/fuzzy"
	"github.com/0c-biggo/0c-ime/internal/autoupdate"
	"github.com/0c-biggo/0c-ime/internal/settings"
	"github.com/0c-biggo/0c-ime/internal/update"
)

func installedVersion() (string, error) {
	if packageManaged() { // 系統套件（#181）：版本記在 distribution.json，沒有收據
		return distributionVersion(), nil
	}
	b, err := os.ReadFile(receiptPath())
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var r struct {
		Version string `json:"version"`
		Active  *bool  `json:"active"`
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return "", err
	}
	if r.Active != nil && !*r.Active {
		return "", nil
	}
	if r.Version == "legacy" {
		return "", nil
	}
	if !update.ValidVersion(r.Version) {
		return "", fmt.Errorf("invalid installed version")
	}
	return r.Version, nil
}

// printSettings 以文字顯示目前的設定與模型狀態。機器可讀的版本是 settings -json。
func printSettings() error {
	path := settings.Path(filepath.Join(basePath(), "user.json"))
	r, err := settings.Read(path)
	if err != nil {
		return err
	}
	listSettings(os.Stdout, r, path)
	printModelStatus(os.Stdout, modelStatus(dialEngine))
	return nil
}

// listSettings 寫出每一項設定的值與簡短說明；path 是下次修改儲存的位置。
func listSettings(w io.Writer, r settings.Report, path string) {
	s := r.Settings
	fmt.Fprintf(w, "設定檔：%s\nselection=%s（neural：神經選字；statistical：只用統計引擎）\npunctuation=%s（提交時補標點，Beta）\ndocument-context=%s（依自己剛提交的文字選字）\n",
		r.Path, s.Selection, onOff(s.Punctuation), onOff(s.DocumentContext))
	if r.Path != path {
		fmt.Fprintf(w, "下次修改儲存至：%s\n", path)
	}
	fmt.Fprintf(w, "english-autocomplete=%s（英文自動補字；Tab 接受、Esc 關閉）\n", onOff(s.EnglishAutocomplete))
	fmt.Fprintf(w, "toggle-key=%s（中英切換鍵：right 右 Shift；both 左右 Shift；left 左 Shift；none 不用 Shift）\n", s.ToggleKey)
	locked := ""
	if s.StartMode == settings.StartEnglish && !s.StartsEnglish() {
		locked = "；目前不用 Shift 切換，固定從注音開始" // #184
	}
	fmt.Fprintf(w, "start-mode=%s（開始時的中英模式：zhuyin 注音；english 英文%s）\n", s.StartMode, locked)
	fmt.Fprintf(w, "mode-memory=%s（切換視窗後的中英模式：native 照平台原本的做法；shared 全部共用同一個模式）\n", s.ModeMemory)
	fmt.Fprintf(w, "esc-clear=%s（按 Esc 清除整段組字；off 只清未打完的注音）\n", onOff(s.EscClear))
	fmt.Fprintf(w, "space-selects=%s（注音打完按空白選字；off 空白輸入空格、↓ 選字）\n", onOff(s.SpaceSelects))
	fmt.Fprintf(w, "mixed-input=%s（中英混打，試用；off 使用手動中英切換）\n", onOff(s.MixedInput))
	fmt.Fprintf(w, "candidate-cursor=%s（修改游標前方／後方字詞）\n", s.CandidateCursor)
	fmt.Fprintf(w, "advance-after-select=%s（選字後游標移到下一個字）\n", onOff(s.AdvanceAfterSelect))
	fmt.Fprintf(w, "candidate-layout=%s（選字排列）\nemoji-shortcut=%s（表情自選快捷鍵）\n", s.CandidateLayout, s.EmojiShortcut)
	fmt.Fprintf(w, "emoji-equals-first=%s（將 = 放在 Emoji 第一項）\n", onOff(s.EmojiEqualsFirst))
	fmt.Fprintf(w, "output-simplified=%s（輸出簡體字：組字和候選仍是繁體，送出時轉成簡體）\n", onOff(s.OutputSimplified))
	fmt.Fprintf(w, "simplified-hotkey=%s（按 %s 切換輸出簡體字；關閉時這個鍵交給應用程式）\n", onOff(s.SimplifiedHotkey), settings.SimplifiedHotkeyLabel(runtime.GOOS))
	fmt.Fprintf(w, "selection-keys=%s（選字鍵：1234567890、1234qweras 或 asdfghjkl;；換成字母時數字仍照位置選字）\n", s.SelectionKeys)
	fmt.Fprintf(w, "ctrl-punctuation=%s（注音模式按 Ctrl＋, . ' ; 與 Ctrl＋Shift＋/ 1 打全形標點；關閉時交給應用程式）\n", onOff(s.CtrlPunctuation))
	if settings.CapsLockEnglishSetting(runtime.GOOS) {
		fmt.Fprintf(w, "caps-lock-english=%s（Caps Lock 開著時直接打英文：字母大寫、數字與符號半形，關掉回到注音；關閉時注音模式不理 Caps Lock）\n", onOff(s.CapsLockEnglish))
	}
	fmt.Fprintf(w, "toneless=%s（不打聲調也能選字：沒打聲調的字依前後文決定，有打的照聲調，組字中的 ' 切字；只在標準排列有效；開啟時中英混打停用）\n", onOff(s.Toneless))
	fmt.Fprintf(w, "keyboard-layout=%s（鍵盤排列：standard 標準；eten 倚天 41 鍵，這時不提供 Emoji 選單與中英混打；hsu 許氏，這時不提供中英混打）\n", s.KeyboardLayout)
	for _, it := range s.Items() {
		if strings.HasPrefix(it.Key, "style-") {
			fmt.Fprintf(w, "%s=%s（%s）\n", it.Key, it.Value, it.Label)
		}
	}
	fuzzyConfig := s.FuzzyConfig()
	for _, p := range fuzzy.Pairs() {
		fmt.Fprintf(w, "fuzzy-%s=%s（%s 模糊音：off 關閉；select 開候選時合併近音；auto 另在語境明顯支持近音時自動改字，實驗）\n", p.ID(), fuzzyConfig.Mode(p), p.Symbols())
	}
}

func isJSONFlag(arg string) bool { return arg == "-json" || arg == "--json" }

func settingsCommand(args []string) error {
	switch {
	case len(args) == 0:
		return printSettings()
	case len(args) == 1 && isJSONFlag(args[0]):
		return emitJSON(os.Stdout, buildSettingsDoc(basePath(), dialEngine))
	}
	return fmt.Errorf("settings 只接受 -json；修改請用 set")
}

// setCommand 修改一到多項設定；引擎只在啟動時讀取，加 -apply 請引擎在沒有組字時交接，下一個按鍵起生效。
func setCommand(args []string) error {
	o, err := parseSetArgs(args)
	if err != nil {
		for _, a := range args {
			if isJSONFlag(a) {
				return failJSON(os.Stdout, err)
			}
		}
		return err
	}
	notice := func(body string) {
		if o.notify {
			if err := notify(autoupdate.Notice{Title: "靈犀注音設定", Body: body}); err != nil {
				fmt.Fprintln(os.Stderr, "無法顯示設定通知：", err)
			}
		}
	}
	res, err := runSet(basePath(), dialEngine, o, notice)
	for _, p := range o.pairs {
		if err == nil && p[0] == settings.PreviewRetiredKey { // 舊腳本照常能跑，但說明它已不需要（stdout 的格式不變）
			fmt.Fprintln(os.Stderr, "0c-update:", settings.PreviewRetiredNote)
		}
	}
	if err != nil {
		if o.json {
			return failJSON(os.Stdout, err)
		}
		return err
	}
	if o.json {
		return emitJSON(os.Stdout, res)
	}
	printSetText(os.Stdout, res, notice)
	return nil
}

// checkCommand 只查詢已簽章索引，不下載（check -feed 是舊的未簽章流程，另走下面的分支）。
func checkCommand(asJSON bool) error {
	base := basePath()
	feed := DefaultFeed
	if s, err := autoupdate.Load(base); err == nil && s.Feed != "" {
		feed = s.Feed
	}
	return runCheck(base, &system{base: base, feed: feed}, 30*time.Second, asJSON, os.Stdout)
}

func configureFlags(args []string) (apply, showNotice bool, err error) {
	for _, arg := range args {
		switch {
		case arg == "-apply" && !apply:
			apply = true
		case arg == "-notify" && !showNotice:
			showNotice = true
		default:
			return false, false, fmt.Errorf("未知或重複設定參數：%s", arg)
		}
	}
	return
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法：0c-update status|settings|set|models|vocab|api|open-settings|engine|auto|finish|mode|pause|resume|snooze|schedule|verify|check|download|download-now|apply|install|rollback|uninstall [選項]")
	}
	action := args[0]
	// macOS 以 root 執行時（sudo installer、MDM）改以登入的使用者身分重新執行，成功時不會返回（#198）
	if err := dropRoot(args); err != nil {
		return err
	}
	delegateToPersonal(args) // 系統安裝的 0c-update、而官網安裝也在時，交給官網那一份（成功時不會返回）
	if err := refusePackageManaged(action, args[1:]); err != nil {
		return err
	}
	switch action {
	case "status", "check", "auto", "finish":
		// macOS：背景項目的登記留在已安裝的 App（#205）；舊輸入法程式的選單、設定程式與排程都經過這幾個命令。
		adoptSchedule(action != "status")
	}
	switch action {
	case "settings":
		return settingsCommand(args[1:])
	case "profile":
		return profileCommand(args[1:])
	case "set":
		return setCommand(args[1:])
	case "models": // 個人目錄的神經模型（#180）
		return modelsCommand(args[1:])
	case "vocab": // 自訂詞彙（#122）
		return vocabCommand(args[1:])
	case "api": // AI 助手的本機 API（#126）
		return apiCommand(args[1:])
	case "open-settings":
		return openSettings(args[1:])
	case "download-now":
		return downloadNowCommand(args[1:])
	case "engine":
		if len(args) != 1 {
			return fmt.Errorf("engine 不接受參數")
		}
		return ensureEngine()
	case "auto", "finish", "mode", "pause", "resume", "snooze", "schedule", "notify-action", "protocol":
		return autoCommand(action, args[1:])
	case "deploy-tsf":
		// 內部用：版本目錄的 TSF DLL 被舊版更新器 regsvr32 時由 DLL 呼叫（#121），不列在用法裡。
		if len(args) != 1 {
			return fmt.Errorf("deploy-tsf 不接受參數")
		}
		return deployTSF()
	case "check-account":
		// 內部用：Windows 安裝程式開始安裝前呼叫（#199），系統管理員權限由另一個帳號確認時以 otherAccountExit 結束並說明，
		// 不列在用法裡。
		if len(args) != 1 {
			return fmt.Errorf("check-account 不接受參數")
		}
		return refuseOtherAccount()
	case "prune":
		// 內部用：安裝、回復、解除安裝成功後在背景清理舊版（#179，Windows），不列在用法裡。
		if len(args) != 1 {
			return fmt.Errorf("prune 不接受參數")
		}
		return pruneCommand()
	}
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	feedURL := flags.String("feed", "", "HTTPS 更新索引 URL")
	channel := flags.String("channel", "public-beta", "public-beta 或 internal-preview")
	archive := flags.String("archive", "", "已下載的 tar.gz 或 zip")
	checksum := flags.String("sha256", "", "來自可信任來源的壓縮包 SHA-256")
	bundle := flags.String("bundle", ".", "已解壓且完整的套件目錄")
	out := flags.String("out", "", "下載保存目錄")
	backup := flags.String("backup", "", "Linux 回復用備份目錄；Windows 依安裝收據回復上一版")
	allowDowngrade := flags.Bool("allow-downgrade", false, "明確允許安裝較舊版本")
	asJSON := flags.Bool("json", false, "status、check：以 JSON 輸出（輸入法選單與設定程式讀取）")
	diagnostics := flags.Bool("diagnostics", false, "status -json：另附平台診斷（macOS 版本、正在執行的輸入法 App），供匯出狀態摘要；輸入法選單的輪詢不帶")
	online := flags.Bool("online", false, "install：從已簽章索引下載並安裝（一行安裝）")
	restoreUser := flags.Bool("restore-user", false, "rollback 時一併還原升級前保存的個人詞庫（目前的詞庫另存於新備份）")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("不接受額外位置參數")
	}
	if *restoreUser && action != "rollback" {
		return fmt.Errorf("-restore-user 只用於 rollback")
	}
	if *channel != "public-beta" && *channel != "internal-preview" {
		return fmt.Errorf("未知的更新通道")
	}
	if action == "check" && *feedURL == "" && *archive == "" {
		return checkCommand(*asJSON)
	}
	if action == "apply" || action == "install" {
		// 以另一個帳號提權時不裝（#199）；安裝程式在 check-account 已擋過，這裡涵蓋 install.ps1 與直接執行。
		if err := refuseOtherAccount(); err != nil {
			return err
		}
	}
	// 之前以 root 安裝留下的檔案，這個帳號改不動；先說明修復指令，不要做到一半才 permission denied（#198）
	if action == "install" || action == "apply" || action == "rollback" || action == "uninstall" {
		if err := refuseForeignOwned(args); err != nil {
			return err
		}
	}
	switch action {
	case "status":
		version, err := installedVersion()
		if err != nil {
			return err
		}
		if *asJSON {
			return statusJSON(version, *diagnostics)
		}
		if packageManaged() {
			if version == "" {
				version = "（版本未知）"
			}
			fmt.Printf("靈犀注音（0c）：%s\n平台：%s/%s\n更新：由系統套件管理員更新\n", version, runtime.GOOS, runtime.GOARCH)
			printInstalls(os.Stdout, installList())
			printModelStatus(os.Stdout, modelStatus(dialEngine))
			return nil
		}
		if version == "" {
			version = "尚未有版本收據"
		}
		fmt.Printf("靈犀注音（0c）：%s\n平台：%s/%s\n收據：%s\n", version, runtime.GOOS, runtime.GOARCH, receiptPath())
		if line := installIntegrity(); line != "" {
			fmt.Println(line)
		}
		if line := ownershipLine(); line != "" {
			fmt.Println(line)
		}
		if engine := autoupdate.PointerVersion(basePath()); engine != "" {
			fmt.Printf("引擎（核心更新）：%s\n", engine)
		}
		if s, err := autoupdate.Load(basePath()); err == nil {
			printState(s)
		}
		fmt.Println(scheduleStatus())
		printInstalls(os.Stdout, installList())
		printModelStatus(os.Stdout, modelStatus(dialEngine))
		return nil
	case "rollback":
		return rollback(*backup, *restoreUser)
	case "uninstall":
		if err := uninstall(); err != nil {
			return err
		}
		if err := removeSchedule(); err != nil {
			return err
		}
		removeResidentEngine()
		// 排程使用的穩定副本；正在執行時（Windows）刪不掉就留著，重新安裝時會被取代。
		_ = os.RemoveAll(filepath.Join(basePath(), "bin"))
		return nil
	case "install", "verify":
		if *online {
			if action != "install" {
				return fmt.Errorf("-online 只用於 install")
			}
			feed := *feedURL
			if feed == "" {
				feed = DefaultFeed
			}
			return installOnline(feed, *channel)
		}
		root, err := filepath.Abs(*bundle)
		if err != nil {
			return err
		}
		if *archive != "" {
			if action != "verify" {
				return fmt.Errorf("安裝壓縮包請使用 apply -archive 與 -sha256")
			}
			var cleanup func()
			root, cleanup, err = update.StageIn(scratchRoot(), *archive, *checksum, runtime.GOOS, runtime.GOARCH)
			if err != nil {
				return err
			}
			defer cleanup()
		}
		r, err := update.Verify(root, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return err
		}
		if action == "verify" {
			fmt.Printf("已驗證靈犀注音 %s（%s，%s/%s）\n", r.Version, r.Channel, r.OS, r.Arch)
			return nil
		}
		if err = canInstall(r, *channel, *allowDowngrade); err != nil {
			return err
		}
		if err = install(root, r); err != nil {
			return err
		}
		afterInstall()
		forgetInstalledTarget(r.Version)
		return nil
	case "check", "download", "apply":
	default:
		return fmt.Errorf("未知命令：%s", action)
	}
	if *feedURL != "" && *archive != "" {
		return fmt.Errorf("-feed 與 -archive 請擇一")
	}
	var feed *update.Feed
	if *feedURL != "" {
		var err error
		feed, err = update.ReadFeed(context.Background(), update.Client(), *feedURL, *channel)
		if err != nil {
			return err
		}
		asset, err := feed.Select(runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return err
		}
		current, err := installedVersion()
		if err != nil {
			return err
		}
		fmt.Printf("更新通道：%s；最新版本：%s；已安裝：%s\n", feed.Channel, feed.Version, current)
		if action == "check" {
			return nil
		}
		directory := *out
		if directory == "" {
			if action == "download" {
				return fmt.Errorf("download 需要 -out 保存目錄")
			}
			directory, err = os.MkdirTemp(scratchRoot(), "0c-download-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(directory)
		}
		*archive, err = update.Download(context.Background(), update.Client(), asset, directory)
		if err != nil {
			return err
		}
		*checksum = asset.SHA256
		if action == "download" {
			fmt.Println(*archive)
			return nil
		}
	} else if action != "apply" || *archive == "" {
		return fmt.Errorf("check/download 需要 -feed；apply 需要 -feed 或 -archive 與 -sha256")
	}
	root, cleanup, err := update.StageIn(scratchRoot(), *archive, *checksum, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	defer cleanup()
	r, err := update.Verify(root, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if feed != nil && (r.Version != feed.Version || r.Channel != feed.Channel) {
		return fmt.Errorf("套件版本或通道與更新索引不符")
	}
	if err = canInstall(r, *channel, *allowDowngrade); err != nil {
		return err
	}
	if err = install(root, r); err != nil {
		return err
	}
	afterInstall()
	forgetInstalledTarget(r.Version)
	return nil
}
func canInstall(r *update.Release, channel string, allowDowngrade bool) error {
	if r.Channel != channel {
		return fmt.Errorf("套件屬於 %s，請明確指定 -channel %s", r.Channel, r.Channel)
	}
	current, err := installedVersion()
	if err != nil {
		return err
	}
	if current != "" {
		order, err := update.CompareVersion(r.Version, current)
		if err != nil {
			return err
		}
		if order < 0 && !allowDowngrade {
			return fmt.Errorf("拒絕從 %s 降級至 %s；請使用 rollback 或明確指定 -allow-downgrade", current, r.Version)
		}
	}
	return nil
}
func main() {
	flush := installerOutput()
	err := run(os.Args[1:])
	if err != nil && !errors.Is(err, errReported) { // JSON 模式已把錯誤輸出到 stdout
		fmt.Fprintln(os.Stderr, "0c-update:", err)
	}
	launchPrune() // 安裝、回復、解除安裝成功過才有；其餘步驟都做完了，最後才啟動（#179）
	flush()
	if errors.Is(err, errOtherAccount) {
		os.Exit(otherAccountExit) // 安裝程式靠這個結束碼擋下安裝（#199）
	}
	if err != nil {
		os.Exit(1)
	}
}
