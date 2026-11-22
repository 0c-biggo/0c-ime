# qsel: the selection model without onnxruntime (README.md).
#
#   make test                      library + tests on a random small model (numpy)
#   make bench                     qsel_bench for this machine
#   make android NDK=<ndk root>    arm64 qsel_bench and libqsel.so for Android
#   make parity MODEL=<dir with model.onnx, model.onnx.data> PREPARE=<sequence-prepare-v2/fixtures.json>
#                                  convert, then compare with onnxruntime (needs onnx, onnxruntime)
BUILD ?= ../../../build/qsel
CC ?= cc
CFLAGS ?= -O3
override CFLAGS += -std=c11 -Wall -Wextra
LDLIBS = -lm -lpthread
PY ?= python3
SRC = qsel.c qsel.h

.PHONY: all test bench android parity clean
all: $(BUILD)/libqsel.so $(BUILD)/qsel_bench

$(BUILD)/libqsel.so: $(SRC)
	@mkdir -p $(BUILD)
	$(CC) $(CFLAGS) -fPIC -shared -o $@ qsel.c $(LDLIBS)

$(BUILD)/qsel_bench: qsel_bench.c $(SRC)
	@mkdir -p $(BUILD)
	$(CC) $(CFLAGS) -o $@ qsel_bench.c qsel.c $(LDLIBS)

test: $(BUILD)/libqsel.so
	$(PY) -I qsel_test.py --lib $(BUILD)/libqsel.so

bench: $(BUILD)/qsel_bench

NDK_CC = $(NDK)/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android29-clang
android: $(SRC) qsel_bench.c
	@test -n "$(NDK)" || { echo "set NDK"; exit 1; }
	@mkdir -p $(BUILD)/android-arm64
	$(NDK_CC) -O3 -std=c11 -Wall -Wextra -o $(BUILD)/android-arm64/qsel_bench qsel_bench.c qsel.c -lm
	$(NDK_CC) -O3 -std=c11 -Wall -Wextra -fPIC -shared -o $(BUILD)/android-arm64/libqsel.so qsel.c -lm

parity: $(BUILD)/libqsel.so
	@test -n "$(MODEL)" || { echo "set MODEL"; exit 1; }
	$(PY) -I qsel_convert.py --model $(MODEL)/model.onnx --data $(MODEL)/model.onnx.data --out $(BUILD)/model.qsel
	$(PY) -I qsel_parity.py --lib $(BUILD)/libqsel.so --qsel $(BUILD)/model.qsel --onnx $(MODEL)/model.onnx \
		$(if $(PREPARE),--prepare $(PREPARE)) --random 50

clean:
	rm -rf $(BUILD)
