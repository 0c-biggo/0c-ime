.PHONY: help upgrade test-upgrade test-release test-source-release test-sim
.DEFAULT_GOAL := help

PULL ?= 1

help:
	@echo 'make upgrade          更新已安裝的 Linux Fcitx5 輸入法（含 git pull）'
	@echo 'make upgrade PULL=0   使用目前工作目錄，略過 git pull'
	@echo 'make test-release     驗證套件檢查與隔離安裝／回復流程'
	@echo 'make test-source-release  驗證 GitHub 原始碼與模型 tarball 流程'
	@echo 'python3 scripts/source_release.py --help  建立含模型的原始碼發佈檔'
	@echo 'python3 scripts/package_release.py --help  建立完整 Linux 套件'
	@echo 'python3 scripts/package_windows.py --help  建立 Windows x64 套件（在 Windows 執行）'
	@echo 'python3 scripts/package_windows_installer.py --help  將 Windows ZIP 製成 EXE 安裝精靈'
	@echo 'go run ./cmd/0c-update status  檢查套件版本'

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
