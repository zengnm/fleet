# OpenClaw CLI 到 Gateway / Node 协议映射

## 1. 说明

本文只整理最新版官方文档和源码中与以下命令直接相关的映射：

- `openclaw node`
- `openclaw devices`
- `openclaw nodes`

映射原则：

- 如果命令直接对应某个 RPC method，就写 method
- 如果命令只是本地服务管理，不经过 Gateway，就明确写“无远端 RPC”
- 如果官方文档已经说明某条旧命令路径不再使用，就明确写当前替代路径

## 2. `openclaw node`

来源：[CLI: node](https://docs.openclaw.ai/cli/node)、[Nodes](https://docs.openclaw.ai/nodes)、[Gateway Protocol](https://docs.openclaw.ai/gateway/protocol)

| CLI | 协议动作 | 说明 |
| --- | --- | --- |
| `openclaw node run --host ... --port ...` | `event(connect.challenge)` -> `req(connect)` -> `res(hello-ok)` | 前台启动 headless node host，并以 `role: "node"` 接入 Gateway WS。 |
| `openclaw node install --host ... --port ...` | 运行后与 `node run` 相同 | `install` 本身是安装后台服务；真正联网时仍然走同一套 `connect` 握手。 |
| `openclaw node status` | 无远端 RPC | 查询本地服务状态。 |
| `openclaw node stop` | 无远端 RPC | 停止本地服务。 |
| `openclaw node restart` | 无远端 RPC | 重启本地服务；重连时再走 `connect` 握手。 |
| `openclaw node uninstall` | 无远端 RPC | 卸载本地服务。 |

### 2.1 `openclaw node run/install` 的连接与认证

连接参数：

- `--host`
- `--port`
- `--tls`
- `--tls-fingerprint`
- `--node-id`
- `--display-name`

认证来源：

- 先看 `OPENCLAW_GATEWAY_TOKEN` / `OPENCLAW_GATEWAY_PASSWORD`
- 再看 `gateway.auth.token` / `gateway.auth.password`
- `gateway.mode=remote` 时，`gateway.remote.token` / `gateway.remote.password` 也可参与远端优先级

首次接入后的设备准入：

1. node host 收到 `connect.challenge`
2. node host 发送 `connect`
3. Gateway 为该 `device.id` 建立 device pairing pending request
4. 操作者用 `openclaw devices approve` 批准
5. 之后 node 使用 `hello-ok.auth.deviceToken` 重连

## 3. `openclaw devices`

来源：[CLI: devices](https://docs.openclaw.ai/cli/devices)、`server-methods/devices.ts`

| CLI | RPC / Event | 说明 |
| --- | --- | --- |
| `openclaw devices list` | `req(device.pair.list)` / `res` | 查询 pending pairing requests 和 paired devices。 |
| `openclaw devices approve [requestId] [--latest]` | `req(device.pair.approve)` / `res`，并广播 `event(device.pair.resolved)` | 批准 device pairing。 |
| `openclaw devices reject <requestId>` | `req(device.pair.reject)` / `res`，并广播 `event(device.pair.resolved)` | 拒绝 device pairing。 |
| `openclaw devices remove <deviceId>` | `req(device.pair.remove)` / `res` | 删除 paired device。 |
| `openclaw devices rotate --device <id> --role <role> [--scope ...]` | `req(device.token.rotate)` / `res` | 轮换指定 role 的 device token。 |
| `openclaw devices revoke --device <id> --role <role>` | `req(device.token.revoke)` / `res` | 撤销指定 role 的 device token。 |

### 3.1 `devices list/approve` 是 node 直连接入的准入链

对 WS node 来说，最新版直连准入不是 `node.pair.*`，而是：

1. node 先 `connect(role=node)`
2. Gateway 产生 `device.pair.requested`
3. 操作者执行 `openclaw devices list`
4. 操作者执行 `openclaw devices approve <requestId>`
5. Gateway 广播 `device.pair.resolved`
6. node 使用 `hello-ok.auth.deviceToken` 后续重连

### 3.2 作用域注意事项

`devices` 命令需要 `operator.pairing` 或更高权限。

文档和服务端实现还额外规定：

- paired-device token 会限制跨设备管理
- `rotate` 不能扩展出 pairing 从未批准过的新 role
- 显式 `--scope` 也不能超过调用方当前会话已有的 operator scopes

## 4. `openclaw nodes`

来源：[CLI: nodes](https://docs.openclaw.ai/cli/nodes)、[Nodes](https://docs.openclaw.ai/nodes)、`server-methods/nodes.ts`、`server-methods/nodes-pending.ts`

| CLI | RPC / Event | 说明 |
| --- | --- | --- |
| `openclaw nodes list` | `req(node.list)` / `res` | 查询已知节点列表。 |
| `openclaw nodes list --connected` | `req(node.list)` / `res` | `--connected` 是 CLI 端过滤。 |
| `openclaw nodes list --last-connected 24h` | `req(node.list)` / `res` | `--last-connected` 是 CLI 端过滤。 |
| `openclaw nodes status` | `req(node.list)` / `res` | 官方文档把它当作状态视图；底层仍是节点列表能力。 |
| `openclaw nodes status --connected` | `req(node.list)` / `res` | CLI 端过滤。 |
| `openclaw nodes pending` | `req(node.pair.list)` / `res` | 查询 gateway-owned node pairing store。 |
| `openclaw nodes approve <requestId>` | `req(node.pair.approve)` / `res`，并广播 `event(node.pair.resolved)` | 批准 node pairing store 条目。 |
| `openclaw nodes reject <requestId>` | `req(node.pair.reject)` / `res`，并广播 `event(node.pair.resolved)` | 拒绝 node pairing store 条目。 |
| `openclaw nodes rename --node <id|name|ip> --name <displayName>` | `req(node.rename)` / `res` | 写 gateway override 名称。 |
| `openclaw nodes describe --node <id|name|ip>` | `req(node.describe)` / `res` | 查询单节点详情。 |
| `openclaw nodes invoke --node <id|name|ip> --command <command> --params <json>` | `req(node.invoke)` / `event(node.invoke.request)` / `req(node.invoke.result)` / `res` | 节点 capability RPC 主链路。 |

### 4.1 关于 `openclaw nodes run`

最新版官方文档已经明确：

- `nodes invoke` 不暴露 `system.run` / `system.run.prepare`
- shell execution 走 `exec host=node`

因此，本文不把 `openclaw nodes run` 当作最新版 node 执行主链路；当前主链路是：

- capability 命令：`openclaw nodes invoke`
- shell / 子进程执行：`exec host=node`

## 5. node helper 命令到 `node.invoke` 的映射

来源：[Nodes](https://docs.openclaw.ai/nodes)

下面这些高层 helper，本质上都落到 `node.invoke`：

| CLI helper | `node.invoke.command` |
| --- | --- |
| `openclaw nodes canvas snapshot` | `canvas.snapshot` |
| `openclaw nodes canvas present` | `canvas.present` |
| `openclaw nodes canvas hide` | `canvas.hide` |
| `openclaw nodes canvas navigate` | `canvas.navigate` |
| `openclaw nodes canvas eval` | `canvas.eval` |
| `openclaw nodes camera list` | `camera.list` |
| `openclaw nodes camera snap` | `camera.snap` |
| `openclaw nodes camera clip` | `camera.clip` |
| `openclaw nodes screen record` | `screen.record` |
| `openclaw nodes location get` | `location.get` |
| `openclaw nodes notify` | `system.notify` |
| `openclaw nodes invoke --command system.which` | `system.which` |

注意：

- 当前 `nodes` 总览页明确写了 A2UI helper 存在，但没有在同页把底层 command 名逐条写死；为避免编造，这里不展开 A2UI 的逐条 command 映射。
- `system.run` 不是这里的 helper 映射目标
- `system.run.prepare` 也不经过 `nodes invoke`

## 6. 三条核心时序

### 6.1 连接

1. `openclaw node run --host ... --port ...`
2. Gateway -> node：`event(connect.challenge)`
3. node -> Gateway：`req(connect)`
4. Gateway -> node：`res(hello-ok)`

### 6.2 node 直连准入

1. node 完成 `connect(role=node)`
2. Gateway 产生 `event(device.pair.requested)`
3. Operator 执行 `openclaw devices list`
4. Operator 执行 `openclaw devices approve <requestId>`
5. Gateway 广播 `event(device.pair.resolved)`
6. node 后续使用 `hello-ok.auth.deviceToken` 重连

### 6.3 节点调用

1. Operator 执行 `openclaw nodes invoke --node ... --command ...`
2. Gateway 收到 `req(node.invoke)`
3. Gateway 向 node 发 `event(node.invoke.request)`
4. node 回 `req(node.invoke.result)`
5. Gateway 先回 node 一个 `res`
6. Gateway 再回 operator 原始 `node.invoke` 的 `res`

## 7. 远程执行链

来源：[Nodes](https://docs.openclaw.ai/nodes)、[CLI: node](https://docs.openclaw.ai/cli/node)、[CLI: nodes](https://docs.openclaw.ai/cli/nodes)

最新版“在别的机器上跑命令”的链路是：

1. 在远端机器启动 `openclaw node run` 或 `openclaw node install`
2. 通过 `devices approve` 完成 device pairing
3. 在 Gateway 侧把 exec 指向 node
4. 执行 `exec host=node`
5. Gateway 把执行请求转发给 node host

要点：

- 模型和消息仍然跑在 gateway
- `system.run` 真正执行在 node host
- approvals 存在 node host 本地
- `openclaw approvals --node <id|name|ip>` 可以从 gateway 端编辑 node approvals
