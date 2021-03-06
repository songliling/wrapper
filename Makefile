PROJ_HOME := $(dir $(realpath $(lastword $(MAKEFILE_LIST))))
OUT_DIR ?= sample
SECTOR_SIZE ?= 2K
INTERVAL ?= 5

check-tools:
	@bash $(PROJ_HOME)scripts/check_toolchain.sh
.PHONY: check-tools

install-deps:
	@bash $(PROJ_HOME)scripts/install_ffi.sh $(PROJ_HOME)
.PHONY: install-deps

all: check-tools install-deps
	go build -o $(PROJ_HOME)bin/bench ./bench
.PHONY: all

fetch-params:
	@bash $(PROJ_HOME)scripts/fetch_params.sh $(PROJ_HOME) $(SECTOR_SIZE)
.PHONY: fetch-params

bench: fetch-params
	bash $(PROJ_HOME)scripts/run_bench.sh $(PROJ_HOME) $(OUT_DIR) $(SECTOR_SIZE) $(INTERVAL)
.PHONY: bench

report:
	go run report/main.go $(OUT_DIR) $(INTERVAL)s
.PHONY: report
