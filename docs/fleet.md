# Fleet 使用文档

本文档描述当前仓库里的 `fleetd`。

## 1. 当前边界

`fleetd` 现在直接提供：

- 节点 WebSocket 接入
- 待认领设备列表
- 设备认领与归属存储
- Fleet Web 页面
- Fleet runtime HTTP API
- `fleet` CLI 对应的后端接口

当前最小链路：

```text
openclaw node ---> fleetd ---> fleet Web / fleet CLI
```

最小闭环里，服务端配置统一使用 `FLEETD_*`，CLI 配置统一使用 `FLEET_*`，节点鉴权状态和归属关系都存放在 `fleetd` 自己的 SQLite。

## 2. 本文哪些示例已经验证

以下链路已在 `2026-04-05` 实机验证：

- `go test ./...`
- `go run ./cmd/fleetd`
- `openclaw --dev node run --host 127.0.0.1 --port 18090 --display-name "Doc Check Node"`
- `GET /healthz`
- 打开 `/fleet/claims` 并看到待认领设备
- `POST /fleet/claims/{pairingID}/approve`
- `go run ./cmd/fleet list`
- `go run ./cmd/fleet describe <node-id>`
- `GET /runtime/fleet/nodes`

另外确认过当前本机 `openclaw` 2026.3.8 的命令行语法：

- `openclaw gateway run`
- `openclaw node run`
- `openclaw node install`
- `openclaw devices approve`
- `openclaw nodes describe`
- `openclaw nodes invoke`
- `openclaw approvals get/set`

有两点需要特别说明：

- `fleet run` 语法是正确的，但真实 OpenClaw 节点默认可能返回 `approval required`；这取决于节点主机的 exec approvals 配置，不是 `fleetd` 路由错误。
- `system.which` 的参数要求会随节点实现变化；本文不再把它当作“复制即成功”的示例。

## 3. 端点总览

`fleetd` 默认监听 `127.0.0.1:8090`，主要端点如下：

- `GET /healthz`: 健康检查
- `GET /`: 浏览器访问时跳到 `/fleet`；WebSocket 升级时作为节点接入口
- `GET /fleet/claims`: 待认领设备页面
- `GET /fleet/nodes`: 当前用户的节点列表页
- `GET /runtime/fleet/nodes`: runtime API 列表接口

节点接入时，`openclaw node run --host/--port` 直接连根路径。

## 4. 配置与优先级

### 4.1 服务端 `fleetd`

服务端配置优先级：

1. 内建默认值
2. `~/.fleet/.env`
3. 当前工作目录 `.env`
4. 进程环境变量

最常用变量：

```bash
FLEETD_LISTEN_ADDR=:8090
FLEETD_BASE_URL=http://127.0.0.1:8090
FLEETD_STORE_DSN=file:fleetd.db?_pragma=busy_timeout(5000)
FLEETD_MASTER_KEY=change-me

FLEETD_JWT_RS256_PUBLIC_KEY=
FLEETD_JWT_USER_ID_FIELD=sub

FLEETD_RUNTIME_AUTH_MODE=api_key
FLEETD_API_KEY=change-me

FLEETD_GATEWAY_TOKEN=change-me
FLEETD_GATEWAY_PASSWORD=
FLEETD_TICK_INTERVAL_MS=15000
FLEETD_REQUEST_TIMEOUT_MS=30000
```

字段说明：

- `FLEETD_JWT_RS256_PUBLIC_KEY`: 配了就启用 Web 页面 RS256 JWT 登录；留空就不登录，页面用户视为 `anonymous`
- `FLEETD_JWT_USER_ID_FIELD`: Web 页面登录后，从 JWT payload 的哪个顶层字段读取用户 ID，默认 `sub`
- `FLEETD_RUNTIME_AUTH_MODE`: runtime API 鉴权方式，支持 `disabled`、`api_key`
- `FLEETD_API_KEY`: `api_key` 模式下必须匹配的请求头值
- `FLEETD_GATEWAY_TOKEN` / `FLEETD_GATEWAY_PASSWORD`: 节点首次接入时使用的 bootstrap 凭证，二选一
- `FLEETD_MASTER_KEY`: 设备 token 和其他签名相关状态的服务端密钥，必须长期稳定

