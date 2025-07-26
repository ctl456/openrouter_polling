# OpenRouter API 代理与持久化管理核心

这是一个使用 Go 语言编写的高性能、数据库驱动的 OpenRouter API 代理服务。它不仅提供了与 OpenAI API 完全兼容的接口（包括流式响应和工具调用），还内置了强大的 API 密钥持久化管理、动态轮询、故障转移和功能丰富的 Web 管理仪表盘。

与依赖环境变量的临时性方案不同，本服务通过 **数据库（支持 SQLite 和 MySQL）** 对所有 OpenRouter 密钥进行持久化管理，确保服务的健壮性和可扩展性。

## 核心特性

*   ✨ **OpenAI API 完全兼容**：提供 `/v1/models` 和 `/v1/chat/completions` 端点，可直接替换 OpenAI SDK 的 `baseURL`，无缝集成现有应用。
*   🚀 **全功能支持**：
    *   **流式响应** (`stream: true`)：提供与原生 API 一致的低延迟体验。
    *   **工具调用 (Tool Calling)**：完全支持 OpenAI 的函数/工具调用功能。
*   🔑 **持久化密钥管理 (数据库驱动)**：
    *   **双数据库支持**：开箱即用地支持 **SQLite**（默认，零配置）和 **MySQL**，满足从个人项目到生产环境的不同需求。
    *   **Web UI 管理**：通过管理仪表盘动态添加（单个或批量）、删除（单个或批量）密钥，所有变更实时生效，无需重启服务。
    *   **环境变量植入**：支持在首次启动时从环境变量 `OPENROUTER_API_KEYS` 中自动“植入”初始密钥到数据库。
*   ⚖️ **智能轮询与故障转移**：
    *   **加权随机轮询**：可为每个密钥设置权重，优先使用高额度或高性能的密钥。
    *   **自动故障转移**：当某个密钥请求失败（如额度耗尽、无效），系统会立即切换到下一个可用密钥重试。
    *   **动态冷却系统**：失败的密钥会进入动态冷却期（失败次数越多，冷却时间越长），避免短期内对失效密钥的无效请求。
*   🩺 **定期健康检查**：后台任务会定期对非活动密钥进行健康检查，一旦密钥恢复可用，则自动重新激活，实现“自愈”。
*   🖥️ **多功能 Web 管理仪表盘**：
    *   **安全登录**：通过管理员密码保护，使用安全的会话管理。
    *   **密钥状态矩阵**：实时监控所有密钥的详细状态（激活、冷却、失败次数、上次使用/失败时间、权重等），支持 **分页浏览**。
    *   **批量操作**：使用复选框选择多个密钥进行一次性删除。
    *   **动态添加**：在文本框中粘贴一个或多个密钥（支持 `key:weight` 格式），一键添加。
    *   **参数配置**：新增独立的 **设置页面**，可在线修改部分服务参数（如默认模型、超时时间、日志级别等）并立即生效。
    *   **系统监控**：在“应用状态”页面查看服务的核心运行时指标。
*   ⚙️ **高度可配置**：所有关键参数均可通过环境变量或 `.env` 文件进行配置。
*   📝 **结构化日志**：使用 Logrus 提供详细、可配置级别的日志输出，方便问题排查。
*   🐳 **Docker 支持**: 提供 `Dockerfile`，并包含数据持久化部署的最佳实践。

## 技术栈

*   **Go**: 主要编程语言
*   **Gin**: 高性能 HTTP Web 框架
*   **GORM**: 强大的 ORM 框架，用于数据库交互 (SQLite, MySQL)
*   **Logrus**: 结构化日志库
*   **Gorilla Sessions**: 用于管理仪表盘的安全会话

## 快速开始

### 前提条件

*   Go 1.23 或更高版本 (用于本地编译)
*   Docker & Docker Compose (推荐用于容器化部署)

### 1. 本地编译运行

1.  **克隆仓库**:
    ```bash
    git clone https://github.com/your-username/openrouter_polling.git
    cd openrouter_polling
    ```

