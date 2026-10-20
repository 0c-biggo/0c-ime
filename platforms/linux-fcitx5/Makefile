# 靈犀注音的 Fcitx 5 薄殼（docs/19）。
#   make test      以真的 0c-server 測試用戶端（不需要 Fcitx 開發套件）
#   make           建出 lib0c.so（需要 libfcitx5core-dev、libfcitx5utils-dev、libfcitx5config-dev）
#   make install   安裝到家目錄（不需要 sudo），見 README；發行版的系統安裝用 repo 根目錄的 make install PREFIX=/usr（#181）
ROOT     := ../..
CXX      ?= g++
CXXFLAGS ?= -O2 -g
SHELL_VERSION ?= development
# 系統安裝的位置（src/locate.h）：外殼先找家目錄的官網安裝，再找這裡。根目錄的 make install 依 PREFIX 傳入。
ZC_SYSTEM_LIBEXECDIR ?= /usr/libexec/0c
ZC_SYSTEM_DATADIR    ?= /usr/share/0c
CXXFLAGS += -DZC_SHELL_VERSION=\"$(SHELL_VERSION)\" -DZC_SYSTEM_LIBEXECDIR=\"$(ZC_SYSTEM_LIBEXECDIR)\" -DZC_SYSTEM_DATADIR=\"$(ZC_SYSTEM_DATADIR)\"
CXXFLAGS += -std=c++20 -fPIC -pthread -Wall -Wextra -Ithird_party
FCITX    := Fcitx5Core Fcitx5Utils Fcitx5Config
BUILD    := build
PREFIX   ?= $(HOME)/.local
ICON_NAMES := 0c-zhuyin.svg 0c-english.svg
LEGACY_ICON_NAMES := 0c-zhuyin-symbolic.svg 0c-english-symbolic.svg
ICON_DIR := $(PREFIX)/share/icons/hicolor/scalable/status

# 0.1.0-dev.15 以前安裝的 zuin-* 檔名；inputmethod/zuin.conf 是輸入法 ID，沿用不刪。
LEGACY   := $(PREFIX)/bin/zuin-server $(PREFIX)/bin/zuin-trace $(PREFIX)/lib/fcitx5/libzuin.so $(PREFIX)/share/fcitx5/addon/zuin.conf \
            $(HOME)/.config/environment.d/90-zuin.conf

CLIENT_SRC := src/client.cpp src/session.cpp src/trace.cpp src/menu.cpp src/locate.cpp
ENGINE_SRC := src/engine.cpp

.PHONY: all test install install-data uninstall clean

all: $(BUILD)/lib0c.so