### 4.2 CLI `fleet`

CLI 配置读取顺序：

1. `~/.fleet/.env`
2. 当前工作目录 `.env`
3. 进程环境变量

说明：

- `FLEET_BASE_URL` 和 `FLEET_API_KEY` 是 CLI 使用的变量名

最小配置：

```bash
FLEET_BASE_URL=http://127.0.0.1:8090
FLEET_API_KEY=change-me
USER_ID=demo-user
```

## 5. 从零跑通

### 5.1 启动服务端

复制模板并改最小配置：

```bash
cp .env.server.example .env
```

本机最小可跑通示例：

```bash
cat > .env <<'EOF'
FLEETD_LISTEN_ADDR=:8090
FLEETD_BASE_URL=http://127.0.0.1:8090
FLEETD_STORE_DSN=file:fleetd.db?_pragma=busy_timeout(5000)
FLEETD_MASTER_KEY=replace-with-a-long-random-string

FLEETD_JWT_RS256_PUBLIC_KEY=
FLEETD_JWT_USER_ID_FIELD=sub
FLEETD_RUNTIME_AUTH_MODE=api_key
FLEETD_API_KEY=replace-me

FLEETD_GATEWAY_TOKEN=replace-with-a-node-bootstrap-token
FLEETD_GATEWAY_PASSWORD=
EOF
go run ./cmd/fleetd
```

健康检查：

```bash
curl http://127.0.0.1:8090/healthz
```

期望返回：

```json
{
  "status": "ok"
}
```

### 5.2 让节点接入 `fleetd`

前台运行节点：

```bash
export OPENCLAW_GATEWAY_TOKEN='replace-with-a-node-bootstrap-token'
openclaw node run --host 127.0.0.1 --port 8090 --display-name "Build Node"
```

如果你想隔离本机 OpenClaw 状态，推荐在验证时使用：

```bash
export OPENCLAW_GATEWAY_TOKEN='replace-with-a-node-bootstrap-token'
openclaw --dev node run --host 127.0.0.1 --port 8090 --display-name "Build Node"
```

安装成后台服务：

```bash
export OPENCLAW_GATEWAY_TOKEN='replace-with-a-node-bootstrap-token'
openclaw node install --host 127.0.0.1 --port 8090 --display-name "Build Node"
```

几点说明：

- 这里连接的是 `fleetd` 本身，不是外部 shared gateway
- `openclaw node run --host/--port` 会直接连根路径
- 首次接入成功后，设备会出现在 `fleetd` 的待认领列表中

### 5.3 认领设备

打开：

```text
http://127.0.0.1:8090/fleet/claims
```

页面会显示：

- 显示名
- 设备 ID 前半部分
- 平台 / 设备族
- `client id / client mode`
- 请求时间
- 远端地址
- 确认输入框

认领时需要输入完整设备 ID。对于 `openclaw node run/install` 接入的节点，可在节点本机查看：

```text
~/.openclaw/identity/device.json
```

如果你配置的是 `fleetd` 作为 gateway backend 的 operator 身份文件，那是另一套身份文件，默认位置仍是：

```text
~/.fleet/device.json
```

认领后会跳转到：

```text
http://127.0.0.1:8090/fleet/nodes
```

当前如果没有配置 `FLEETD_JWT_RS256_PUBLIC_KEY`，需要注意：

- 页面身份固定是 `anonymous`
- 你在页面上认领的节点，归属用户也会是 `anonymous`
- 因此 CLI 和 runtime API 示例里必须把 `USER_ID` 设成 `anonymous` 才能看到这些节点

如果你要做真实多用户隔离，就配置 `FLEETD_JWT_RS256_PUBLIC_KEY`。页面登录后会从 JWT payload 的 `FLEETD_JWT_USER_ID_FIELD` 字段读取用户 ID；默认字段是 `sub`。

