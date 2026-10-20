SHELL := /bin/bash
.ONESHELL:
.NOTPARALLEL:
.DEFAULT_GOAL := run

DEPLOY_DIR := $(HOME)/tmp/my-s3

.PHONY: run stage stop

stage:
	@set -euo pipefail
	deploy='$(DEPLOY_DIR)'
	mkdir -p "$$deploy"
	go build -o "$$deploy/s3.next" .
	rm -rf -- "$$deploy/web.next"
	cp -R web "$$deploy/web.next"

stop:
	@set -euo pipefail
	deploy='$(DEPLOY_DIR)'
	pids=()
	if [[ -f "$$deploy/server.pid" ]]; then pids+=("$$(cat "$$deploy/server.pid")"); fi
	if command -v fuser >/dev/null; then
		for pid in $$(fuser -n tcp 8080 2>/dev/null || true); do pids+=("$$pid"); done
	fi
	for pid in "$${pids[@]}"; do
		if [[ "$$pid" =~ ^[0-9]+$$ ]] && [[ "$$(readlink "/proc/$$pid/exe" 2>/dev/null || true)" == "$$deploy/s3" ]]; then
			kill -TERM "$$pid" 2>/dev/null || true
			for attempt in {1..50}; do
				if ! kill -0 "$$pid" 2>/dev/null; then break; fi
				sleep 0.1
			done
		fi
	done
	rm -f -- "$$deploy/server.pid"

# Rebuild and restart in the background while keeping the data directory.
run: stage stop
	@set -euo pipefail
	deploy='$(DEPLOY_DIR)'
	if (echo > /dev/tcp/127.0.0.1/8080) 2>/dev/null; then
		echo 'Port 8080 is in use by another process; deployment was not replaced.' >&2
		exit 1
	fi
	mv -f -- "$$deploy/s3.next" "$$deploy/s3"
	rm -rf -- "$$deploy/web"
	mv -- "$$deploy/web.next" "$$deploy/web"
	cd "$$deploy"
	nohup setsid ./s3 >server.log 2>&1 < /dev/null &
	echo $$! >server.pid
	for attempt in {1..50}; do
		if curl --fail --silent http://127.0.0.1:8080/_ui/ >/dev/null; then
			echo 'Ready: http://127.0.0.1:8080/_ui/'
			exit 0
		fi
		sleep 0.1
	done
	echo "Service did not start; see $$deploy/server.log" >&2
	exit 1
