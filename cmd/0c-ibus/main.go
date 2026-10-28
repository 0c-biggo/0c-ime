//go:build linux

// 0c-ibus 是靈犀注音的 IBus 外殼：由 ibus-daemon 依元件描述檔啟動，透過 IBus 的私有 D-Bus
// 提供 engine，按鍵與畫面交給 0c-server（與 Fcitx5 外殼共用同一個引擎與個人資料）。
//
//	0c-ibus --ibus        由 ibus-daemon 啟動
//	0c-ibus --component   印出 IBus 元件描述檔
//	0c-ibus register      登錄到目前使用者的 IBus（元件檔、環境、GNOME／Cinnamon 輸入來源）；-gtk-module=false／-qt-module=false 停用 GTK／Qt 輸入設定
//	0c-ibus unregister    移除登錄
//	0c-ibus toolkits      已登錄時補上缺少的 GTK／Qt 輸入設定與 X11 工作階段設定（更新器在換外殼後呼叫），不重啟 IBus
package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/0c-biggo/0c-ime/internal/installs"
	"github.com/godbus/dbus/v5"
)

var buildVersion = "development"

// debug 由 ZC_IBUS_DEBUG=1 開啟，記錄 IBus 呼叫的方法名稱與欄位類型（不含文字）到
// $XDG_RUNTIME_DIR/0c/ibus-debug.log；ibus-daemon 把元件的 stderr 接到 /dev/null。
var debug = os.Getenv("ZC_IBUS_DEBUG") == "1"

func debugArgs(member string, body []interface{}) []interface{} {
	switch member {
	case "Set", "SetCapabilities", "CreateEngine", "PropertyActivate", "CandidateClicked", "Get":
		return body
	}
	return nil
}

type service struct {
	signal          func(dbus.ObjectPath, string, ...interface{}) error // conn.Emit；測試時換成紀錄
	order           *orderer
	client          *client
	documentContext bool

	mu      sync.Mutex // 所有 engine 狀態與送出的訊號都在這把鎖裡
	engines map[dbus.ObjectPath]*engine
	nextID  int
	focused *engine

	update          updateStatus
	updateRunning   string // 執行中的 0c-update 命令（auto／finish）
	updateFailure   string
	settingsFailure string
	vocabFailure    string
	openingSettings bool
	querying        bool
	refreshed       time.Time
	lastKey         time.Time // 最近一次按鍵；更新器據此判斷閒置
	// 「輸出簡體字」（#169）：目前的狀態（快照、0c-update status 與選單切換更新）、引擎能不能即時切換，
	// 以及選單切換的次數（切換前開始的狀態查詢回來時不得把勾選改回去）。
	simplified, simplifiedLive bool
	simplifiedSeq              uint64
	address                    string           // ibus-daemon 的位址，交給更新器在更新後重新叫起外殼
	clock                      func() time.Time // 單按 Shift 計時用；nil 為 time.Now，測試時換成假時鐘
}

func (s *service) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

func (s *service) settingsProperty() property {
	label := settingsLabel
	fallback := s.update.selectionState == "fallback" || s.update.punctuationState == "fallback"
	if s.openingSettings {
		label = "正在開啟靈犀注音設定"
	} else if s.settingsFailure != "" {
		label += "（" + s.settingsFailure + "，點此重試）"
	} else if fallback {
		label += "（模型已退回）"
	}
	return property{key: propSettings, typ: propNormal, sensitive: true, label: label}
}

// vocabProperty 是「我的詞彙…」（#125）：features.vocab 為假時不顯示（visible=false），點了開設定程式的 vocab 頁。
func (s *service) vocabProperty() property {
	label := vocabLabel
	if s.vocabFailure != "" {
		label += "（" + s.vocabFailure + "，點此重試）"
	}
	return property{key: propVocab, typ: propNormal, sensitive: true, label: label, hidden: !s.update.vocab}
}

// simplifiedProperty 是可以打勾的「輸出簡體字」（#169）：0c-update 回報這項設定、或引擎能即時切換時才顯示。
func (s *service) simplifiedProperty() property {
	return property{key: propSimplified, typ: propToggle, sensitive: true, label: simplifiedText, checked: s.simplified,
		hidden: !s.update.simplifiedKnown && !s.simplifiedLive}
}