### 5.4 当前怎样减少误认领

当前页面不再使用“认领码”。

现在的确认方式是：

- 页面只展示设备 `device_id` 的前半部分，并继续核对显示名、平台、`client id / client mode`、远端地址和请求时间
- 提交认领前，再输入该设备完整的 `device_id`（大小写不敏感）

这不是额外的安全因子，但它有一个明确目的：

- 防止用户在多台待认领设备并排出现时点错行、认错机器

如果你想进一步降低误认领概率，实际最有效的是：

- 节点启动时总是设置清晰的 `--display-name`
- 把节点部署位置或用途编码进显示名，例如 `bj-build-mac-01`
- 认领时同时核对页面上的平台、远端地址和请求时间

### 5.5 解除认领

已认领节点可以在 `/fleet/nodes` 列表页或节点详情页执行“解除认领”。

这个操作是设备级的：

- 会删除当前用户对该设备的归属
- 会移除该设备下的所有节点
- 设备会重新回到待认领列表

如果后面要做真正有安全意义的认领因子，方向仍然是：

- 只在节点侧显示一次性确认信息，不在待认领页回显
- 或由节点运维侧通过带外渠道单独下发

## 6. Web 登录与鉴权

### 6.1 页面鉴权

Web 页面鉴权只有两种状态：

- 未配置 `FLEETD_JWT_RS256_PUBLIC_KEY`: 不做页面登录，页面用户视为 `anonymous`
- 已配置 `FLEETD_JWT_RS256_PUBLIC_KEY`: 使用 RS256 校验 JWT

当页面鉴权开启时：

- 打开 `/fleet/*` 会先跳转到 `/fleet/login`
- 登录页只做一件事：把你粘贴的 JWT 放进 cookie
- 页面里的“当前用户”以及节点隔离都取决于 token 里 `FLEETD_JWT_USER_ID_FIELD` 指定的顶层字段

### 6.2 Runtime API 鉴权

`FLEETD_RUNTIME_AUTH_MODE` 支持：

- `disabled`
- `api_key`

`api_key` 模式下必须带：

- `API_KEY: <FLEETD_API_KEY>`
- `USER_ID: <目标用户>`

## 7. CLI 示例

建议先写入 `~/.fleet/.env`：

```bash
FLEET_BASE_URL=http://127.0.0.1:8090
FLEET_API_KEY=replace-me
USER_ID=anonymous
```

CLI 现在推荐直接使用：

- `fleet list`
- `fleet describe`
- `fleet invoke`
- `fleet run`

列出节点：

```bash
go run ./cmd/fleet list
```

查看详情：

```bash
go run ./cmd/fleet describe <node-id>
```

查看状态：

```bash
go run ./cmd/fleet status <node-id>
```

通用 invoke 形态：

```bash
go run ./cmd/fleet invoke <node-id> <command> --json '{"key":"value"}'
```

下面这些例子已经在真实节点上验证通过：

读取节点主机的 exec approvals：

```bash
go run ./cmd/fleet invoke <node-id> system.execApprovals.get --json '{}'
```

准备一次 `system.run` 计划：

```bash
go run ./cmd/fleet invoke <node-id> system.run.prepare --json '{"command":["uname","-a"],"rawCommand":"uname -a"}'
```

`system.which` 的可执行示例：

```bash
go run ./cmd/fleet invoke <node-id> system.which --json '{"name":"git","bins":["/usr/bin/git","/opt/homebrew/bin/git"]}'
```

说明：

- `system.execApprovals.get` 很适合用来确认节点侧 approvals 当前状态
- `system.run.prepare` 能验证 `invoke` 链路没问题，还能看到节点规范化后的可执行路径
- `system.which` 必须带 `bins`；不同机器上结果可能为空，这取决于节点主机是否真的有这些路径

浏览器能力示例：

这组写法没有在本文环境做端到端实机验证，但我已按 `2026-04-06` 时的 OpenClaw 官方文档和 `openclaw` npm 包 `2026.4.2` 运行时代码核对过当前调用形态。

