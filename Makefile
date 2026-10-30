SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.ONESHELL:
.NOTPARALLEL:
.DEFAULT_GOAL := build

DEPLOY_DIR ?= $(HOME)/tmp/my-s3

.PHONY: help build run stop clean

help:
	@printf '%s\n' \
		'make build  构建程序并复制页面到部署目录' \
		'make run    后台运行；需先 build，已运行时先停止再启动' \
		'make stop   停止服务' \
		'make clean  停止服务并删除整个部署目录，包括 data'

build:
	mkdir -p "$(DEPLOY_DIR)"
	go build -o "$(DEPLOY_DIR)/s3.next" .
	mv -f "$(DEPLOY_DIR)/s3.next" "$(DEPLOY_DIR)/s3"
	rm -rf -- "$(DEPLOY_DIR)/web"
	cp -R web "$(DEPLOY_DIR)/web"

run: stop
	cd "$(DEPLOY_DIR)"
	test -x ./s3
	nohup ./s3 >server.log 2>&1 < /dev/null &
	echo $$! >server.pid
	echo "Started: http://127.0.0.1:8080/_ui/ (log: $(DEPLOY_DIR)/server.log)"

stop:
	@if [[ -f "$(DEPLOY_DIR)/server.pid" ]]; then
		pid=$$(cat "$(DEPLOY_DIR)/server.pid")
		executable=$$(readlink "/proc/$$pid/exe" 2>/dev/null || true)
		if [[ "$$executable" == "$(DEPLOY_DIR)/s3" || "$$executable" == "$(DEPLOY_DIR)/s3 (deleted)" ]]; then
			kill -TERM "$$pid" 2>/dev/null || true
			for attempt in {1..50}; do
				if [[ ! -e "/proc/$$pid/exe" ]]; then break; fi
				sleep 0.1
			done
			if [[ -e "/proc/$$pid/exe" ]]; then
				echo 'Service has not stopped yet.' >&2
				exit 1
			fi
		fi
		rm -f -- "$(DEPLOY_DIR)/server.pid"
	fi

clean: stop
	rm -rf -- "$(DEPLOY_DIR)"
