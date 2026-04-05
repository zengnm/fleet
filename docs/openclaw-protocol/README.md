# OpenClaw 最新 Gateway / Node 协议整理

本文档目录只覆盖 **最新版 OpenClaw Gateway WebSocket + node 直连接入协议**，不包含 legacy Bridge TCP JSONL。

## 文件说明

- `protocol.md`
  - 最新协议的类型定义、握手、配对、节点调用链
  - 重点覆盖 `req / res / event`、`connect`、`node.invoke`、`device.pair.*`、`node.pair.*`
- `commands.md`
  - `openclaw node`、`openclaw devices`、`openclaw nodes` 与底层 RPC / event 的对应关系

## 来源

优先级固定为：

1. 官方 schema / 源码
2. 官方 Gateway Protocol 文档
3. 官方 Nodes 文档
4. 官方 CLI 文档

本目录整理时使用的官方来源：

- [Gateway Protocol](https://docs.openclaw.ai/gateway/protocol)
- [Nodes](https://docs.openclaw.ai/nodes)
- [CLI: node](https://docs.openclaw.ai/cli/node)
- [CLI: nodes](https://docs.openclaw.ai/cli/nodes)
- [CLI: devices](https://docs.openclaw.ai/cli/devices)
- [schema.ts](https://github.com/openclaw/openclaw/blob/main/src/gateway/protocol/schema.ts)
- [frames.ts](https://github.com/openclaw/openclaw/blob/main/src/gateway/protocol/schema/frames.ts)
- [nodes.ts](https://github.com/openclaw/openclaw/blob/main/src/gateway/protocol/schema/nodes.ts)
- [devices.ts](https://github.com/openclaw/openclaw/blob/main/src/gateway/protocol/schema/devices.ts)
- [snapshot.ts](https://github.com/openclaw/openclaw/blob/main/src/gateway/protocol/schema/snapshot.ts)
- [client-info.ts](https://github.com/openclaw/openclaw/blob/main/src/gateway/protocol/client-info.ts)
- [server-methods/devices.ts](https://github.com/openclaw/openclaw/blob/main/src/gateway/server-methods/devices.ts)
- [server-methods/nodes.ts](https://github.com/openclaw/openclaw/blob/main/src/gateway/server-methods/nodes.ts)
- [server-methods/nodes-pending.ts](https://github.com/openclaw/openclaw/blob/main/src/gateway/server-methods/nodes-pending.ts)
- [node-catalog.ts](https://github.com/openclaw/openclaw/blob/main/src/gateway/node-catalog.ts)
- [node-list-types.ts](https://github.com/openclaw/openclaw/blob/main/src/shared/node-list-types.ts)

## 关键结论

- 最新协议顶层帧只有 `req / res / event`。
- 最新协议没有独立顶层 `invoke / invoke-res` frame。
- 当前等价调用链是：
  - 操作者发送 `req(method="node.invoke")`
  - 网关向 node 发送 `event("node.invoke.request")`
  - node 回传 `req(method="node.invoke.result")`
  - 网关再以 `res` 完成原始请求
- `openclaw node` 直接接入 Gateway 时，握手门禁是 **device pairing**。
- `node.pair.*` 是另一套 gateway-owned node pairing store，不是 WS `connect` 门禁。
- `system.run` / `system.run.prepare` 不走 `openclaw nodes invoke`；壳执行走 `exec host=node`。