先确认节点是否真的暴露了浏览器代理命令：

```bash
go run ./cmd/fleet describe <node-id>
```

至少满足其一才继续：

- `Caps` 里有 `browser`
- `Commands` 里有 `browser.proxy`

如果节点没有这项能力，先检查节点主机上的 OpenClaw 浏览器插件是否启用；当你配置了 `plugins.allow` 时，还必须显式包含 `browser`。

先打开一个页面：

```bash
go run ./cmd/fleet invoke <node-id> browser.proxy --json '{
  "method": "POST",
  "path": "/tabs/open",
  "body": {
    "url": "https://example.com"
  },
  "timeoutMs": 20000
}'
```

列出现有标签页：

```bash
go run ./cmd/fleet invoke <node-id> browser.proxy --json '{
  "method": "GET",
  "path": "/tabs",
  "timeoutMs": 20000
}'
```

拿到 `targetId` 后再导航：

```bash
go run ./cmd/fleet invoke <node-id> browser.proxy --json '{
  "method": "POST",
  "path": "/navigate",
  "body": {
    "targetId": "<target-id>",
    "url": "https://example.com"
  },
  "timeoutMs": 20000
}'
```

抓取页面 snapshot：

```bash
go run ./cmd/fleet invoke <node-id> browser.proxy --json '{
  "method": "GET",
  "path": "/snapshot",
  "query": {
    "targetId": "<target-id>",
    "interactive": "true"
  },
  "timeoutMs": 20000
}'
```

如果 snapshot 返回了可点击元素的 `ref`，再做一次点击：

```bash
go run ./cmd/fleet invoke <node-id> browser.proxy --json '{
  "method": "POST",
  "path": "/act",
  "body": {
    "kind": "click",
    "targetId": "<target-id>",
    "ref": "e12"
  },
  "timeoutMs": 20000
}'
```

补充说明：

- `browser.proxy` 的参数形态本质上是对 OpenClaw 浏览器控制路由的透传，核心字段是 `method`、`path`、`query`、`body`
- 常见路径包括 `/tabs`、`/tabs/open`、`/navigate`、`/snapshot`、`/act`
- `navigate`、`act`、AI snapshot 等能力通常依赖 Playwright；如果节点主机没装完整 Playwright，节点可能返回 `501`
- 页面跳转后 `ref` 可能失效；这是浏览器 snapshot 的正常行为，需要重新抓一次 `/snapshot`

远程执行形态：

```bash
go run ./cmd/fleet run <node-id> -- uname -a
```

默认会直接输出远端命令的 stdout/stderr；如果你需要机器可读结果，再加：

```bash
go run ./cmd/fleet run <node-id> --json -- uname -a
```

这个例子我已经实测通过，但前提是先在节点主机放行目标可执行文件：

```bash
openclaw approvals allowlist add "/usr/bin/uname"
go run ./cmd/fleet run <node-id> -- uname -a
```

再补一个容易走通的例子：

```bash
openclaw approvals allowlist add "/usr/bin/sw_vers"
go run ./cmd/fleet run <node-id> -- sw_vers
```

注意：

- `invoke` 能否成功，取决于节点实际暴露的 `commands` 和参数要求
- `run` 在真实节点上会受节点主机本地 exec approvals 约束
- 在当前 `fleetd` 独立模式下，最直接可行的处理方式是在节点主机本机执行 `openclaw approvals ...`

## 8. Runtime HTTP API 示例

列出节点：

```bash
curl http://127.0.0.1:8090/runtime/fleet/nodes \
  -H 'API_KEY: replace-me' \
  -H 'USER_ID: anonymous'
```

查看详情：

```bash
curl http://127.0.0.1:8090/runtime/fleet/nodes/<node-id> \
  -H 'API_KEY: replace-me' \
  -H 'USER_ID: anonymous'
```

通用 invoke：

```bash
curl -X POST http://127.0.0.1:8090/runtime/fleet/nodes/<node-id>/invoke \
  -H 'API_KEY: replace-me' \
  -H 'USER_ID: anonymous' \
  -H 'Content-Type: application/json' \
  -d '{
    "command": "system.run.prepare",
    "params": {
      "command": ["uname", "-a"],
      "rawCommand": "uname -a"
    }
  }'
```

