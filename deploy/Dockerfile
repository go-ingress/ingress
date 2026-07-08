# Hermes 镜像（scratch + 预编译二进制，最小体积）
# 用法：先 GOOS=linux GOARCH=amd64 go build -o deploy/hermes ./cmd/hermes，再 docker build -f deploy/Dockerfile deploy/
FROM scratch
COPY hermes /hermes
ENTRYPOINT ["/hermes"]
