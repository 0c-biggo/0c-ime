ROOT := ../..
BUILD := build
APP := $(BUILD)/0c.app
DATA_DIR ?= $(abspath $(ROOT)/build/data)
# scripts/prepare_neural.py --platform macos-arm64 的輸出；空值則不內附神經模型（退回統計引擎）。
NEURAL_DIR ?=
# App 內附的 0c-update（自動更新排程以 SMAppService 執行它，見 Sources/UpdateAgent.swift）；空值則不內附排程。
UPDATER ?=
# 只支援 stdio 的 AI 工具用的轉接 0c-mcp（#126），放在 Contents/MacOS/0c-mcp；空值則不內附。
MCP ?=
# scripts/build_settings_macos.py 產生的 0c-settings.app（設定程式）；內附在 Contents/Helpers/，空值則不內附。
SETTINGS_APP ?=
# 排程喚起間隔（秒）；查詢頻率由協調器決定。E2E 建置用 60。
AGENT_INTERVAL ?= 900
SWIFTC ?= xcrun swiftc
SWIFTFLAGS := -swift-version 5 -O -target $(shell uname -m)-apple-macosx13.0
SHARED := Sources/Protocol.swift Sources/MarkedText.swift Sources/EngineClient.swift Sources/KeyMapping.swift Sources/ModelStatus.swift Sources/CaretGeometry.swift Sources/RequestQueue.swift Sources/SecureInputPolicy.swift Sources/RightShift.swift

.PHONY: all app server test test-controller-recovery test-request-queue test-input-source test-retire test-ui test-update-ui test-update-notification test-settings-launcher data-basic install clean icon
all: app

