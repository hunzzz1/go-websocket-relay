# Go WebSocket Relay Server

> 🇨🇳 中文说明在前，🇺🇸 English description follows below.

## 🇨🇳 中文说明

一个用 Go 编写的**极简 WebSocket 中继服务**，基于 Gorilla/WebSocket 实现。

主要用途：

- 接收客户端的 WebSocket 连接
- 按“用户标识 token”管理连接分组（同一用户可多端在线）
- 提供一个 HTTP 推送接口，将消息推到单个用户或全部在线用户
- 支持可选的延时推送
- 全部配置集中在 `config.json` + 环境变量，部署简单

### 功能特性

- WebSocket 长连接管理
- 按 token 分组（同一 token 多连接）
- HTTP 推送接口，可单用户推送 / 全站广播
- API Key 校验，防止未授权调用
- 支持 `delay_seconds` 延时发送
- `/health` 健康检查接口
- 支持自定义：
  - WebSocket 路径（`ws_path`）
  - 推送接口路径（`push_path`）



---

## 🧱 打包与构建 · Build & Packaging

### 本地构建（推荐）

```bash
go build -o relay main.go
```

会在当前目录生成一个名为 `relay` 的可执行文件，然后可以直接运行：

```bash
./relay
```

请确认：

- 如使用 `config.json`，它位于当前工作目录
- 或者通过环境变量覆盖配置（见下文“配置 / Configuration”）

### 交叉编译示例（在本机为 Linux 服务器打包）

构建 Linux amd64：

```bash
GOOS=linux GOARCH=amd64 go build -o relay-linux-amd64 main.go
```

构建 Linux arm64：

```bash
GOOS=linux GOARCH=arm64 go build -o relay-linux-arm64 main.go
```

在服务器上：

```bash
chmod +x relay-linux-amd64
./relay-linux-amd64
```

也可以在运行前设置环境变量覆盖默认配置：

```bash
export PORT=3000
export RELAY_API_KEY="your_secure_key"
export WS_PATH="/ws"
export PUSH_PATH="/api/push"
./relay-linux-amd64
```

---
### 配置（config.json + 环境变量）

服务启动时会执行以下流程：

1. 先读取环境变量作为默认值：
   - `PORT`         （默认：`"3000"`）
   - `RELAY_API_KEY`（默认：`"U2FsdGVkX18ucQzBA+ozhc3ySrVZ"`）
   - `WS_PATH`      （默认：`"/ws"`）
   - `PUSH_PATH`    （默认：`"/api/push"`）
2. 在当前工作目录查找 `config.json` 并尝试解析
3. 如果不存在或解析失败：
   - 使用默认配置
   - 自动生成一个 `config.json`
4. 确保最终配置中：`port`、`ws_path`、`api_key`、`push_path` 均不为空

`config.json` 示例：

```json
{
  "port": "3000",
  "api_key": "change_me_to_a_secure_key",
  "ws_path": "/ws",
  "push_path": "/api/push"
}
```

> 建议线上务必修改 `api_key` 为随机复杂值。

---

### 运行方式

直接运行：

```bash
go run main.go
```

或带环境变量：

```bash
PORT=3000 RELAY_API_KEY="your_api_key_here" WS_PATH="/ws" PUSH_PATH="/api/push" go run main.go
```

启动正常时日志类似：

```text
Go Relay server listening on http://localhost:3000
WebSocket path = /ws
Push API path = /api/push
```

---

### WebSocket 使用

#### 1. 连接

默认 WebSocket URL：

```text
ws://localhost:3000/ws
```

你可以在查询参数中携带 `token`，用于标识当前用户：

```text
ws://localhost:3000/ws?token=USER_123
```

#### 2. 通过消息 identify（可选）

也可以在连接建立后，手动发送一条 `identify` 事件：

```json
{
  "event": "identify",
  "data": {
    "token": "USER_123"
  }
}
```

服务端会将该连接归类到 `USER_123` 分组。  
支持一个 token 对应多个连接（例如：同一账号 Web + 移动端同时在线）。

---

### HTTP 推送接口

#### 接口路径

- 默认：`/api/push`
- 可通过：
  - `config.json` 中的 `push_path`
  - 或环境变量 `PUSH_PATH`
  修改为任意路径（例如 `/internal/push`）

