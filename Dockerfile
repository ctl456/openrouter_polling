# Dockerfile

# ---- Build Stage ----
# 使用官方的 Go 镜像作为构建环境
# 选择一个与你本地开发环境匹配或兼容的 Go 版本
FROM golang:1.23-alpine3.21 AS builder

# 设置工作目录
WORKDIR /app

# 预先复制 go.mod 和 go.sum 文件，并下载依赖
# 这样可以利用 Docker 的层缓存，只有在这些文件变化时才重新下载依赖
COPY go.mod go.sum ./

# 国内环境换源
# RUN go env -w GO111MODULE=on
# RUN go env -w GOPROXY=https://goproxy.cn,direct

RUN go mod download && go mod verify

# 复制项目源代码到工作目录
COPY . .

# 构建 Go 应用
# -ldflags="-w -s" 用于减小二进制文件体积（移除调试信息和符号表）
# CGO_ENABLED=0 确保静态链接，避免依赖外部 C 库（对于 Alpine 尤其重要）
# GOOS=linux GOARCH=amd64 指定目标操作系统和架构（如果你的构建环境不同于目标环境）
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /app/openrouter-proxy .

# ---- Release Stage ----
# 使用一个轻量级的基础镜像（如 Alpine Linux）来运行应用
FROM alpine:latest

# 设置工作目录
WORKDIR /app

# 从构建阶段复制编译好的二进制文件
COPY --from=builder /app/openrouter-proxy /app/openrouter-proxy

# 【重要】复制 static 目录下的 HTML 文件到镜像中
# 确保你的 HTML 模板 (dashboard.html, login.html) 在这个路径下
COPY static ./static

# （可选）如果你有一个 .env 文件模板，并且希望在容器启动时通过某种方式填充它，
# 或者如果你的应用可以直接读取 .env 文件（不推荐在生产镜像中直接打包 .env），
# 你可能需要考虑其他配置管理策略（如 Docker Secrets, ConfigMaps, 或通过环境变量注入）。
# 此处我们假设配置将主要通过环境变量提供。

# 暴露应用监听的端口 (与配置中的 PORT 对应)
# Dockerfile 中的 EXPOSE 仅为文档作用，实际端口映射在 docker run 时指定
EXPOSE 8000

# 设置容器启动时执行的命令
# CMD ["/app/openrouter-proxy"]
# 或者，如果你希望能够更容易地传递命令行参数（虽然此应用目前不使用）
ENTRYPOINT ["/app/openrouter-proxy"]
