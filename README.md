# OpenRouter API 轮询与密钥管理服务（个人）

这是一个使用 Go 语言编写的高性能 OpenRouter API 轮询服务。它提供了与 OpenAI API 兼容的接口，并内置了强大的 API 密钥管理、轮询、故障转移和 Web 管理仪表盘功能。该服务旨在帮助开发者更稳定、高效、经济地使用 OpenRouter 提供的多种大型语言模型。

## 核心特性

*   ✨ **OpenAI API 兼容性**：提供 `/v1/models` 和 `/v1/chat/completions` 端点，可直接替换 OpenAI SDK 的 `baseURL`，无缝集成现有应用。
*   🔑 **多密钥管理与轮询**：支持配置和管理多个 OpenRouter API 密钥，并进行轮询使用，有效分摊请求压力。
*   ⚖️ **权重密钥选择**：可以为每个 OpenRouter API 密钥设置权重，实现加权随机选择，优先使用高额度或高性能的密钥。
*   🔄 **自动故障转移与冷却**：当某个密钥请求失败（如额度耗尽、无效密钥、服务暂时不可用），系统会自动切换到下一个可用密钥，并对失败密钥进行动态冷却，避免短期内再次使用。
*   🩺 **定期健康检查**：后台任务会定期对处于冷却或失败状态的密钥进行健康检查，一旦密钥恢复可用，则自动重新激活。
*   💨 **流式响应支持**：完全支持 `/v1/chat/completions` 端点的流式响应 (`stream: true`)，提供与原生 API 一致的体验。
*   🛡️ **安全管理**：
    *   可选的服务 API 密钥 (`APP_API_KEY`) 用于保护代理服务自身的 `/v1` 接口。
    *   基于会话的安全管理仪表盘，通过管理员密码 (`ADMIN_PASSWORD`) 和会话密钥 (`SESSION_SECRET_KEY`) 进行保护。
*   🖥️ **Web 管理仪表盘**：
    *   **安全登录**：提供独立的登录页面进行身份验证。
    *   **密钥状态监控**：实时查看所有 OpenRouter 密钥的当前状态（激活、冷却中、失败次数、上次使用时间等）。
    *   **动态密钥管理**：通过仪表盘动态添加新的 OpenRouter 密钥（支持带权重格式）或删除现有密钥，无需重启服务。
*   ⚙️ **灵活配置**：所有关键参数均可通过环境变量或 `.env` 文件进行配置。
*   📝 **详细日志**：使用 Logrus 提供结构化、可配置级别的日志输出，方便问题排查和监控。
*   🚀 **高性能**：基于 Gin Web 框架构建，并针对并发和性能进行了优化。
*   🐳 **Docker 支持**: 提供 `Dockerfile` 示例，方便容器化部署。

## 技术栈

*   **Go**: 主要编程语言
*   **Gin**: 高性能 HTTP Web 框架
*   **Logrus**: 结构化日志库
*   **Gorilla Sessions**: 用于管理仪表盘的会话
*   **godotenv**: 用于从 `.env` 文件加载环境变量

## 快速开始

### 前提条件

*   Go 1.23 或更高版本 (用于本地编译)
*   Docker  (用于容器化部署)

### 安装与运行 (本地编译)

1.  **克隆仓库**:
    ```bash
    git clone https://github.com/ctl456/openrouter_polling.git
    cd openrouter_polling
    ```

2.  **配置环境变量**:
    复制 `.env.example` (如果提供) 为 `.env` 文件，或者直接创建 `.env` 文件，并根据下面的 "配置说明" 章节填写必要的配置。

3.  **编译**:
    ```bash
    go build -o openrouter-polling .
    ```
    这会在项目根目录下生成一个名为 `openrouter-polling` 的可执行文件。

4.  **运行服务**:
    ```bash
    ./openrouter-polling
    ```
    服务启动后，你会看到日志输出，包括监听的端口和配置信息。

### 使用 Docker 部署

本项目提供了 `Dockerfile` 用于容器化部署。

**1. 直接使用 Dockerfile 构建和运行（推荐）**

a. **构建 Docker 镜像**:
在项目根目录下运行：
```bash
docker build -t openrouter-polling:latest .
```
(你可以自定义镜像标签，例如 `yourname/openrouter-polling:v1.0`)

b. **运行 Docker 容器**:
```bash
docker run -d \
  -p 8000:8000 \
  --name my-openrouter-proxy \
  -e OPENROUTER_API_KEYS="sk-or-v1key1...,sk-or-v1key2...:5" \
  -e ADMIN_PASSWORD="your_strong_admin_password" \
  -e SESSION_SECRET_KEY="your_super_secret_random_string" \
  -e APP_API_KEY="sk-xxxx" \
  -e GIN_MODE="release" \
  -e LOG_LEVEL="info" \
  -e PORT="8000" \
  -e DEFAULT_MODEL="deepseek/deepseek-chat-v3-0324:free" \
  # 添加其他必要的环境变量
  openrouter-polling:latest
```
*   `-d`: 后台运行。
*   `-p 8000:8000`: 将主机的 8000 端口映射到容器的 8000 端口。
*   `--name`: 给容器命名。
*   `-e`: 设置环境变量。**请务必替换示例值为你的真实配置。**