#### 认证方式

所有推送请求都需要提供正确的 API Key，可从以下位置读取：

1. 请求头 `X-API-KEY`
2. 请求头 `API-KEY`
3. 查询参数 `?api_key=`

当 Key 缺失或错误时，会返回：

```json
{
  "code": -1,
  "msg": "invalid api key"
}
```

#### 请求体格式

```json
{
  "event_name": "eventName",
  "subject": { "any": "payload" },
  "delay_seconds": 0,
  "token": "USER_123"
}
```

字段含义：

- `event_name` *(必填)*：推送到 WebSocket 客户端的事件名（对应 `event` 字段）  
- `subject`    *(必填)*：任意结构的数据，在客户端 `data.subject` 中收到  
- `delay_seconds` *(选填)*：延迟多少秒后发送，小于等于 0 表示立即发送  
- `token`      *(选填)*：用于路由到指定用户；为空或无法解析则视为广播

`token` 转 userID 的规则（简化说明）：

- 数字类型 → 转成字符串（如 `123` → `"123"`）  
- 字符串 → 原样使用  
- 对象 → 优先找 `id` 或 `user_id` 字段  
- `null` 或以上都不满足 → 视为广播

#### 单用户推送示例

```bash
curl -X POST "http://localhost:3000/api/push"   -H "Content-Type: application/json"   -H "X-API-KEY: your_api_key_here"   -d '{
    "event_name": "userMessage",
    "subject": { "text": "hello" },
    "delay_seconds": 0,
    "token": "USER_123"
  }'
```

#### 全站广播示例

```bash
curl -X POST "http://localhost:3000/api/push"   -H "Content-Type: application/json"   -H "X-API-KEY: your_api_key_here"   -d '{
    "event_name": "broadcast",
    "subject": { "text": "hello everyone" },
    "delay_seconds": 0
  }'
```

---

### WebSocket 客户端收到的消息格式

客户端会收到如下结构：

```json
{
  "event": "eventName",
  "data": {
    "subject": { "any": "payload" },
    "ts": 1733890000000,
    "token": "USER_123"
  }
}
```

- `event`  ：对应 HTTP 请求中的 `event_name`  
- `subject`：原样透传 HTTP 请求中的 `subject`  
- `ts`     ：服务端发送时的时间戳（毫秒）  
- `token`  ：HTTP 请求中原始的 `token` 值（如果有）

---

### 健康检查接口

- 路径：`/health`  
- 方法：`GET`  

返回示例：

```json
{
  "status": "ok"
}
```

可用于服务探活、健康检查、监控集成等。

---

### 生产环境小建议

- 一定要修改默认 `api_key`，使用随机复杂值  
- 建议放在 Nginx / Caddy / 其它代理之后，并使用 TLS（`wss://`）  
- 如要支撑更高连接数，请适当调整：
  - `ulimit -n`（文件描述符上限）  
  - 内核 TCP 参数（`sysctl`）  
- 如需多实例横向扩展，可在此基础上增加：
  - Redis Pub/Sub 或其它消息队列
  - 上层负载均衡（例如：一个独立网关转发到多个 relay 实例）

---

## 🇺🇸 English Description

A minimal WebSocket relay service written in Go, using Gorilla/WebSocket.

It is designed as a small standalone component that:

- Accepts WebSocket connections from clients
- Groups connections by a logical user identifier (token)
- Exposes an HTTP API to push messages to a single user or broadcast to all
- Optionally delays message delivery by a given number of seconds
- Uses a simple `config.json` + environment variables for configuration

### Features

- WebSocket server with connection tracking
- Grouping by user token (multiple connections per token)
- HTTP push endpoint for single-user or broadcast messages
- API Key based authentication for push requests
- Optional delayed push via `delay_seconds`
- `/health` endpoint for health checks
- Customizable paths:
  - WebSocket path (`ws_path`)
  - Push API path (`push_path`)

---

### Configuration

On startup the server:

1. Reads environment variables as defaults:
   - `PORT`          (default: `"3000"`)
   - `RELAY_API_KEY` (default: `"U2FsdGVkX18ucQzBA+ozhc3ySrVZ"`)
   - `WS_PATH`       (default: `"/ws"`)
   - `PUSH_PATH`     (default: `"/api/push"`)
