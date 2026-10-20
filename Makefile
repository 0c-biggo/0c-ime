.PHONY: help assets upgrade test-upgrade test-release test-source-release test-sim
.DEFAULT_GOAL := help

PULL ?= 1

help:
	@echo 'make assets           下載與這份原始碼相符的字典與模型（RELEASE=vX.Y.Z 指定版本，MODELS=0 只取字典）'
	@echo 'make upgrade          更新已安裝的 Linux Fcitx5 輸入法（含 git pull）'
	@echo 'make upgrade PULL=0   使用目前工作目錄，略過 git pull'
	@echo 'make test-release     驗證套件檢查與隔離安裝／回復流程'
	@echo 'make test-source-release  驗證 GitHub 原始碼與模型 tarball 流程'
	@echo 'python3 scripts/source_release.py --help  建立原始碼發佈檔與另附的模型檔'
	@echo 'make build PREFIX=/usr && make install PREFIX=/usr DESTDIR=…  發行版的系統安裝（由套件管理員更新，見 platforms/linux-system）'
	@echo 'python3 scripts/package_release.py --help  建立完整 Linux 套件'
	@echo 'python3 scripts/package_windows.py --help  建立 Windows x64 套件（在 Windows 執行）'
	@echo 'python3 scripts/package_windows_installer.py --help  將 Windows ZIP 製成 EXE 安裝精靈'
	@echo 'go run ./cmd/0c-update status  檢查套件版本'

assets:
	python3 scripts/fetch_assets.py $(if $(RELEASE),--release $(RELEASE),) $(if $(filter 0,$(MODELS)),--no-models,)

upgrade:
	python3 scripts/upgrade.py $(if $(filter 0,$(PULL)),--no-pull,)

test-upgrade:
	python3 -m unittest discover -s scripts -p 'test_upgrade.py'

test-sim:
	go test -count=1 -timeout 60m -run TestSimulated ./internal/autoupdate/ -sim.seeds=300 -sim.days=21

test-release:
	python3 -m unittest discover -s scripts -p 'test_release.py'
	python3 -m unittest discover -s platforms/linux-fcitx5/test -p 'l3_result_test.py'
	$(MAKE) test-source-release

test-source-release:
	python3 scripts/check_public_tree.py
	python3 -m unittest discover -s scripts -p 'test_source_release.py'
	python3 -m unittest discover -s scripts -p 'test_copyright.py'

# ---- 系統安裝（Linux 發行版套件，#181）----
# make && make install PREFIX=/usr DESTDIR=<暫存根目錄>：把兩種外殼（Fcitx5、IBus）、引擎、0c-update、設定程式、
# 圖示、應用程式選單項目、字典與神經推論 worker 放到系統目錄，並放上 distribution.json（由套件管理員更新）。
# 不寫安裝收據、不建 systemd 單元、不碰任何家目錄；個人資料照舊在 ~/.local/share/zuin。路徑與相依見 platforms/linux-system/README.md。
PREFIX       ?= /usr/local
BINDIR       ?= $(PREFIX)/bin
LIBDIR       ?= $(PREFIX)/lib
LIBEXECDIR   ?= $(PREFIX)/libexec
DATAROOTDIR  ?= $(PREFIX)/share
DESTDIR      ?=
GO           ?= go
CARGO        ?= cargo
GOBUILDFLAGS ?= -trimpath
VERSION      ?= $(shell git describe --tags --match 'v[0-9]*' 2>/dev/null | sed 's/^v//')
# 字典：0c-ime 原始碼 tarball 的 assets/data（make assets）；也可指向套件或 0c-compile 的輸出。
DATA         ?= assets/data
FCITX5       ?= 1
IBUS         ?= 1
SETTINGS     ?= 1
# 已建好的設定程式（例如 scripts/build_settings_linux.py 的輸出）；空的時候以 cargo 建置。
SETTINGS_BIN ?=

ZC_LIBEXEC   := $(LIBEXECDIR)/0c
ZC_SHARE     := $(DATAROOTDIR)/0c
SYSBUILD     := build/system
GO_MODULE    := $(shell $(GO) list -m 2>/dev/null)
GO_LDFLAGS   := -X main.buildVersion=$(VERSION) -X $(GO_MODULE)/internal/installs.LibexecDir=$(ZC_LIBEXEC) \
                -X $(GO_MODULE)/internal/installs.DataDir=$(ZC_SHARE) -X $(GO_MODULE)/internal/installs.BinDir=$(BINDIR)