## 配置说明

通过环境变量配置服务。如果使用本地编译运行方式，可以将这些变量写入项目根目录下的 `.env` 文件中，应用会自动加载。对于 Docker 部署，请通过 `docker run -e` 或 `docker-compose.yml` 的 `environment` 部分传递环境变量。

| 环境变量                    | 描述                                                                                               | 默认值/重要性                                                    |
| :-------------------------- | :------------------------------------------------------------------------------------------------- | :--------------------------------------------------------------- |
| `OPENROUTER_API_KEYS`     | **必需**。OpenRouter API 密钥列表，逗号分隔。格式：`key1,key2:weight,key3`。权重为可选整数，默认为1。 | 无默认值                                                         |
| `ADMIN_PASSWORD`          | **必需**。管理仪表盘的登录密码。                                                                         | `"admin"` (强烈建议修改!)                                        |
| `SESSION_SECRET_KEY`      | **必需**。用于会话 Cookie 签名和加密的密钥。必须是一个长且随机的字符串，以保证会话安全。                      | `"a-very-secret-and-random-key-replace-this-in-production"` (必须修改!) |
| `APP_API_KEY`             | 可选。如果设置，则所有对 `/v1/*` 接口的请求都需要在 `Authorization` 头部提供 `Bearer <APP_API_KEY>`。       | 空 (不启用保护)                                                  |
| `PORT`                      | 服务监听的端口号 (在容器内部)。对于 Docker，通常固定为 `8000`，通过端口映射暴露。                             | `"8000"`                                                         |
| `LOG_LEVEL`                 | 日志级别：`trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic`。                                | `"info"`                                                         |
| `GIN_MODE`                  | Gin 框架运行模式：`debug` 或 `release`。生产环境推荐 `release`。                                         | `"debug"`                                                        |
| `DEFAULT_MODEL`             | 如果客户端请求中未指定模型，则使用的默认模型 ID。                                                              | `"deepseek/deepseek-chat-v3-0324:free"`                                       |
| `REQUEST_TIMEOUT_SECONDS`   | 对 OpenRouter 发出请求的超时时间（秒）。                                                                 | `180` (3 分钟)                                                   |
| `KEY_FAILURE_COOLDOWN_SECONDS` | API 密钥失败后的基础冷却时间（秒）。实际冷却时间可能会根据连续失败次数动态增加。                                       | `600` (10 分钟)                                                  |
| `KEY_MAX_CONSECUTIVE_FAILURES` | API 密钥在被标记为非活动状态前的最大连续失败次数。                                                              | `3`                                                              |
| `RETRY_WITH_NEW_KEY_COUNT`  | 当一个密钥失败时，尝试使用池中其他密钥的次数。                                                                  | `3`                                                              |
| `HEALTH_CHECK_INTERVAL_SECONDS` | 对非活动密钥进行健康检查的间隔时间（秒）。                                                                   | `300` (5 分钟)                                                   |
| `HTTP_REFERER`              | (可选) 发往 OpenRouter 请求时携带的 `HTTP-Referer` 头部内容。                                                 | `"https://your-app-name.com"`                                  |
| `X_TITLE`                   | (可选) 发往 OpenRouter 请求时携带的 `X-Title` 头部内容。                                                    | `"Your App Name"`                                                |
| `OPENROUTER_API_URL`        | OpenRouter 聊天 API 的目标 URL。                                                                           | `"https://openrouter.ai/api/v1/chat/completions"`              |
| `OPENROUTER_MODELS_URL`     | OpenRouter 模型列表 API 的目标 URL。                                                                       | `"https://openrouter.ai/api/v1/models"`                        |

## API 端点

### 代理接口 (受 `APP_API_KEY` 保护，如果已配置)

*   **GET `/v1/models`**: 获取 OpenAI 格式的模型列表。服务会从 OpenRouter 获取模型列表并转换为兼容格式。
*   **POST `/v1/chat/completions`**: 处理聊天请求，支持流式 (`stream: true`) 和非流式响应。请求体和响应体与 OpenAI API 规范兼容。

### 管理接口

*   **GET `/admin/login`**: 显示管理员登录页面。
*   **POST `/admin/login`**: 处理管理员登录请求。
    *   请求体: `{"password": "YOUR_ADMIN_PASSWORD"}`
*   **GET `/admin/dashboard`**: (需要登录) 显示管理仪表盘页面。
*   **POST `/admin/logout`**: (需要登录) 管理员登出。
*   **GET `/admin/key-status`**: (需要登录) 获取所有 OpenRouter API 密钥的当前状态。
*   **POST `/admin/add-key`**: (需要登录) 添加一个新的 OpenRouter API 密钥。
    *   请求体: `{"openrouter_api_key": "sk-or-v1yourkeyhere"}` 或 `{"openrouter_api_key": "sk-or-v1yourkeyhere:5"}` (带权重)