2.  **配置环境变量**:
    复制 `.env.example` 为 `.env` 文件，并根据“配置说明”章节填写必要配置。对于本地测试，默认的 SQLite 配置通常无需修改。
    ```bash
    cp .env.example .env
    ```
    **首次启动时**，你可以在 `.env` 文件中设置 `OPENROUTER_API_KEYS` 来植入初始密钥。

3.  **编译**:
    ```bash
    go build -o openrouter-polling .
    ```

4.  **运行服务**:
    ```bash
    ./openrouter-polling
    ```
    服务启动后，将会在项目根目录创建 `openrouter_keys.db` (SQLite 数据库文件)。

### 2. 使用 Docker 部署 (推荐)

使用 Docker 是最佳的部署方式，可以轻松实现数据持久化。

1.  **准备 `.env` 文件**:
    同样，复制 `.env.example` 为 `.env`，并根据你的需求修改。确保设置了强密码 `ADMIN_PASSWORD`。

2.  **构建并运行容器**:
    在项目根目录下运行以下命令：
    ```bash
    docker build -t openrouter-polling:latest .
    ```
    然后使用以下命令运行容器，**注意 `-v` 参数用于持久化 SQLite 数据库**：
    ```bash
    docker run -d \
      -p 8000:8000 \
      --name my-openrouter-polling \
      --restart always \
      -v $(pwd)/data:/app/data \
      --env-file ./.env \
      openrouter-polling:latest
    ```
    *   `-d`: 后台运行。
    *   `-p 8000:8000`: 将主机的 8000 端口映射到容器的 8000 端口。
    *   `--restart always`: 确保容器在退出时总是自动重启。
    *   `-v $(pwd)/data:/app/data`: **(关键)** 将主机当前目录下的 `data` 文件夹挂载到容器的 `/app/data` 目录。请修改 `.env` 文件中的 `DB_CONNECTION_STRING_SQLITE` 为 `data/openrouter_keys.db` 以将数据库文件保存在此持久化目录中。
    *   `--env-file ./.env`: 从 `.env` 文件加载所有环境变量，这是管理配置的最佳方式。

## 配置说明

通过环境变量或项目根目录的 `.env` 文件配置服务。Docker 部署时推荐使用 `--env-file`。

### 数据库配置

| 环境变量                      | 描述                                                                                                                            | 默认值                  |
| :---------------------------- | :------------------------------------------------------------------------------------------------------------------------------ | :---------------------- |
| `DB_TYPE`                     | **必需**。数据库类型，支持 `"sqlite"` 或 `"mysql"`。                                                                              | `"sqlite"`              |
| `DB_CONNECTION_STRING_SQLITE` | 当 `DB_TYPE="sqlite"` 时使用。数据库文件路径。推荐使用 `data/openrouter_keys.db` 以配合 Docker volume。                           | `"openrouter_keys.db"`  |
| `MYSQL_HOST`                  | 当 `DB_TYPE="mysql"` 时使用。MySQL 服务器地址。                                                                                   | `127.0.0.1`             |
| `MYSQL_PORT`                  | MySQL 端口。                                                                                                                    | `3306`                  |
| `MYSQL_DBNAME`                | MySQL 数据库名。                                                                                                                | `openrouter_proxy`      |
| `MYSQL_USER`                  | MySQL 用户名。                                                                                                                  | `root`                  |
| `MYSQL_PASSWORD`              | MySQL 密码。                                                                                                                    | 无                      |

### 核心配置