# Fcitx5 只從自己的 addon 目錄載入外殼（Debian 是多架構的 /usr/lib/<triplet>/fcitx5）。PREFIX=/usr 時裝到那裡、
# addon 設定寫相對的 Library；其他 PREFIX 裝到 $(LIBDIR)/fcitx5，Library 寫絕對路徑（Fcitx5 不必另外設定）。
FCITX5_SYSTEM_ADDONDIR := $(shell pkg-config --variable=libdir Fcitx5Core 2>/dev/null)/fcitx5
FCITX5_ADDONDIR ?= $(if $(filter /usr,$(PREFIX)),$(FCITX5_SYSTEM_ADDONDIR),$(LIBDIR)/fcitx5)
FCITX5_LIBRARY  := $(if $(filter $(FCITX5_SYSTEM_ADDONDIR),$(FCITX5_ADDONDIR)),lib0c,$(FCITX5_ADDONDIR)/lib0c)
# ibus-daemon 只讀它自己的元件目錄（編譯時的 datadir/ibus/component，Debian 是 /usr/share/ibus/component；
# 設了 IBUS_COMPONENT_PATH 才讀別處），不看 PREFIX：PREFIX=/usr/local 時裝到 /usr/local/share 不會被讀到。
# 所以預設一律裝到 ibus 的目錄（pkg-config ibus-1.0 的 datadir；沒有 libibus-1.0-dev 時是 /usr/share）。
IBUS_DATADIR      := $(or $(shell pkg-config --variable=datadir ibus-1.0 2>/dev/null),/usr/share)
IBUS_COMPONENTDIR ?= $(IBUS_DATADIR)/ibus/component
SETTINGS_OUT := $(if $(SETTINGS_BIN),$(SETTINGS_BIN),$(SYSBUILD)/settings/release/0c-settings)

.PHONY: build build-go build-fcitx5 build-settings install uninstall check-version

build: build-go $(if $(filter 1,$(FCITX5)),build-fcitx5) $(if $(filter 1,$(SETTINGS)),$(if $(SETTINGS_BIN),,build-settings))

check-version:
	@test -n '$(VERSION)' || { echo '請指定 VERSION（例如 make VERSION=0.2.3；沒有 git 時無法自動取得）' >&2; exit 1; }

# 0c-update、0c-ibus、0c-mcp 不依賴 C 函式庫（同官網套件）；0c-server 照官網套件以預設方式建置。
build-go: check-version
	@mkdir -p $(SYSBUILD)
	$(GO) build $(GOBUILDFLAGS) -ldflags '$(GO_LDFLAGS)' -o $(SYSBUILD)/0c-server ./cmd/0c-server
	$(foreach b,0c-update 0c-mcp $(if $(filter 1,$(IBUS)),0c-ibus),CGO_ENABLED=0 $(GO) build $(GOBUILDFLAGS) -ldflags '$(GO_LDFLAGS)' -o $(SYSBUILD)/$(b) ./cmd/$(b) &&) true

build-fcitx5: check-version
	$(MAKE) -C platforms/linux-fcitx5 BUILD=$(abspath $(SYSBUILD))/fcitx5 SHELL_VERSION=$(VERSION) \
		ZC_SYSTEM_LIBEXECDIR=$(ZC_LIBEXEC) ZC_SYSTEM_DATADIR=$(ZC_SHARE) $(abspath $(SYSBUILD))/fcitx5/lib0c.so

build-settings: check-version
	cd platforms/settings && ZC_BUILD_VERSION=$(VERSION) ZC_SYSTEM_LIBEXECDIR=$(ZC_LIBEXEC) ZC_SYSTEM_BINDIR=$(BINDIR) \
		CARGO_TARGET_DIR=$(abspath $(SYSBUILD))/settings $(CARGO) build --release --locked --features tauri/custom-protocol

install: check-version
	@test -f '$(DATA)/manifest.json' || { echo '找不到字典 $(DATA)/manifest.json；以 DATA=<字典目錄> 指定（0c-ime 原始碼 tarball 在 assets/data）' >&2; exit 1; }
	@test -x $(SYSBUILD)/0c-server || { echo '請先執行 make build（以同樣的 PREFIX 與 VERSION）' >&2; exit 1; }
	install -Dm755 $(SYSBUILD)/0c-server $(DESTDIR)$(ZC_LIBEXEC)/0c-server
	install -Dm755 $(SYSBUILD)/0c-update $(DESTDIR)$(ZC_LIBEXEC)/0c-update
	install -Dm755 $(SYSBUILD)/0c-mcp $(DESTDIR)$(BINDIR)/0c-mcp
	install -d $(DESTDIR)$(ZC_SHARE)/data
	cp -R --no-preserve=mode,ownership '$(DATA)/.' $(DESTDIR)$(ZC_SHARE)/data/
	chmod -R u=rwX,go=rX $(DESTDIR)$(ZC_SHARE)/data
	install -Dm644 platforms/neural/runtime/sequence_runtime.py $(DESTDIR)$(ZC_SHARE)/neural/sequence_runtime.py
	install -Dm644 platforms/neural/runtime/punct_runtime.py $(DESTDIR)$(ZC_SHARE)/neural/punct_runtime.py
	printf '{"schema":1,"updates":"package-manager","version":"%s"}\n' '$(VERSION)' > $(DESTDIR)$(ZC_SHARE)/distribution.json
	chmod 644 $(DESTDIR)$(ZC_SHARE)/distribution.json
	$(foreach i,0c-zhuyin 0c-english,install -Dm644 platforms/linux-fcitx5/icons/$(i).svg $(DESTDIR)$(DATAROOTDIR)/icons/hicolor/scalable/status/$(i).svg &&) true