// setSimplified 在鎖內呼叫：「輸出簡體字」的狀態改變時，選單的勾選與注／英指示（注簡）跟著換。
// live 為真表示來源是回報即時切換的引擎。
func (s *service) setSimplified(on, live bool) {
	s.simplifiedLive = s.simplifiedLive || live
	if on == s.simplified {
		return
	}
	s.simplified = on
	if e := s.focused; e != nil {
		e.emit("UpdateProperty", e.modeProperty().variant())
		e.emit("UpdateProperty", s.simplifiedProperty().variant())
	}
}

func (s *service) updateProperty() property {
	row := statusLabel(s.update)
	if s.update.action == "finish" {
		row += "（點此完成更新）"
	} else {
		row += "（點此檢查更新）"
	}
	switch {
	case s.updateRunning == "auto":
		row = "正在檢查並準備更新"
	case s.updateRunning == "finish":
		row = "正在完成已準備好的更新"
	case s.updateFailure != "":
		row = s.updateFailure + "（點此重試）"
	}
	// 由系統套件管理員更新時（#181）沒有更新列：一律登錄、只改 visible（同「我的詞彙…」）。
	return property{key: propUpdate, typ: propNormal, sensitive: true, label: row, hidden: s.update.packageManaged}
}

// refreshMenu 在鎖內呼叫：把設定、我的詞彙與更新列送給目前焦點的 engine（GNOME 只顯示它的屬性）。
// 「我的詞彙…」的出現與消失也走 UpdateProperty 的 visible：GNOME Shell 每次換 engine 只收第一個
// RegisterProperties（ibusManager.js 的 _engineChanged 收到一次就不再聽），之後重新登錄會被丟掉，
// 只有既有屬性的 UpdateProperty 會生效（_updateSubProperty，IBusProperty.update 連 visible 一起更新）。
func (s *service) refreshMenu() {
	if e := s.focused; e != nil {
		e.emit("UpdateProperty", s.settingsProperty().variant())
		e.emit("UpdateProperty", s.vocabProperty().variant())
		e.emit("UpdateProperty", s.simplifiedProperty().variant())
		e.emit("UpdateProperty", s.updateProperty().variant())
	}
}

// queryStatus 在背景讀更新狀態；沒有重疊查詢，不阻塞按鍵與選單。
func (s *service) queryStatus(command []string) {
	if command == nil && (s.querying || s.updateRunning != "") {
		return
	}
	if command != nil {
		if s.updateRunning != "" {
			return
		}
		s.updateRunning, s.updateFailure = command[0], ""
		s.refreshMenu()
	}
	s.querying = true
	s.refreshed = time.Now()
	seq := s.simplifiedSeq
	go func() {
		failure := ""
		if command != nil {
			if out, ok := runUpdater(command, 30*time.Minute); !ok {
				if out == "" {
					out = "無法執行更新工具"
				}
				failure = "操作失敗：" + firstLine(out)
			}
		}
		out, ok := runUpdater([]string{"status", "--json"}, 20*time.Second)
		s.mu.Lock()
		defer s.mu.Unlock()
		if command != nil {
			s.updateRunning, s.updateFailure = "", failure
		}
		s.querying = false
		if !ok {
			out = ""
		}
		s.update = readStatus(out)
		if s.update.simplifiedKnown && seq == s.simplifiedSeq {
			s.setSimplified(s.update.outputSimplified, s.update.simplifiedLive)
		}
		s.refreshMenu()
	}()
}

// openSettings 不排進更新命令（更新檢查最長 30 分鐘）；0c-update 啟動設定程式後立刻返回，失敗時也會送桌面通知。
// vocab 為真時開「我的詞彙」頁；失敗原因顯示在被點的那一列。
func (s *service) openSettings(vocab bool) {
	if s.openingSettings {
		return
	}
	s.openingSettings, s.settingsFailure, s.vocabFailure = true, "", ""
	s.refreshMenu()
	args := []string{"open-settings"}
	if vocab {
		args = append(args, "vocab")
	}
	go func() {
		out, ok := runUpdater(args, time.Minute)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.openingSettings = false
		if !ok {
			if out == "" {
				out = "無法執行更新工具"
			}
			if vocab {
				s.vocabFailure = "開啟失敗：" + firstLine(out)
			} else {
				s.settingsFailure = "開啟失敗：" + firstLine(out)
			}
		}
		s.refreshMenu()
	}()
}

func (s *service) createEngine(name string) (dbus.ObjectPath, error) {
	if name != engineName {
		return "", dbus.MakeFailedError(fmt.Errorf("不認得的 engine %q", name))
	}
	s.nextID++
	path := dbus.ObjectPath(enginePrefix + strconv.Itoa(s.nextID))
	s.engines[path] = &engine{svc: s, path: path, session: newSession(s.client, s.documentContext)}
	return path, nil
}