$(BUILD)/lib0c.so: $(CLIENT_SRC) $(ENGINE_SRC) src/*.h
	@mkdir -p $(BUILD)
	$(CXX) $(CXXFLAGS) $$(pkg-config --cflags $(FCITX)) -shared -o $@ $(CLIENT_SRC) $(ENGINE_SRC) $$(pkg-config --libs $(FCITX))

$(BUILD)/session_test: $(CLIENT_SRC) test/session_test.cpp src/*.h
	@mkdir -p $(BUILD)
	$(CXX) $(CXXFLAGS) -o $@ $(CLIENT_SRC) test/session_test.cpp

$(BUILD)/trace_test: src/trace.cpp src/trace.h test/trace_test.cpp
	@mkdir -p $(BUILD)
	$(CXX) $(CXXFLAGS) -o $@ src/trace.cpp test/trace_test.cpp

$(BUILD)/0c-server: $(shell find $(ROOT)/core $(ROOT)/bridge $(ROOT)/internal $(ROOT)/cmd/0c-server -name '*.go')
	@mkdir -p $(BUILD)
	cd $(ROOT) && go build -o platforms/linux-fcitx5/$(BUILD)/0c-server ./cmd/0c-server

$(BUILD)/0c-trace: $(shell find $(ROOT)/cmd/0c-trace $(ROOT)/core/proto -name '*.go')
	@mkdir -p $(BUILD)
	cd $(ROOT) && go build -o platforms/linux-fcitx5/$(BUILD)/0c-trace ./cmd/0c-trace

$(BUILD)/legacy_socket_test: $(CLIENT_SRC) test/legacy_socket_test.cpp src/*.h
	@mkdir -p $(BUILD)
	$(CXX) $(CXXFLAGS) -o $@ $(CLIENT_SRC) test/legacy_socket_test.cpp

$(BUILD)/menu_test: src/menu.cpp src/menu.h src/locate.cpp src/locate.h test/menu_test.cpp
	@mkdir -p $(BUILD)
	$(CXX) $(CXXFLAGS) -o $@ src/menu.cpp src/locate.cpp test/menu_test.cpp

$(BUILD)/locate_test: src/locate.cpp src/locate.h test/locate_test.cpp
	@mkdir -p $(BUILD)
	$(CXX) $(CXXFLAGS) -o $@ src/locate.cpp test/locate_test.cpp

$(BUILD)/panel_test: test/panel_test.cpp src/panel.h src/session.h
	@mkdir -p $(BUILD)
	$(CXX) $(CXXFLAGS) -o $@ test/panel_test.cpp

$(BUILD)/keys_test: test/keys_test.cpp src/keys.h
	@mkdir -p $(BUILD)
	$(CXX) $(CXXFLAGS) -o $@ test/keys_test.cpp

$(BUILD)/replay_guard_test: $(CLIENT_SRC) test/replay_guard_test.cpp src/*.h
	@mkdir -p $(BUILD)
	$(CXX) $(CXXFLAGS) -o $@ $(CLIENT_SRC) test/replay_guard_test.cpp

test: $(BUILD)/session_test $(BUILD)/0c-server $(BUILD)/trace_test $(BUILD)/replay_guard_test $(BUILD)/legacy_socket_test $(BUILD)/menu_test $(BUILD)/locate_test $(BUILD)/panel_test $(BUILD)/keys_test
	$(BUILD)/menu_test
	$(BUILD)/locate_test
	$(BUILD)/panel_test
	$(BUILD)/keys_test
	$(BUILD)/replay_guard_test
	$(BUILD)/legacy_socket_test $(abspath $(BUILD)/0c-server) $(abspath $(ROOT)/testdata/toy)
	$(BUILD)/trace_test
	$(BUILD)/session_test $$(mktemp -u /tmp/zuin-test-XXXXXX.sock) $(abspath $(BUILD)/0c-server) $(abspath $(ROOT)/testdata/toy) $$(mktemp -u /tmp/zuin-test-XXXXXX.json)

install: $(BUILD)/lib0c.so $(BUILD)/0c-server $(BUILD)/0c-trace
	install -Dm755 $(BUILD)/0c-server $(PREFIX)/bin/0c-server.new
	mv -f $(PREFIX)/bin/0c-server.new $(PREFIX)/bin/0c-server
	install -Dm644 $(BUILD)/lib0c.so $(PREFIX)/lib/fcitx5/lib0c.so.new
	mv -f $(PREFIX)/lib/fcitx5/lib0c.so.new $(PREFIX)/lib/fcitx5/lib0c.so
	install -Dm755 $(BUILD)/0c-trace $(PREFIX)/bin/0c-trace.new
	mv -f $(PREFIX)/bin/0c-trace.new $(PREFIX)/bin/0c-trace
	@# Library 寫成外殼的絕對路徑，Fcitx5 不必設定 FCITX_ADDON_DIRS（見 scripts/install_package.py 的 addon_conf）。
	install -d $(PREFIX)/share/fcitx5/addon
	sed 's|^Library=lib0c$$|Library=$(abspath $(PREFIX))/lib/fcitx5/lib0c|' 0c-addon.conf > $(PREFIX)/share/fcitx5/addon/0c.conf
	install -Dm644 0c-im.conf $(PREFIX)/share/fcitx5/inputmethod/zuin.conf
	$(foreach icon,$(ICON_NAMES),install -Dm644 icons/$(icon) $(ICON_DIR)/$(icon);)
	rm -f $(addprefix $(ICON_DIR)/,$(LEGACY_ICON_NAMES))
	rm -f $(LEGACY)
	@# 舊版 make install 寫入的 addon 搜尋路徑（只寫 Debian 多架構目錄）已不需要；內容改過的保留。
	@f=$(HOME)/.config/environment.d/90-0c.conf; \
	if [ -f "$$f" ] && [ "$$(grep -v '^#' "$$f")" = 'FCITX_ADDON_DIRS=$${HOME}/.local/lib/fcitx5:/usr/lib/x86_64-linux-gnu/fcitx5' ]; then \
		rm -f "$$f"; echo "已移除不再需要的 $$f"; fi
	@echo "資料包請另外複製到 $(PREFIX)/share/zuin/data（見 README）"

# 編譯限制 bigram 筆數的資料包。
install-data:
	cd $(ROOT) && go run ./cmd/0c-compile -bigram-max 12000000 -out $(PREFIX)/share/zuin/data

uninstall:
	rm -f $(PREFIX)/bin/0c-server $(PREFIX)/bin/0c-trace $(PREFIX)/lib/fcitx5/lib0c.so $(PREFIX)/share/fcitx5/addon/0c.conf \
	      $(PREFIX)/share/fcitx5/inputmethod/zuin.conf $(HOME)/.config/environment.d/90-0c.conf $(LEGACY)
	rm -f $(addprefix $(ICON_DIR)/,$(ICON_NAMES) $(LEGACY_ICON_NAMES))

clean:
	rm -rf $(BUILD)