2. Tries to load `config.json` from the current working directory
3. If `config.json` does not exist or is invalid:
   - Uses default values
   - Writes a new `config.json`
4. Ensures `port`, `ws_path`, `api_key`, and `push_path` are not empty

Example `config.json`:

```json
{
  "port": "3000",
  "api_key": "change_me_to_a_secure_key",
  "ws_path": "/ws",
  "push_path": "/api/push"
}
```

> In production, you should change `api_key` to a strong random value.

---

### Running

Simple run:

```bash
go run main.go
```

With explicit environment variables:

```bash
PORT=3000 RELAY_API_KEY="your_api_key_here" WS_PATH="/ws" PUSH_PATH="/api/push" go run main.go
```

On success you should see logs similar to:

```text
Go Relay server listening on http://localhost:3000
WebSocket path = /ws
Push API path = /api/push
```

---

### WebSocket Usage

#### 1. Connect

Default WebSocket URL:

```text
ws://localhost:3000/ws
```

You can pass a token in the query string, which will be used as the user identifier:

```text
ws://localhost:3000/ws?token=USER_123
```

#### 2. Identify via message (optional)

Clients can also send an `identify` event after connecting:

```json
{
  "event": "identify",
  "data": {
    "token": "USER_123"
  }
}
```

The server then associates that connection with the token.  
Multiple connections can share the same token (e.g. multiple devices of the same user).

---

### Push API

#### Path

- Default: `/api/push`
- Can be customized via:
  - `push_path` in `config.json`
  - or `PUSH_PATH` environment variable

#### Authentication

The API Key is required and can be provided in any of these:

- `X-API-KEY` header
- `API-KEY` header
- `?api_key=` query parameter

If the key is missing or invalid, the server returns:

```json
{
  "code": -1,
  "msg": "invalid api key"
}
```

#### Request Body

```json
{
  "event_name": "eventName",
  "subject": { "any": "payload" },
  "delay_seconds": 0,
  "token": "USER_123"
}
```

Field meanings:

- `event_name` (string, required): Event name forwarded to WebSocket clients
- `subject` (any, required): Arbitrary payload delivered as `data.subject`
- `delay_seconds` (int, optional): Delay in seconds before sending (`<= 0` means send immediately)
- `token` (any, optional): Determines the target user; if missing or invalid → broadcast

The `token` is converted to a user ID using the following rules:

- Number → string (e.g. `123` → `"123"`)
- String → as-is
- Object → uses `id` or `user_id` field if present
- `null` or cannot be resolved → treated as broadcast

#### Example: push to a single user

```bash
curl -X POST "http://localhost:3000/api/push"   -H "Content-Type: application/json"   -H "X-API-KEY: your_api_key_here"   -d '{
    "event_name": "userMessage",
    "subject": { "text": "hello" },
    "delay_seconds": 0,
    "token": "USER_123"
  }'
```

#### Example: broadcast to all

```bash
curl -X POST "http://localhost:3000/api/push"   -H "Content-Type: application/json"   -H "X-API-KEY: your_api_key_here"   -d '{
    "event_name": "broadcast",
    "subject": { "text": "hello everyone" },
    "delay_seconds": 0
  }'
```

---

### Message Format to WebSocket Clients

Clients receive messages in the following structure:

```json
{
  "event": "eventName",
  "data": {
    "subject": { "any": "payload" },
    "ts": 1733890000000,
    "token": "USER_123"
  }
}
```

- `event`  : Same as `event_name` from the HTTP API
- `subject`: Original `subject` payload
- `ts`     : Server-side timestamp (milliseconds since epoch)
- `token`  : Original `token` value from the push request (if any)

---

### Health Check

A simple endpoint is provided:

- Path: `/health`
- Method: `GET`

Example response:

```json
{
  "status": "ok"
}
```

This can be used for monitoring and readiness checks.

---

### Production Notes

- Change the default `api_key` before going to production.
- Prefer running behind a reverse proxy (e.g. Nginx / Caddy) and use TLS (`wss://`).
- For higher connection counts, tune:
  - `ulimit -n` (file descriptor limits)
  - OS-level TCP settings (`sysctl`)
- For horizontal scaling, consider:
  - A message bus (e.g. Redis Pub/Sub) to fan out events
  - A separate gateway or load balancer in front of multiple relay instances.