ifeq ($(FCITX5),1)
	install -Dm644 $(SYSBUILD)/fcitx5/lib0c.so $(DESTDIR)$(FCITX5_ADDONDIR)/lib0c.so
	install -d $(DESTDIR)$(DATAROOTDIR)/fcitx5/addon
	sed 's|^Library=lib0c$$|Library=$(FCITX5_LIBRARY)|' platforms/linux-fcitx5/0c-addon.conf > $(DESTDIR)$(DATAROOTDIR)/fcitx5/addon/0c.conf
	chmod 644 $(DESTDIR)$(DATAROOTDIR)/fcitx5/addon/0c.conf
	install -Dm644 platforms/linux-fcitx5/0c-im.conf $(DESTDIR)$(DATAROOTDIR)/fcitx5/inputmethod/zuin.conf
endif
ifeq ($(IBUS),1)
	install -Dm755 $(SYSBUILD)/0c-ibus $(DESTDIR)$(ZC_LIBEXEC)/0c-ibus
	install -d $(DESTDIR)$(IBUS_COMPONENTDIR)
	sed -e 's|@LIBEXECDIR@|$(ZC_LIBEXEC)|g' -e 's|@DATAROOTDIR@|$(DATAROOTDIR)|g' platforms/linux-ibus/0c.xml.in > $(DESTDIR)$(IBUS_COMPONENTDIR)/0c.xml
	chmod 644 $(DESTDIR)$(IBUS_COMPONENTDIR)/0c.xml
endif
ifeq ($(SETTINGS),1)
	install -Dm755 $(SETTINGS_OUT) $(DESTDIR)$(BINDIR)/0c-settings
	install -Dm644 platforms/settings/icons/128x128.png $(DESTDIR)$(DATAROOTDIR)/icons/hicolor/128x128/apps/0c-settings.png
	install -d $(DESTDIR)$(DATAROOTDIR)/applications
	sed 's|@BINDIR@|$(BINDIR)|g' platforms/settings/0c-settings.desktop.in > $(DESTDIR)$(DATAROOTDIR)/applications/0c-settings.desktop
	chmod 644 $(DESTDIR)$(DATAROOTDIR)/applications/0c-settings.desktop
endif

uninstall:
	rm -f $(DESTDIR)$(ZC_LIBEXEC)/0c-server $(DESTDIR)$(ZC_LIBEXEC)/0c-update $(DESTDIR)$(ZC_LIBEXEC)/0c-ibus \
	      $(DESTDIR)$(BINDIR)/0c-mcp $(DESTDIR)$(BINDIR)/0c-settings $(DESTDIR)$(ZC_SHARE)/distribution.json \
	      $(DESTDIR)$(ZC_SHARE)/neural/sequence_runtime.py $(DESTDIR)$(ZC_SHARE)/neural/punct_runtime.py \
	      $(DESTDIR)$(FCITX5_ADDONDIR)/lib0c.so $(DESTDIR)$(DATAROOTDIR)/fcitx5/addon/0c.conf \
	      $(DESTDIR)$(DATAROOTDIR)/fcitx5/inputmethod/zuin.conf $(DESTDIR)$(IBUS_COMPONENTDIR)/0c.xml \
	      $(DESTDIR)$(DATAROOTDIR)/applications/0c-settings.desktop $(DESTDIR)$(DATAROOTDIR)/icons/hicolor/128x128/apps/0c-settings.png \
	      $(DESTDIR)$(DATAROOTDIR)/icons/hicolor/scalable/status/0c-zhuyin.svg $(DESTDIR)$(DATAROOTDIR)/icons/hicolor/scalable/status/0c-english.svg
	rm -rf $(DESTDIR)$(ZC_SHARE)/data
	-rmdir $(DESTDIR)$(ZC_SHARE)/neural $(DESTDIR)$(ZC_SHARE) $(DESTDIR)$(ZC_LIBEXEC) 2>/dev/null
	-rmdir $(DESTDIR)$(DATAROOTDIR)/fcitx5/inputmethod $(DESTDIR)$(DATAROOTDIR)/fcitx5/addon 2>/dev/null # 只刪空的：install 時建了，別的套件的檔案還在就留著