另一个已经验证可用的 invoke 例子：

```bash
curl -X POST http://127.0.0.1:8090/runtime/fleet/nodes/<node-id>/invoke \
  -H 'API_KEY: replace-me' \
  -H 'USER_ID: anonymous' \
  -H 'Content-Type: application/json' \
  -d '{
    "command": "system.execApprovals.get",
    "params": {}
  }'
```

远程执行：

```bash
curl -X POST http://127.0.0.1:8090/runtime/fleet/nodes/<node-id>/run \
  -H 'API_KEY: replace-me' \
  -H 'USER_ID: anonymous' \
  -H 'Content-Type: application/json' \
  -d '{
    "command": ["uname", "-a"]
  }'
```

浏览器能力示例：

和 CLI 一样，这里前提仍然是节点详情里能看到 `browser.proxy` 或 `browser` capability。

先开一个标签页：

```bash
curl -X POST http://127.0.0.1:8090/runtime/fleet/nodes/<node-id>/invoke \
  -H 'API_KEY: replace-me' \
  -H 'USER_ID: anonymous' \
  -H 'Content-Type: application/json' \
  -d '{
    "command": "browser.proxy",
    "params": {
      "method": "POST",
      "path": "/tabs/open",
      "body": {
        "url": "https://example.com"
      },
      "timeoutMs": 20000
    }
  }'
```

列标签页：

```bash
curl -X POST http://127.0.0.1:8090/runtime/fleet/nodes/<node-id>/invoke \
  -H 'API_KEY: replace-me' \
  -H 'USER_ID: anonymous' \
  -H 'Content-Type: application/json' \
  -d '{
    "command": "browser.proxy",
    "params": {
      "method": "GET",
      "path": "/tabs",
      "timeoutMs": 20000
    }
  }'
```

拿到 `targetId` 后导航：

```bash
curl -X POST http://127.0.0.1:8090/runtime/fleet/nodes/<node-id>/invoke \
  -H 'API_KEY: replace-me' \
  -H 'USER_ID: anonymous' \
  -H 'Content-Type: application/json' \
  -d '{
    "command": "browser.proxy",
    "params": {
      "method": "POST",
      "path": "/navigate",
      "body": {
        "targetId": "<target-id>",
        "url": "https://example.com"
      },
      "timeoutMs": 20000
    }
  }'
```

抓 interactive snapshot：

```bash
curl -X POST http://127.0.0.1:8090/runtime/fleet/nodes/<node-id>/invoke \
  -H 'API_KEY: replace-me' \
  -H 'USER_ID: anonymous' \
  -H 'Content-Type: application/json' \
  -d '{
    "command": "browser.proxy",
    "params": {
      "method": "GET",
      "path": "/snapshot",
      "query": {
        "targetId": "<target-id>",
        "interactive": "true"
      },
      "timeoutMs": 20000
    }
  }'
```

基于 snapshot 返回的 `ref` 做点击：

```bash
curl -X POST http://127.0.0.1:8090/runtime/fleet/nodes/<node-id>/invoke \
  -H 'API_KEY: replace-me' \
  -H 'USER_ID: anonymous' \
  -H 'Content-Type: application/json' \
  -d '{
    "command": "browser.proxy",
    "params": {
      "method": "POST",
      "path": "/act",
      "body": {
        "kind": "click",
        "targetId": "<target-id>",
        "ref": "e12"
      },
      "timeoutMs": 20000
    }
  }'
```

## 9. 持久化与状态

默认 SQLite 文件由 `FLEETD_STORE_DSN` 指定。

当前服务端会把这些信息存在本地库里：

- 待认领设备
- 已认领设备与用户归属
- 已认领节点与用户归属
- 节点设备 token 状态

这意味着：

- 删除数据库会丢失认领关系
- 删除数据库也会让节点下次重连时重新走一次待认领流程