func (s *service) dispatch(msg *dbus.Message, path dbus.ObjectPath, iface, member string) ([]interface{}, error) {
	release := s.order.wait(msg)
	defer release()
	s.mu.Lock()
	defer s.mu.Unlock()
	body := msg.Body
	if debug && member != "ProcessKeyEvent" && member != "SetSurroundingText" && member != "SetCursorLocation" {
		// 只記方法與欄位類型，不記按鍵與文件文字。
		log.Printf("%s %s.%s %v", path, iface, member, debugArgs(member, body))
	}
	if iface == ifaceIntrospect && member == "Introspect" {
		if path == factoryPath {
			return []interface{}{factoryIntrospection}, nil
		}
		return []interface{}{engineIntrospection}, nil
	}
	if path == factoryPath {
		if iface == ifaceFactory && member == "CreateEngine" && len(body) == 1 {
			name, _ := body[0].(string)
			p, err := s.createEngine(name)
			return []interface{}{p}, err
		}
		return nil, unknown(member)
	}
	e := s.engines[path]
	if e == nil {
		return nil, dbus.MakeFailedError(fmt.Errorf("engine %s 不存在", path))
	}
	switch iface {
	case ifaceService:
		if member == "Destroy" {
			e.finish("destroy")
			e.session.close()
			delete(s.engines, path)
			if s.focused == e {
				s.focused = nil
			}
			return nil, nil
		}
	case ifaceProperties:
		return s.engineProperty(e, member, body)
	case ifaceEngine, "":
		return s.engineMethod(e, member, body)
	}
	return nil, unknown(member)
}

func (s *service) engineProperty(e *engine, member string, body []interface{}) ([]interface{}, error) {
	name := ""
	if len(body) >= 2 {
		name, _ = body[1].(string)
	}
	switch member {
	case "Get":
		switch name {
		case "FocusId":
			return []interface{}{dbus.MakeVariant(false)}, nil
		case "ActiveSurroundingText":
			return []interface{}{dbus.MakeVariant(true)}, nil
		}
	case "Set":
		if name == "ContentType" && len(body) == 3 {
			if v, ok := body[2].(dbus.Variant); ok {
				if f, ok := v.Value().([]interface{}); ok && len(f) == 2 {
					e.purpose, _ = f[0].(uint32)
					e.hints, _ = f[1].(uint32)
					e.state()
				}
			}
			return nil, nil
		}
	case "GetAll":
		return []interface{}{map[string]dbus.Variant{"FocusId": dbus.MakeVariant(false),
			"ActiveSurroundingText": dbus.MakeVariant(true)}}, nil
	}
	return nil, unknown(member)
}

func u32(body []interface{}, i int) uint32 {
	if i < len(body) {
		v, _ := body[i].(uint32)
		return v
	}
	return 0
}

func (s *service) engineMethod(e *engine, member string, body []interface{}) ([]interface{}, error) {
	switch member {
	case "ProcessKeyEvent":
		s.lastKey = time.Now()
		return []interface{}{e.processKey(u32(body, 0), u32(body, 1), u32(body, 2))}, nil
	case "FocusIn", "FocusInId":
		s.focused = e
		// 每次取得焦點都重新啟用：mode-memory=native 時換視窗回到開始時的模式（start-mode，#160；同 Fcitx5 外殼；GNOME Wayland 上所有視窗
		// 共用一個輸入框，換視窗只看得到 FocusOut／FocusIn），shared 時由核心回到共用的模式（#115）。
		e.enable()
		e.registerProperties()
		if time.Since(s.refreshed) > time.Minute {
			s.queryStatus(nil)
		}
	case "FocusOut", "FocusOutId":
		e.finish("focus_out")
		if s.focused == e {
			s.focused = nil
		}
	case "Reset":
		e.finish("reset")
	case "Enable":
		e.enable()
	case "Disable":
		e.finish("disable")
	case "SetCapabilities":
		e.caps = u32(body, 0)
	case "SetSurroundingText":
		if len(body) == 3 {
			v, _ := body[0].(dbus.Variant)
			text, ok := textOf(v)
			e.surrounding = docText{text: text, cursor: u32(body, 1), anchor: u32(body, 2), valid: ok}
			e.state()
			if e.active && e.session.contextAllowed() {
				e.context.observed(e.document())
			} else {
				e.context.clear()
			}
		}
	case "CandidateClicked":
		e.selectCandidate(u32(body, 0))
	case "PageUp":
		e.navigate("PageUp")
	case "PageDown":
		e.navigate("PageDown")
	case "CursorUp":
		e.navigate("Up")
	case "CursorDown":
		e.navigate("Down")
	case "PropertyActivate":
		name, _ := body[0].(string)
		switch name {
		case propInputMode:
			e.toggleMode()
		case propSettings:
			s.openSettings(false)
		case propVocab:
			s.openSettings(true)
		case propSimplified:
			e.toggleSimplified()
		case propUpdate:
			if !s.update.packageManaged {
				s.queryStatus(statusCommand(s.update))
			}
		}
	case "SetCursorLocation", "PropertyShow", "PropertyHide", "ProcessHandWritingEvent", "CancelHandWriting",
		"PanelExtensionReceived", "PanelExtensionRegisterKeys":
	default:
		return nil, unknown(member)
	}
	return nil, nil
}

