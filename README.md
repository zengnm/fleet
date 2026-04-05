# fleetd

`fleetd` 是独立的 Fleet 服务。

当前链路是：

```text
openclaw node ---> fleetd ---> fleet CLI / fleet Web
```

它现在自己承担三件事：

- 节点 WebSocket 接入与配对
- 用户认领与节点归属存储
- Web 页面与 runtime HTTP API

它自身就能完成节点接入、认领和 CLI/Web 访问的最小闭环。

## 仓库结构

- `cmd/fleetd`: 服务端入口
- `cmd/fleet`: 用户侧 CLI
- `internal/fleetd`: WebSocket 接入、配对、页面、runtime API、认证
- `internal/fleet`: Fleet 服务层
- `internal/store`: SQLite 存储
- `pkg/spec`: 对外请求/响应结构
- `docs/fleet.md`: 详细部署和使用文档

## 快速验证

服务端：

```bash
cp .env.server.example .env
go run ./cmd/fleetd
```

节点接入：

```bash
export OPENCLAW_GATEWAY_TOKEN='change-me'
openclaw --dev node run --host 127.0.0.1 --port 8090 --display-name "Build Node"
```

认领后列节点：

```bash
FLEET_BASE_URL=http://127.0.0.1:8090 \
FLEET_API_KEY=change-me \
USER_ID=anonymous \
go run ./cmd/fleet list
```

说明：

- 默认示例里 `FLEETD_AUTH_MODE=disabled`，页面认领产生的用户是 `anonymous`
- `openclaw node run --host/--port` 直接连接 `fleetd` 根路径
- 更完整的配置、鉴权、API、反向代理和故障排查见 [docs/fleet.md](/Users/zengnianmei/workspace/source/fleetd/docs/fleet.md)

## 开发

```bash
go test ./...
```