$(BUILD)/0c: $(wildcard Sources/*.swift)
	@mkdir -p $(BUILD)
	$(SWIFTC) $(SWIFTFLAGS) -framework AppKit -framework InputMethodKit -framework Carbon -framework ServiceManagement -framework UserNotifications $^ -o $@

$(BUILD)/0c-settings-launcher: SettingsLauncher/Launcher.swift SettingsLauncher/main.swift
	@mkdir -p $(BUILD)
	$(SWIFTC) $(SWIFTFLAGS) -framework AppKit -framework CoreServices $^ -o $@

ifneq ($(strip $(SETTINGS_APP)),)
app: $(BUILD)/0c-settings-launcher
endif

server:
	@mkdir -p $(BUILD)
	cd $(ROOT) && go build -o platforms/macos/$(BUILD)/0c-server ./cmd/0c-server

# Data is explicitly selected; never silently package the toy test dictionary.
app: $(BUILD)/0c server Info.plist Icon/0c.tiff Icon/AppIcon.icns UpdateAgent.plist
	test -f "$(DATA_DIR)/manifest.json"
	@mkdir -p "$(APP)/Contents/MacOS" "$(APP)/Contents/Resources"
	rm -rf "$(APP)/Contents/MacOS"
	mkdir -p "$(APP)/Contents/MacOS"
	cp $(BUILD)/0c $(BUILD)/0c-server "$(APP)/Contents/MacOS/"
	rm -rf "$(APP)/Contents/Library" "$(APP)/Contents/Helpers"
	if [ -n "$(SETTINGS_APP)" ]; then \
	  test -f "$(SETTINGS_APP)/Contents/MacOS/0c-settings" && \
	  mkdir -p "$(APP)/Contents/Helpers" "$(APP)/Contents/Helpers/0c-settings-launcher.app/Contents/MacOS" "$(APP)/Contents/Helpers/0c-settings-launcher.app/Contents/Resources" && \
	  ditto "$(SETTINGS_APP)" "$(APP)/Contents/Helpers/0c-settings.app" && \
	  cp $(BUILD)/0c-settings-launcher "$(APP)/Contents/Helpers/0c-settings-launcher.app/Contents/MacOS/" && \
	  cp SettingsLauncher/Info.plist "$(APP)/Contents/Helpers/0c-settings-launcher.app/Contents/Info.plist" && \
	  { [ -z "$(BUNDLE_VERSION)" ] || { plutil -replace CFBundleVersion -string "$(BUNDLE_VERSION)" "$(APP)/Contents/Helpers/0c-settings-launcher.app/Contents/Info.plist" && \
	    plutil -replace CFBundleShortVersionString -string "$(SHORT_VERSION)" "$(APP)/Contents/Helpers/0c-settings-launcher.app/Contents/Info.plist"; }; } && \
	  cp Icon/AppIcon.icns "$(APP)/Contents/Helpers/0c-settings-launcher.app/Contents/Resources/" && \
	  codesign --force --sign - "$(APP)/Contents/Helpers/0c-settings-launcher.app"; fi
	if [ -n "$(UPDATER)" ]; then \
	  cp "$(UPDATER)" "$(APP)/Contents/MacOS/0c-update" && \
	  mkdir -p "$(APP)/Contents/Library/LaunchAgents" && \
	  sed 's/@INTERVAL@/$(AGENT_INTERVAL)/' UpdateAgent.plist > "$(APP)/Contents/Library/LaunchAgents/com.biggo.0c.update.plist" && \
	  plutil -lint "$(APP)/Contents/Library/LaunchAgents/com.biggo.0c.update.plist"; fi
	if [ -n "$(MCP)" ]; then cp "$(MCP)" "$(APP)/Contents/MacOS/0c-mcp"; fi
	cp Info.plist "$(APP)/Contents/Info.plist"
	# 版本號由打包程式給（scripts/macos_version.py）：CFBundleVersion 隨版本遞增，LaunchServices 才不會開到備份裡的舊 App（#77）；
	# ZCBuildVersion 是完整版本，外殼用它判斷自己是不是更新目標。開發建置不給，維持 Info.plist 的預設值。
	if [ -n "$(BUNDLE_VERSION)" ]; then \
	  plutil -replace CFBundleVersion -string "$(BUNDLE_VERSION)" "$(APP)/Contents/Info.plist" && \
	  plutil -replace CFBundleShortVersionString -string "$(SHORT_VERSION)" "$(APP)/Contents/Info.plist"; fi
	if [ -n "$(BUILD_VERSION)" ]; then plutil -replace ZCBuildVersion -string "$(BUILD_VERSION)" "$(APP)/Contents/Info.plist"; fi
	cp Icon/0c.tiff "$(APP)/Contents/Resources/0c.tiff"
	cp Icon/AppIcon.icns "$(APP)/Contents/Resources/AppIcon.icns"
	for l in Resources/*.lproj; do mkdir -p "$(APP)/Contents/Resources/$$(basename $$l)" && cp $$l/InfoPlist.strings "$(APP)/Contents/Resources/$$(basename $$l)/"; done
	rm -rf "$(APP)/Contents/Resources/data" "$(APP)/Contents/Resources/neural"
	ditto "$(DATA_DIR)" "$(APP)/Contents/Resources/data"
	if [ -n "$(NEURAL_DIR)" ]; then test -f "$(NEURAL_DIR)/select/manifest.json" && ditto "$(NEURAL_DIR)" "$(APP)/Contents/Resources/neural"; fi
	plutil -lint "$(APP)/Contents/Info.plist"
	codesign --force --sign - "$(APP)/Contents/MacOS/0c-server"
	if [ -n "$(UPDATER)" ]; then codesign --force --sign - "$(APP)/Contents/MacOS/0c-update"; fi
	if [ -n "$(MCP)" ]; then codesign --force --sign - "$(APP)/Contents/MacOS/0c-mcp"; fi
	codesign --force --sign - "$(APP)"
	codesign --verify --strict "$(APP)"

# 選單列圖示由 Icon/make_icon.swift 產生，產物 Icon/0c.tiff 隨原始碼保存；改圖時重跑。
# App 圖示 Icon/AppIcon.icns 由 Icon/make_app_icon.swift 產生（同官網 favicon），同樣隨原始碼保存。
icon:
	@mkdir -p $(BUILD)
	swift Icon/make_icon.swift Icon/0c.tiff $(BUILD)/icon-preview.png

data-basic:
	cd $(ROOT) && go run ./cmd/0c-compile -bigram '' -out build/macos-basic-data

$(BUILD)/integration-test: $(SHARED) Tests/main.swift
	@mkdir -p $(BUILD)
	$(SWIFTC) $(SWIFTFLAGS) -framework AppKit -framework Carbon $^ -o $@

test: $(BUILD)/integration-test server test-input-source test-request-queue test-retire test-controller-recovery
	$(BUILD)/integration-test "$(abspath $(BUILD)/0c-server)" "$(abspath $(ROOT)/testdata/toy)"

# Same controller/IPC implementation with headless host adapters; no IMK
# registration, desktop event injection, installation or real user data.
$(BUILD)/controller-recovery/InputController.swift: Sources/InputController.swift Tests/ControllerRecovery/prepare.py
	@mkdir -p "$(dir $@)"
	python3 Tests/ControllerRecovery/prepare.py $< $@

$(BUILD)/controller-recovery-test: $(filter-out Sources/ModelStatus.swift,$(SHARED)) $(BUILD)/controller-recovery/InputController.swift Tests/ControllerRecovery/Host.swift Tests/ControllerRecovery/main.swift
	@mkdir -p $(BUILD)
	$(SWIFTC) $(SWIFTFLAGS) -framework AppKit -framework Carbon $^ -o $@

test-controller-recovery: $(BUILD)/controller-recovery-test server
	python3 Tests/ControllerRecovery/run.py "$(abspath $(BUILD)/0c-server)" "$(abspath $(ROOT)/testdata/toy)" "$(abspath $(BUILD)/controller-recovery-test)"

$(BUILD)/request-queue-test: Sources/Protocol.swift Sources/RequestQueue.swift Tests/RequestQueue/main.swift
	@mkdir -p $(BUILD)
	$(SWIFTC) $(SWIFTFLAGS) $^ -o $@

test-request-queue: $(BUILD)/request-queue-test
	$(BUILD)/request-queue-test

$(BUILD)/retire-test: Sources/Retire.swift Sources/Handover.swift Tests/Retire/main.swift
	@mkdir -p $(BUILD)
	$(SWIFTC) $(SWIFTFLAGS) $^ -o $@

test-retire: $(BUILD)/retire-test
	$(BUILD)/retire-test

$(BUILD)/input-source-test: Sources/InputSourceLookup.swift Tests/InputSource/main.swift
	@mkdir -p $(BUILD)
	$(SWIFTC) $(SWIFTFLAGS) $^ -o $@

test-input-source: $(BUILD)/input-source-test
	$(BUILD)/input-source-test

$(BUILD)/presentation-test: Sources/Protocol.swift Sources/MarkedText.swift Sources/CandidatePanel.swift Tests/Presentation/main.swift
	@mkdir -p $(BUILD)
	$(SWIFTC) $(SWIFTFLAGS) -framework AppKit $^ -o $@

test-ui: $(BUILD)/presentation-test
	$(BUILD)/presentation-test

$(BUILD)/update-menu-test: Sources/UpdateMenu.swift Tests/UpdateMenu/main.swift
	@mkdir -p $(BUILD)
	$(SWIFTC) $(SWIFTFLAGS) -framework AppKit $^ -o $@

test-update-ui: $(BUILD)/update-menu-test
	$(BUILD)/update-menu-test

$(BUILD)/update-notification-test: Sources/UpdateMenu.swift Sources/UpdateNotification.swift Tests/UpdateNotification/main.swift
	@mkdir -p $(BUILD)
	$(SWIFTC) $(SWIFTFLAGS) -framework AppKit -framework UserNotifications $^ -o $@

test-update-notification: $(BUILD)/update-notification-test
	$(BUILD)/update-notification-test

$(BUILD)/settings-launcher-test: SettingsLauncher/Launcher.swift Tests/SettingsLauncher/main.swift
	@mkdir -p $(BUILD)
	$(SWIFTC) $(SWIFTFLAGS) -framework AppKit -framework CoreServices $^ -o $@

test-settings-launcher: $(BUILD)/settings-launcher-test
	$(BUILD)/settings-launcher-test

# First install only. Updating a running input method needs a logout/relogin.
install: app
	test ! -e "$(HOME)/Library/Input Methods/0c.app"
	test ! -e "$(HOME)/Library/Input Methods/Zuin.app"
	mkdir -p "$(HOME)/Library/Input Methods"
	ditto "$(APP)" "$(HOME)/Library/Input Methods/0c.app"
	@echo "已安裝。登出再登入，在系統設定 → 鍵盤 → 文字輸入 → 編輯加入 靈犀注音。"

clean:
	rm -rf $(BUILD)