## 10. 反向代理与 TLS

`fleetd` 自己只提供 HTTP，不直接终止 TLS。

如果你把它放到 Nginx、Caddy、Traefik 等反向代理后面，需要确保：

- `/` 的 WebSocket 升级能被转发
- `/fleet/*` 和 `/runtime/*` 的普通 HTTP 请求能被转发

如果只转发了页面路由，没有转发 WebSocket upgrade，节点会连不上。

## 11. 故障排查

### 11.1 节点接入时报 404

先确认你运行的是当前版本 `fleetd`。节点接入只走根路径上的 WebSocket upgrade。

如果还是 404，通常是反向代理没有把根路径的 WebSocket 升级转发过来。

### 11.2 `/fleet/claims` 看不到节点

检查：

- `fleetd` 是否已经启动
- `openclaw node run/install` 是否还在运行
- `OPENCLAW_GATEWAY_TOKEN` 或 `OPENCLAW_GATEWAY_PASSWORD` 是否与服务端一致
- 反向代理是否转发了 WebSocket upgrade

### 11.3 CLI 能访问但看不到刚认领的节点

最常见原因是用户 ID 不一致：

- 页面在未配置 `FLEETD_JWT_RS256_PUBLIC_KEY` 时认领出的用户是 `anonymous`
- CLI / API 带的是别的 `USER_ID`

### 11.4 `fleet run` 返回 `approval required`

这是节点主机的执行审批策略，不是 `fleetd` 路由错误。

最小排查顺序：

1. 先确认节点在线，并且命令声明没问题：

```bash
go run ./cmd/fleet describe <node-id>
go run ./cmd/fleet invoke <node-id> system.run.prepare --json '{"command":["uname","-a"],"rawCommand":"uname -a"}'
```

2. 在节点主机本机查看 approvals：

```bash
openclaw approvals get
```

3. 如果你只是要先把流程跑通，直接放行目标可执行文件：

```bash
openclaw approvals allowlist add "/usr/bin/uname"
```

4. 然后重试：

```bash
go run ./cmd/fleet run <node-id> -- uname -a
```

5. 如果还不行，再看 JSON 版状态：

```bash
openclaw approvals get --json
```

你应该能看到类似这样的结构：

```json
{
  "version": 1,
  "defaults": {},
  "agents": {
    "*": {
      "allowlist": [
        {
          "pattern": "/usr/bin/uname"
        }
      ]
    }
  }
}
```

关键点：

- approvals 是节点主机本地状态，默认文件在 `~/.openclaw/exec-approvals.json`
- 当前这个独立 `fleetd` 方案没有实现 OpenClaw gateway 那套 operator approvals 管理 RPC
- 所以在 `fleetd` 独立模式下，最直接可行的方式是登录到节点主机本机执行 `openclaw approvals ...`
- 如果节点和 `fleetd` 在同一台机器上，本机执行即可
- 如果节点在远端机器上，就要在那台远端节点机器上执行

常见错误和对应动作：

- `SYSTEM_RUN_DENIED: approval required`
  说明命令还没被放行，先加 allowlist 或按节点侧策略完成批准
- `SYSTEM_RUN_DENIED: allowlist miss`
  说明当前策略已经是 allowlist，但目标可执行路径不在 allowlist 内

最容易走通的一组命令是：

```bash
openclaw approvals allowlist add "/usr/bin/uname"
openclaw approvals allowlist add "/usr/bin/sw_vers"
```

## 12. 相关文档

- 服务端配置模板：[.env.server.example](/Users/zengnianmei/workspace/source/fleetd/.env.server.example)
- CLI 配置模板：[.env.cli.example](/Users/zengnianmei/workspace/source/fleetd/.env.cli.example)
- OpenClaw 协议整理：[docs/openclaw-protocol/README.md](/Users/zengnianmei/workspace/source/fleetd/docs/openclaw-protocol/README.md)
- 协议命令映射：[docs/openclaw-protocol/commands.md](/Users/zengnianmei/workspace/source/fleetd/docs/openclaw-protocol/commands.md)