| 环境变量                    | 描述                                                                                                                            | 默认值/重要性                                                    |
| :-------------------------- | :------------------------------------------------------------------------------------------------------------------------------ | :--------------------------------------------------------------- |
| `ADMIN_PASSWORD`            | **必需**。管理仪表盘的登录密码。                                                                                                  | `"123456"` (**极不安全, 必须修改!**)                               |
| `OPENROUTER_API_KEYS`       | **仅用于首次启动植入**。逗号分隔的 OpenRouter 密钥列表 (`key1,key2:weight`)。服务启动后，密钥管理完全由数据库和仪表盘接管。        | 空                                                               |
| `APP_API_KEY`               | 可选。用于保护 `/v1/*` 代理接口。如果设置，客户端请求头需包含 `Authorization: Bearer <APP_API_KEY>`。                               | 空 (不启用保护)                                                  |
| `PORT`                      | 服务监听的端口号。                                                                                                                | `"8000"`                                                         |
| `GIN_MODE`                  | Gin 运行模式：`debug` 或 `release`。生产环境推荐 `release`。                                                                      | `"debug"`                                                        |
| `LOG_LEVEL`                 | 日志级别：`trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic`。                                                              | `"info"`                                                         |
| `DEFAULT_MODEL`             | 如果客户端请求中未指定模型，则使用的默认模型 ID。                                                                                   | `"deepseek/deepseek-chat-v3-0324:free"`                          |
| `REQUEST_TIMEOUT_SECONDS`   | 对 OpenRouter 发出请求的超时时间（秒）。                                                                                          | `180` (3 分钟)                                                   |
| `KEY_FAILURE_COOLDOWN_SECONDS` | API 密钥失败后的基础冷却时间（秒）。                                                                                              | `600` (10 分钟)                                                  |
| `KEY_MAX_CONSECUTIVE_FAILURES` | 密钥在被标记为非活动状态前的最大连续失败次数。                                                                                      | `3`                                                              |
| `RETRY_WITH_NEW_KEY_COUNT`  | 当一个密钥失败时，尝试使用池中其他密钥的次数。                                                                                      | `4`                                                              |
| `HEALTH_CHECK_INTERVAL_SECONDS` | 对非活动密钥进行健康检查的间隔时间（秒）。                                                                                        | `300` (5 分钟)                                                   |

## API 端点

### 代理接口 (受 `APP_API_KEY` 保护)

*   **GET `/v1/models`**: 获取 OpenAI 格式的模型列表。
*   **POST `/v1/chat/completions`**: 处理聊天请求，支持流式、非流式和工具调用。

### 管理接口 (受会话 Cookie 保护)

*   **GET `/admin/login`**: 显示管理员登录页面。
*   **POST `/admin/login`**: 处理管理员登录。
*   **GET `/admin/dashboard`**: 显示管理仪表盘主页面。
*   **POST `/admin/logout`**: 管理员登出。
*   **GET `/admin/key-status`**: 获取密钥状态列表（支持分页 `?page=1&limit=10`）。
*   **POST `/admin/add-keys`**: 批量添加新密钥。
*   **DELETE `/admin/delete-key/:suffix`**: 删除单个密钥。
*   **POST `/admin/delete-keys-batch`**: 批量删除选中的密钥。
*   **GET `/admin/app-status`**: 获取应用运行时状态。
*   **GET `/admin/settings-page`**: 显示动态配置页面。
*   **GET `/admin/settings`**: 获取当前可热重载的配置。
*   **POST `/admin/settings`**: 更新并热重载配置。

## 管理仪表盘

通过浏览器访问 `http://<你的服务器地址>:<端口>/admin/login` (例如 `http://localhost:8000/admin/login`)。

**功能亮点**:
*   **密钥矩阵**: 在一个清晰的表格中查看所有密钥的实时状态，支持分页。
*   **批量操作**: 使用复选框选择多个密钥进行一次性删除。
*   **动态添加**: 在文本框中粘贴一个或多个密钥（支持 `key:weight` 格式），一键添加。
*   **参数配置**: 访问独立的“设置”页面，动态调整日志级别、默认模型、超时等参数，无需重启服务。
*   **系统监控**: 在“应用状态”页面查看服务的核心运行时指标。

## 安全注意事项

*   🔒 **管理员密码**: **必须**修改 `ADMIN_PASSWORD` 为一个强密码。
*   🛡️ **保护代理接口**: 如果服务暴露于公网，**强烈建议**配置 `APP_API_KEY`。
*   🌐 **数据库安全**: 如果使用 MySQL，请确保数据库连接信息的安全。如果使用 SQLite，请确保数据库文件 (`.db`) 不会通过任何Web服务被意外暴露。
*   🔑 **HTTPS**: 在生产环境中，强烈建议将此服务部署在 Nginx, Caddy 等反向代理之后，并启用 HTTPS。

## 许可证

This repository is licensed under the [MIT License](LICENSE).