func unknown(member string) error {
	return dbus.NewError("org.freedesktop.DBus.Error.UnknownMethod", []interface{}{"不支援 " + member})
}

// ibusAddress 照 libibus 的 ibus_get_address 找 ibus-daemon 的位址。
func ibusAddress() (string, error) {
	if a := os.Getenv("IBUS_ADDRESS"); a != "" {
		return a, nil
	}
	if path, err := ibusAddressFile(); err == nil {
		if b, err := os.ReadFile(path); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if a, ok := strings.CutPrefix(line, "IBUS_ADDRESS="); ok && a != "" {
					return a, nil
				}
			}
		}
	}
	out, err := exec.Command("ibus", "address").Output()
	if a := strings.TrimSpace(string(out)); err == nil && a != "" && a != "(null)" {
		return a, nil
	}
	return "", fmt.Errorf("找不到 ibus-daemon 的位址")
}

func ibusAddressFile() (string, error) {
	if p := os.Getenv("IBUS_ADDRESS_FILE"); p != "" {
		return p, nil
	}
	var id []byte
	var err error
	for _, p := range []string{"/var/lib/dbus/machine-id", "/etc/machine-id"} {
		if id, err = os.ReadFile(p); err == nil {
			break
		}
	}
	if err != nil {
		return "", err
	}
	host, display := "unix", "0"
	if w := os.Getenv("WAYLAND_DISPLAY"); w != "" {
		display = w
	} else if d := os.Getenv("DISPLAY"); d != "" {
		h, rest, found := strings.Cut(d, ":")
		if found {
			display, _, _ = strings.Cut(rest, ".")
		}
		if h != "" {
			host = h
		}
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(config, "ibus/bus", strings.TrimSpace(string(id))+"-"+host+"-"+display), nil
}

// handOffToPersonal：ibus-daemon 帶起的是系統安裝的 0c-ibus，而這個帳號另外有官網安裝時，原地換成官網的
// 0c-ibus（成功時不會返回）。兩個元件同名，IBUS_COMPONENT_PATH 常把 /usr/share 排在家目錄前面；不交給官網
// 那一份的話，官網更新器換掉的外殼要等系統套件移除才生效（#181，.13 實測）。
func handOffToPersonal() {
	self, err := os.Executable()
	if err != nil || !installs.Default().InSystem(self) {
		return
	}
	if p, ok := installs.Default().Personal(); ok && p.IBus != "" {
		err = syscall.Exec(p.IBus, append([]string{p.IBus}, os.Args[1:]...), os.Environ())
		log.Printf("0c-ibus: 無法交給官網安裝的 %s，照常使用系統安裝：%v", p.IBus, err)
	}
}

// component 是 ibus-daemon 讀的元件描述檔；路徑都是絕對路徑（ibus-daemon 不搜尋 PATH）。
// component 是元件描述檔：官網安裝寫到 ~/.local/share/ibus/component（register），系統安裝由 make install 從
// platforms/linux-ibus/0c.xml.in 產生（測試核對兩者一致）。
func component(exe, home string) string {
	icon := filepath.Join(home, ".local/share/icons/hicolor/scalable/status/0c-zhuyin.svg")
	setup := updaterPath()
	if l := installs.Default(); l.InSystem(exe) {
		icon = filepath.Join(filepath.Dir(l.Data), "icons/hicolor/scalable/status/0c-zhuyin.svg")
		setup = filepath.Join(l.Libexec, "0c-update")
	}
	return componentXML(exe, icon, setup)
}

func componentXML(exe, icon, setup string) string {
	esc := func(s string) string {
		var b strings.Builder
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	return `<?xml version="1.0" encoding="utf-8"?>
<component>
    <name>` + componentName + `</name>
    <description>靈犀注音</description>
    <exec>` + esc(exe) + ` --ibus</exec>
    <author>BigGo</author>
    <engines>
        <engine>
            <name>` + engineName + `</name>
            <language>zh_TW</language>
            <author>BigGo</author>
            <icon>` + esc(icon) + `</icon>
            <layout>us</layout>
            <longname>靈犀注音</longname>
            <description>靈犀注音</description>
            <rank>80</rank>
            <symbol>注</symbol>
            <setup>` + esc(setup) + ` open-settings</setup>
        </engine>
    </engines>
</component>
`
}

func main() {
	fromDaemon := flag.Bool("ibus", false, "由 ibus-daemon 啟動")
	printComponent := flag.Bool("component", false, "印出 IBus 元件描述檔")
	version := flag.Bool("version", false, "印出版本")
	if len(os.Args) > 1 && (os.Args[1] == "register" || os.Args[1] == "unregister" || os.Args[1] == "toolkits") {
		log.SetFlags(0)
		sub := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
		sub.Bool("gtk-module", true, "讓 GTK 程式改用 IBus 的 GTK 模組（GNOME 失焦不丟組字）；false 停用，之後不再自動加回")
		sub.Bool("qt-module", true, "讓 Qt 6.8 以上的程式改用 IBus 的 Qt 外掛（GNOME 失焦不丟組字）；false 停用，之後不再自動加回")
		sub.Parse(os.Args[2:])
		// 沒寫的旗標維持原本的選擇：重跑安裝不會把使用者停用的設定加回來。
		want := map[string]bool{}
		sub.Visit(func(f *flag.Flag) { want[f.Name] = f.Value.String() == "true" })
		run := unregister
		switch os.Args[1] {
		case "register":
			run = func() error { return register(want) }
		case "toolkits":
			run = syncRegisteredToolkits
		}
		if err := run(); err != nil {
			log.Fatal(err)
		}
		return
	}
	flag.Parse()
	switch {
	case *version:
		fmt.Println(buildVersion)
		return
	case *printComponent:
		exe, err := os.Executable()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(component(exe, os.Getenv("HOME")))
		return
	case !*fromDaemon:
		fmt.Fprintln(os.Stderr, "0c-ibus 由 ibus-daemon 啟動；以 0c-ibus register 登錄到 IBus。")
		os.Exit(2)
	}
	handOffToPersonal()
	log.SetPrefix("0c-ibus: ")
	if debug {
		dir := filepath.Join(runtimeDir(), "0c")
		os.MkdirAll(dir, 0o700)
		if f, err := os.OpenFile(filepath.Join(dir, "ibus-debug.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			log.SetOutput(f)
		}
	}
	address, err := ibusAddress()
	if err != nil {
		log.Fatal(err)
	}
	// 預設確認自己剛提交的文字供下一段選字（神經選字的必要條件）；
	// ZUIN_DOCUMENT_CONTEXT=0 只供排錯時讓外殼完全不追蹤，與 Fcitx5 外殼相同。
	s := &service{order: newOrderer(), client: newClient(), engines: map[dbus.ObjectPath]*engine{},
		documentContext: os.Getenv("ZUIN_DOCUMENT_CONTEXT") != "0", lastKey: time.Now(), address: address,
		update: readStatus("")} // 第一次查詢前：套件管理員模式也不顯示更新列
	conn, err := dbus.Connect(address, dbus.WithHandler(handler{s}), dbus.WithIncomingInterceptor(s.order.intercept))
	if err != nil {
		log.Fatalf("連不上 ibus-daemon：%v", err)
	}
	s.signal = conn.Emit
	reply, err := conn.RequestName(componentName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		log.Fatalf("無法取得 %s（%v）", componentName, err)
	}
	s.startGuard()
	s.mu.Lock()
	s.queryStatus(nil)
	s.mu.Unlock()
	go func() {
		// 與 Fcitx5 外殼相同每 5 秒更新選單狀態，但只在靈犀注音有焦點時：沒在用時不必一直啟動 0c-update。
		for range time.Tick(5 * time.Second) {
			s.mu.Lock()
			if s.focused != nil {
				s.queryStatus(nil)
			}
			s.mu.Unlock()
		}
	}()
	<-conn.Context().Done() // ibus-daemon 結束時連線關閉，跟著結束；0c-server 繼續服務其他外殼
}
