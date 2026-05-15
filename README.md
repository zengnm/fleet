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
- `cmd/fleetn`: 节点端一键接入 CLI
- `internal/fleetd`: WebSocket 接入、配对、页面、runtime API、认证
- `internal/fleet`: Fleet 服务层
- `internal/fleetnode`: 节点端 agent、identity、远程命令执行与用户级服务安装
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

也可以使用本仓库自带的节点端 CLI，不依赖 `openclaw node`：

```bash
go run ./cmd/fleetn register \
  --server http://127.0.0.1:8090 \
  --token change-me \
  --name "Build Node"
```

如果服务端没有配置 `FLEETD_GATEWAY_TOKEN` / `FLEETD_GATEWAY_PASSWORD`，可以不传 `--token`。默认前台常驻运行；需要安装用户级后台服务时增加 `--install`。`fleetn --install` 使用本机用户级服务机制：macOS 是 LaunchAgent，Linux 是 systemd user service，Windows 是当前用户 Scheduled Task。

`fleetn` 的 `system.run` 默认需要节点本机 exec approvals 放行。修改 approvals 必须在节点本机执行；Fleet CLI 只支持远程查看：

```bash
go run ./cmd/fleetn approvals add /bin/sh
go run ./cmd/fleet invoke --node <node-id> --command system.execApprovals.get --params '{}'
```

放行 shell 后可以用 `fleet run` 简洁执行 shell 命令：

```bash
go run ./cmd/fleet run --node <node-id> -- 'uname -a'
```

`fleetn` 的 `browser.proxy` 默认使用内置 Chrome/CDP 代理，不依赖节点主机安装 OpenClaw；需要接外部浏览器代理时，可设置 `FLEETN_BROWSER_PROXY_URL` 或 `--browser-proxy`。Chrome/Chromium 不在常见路径时，可设置 `FLEETN_BROWSER_EXECUTABLE_PATH` 或 `--browser-executable`。默认 headless；需可见窗口时，可在 `~/.fleetn/config.json` 设置 `"browserHeadless": false`，或注册时使用 `--browser-headless false` / `FLEETN_BROWSER_HEADLESS=false`。

认领后列节点：

```bash
FLEET_BASE_URL=http://127.0.0.1:8090 \
FLEET_API_KEY=change-me \
USER_ID=anonymous \
go run ./cmd/fleet list
```

设备认领时，需要输入完整设备 ID。对于 `openclaw node` 启动的节点，可在节点本机的 `~/.openclaw/identity/device.json` 查看该值。

说明：

- 默认示例里没有配置 `FLEETD_JWT_RS256_PUBLIC_KEY`，页面认领产生的用户是 `anonymous`
- `openclaw node run --host/--port` 直接连接 `fleetd` 根路径
- `fleetn` 支持 `status`、`stop`、`restart`、`uninstall` 管理用户级后台服务；`status` 返回简洁状态值，如 `running` 或 `stopped`
- 更完整的配置、鉴权、API、反向代理和故障排查见 [docs/fleet.md](/Users/zengnianmei/workspace/source/fleetd/docs/fleet.md)

## 开发

```bash
go test ./...
```

## 代办

- 当前只实现了 headless / shell 类型节点，其它类型待实现