*   **DELETE `/admin/delete-key/:suffix`**: (需要登录) 根据密钥的后缀删除一个 OpenRouter API 密钥。
    *   例如: `DELETE /admin/delete-key/...xyz` (其中 `...xyz` 是密钥的末尾4位，由 `utils.SafeSuffix` 生成)
*   **GET `/admin/app-status`**: (需要登录) 获取应用程序的运行时状态和配置信息。
*   **POST `/admin/reload-keys`**: (需要登录) 使用提供的密钥字符串批量重新加载所有密钥 (会覆盖现有密钥)。
    *   请求体: `{"openrouter_api_keys_str": "keyA,keyB:2,..."}`

## 管理仪表盘

通过浏览器访问 `http://<你的服务器地址>:<映射的主机端口>/admin/login` (例如 `http://localhost:8000/admin/login` 或你在 Docker 映射的其他端口)。
使用你在环境变量中配置的 `ADMIN_PASSWORD` 登录。

登录后，你将能够：

*   查看所有已配置的 OpenRouter API 密钥及其详细状态：
    *   密钥后缀 (用于识别)
    *   是否激活
    *   连续失败次数
    *   上次失败时间
    *   冷却截止时间
    *   上次使用时间
    *   权重
*   动态添加新的 OpenRouter API 密钥（支持 `key:weight` 格式）。
*   根据密钥后缀动态移除不再需要的密钥。
*   手动刷新密钥状态列表。
*   安全登出。

## 安全注意事项

*   🔒 **强烈建议修改默认密码和密钥**：
    *   **`ADMIN_PASSWORD`**: 默认的 `"admin"` 密码极不安全，请务必在首次部署时修改为一个强密码。
    *   **`SESSION_SECRET_KEY`**: 默认的会话密钥仅用于演示，生产环境必须替换为一个长且随机的字符串，以保证会话Cookie的安全性。
*   🛡️ **保护代理接口**: 如果你的服务将暴露在公网上，强烈建议配置 `APP_API_KEY`，并要求所有调用 `/v1/*` 接口的客户端在 `Authorization` 头部提供此密钥 (格式: `Bearer <APP_API_KEY>`)。
*   🌐 **HTTPS**: 在生产环境中，强烈建议将此服务部署在 HTTPS 反向代理（如 Nginx, Caddy, Traefik）之后。如果直接暴露服务并使用 HTTPS，请确保在 `main.go` 中正确配置会话 Cookie 的 `Secure` 标志为 `true` (对于Docker部署，如果反向代理处理SSL终止，则可能不需要在应用层面设置Secure)。
*   🔑 **API 密钥安全**:
    *   对于本地编译运行，确保包含 `OPENROUTER_API_KEYS` 的 `.env` 文件安全，不要提交到公共代码仓库。
    *   对于 Docker 部署，通过环境变量传递敏感信息，并确保管理好这些环境变量的来源 (例如，Docker Compose 的 `.env` 文件要加入 `.gitignore`，或者使用 CI/CD 系统的 secrets 功能)。**不要将密钥硬编码到 Dockerfile 或 docker-compose.yml 文件中并提交。**

## 并发与性能

*   **并发安全**: 项目中的 `ApiKeyManager` 使用 `sync.Mutex` 来保护对密钥状态列表的并发访问，确保了在多 goroutine 环境下的数据一致性和线程安全。
*   **HTTP 客户端**: 全局共享一个配置了连接池和合理超时的 `http.Client` 实例，以提高向上游 OpenRouter 发出请求的效率和性能。
*   **Gin 框架**: Gin 本身是一个为高性能设计的 Web 框架。在 `release` 模式下运行时，它会提供最佳性能。
*   **流式处理**: 对聊天完成的流式响应进行了优化，使用带缓冲的读取器 (`bufio.Reader`) 并及时刷新 (`http.Flusher`) 输出，以确保低延迟。
*   **健康检查**: 健康检查在独立的 goroutine 中异步执行，不会阻塞主请求处理流程。

## 后期维护与扩展

*   **模块化设计**: 代码被组织到不同的包中，使得各个功能模块职责清晰，易于理解、修改和扩展。
*   **配置驱动**: 大部分行为可以通过环境变量进行配置，方便在不同环境（开发、测试、生产）中部署和调整。
*   **注释与文档**: 关键代码段均包含中文注释，此 README 文件也提供了全面的项目说明。
*   **错误处理**: 尝试提供统一和信息丰富的错误响应，便于客户端调试。

## 贡献

欢迎提交 Pull Request 或 Issue 来改进此项目！

## 许可证

This repository is licensed under the [MIT License](https://github.com/ctl456/openrouter_polling/blob/main/LICENSE)

