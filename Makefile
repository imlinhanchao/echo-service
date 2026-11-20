.PHONY: run build fmt vet test test-mysql e2e resume install-service docker-up docker-down clean

APP := echo-service
TOOLS := resume-audiobook split-audiobook prune-full-audio

run:
	go run ./cmd/$(APP)

build:
	mkdir -p bin
	go build -o bin/$(APP) ./cmd/$(APP)
	@for tool in $(TOOLS); do go build -o bin/$$tool ./cmd/$$tool; done

fmt:
	gofmt -w ./cmd ./internal

vet:
	go vet ./...

test:
	go test ./...

# MySQL 集成测试：优先使用当前环境变量，其次从 .env 读取（测试库 echo_service_test）
test-mysql:
	@test -f .env || (echo "缺少 .env，请先执行 cp .env.example .env" && exit 1)
	ECHO_TEST_MYSQL=1 \
	ECHO_MYSQL_HOST="$${ECHO_MYSQL_HOST:-$$(sed -n 's/^ECHO_MYSQL_HOST=//p' .env)}" \
	ECHO_MYSQL_PORT="$${ECHO_MYSQL_PORT:-$$(sed -n 's/^ECHO_MYSQL_PORT=//p' .env)}" \
	ECHO_MYSQL_USER="$${ECHO_MYSQL_USER:-$$(sed -n 's/^ECHO_MYSQL_USER=//p' .env)}" \
	ECHO_MYSQL_PASSWORD="$${ECHO_MYSQL_PASSWORD:-$$(sed -n 's/^ECHO_MYSQL_PASSWORD=//p' .env)}" \
	ECHO_TEST_MYSQL_DB="$${ECHO_TEST_MYSQL_DB:-echo_service_test}" \
	go test ./internal/store -v

# 端到端验收：需要已启动的服务、zlib 会话和 TTS 服务，见 scripts/e2e.sh 头部说明
e2e:
	bash scripts/e2e.sh

resume:
	@test -n "$(JOB)" || (echo "用法: make resume JOB=<任务ID>" && exit 1)
	./bin/resume-audiobook -job $(JOB)

# 安装为 systemd 用户服务（开机自启，崩溃自动拉起）
install-service:
	bash scripts/install-service.sh

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

clean:
	rm -rf bin .tmp data/users
