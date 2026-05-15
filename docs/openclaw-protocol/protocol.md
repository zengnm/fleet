# OpenClaw 最新 Gateway / Node 协议

## 1. 范围与原则

本文只整理你关心的最新版协议范围：

- Gateway WebSocket 顶层帧
- `connect` 握手
- node 直连接入
- device pairing
- node pairing
- node 查询 / 管理 / 调用
- 节点待处理工作队列

不包含：

- legacy Bridge TCP JSONL
- 与 node / devices / gateway 主链路无关的其它方法族

字段来源规则：

- 优先采用官方 `schema/*.ts`
- 如果 schema 没有导出某个响应类型，则采用官方服务端实现
- 如果 schema 和 prose docs 冲突，以 schema 为准
- 没有官方来源的字段一律不写

为便于阅读，下文使用两个辅助别名：

```ts
type NonEmptyString = string; // minLength >= 1
type UnixMs = number; // integer >= 0
```

## 2. 协议版本与顶层帧

来源：`schema/protocol-schemas.ts`、`schema/frames.ts`

当前 `PROTOCOL_VERSION = 3`。

### 2.1 `req`

来源：`RequestFrameSchema`

```ts
type RequestFrame = {
  type: "req";
  id: NonEmptyString;
  method: NonEmptyString;
  params?: unknown;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_001",
  "method": "node.invoke",
  "params": {
    "nodeId": "node-host-01",
    "command": "system.which",
    "params": { "name": "git" },
    "timeoutMs": 15000,
    "idempotencyKey": "idem_001"
  }
}
```

### 2.2 `res`

来源：`ResponseFrameSchema`

```ts
type ResponseFrame = {
  type: "res";
  id: NonEmptyString;
  ok: boolean;
  payload?: unknown;
  error?: ErrorShape;
};
```

示例：

```json
{
  "type": "res",
  "id": "req_001",
  "ok": true,
  "payload": {
    "ok": true,
    "nodeId": "node-host-01",
    "command": "system.which",
    "payload": {
      "name": "git",
      "path": "/usr/bin/git"
    },
    "payloadJSON": "{\"name\":\"git\",\"path\":\"/usr/bin/git\"}"
  }
}
```

### 2.3 `event`

来源：`EventFrameSchema`

```ts
type EventFrame = {
  type: "event";
  event: NonEmptyString;
  payload?: unknown;
  seq?: UnixMs;
  stateVersion?: StateVersion;
};
```

示例：

```json
{
  "type": "event",
  "event": "node.invoke.request",
  "payload": {
    "id": "inv_001",
    "nodeId": "node-host-01",
    "command": "system.which",
    "paramsJSON": "{\"name\":\"git\"}",
    "timeoutMs": 15000,
    "idempotencyKey": "idem_001"
  }
}
```

### 2.4 `ErrorShape`

来源：`ErrorShapeSchema`

```ts
type ErrorShape = {
  code: NonEmptyString;
  message: NonEmptyString;
  details?: unknown;
  retryable?: boolean;
  retryAfterMs?: UnixMs;
};
```

示例：

```json
{
  "type": "res",
  "id": "req_001",
  "ok": false,
  "error": {
    "code": "UNAVAILABLE",
    "message": "node not connected",
    "details": {
      "code": "NOT_CONNECTED"
    }
  }
}
```

### 2.5 `tick`

来源：`TickEventSchema`

```ts
type TickEvent = {
  ts: UnixMs;
};
```

示例：

```json
{
  "type": "event",
  "event": "tick",
  "payload": {
    "ts": 1737264000000
  }
}
```

### 2.6 `shutdown`

来源：`ShutdownEventSchema`

```ts
type ShutdownEvent = {
  reason: NonEmptyString;
  restartExpectedMs?: UnixMs;
};
```

示例：

```json
{
  "type": "event",
  "event": "shutdown",
  "payload": {
    "reason": "update",
    "restartExpectedMs": 5000
  }
}
```

## 3. `connect` 握手

来源：`schema/frames.ts`、`schema/snapshot.ts`、`protocol/client-info.ts`、[Gateway Protocol](https://docs.openclaw.ai/gateway/protocol)

### 3.1 `connect.challenge`

来源：官方 Gateway Protocol 文档

当前 `connect.challenge` 不在 `schema/*.ts` 中导出，但官方协议文档给出了 payload 字段。

```ts
type ConnectChallengeEvent = {
  type: "event";
  event: "connect.challenge";
  payload: {
    nonce: NonEmptyString;
    ts: UnixMs;
  };
};
```

示例：

```json
{
  "type": "event",
  "event": "connect.challenge",
  "payload": {
    "nonce": "srv_nonce_001",
    "ts": 1737264000000
  }
}
```

### 3.2 `GatewayClientId`

来源：`protocol/client-info.ts`

```ts
type GatewayClientId =
  | "webchat-ui"
  | "openclaw-control-ui"
  | "openclaw-tui"
  | "webchat"
  | "cli"
  | "gateway-client"
  | "openclaw-macos"
  | "openclaw-ios"
  | "openclaw-android"
  | "node-host"
  | "test"
  | "fingerprint"
  | "openclaw-probe";
```

### 3.3 `GatewayClientMode`

来源：`protocol/client-info.ts`

```ts
type GatewayClientMode =
  | "webchat"
  | "cli"
  | "ui"
  | "backend"
  | "node"
  | "probe"
  | "test";
```

### 3.4 `ConnectParams`

来源：`ConnectParamsSchema`

```ts
type ConnectParams = {
  minProtocol: number;
  maxProtocol: number;
  client: {
    id: GatewayClientId;
    displayName?: NonEmptyString;
    version: NonEmptyString;
    platform: NonEmptyString;
    deviceFamily?: NonEmptyString;
    modelIdentifier?: NonEmptyString;
    mode: GatewayClientMode;
    instanceId?: NonEmptyString;
  };
  caps?: NonEmptyString[];
  commands?: NonEmptyString[];
  permissions?: Record<NonEmptyString, boolean>;
  pathEnv?: string;
  role?: NonEmptyString;
  scopes?: NonEmptyString[];
  device?: {
    id: NonEmptyString;
    publicKey: NonEmptyString;
    signature: NonEmptyString;
    signedAt: UnixMs;
    nonce: NonEmptyString;
  };
  auth?: {
    token?: string;
    bootstrapToken?: string;
    deviceToken?: string;
    password?: string;
  };
  locale?: string;
  userAgent?: string;
};
```

node 直连时的关键点：

- `client.mode` 必须是 `"node"`
- `role` 通常是 `"node"`
- `device` 必须带上设备身份与签名
- `commands` 是 node 声明的可调用命令清单
- `permissions` 是 node 声明的权限开关
- `auth.deviceToken` 用于已配对设备的后续重连

示例：

```json
{
  "type": "req",
  "id": "conn_001",
  "method": "connect",
  "params": {
    "minProtocol": 3,
    "maxProtocol": 3,
    "client": {
      "id": "node-host",
      "displayName": "Build Node",
      "version": "1.2.3",
      "platform": "linux",
      "deviceFamily": "server",
      "mode": "node"
    },
    "caps": ["system", "browser"],
    "commands": ["system.run", "system.which"],
    "permissions": {
      "browser.enabled": true
    },
    "pathEnv": "/usr/local/bin:/usr/bin:/bin",
    "role": "node",
    "scopes": [],
    "device": {
      "id": "node-host-01",
      "publicKey": "pubkey_001",
      "signature": "sig_001",
      "signedAt": 1737264000000,
      "nonce": "srv_nonce_001"
    },
    "auth": {
      "token": "gateway_shared_token"
    },
    "locale": "en-US",
    "userAgent": "openclaw-node-host/1.2.3"
  }
}
```

### 3.5 `StateVersion`

来源：`StateVersionSchema`

```ts
type StateVersion = {
  presence: number;
  health: number;
};
```

示例：

```json
{
  "presence": 10,
  "health": 4
}
```

### 3.6 `PresenceEntry`

来源：`PresenceEntrySchema`

```ts
type PresenceEntry = {
  host?: NonEmptyString;
  ip?: NonEmptyString;
  version?: NonEmptyString;
  platform?: NonEmptyString;
  deviceFamily?: NonEmptyString;
  modelIdentifier?: NonEmptyString;
  mode?: NonEmptyString;
  lastInputSeconds?: number;
  reason?: NonEmptyString;
  tags?: NonEmptyString[];
  text?: string;
  ts: UnixMs;
  deviceId?: NonEmptyString;
  roles?: NonEmptyString[];
  scopes?: NonEmptyString[];
  instanceId?: NonEmptyString;
};
```

示例：

```json
{
  "host": "build-node.local",
  "ip": "192.168.1.10",
  "version": "1.2.3",
  "platform": "linux",
  "deviceFamily": "server",
  "mode": "node",
  "ts": 1737264000000,
  "deviceId": "node-host-01",
  "roles": ["node"],
  "scopes": []
}
```

### 3.7 `Snapshot`

来源：`SnapshotSchema`

`health` 在 schema 中是 `Type.Any()`，这里只能精确写成 `unknown`。

```ts
type SessionDefaults = {
  defaultAgentId: NonEmptyString;
  mainKey: NonEmptyString;
  mainSessionKey: NonEmptyString;
  scope?: NonEmptyString;
};

type Snapshot = {
  presence: PresenceEntry[];
  health: unknown;
  stateVersion: StateVersion;
  uptimeMs: UnixMs;
  configPath?: NonEmptyString;
  stateDir?: NonEmptyString;
  sessionDefaults?: SessionDefaults;
  authMode?: "none" | "token" | "password" | "trusted-proxy";
  updateAvailable?: {
    currentVersion: NonEmptyString;
    latestVersion: NonEmptyString;
    channel: NonEmptyString;
  };
};
```

示例：

```json
{
  "presence": [],
  "health": {},
  "stateVersion": {
    "presence": 10,
    "health": 4
  },
  "uptimeMs": 123456,
  "authMode": "token"
}
```

### 3.8 `HelloOk`

来源：`HelloOkSchema`

```ts
type HelloOk = {
  type: "hello-ok";
  protocol: number;
  server: {
    version: NonEmptyString;
    connId: NonEmptyString;
  };
  features: {
    methods: NonEmptyString[];
    events: NonEmptyString[];
  };
  snapshot: Snapshot;
  canvasHostUrl?: NonEmptyString;
  auth?: {
    deviceToken: NonEmptyString;
    role: NonEmptyString;
    scopes: NonEmptyString[];
    issuedAtMs?: UnixMs;
    deviceTokens?: Array<{
      deviceToken: NonEmptyString;
      role: NonEmptyString;
      scopes: NonEmptyString[];
      issuedAtMs: UnixMs;
    }>;
  };
  policy: {
    maxPayload: number;
    maxBufferedBytes: number;
    tickIntervalMs: number;
  };
};
```

示例：

```json
{
  "type": "res",
  "id": "conn_001",
  "ok": true,
  "payload": {
    "type": "hello-ok",
    "protocol": 3,
    "server": {
      "version": "1.2.3",
      "connId": "gw_conn_001"
    },
    "features": {
      "methods": ["connect", "device.pair.list", "node.invoke"],
      "events": ["tick", "shutdown", "device.pair.requested", "node.invoke.request"]
    },
    "snapshot": {
      "presence": [],
      "health": {},
      "stateVersion": {
        "presence": 10,
        "health": 4
      },
      "uptimeMs": 123456
    },
    "auth": {
      "deviceToken": "device_token_001",
      "role": "node",
      "scopes": [],
      "issuedAtMs": 1737264001000
    },
    "policy": {
      "maxPayload": 1048576,
      "maxBufferedBytes": 1048576,
      "tickIntervalMs": 15000
    }
  }
}
```

## 4. node 直接接入与 device pairing

来源：`schema/devices.ts`、`server-methods/devices.ts`、`infra/device-pairing.ts`、[Nodes](https://docs.openclaw.ai/nodes)、[CLI: devices](https://docs.openclaw.ai/cli/devices)

关键事实：

- WS node 的握手门禁是 **device pairing**
- 首次 `connect(role=node)` 会产生 device pairing pending request
- 批准后，后续连接可使用 `hello-ok.auth.deviceToken`
- `device.pair.*` 管的是设备身份与 token

### 4.1 `device.pair.requested`

来源：`DevicePairRequestedEventSchema`

```ts
type DevicePairRequestedEventPayload = {
  requestId: NonEmptyString;
  deviceId: NonEmptyString;
  publicKey: NonEmptyString;
  displayName?: NonEmptyString;
  platform?: NonEmptyString;
  deviceFamily?: NonEmptyString;
  clientId?: NonEmptyString;
  clientMode?: NonEmptyString;
  role?: NonEmptyString;
  roles?: NonEmptyString[];
  scopes?: NonEmptyString[];
  remoteIp?: NonEmptyString;
  silent?: boolean;
  isRepair?: boolean;
  ts: UnixMs;
};
```

示例：

```json
{
  "type": "event",
  "event": "device.pair.requested",
  "payload": {
    "requestId": "dpr_001",
    "deviceId": "node-host-01",
    "publicKey": "pubkey_001",
    "displayName": "Build Node",
    "platform": "linux",
    "deviceFamily": "server",
    "clientId": "node-host",
    "clientMode": "node",
    "role": "node",
    "roles": ["node"],
    "scopes": [],
    "remoteIp": "192.168.1.10",
    "silent": false,
    "ts": 1737264002000
  }
}
```

### 4.2 `device.pair.resolved`

来源：`DevicePairResolvedEventSchema`

```ts
type DevicePairResolvedEventPayload = {
  requestId: NonEmptyString;
  deviceId: NonEmptyString;
  decision: NonEmptyString;
  ts: UnixMs;
};
```

示例：

```json
{
  "type": "event",
  "event": "device.pair.resolved",
  "payload": {
    "requestId": "dpr_001",
    "deviceId": "node-host-01",
    "decision": "approved",
    "ts": 1737264003000
  }
}
```

### 4.3 `device.pair.list`

来源：`DevicePairListParamsSchema` + `server-methods/devices.ts`

```ts
type DevicePairListParams = {};

type DeviceAuthTokenSummary = {
  role: string;
  scopes: string[];
  createdAtMs: UnixMs;
  rotatedAtMs?: UnixMs;
  revokedAtMs?: UnixMs;
  lastUsedAtMs?: UnixMs;
};

type RedactedPairedDevice = {
  deviceId: string;
  publicKey: string;
  displayName?: string;
  platform?: string;
  deviceFamily?: string;
  clientId?: string;
  clientMode?: string;
  role?: string;
  roles?: string[];
  scopes?: string[];
  remoteIp?: string;
  createdAtMs: UnixMs;
  approvedAtMs: UnixMs;
  tokens?: DeviceAuthTokenSummary[];
};

type DevicePairingPendingRequest = {
  requestId: string;
  deviceId: string;
  publicKey: string;
  displayName?: string;
  platform?: string;
  deviceFamily?: string;
  clientId?: string;
  clientMode?: string;
  role?: string;
  roles?: string[];
  scopes?: string[];
  remoteIp?: string;
  silent?: boolean;
  isRepair?: boolean;
  ts: UnixMs;
};

type DevicePairListResult = {
  pending: DevicePairingPendingRequest[];
  paired: RedactedPairedDevice[];
};
```

示例：

```json
{
  "type": "req",
  "id": "req_dev_list",
  "method": "device.pair.list",
  "params": {}
}
```

```json
{
  "type": "res",
  "id": "req_dev_list",
  "ok": true,
  "payload": {
    "pending": [
      {
        "requestId": "dpr_001",
        "deviceId": "node-host-01",
        "publicKey": "pubkey_001",
        "displayName": "Build Node",
        "platform": "linux",
        "deviceFamily": "server",
        "clientId": "node-host",
        "clientMode": "node",
        "role": "node",
        "roles": ["node"],
        "scopes": [],
        "remoteIp": "192.168.1.10",
        "silent": false,
        "ts": 1737264002000
      }
    ],
    "paired": []
  }
}
```

### 4.4 `device.pair.approve`

来源：`DevicePairApproveParamsSchema` + `server-methods/devices.ts`

```ts
type DevicePairApproveParams = {
  requestId: NonEmptyString;
};

type DevicePairApproveResult = {
  requestId: string;
  device: RedactedPairedDevice;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_dev_approve",
  "method": "device.pair.approve",
  "params": {
    "requestId": "dpr_001"
  }
}
```

```json
{
  "type": "res",
  "id": "req_dev_approve",
  "ok": true,
  "payload": {
    "requestId": "dpr_001",
    "device": {
      "deviceId": "node-host-01",
      "publicKey": "pubkey_001",
      "displayName": "Build Node",
      "platform": "linux",
      "deviceFamily": "server",
      "clientId": "node-host",
      "clientMode": "node",
      "role": "node",
      "roles": ["node"],
      "scopes": [],
      "createdAtMs": 1737264003000,
      "approvedAtMs": 1737264003000,
      "tokens": [
        {
          "role": "node",
          "scopes": [],
          "createdAtMs": 1737264003000
        }
      ]
    }
  }
}
```

### 4.5 `device.pair.reject`

来源：`DevicePairRejectParamsSchema` + `server-methods/devices.ts`

```ts
type DevicePairRejectParams = {
  requestId: NonEmptyString;
};

type DevicePairRejectResult = {
  requestId: string;
  deviceId: string;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_dev_reject",
  "method": "device.pair.reject",
  "params": {
    "requestId": "dpr_001"
  }
}
```

```json
{
  "type": "res",
  "id": "req_dev_reject",
  "ok": true,
  "payload": {
    "requestId": "dpr_001",
    "deviceId": "node-host-01"
  }
}
```

### 4.6 `device.pair.remove`

来源：`DevicePairRemoveParamsSchema` + `server-methods/devices.ts`

```ts
type DevicePairRemoveParams = {
  deviceId: NonEmptyString;
};

type DevicePairRemoveResult = {
  deviceId: string;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_dev_remove",
  "method": "device.pair.remove",
  "params": {
    "deviceId": "node-host-01"
  }
}
```

```json
{
  "type": "res",
  "id": "req_dev_remove",
  "ok": true,
  "payload": {
    "deviceId": "node-host-01"
  }
}
```

### 4.7 `device.token.rotate`

来源：`DeviceTokenRotateParamsSchema` + `server-methods/devices.ts`

```ts
type DeviceTokenRotateParams = {
  deviceId: NonEmptyString;
  role: NonEmptyString;
  scopes?: NonEmptyString[];
};

type DeviceTokenRotateResult = {
  deviceId: string;
  role: string;
  token: string;
  scopes: string[];
  rotatedAtMs: UnixMs;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_token_rotate",
  "method": "device.token.rotate",
  "params": {
    "deviceId": "node-host-01",
    "role": "node",
    "scopes": []
  }
}
```

```json
{
  "type": "res",
  "id": "req_token_rotate",
  "ok": true,
  "payload": {
    "deviceId": "node-host-01",
    "role": "node",
    "token": "device_token_002",
    "scopes": [],
    "rotatedAtMs": 1737265000000
  }
}
```

### 4.8 `device.token.revoke`

来源：`DeviceTokenRevokeParamsSchema` + `server-methods/devices.ts`

```ts
type DeviceTokenRevokeParams = {
  deviceId: NonEmptyString;
  role: NonEmptyString;
};

type DeviceTokenRevokeResult = {
  deviceId: string;
  role: string;
  revokedAtMs: UnixMs;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_token_revoke",
  "method": "device.token.revoke",
  "params": {
    "deviceId": "node-host-01",
    "role": "node"
  }
}
```

```json
{
  "type": "res",
  "id": "req_token_revoke",
  "ok": true,
  "payload": {
    "deviceId": "node-host-01",
    "role": "node",
    "revokedAtMs": 1737266000000
  }
}
```

## 5. `node.pair.*`：独立的 node pairing store

来源：`schema/nodes.ts`、`infra/node-pairing.ts`、`server-methods/nodes.ts`、[Nodes](https://docs.openclaw.ai/nodes)

关键事实：

- `node.pair.*` 不是 WS `connect` 门禁
- 它是 gateway 自己维护的一套 node pairing store
- CLI `openclaw nodes pending/approve/reject/rename` 走的是这套方法

### 5.1 `node.pair.request`

来源：`NodePairRequestParamsSchema` + `infra/node-pairing.ts`

```ts
type NodePairRequestParams = {
  nodeId: NonEmptyString;
  displayName?: NonEmptyString;
  platform?: NonEmptyString;
  version?: NonEmptyString;
  coreVersion?: NonEmptyString;
  uiVersion?: NonEmptyString;
  deviceFamily?: NonEmptyString;
  modelIdentifier?: NonEmptyString;
  caps?: NonEmptyString[];
  commands?: NonEmptyString[];
  remoteIp?: NonEmptyString;
  silent?: boolean;
};

type NodePairRequestResult = {
  status: "pending";
  request: {
    requestId: string;
    nodeId: string;
    displayName?: string;
    platform?: string;
    version?: string;
    coreVersion?: string;
    uiVersion?: string;
    deviceFamily?: string;
    modelIdentifier?: string;
    caps?: string[];
    commands?: string[];
    remoteIp?: string;
    silent?: boolean;
    ts: UnixMs;
  };
  created: boolean;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_node_pair_request",
  "method": "node.pair.request",
  "params": {
    "nodeId": "ios-node-01",
    "displayName": "Alice iPhone",
    "platform": "ios",
    "version": "1.2.3",
    "deviceFamily": "phone",
    "caps": ["camera", "canvas", "location"],
    "commands": ["camera.snap", "canvas.snapshot", "location.get"],
    "silent": false
  }
}
```

```json
{
  "type": "res",
  "id": "req_node_pair_request",
  "ok": true,
  "payload": {
    "status": "pending",
    "request": {
      "requestId": "npr_001",
      "nodeId": "ios-node-01",
      "displayName": "Alice iPhone",
      "platform": "ios",
      "version": "1.2.3",
      "deviceFamily": "phone",
      "caps": ["camera", "canvas", "location"],
      "commands": ["camera.snap", "canvas.snapshot", "location.get"],
      "silent": false,
      "ts": 1737267000000
    },
    "created": true
  }
}
```

### 5.2 `node.pair.requested`

来源：`server-methods/nodes.ts`

```ts
type NodePairRequestedEventPayload = NodePairRequestResult["request"];
```

示例：

```json
{
  "type": "event",
  "event": "node.pair.requested",
  "payload": {
    "requestId": "npr_001",
    "nodeId": "ios-node-01",
    "displayName": "Alice iPhone",
    "platform": "ios",
    "commands": ["camera.snap", "canvas.snapshot", "location.get"],
    "ts": 1737267000000
  }
}
```

### 5.3 `node.pair.list`

来源：`NodePairListParamsSchema` + `infra/node-pairing.ts`

```ts
type NodePairListParams = {};

type NodeApprovalScope =
  | "operator.pairing"
  | "operator.write"
  | "operator.admin";

type NodePairingPendingEntry = {
  requestId: string;
  nodeId: string;
  displayName?: string;
  platform?: string;
  version?: string;
  coreVersion?: string;
  uiVersion?: string;
  deviceFamily?: string;
  modelIdentifier?: string;
  caps?: string[];
  commands?: string[];
  remoteIp?: string;
  silent?: boolean;
  ts: UnixMs;
  requiredApproveScopes: NodeApprovalScope[];
};

type NodePairingPairedNode = {
  nodeId: string;
  token: string;
  displayName?: string;
  platform?: string;
  version?: string;
  coreVersion?: string;
  uiVersion?: string;
  deviceFamily?: string;
  modelIdentifier?: string;
  caps?: string[];
  commands?: string[];
  permissions?: Record<string, boolean>;
  remoteIp?: string;
  bins?: string[];
  createdAtMs: UnixMs;
  approvedAtMs: UnixMs;
  lastConnectedAtMs?: UnixMs;
};

type NodePairListResult = {
  pending: NodePairingPendingEntry[];
  paired: NodePairingPairedNode[];
};
```

示例：

```json
{
  "type": "req",
  "id": "req_node_pair_list",
  "method": "node.pair.list",
  "params": {}
}
```

```json
{
  "type": "res",
  "id": "req_node_pair_list",
  "ok": true,
  "payload": {
    "pending": [
      {
        "requestId": "npr_001",
        "nodeId": "ios-node-01",
        "displayName": "Alice iPhone",
        "platform": "ios",
        "commands": ["camera.snap", "canvas.snapshot", "location.get"],
        "ts": 1737267000000,
        "requiredApproveScopes": ["operator.pairing", "operator.write"]
      }
    ],
    "paired": []
  }
}
```

### 5.4 `node.pair.approve`

来源：`NodePairApproveParamsSchema` + `server-methods/nodes.ts`

```ts
type NodePairApproveParams = {
  requestId: NonEmptyString;
};

type NodePairApproveResult = {
  requestId: string;
  node: NodePairingPairedNode;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_node_pair_approve",
  "method": "node.pair.approve",
  "params": {
    "requestId": "npr_001"
  }
}
```

```json
{
  "type": "res",
  "id": "req_node_pair_approve",
  "ok": true,
  "payload": {
    "requestId": "npr_001",
    "node": {
      "nodeId": "ios-node-01",
      "token": "node_pair_token_001",
      "displayName": "Alice iPhone",
      "platform": "ios",
      "commands": ["camera.snap", "canvas.snapshot", "location.get"],
      "createdAtMs": 1737268000000,
      "approvedAtMs": 1737268000000
    }
  }
}
```

### 5.5 `node.pair.reject`

来源：`NodePairRejectParamsSchema` + `server-methods/nodes.ts`

```ts
type NodePairRejectParams = {
  requestId: NonEmptyString;
};

type NodePairRejectResult = {
  requestId: string;
  nodeId: string;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_node_pair_reject",
  "method": "node.pair.reject",
  "params": {
    "requestId": "npr_001"
  }
}
```

```json
{
  "type": "res",
  "id": "req_node_pair_reject",
  "ok": true,
  "payload": {
    "requestId": "npr_001",
    "nodeId": "ios-node-01"
  }
}
```

### 5.6 `node.pair.resolved`

来源：`server-methods/nodes.ts`

```ts
type NodePairResolvedEventPayload = {
  requestId: string;
  nodeId: string;
  decision: string;
  ts: UnixMs;
};
```

示例：

```json
{
  "type": "event",
  "event": "node.pair.resolved",
  "payload": {
    "requestId": "npr_001",
    "nodeId": "ios-node-01",
    "decision": "approved",
    "ts": 1737268000000
  }
}
```

### 5.7 `node.pair.verify`

来源：`NodePairVerifyParamsSchema` + `infra/node-pairing.ts`

```ts
type NodePairVerifyParams = {
  nodeId: NonEmptyString;
  token: NonEmptyString;
};

type NodePairVerifyResult =
  | { ok: false }
  | { ok: true; node: NodePairingPairedNode };
```

示例：

```json
{
  "type": "req",
  "id": "req_node_pair_verify",
  "method": "node.pair.verify",
  "params": {
    "nodeId": "ios-node-01",
    "token": "node_pair_token_001"
  }
}
```

```json
{
  "type": "res",
  "id": "req_node_pair_verify",
  "ok": true,
  "payload": {
    "ok": true,
    "node": {
      "nodeId": "ios-node-01",
      "token": "node_pair_token_001",
      "displayName": "Alice iPhone",
      "platform": "ios",
      "createdAtMs": 1737268000000,
      "approvedAtMs": 1737268000000
    }
  }
}
```

## 6. 节点查询与管理

来源：`schema/nodes.ts`、`node-catalog.ts`、`node-list-types.ts`、`server-methods/nodes.ts`

### 6.1 `node.rename`

```ts
type NodeRenameParams = {
  nodeId: NonEmptyString;
  displayName: NonEmptyString;
};

type NodeRenameResult = {
  nodeId: string;
  displayName: string;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_node_rename",
  "method": "node.rename",
  "params": {
    "nodeId": "ios-node-01",
    "displayName": "Alice Phone"
  }
}
```

```json
{
  "type": "res",
  "id": "req_node_rename",
  "ok": true,
  "payload": {
    "nodeId": "ios-node-01",
    "displayName": "Alice Phone"
  }
}
```

### 6.2 `node.list`

来源：`NodeListParamsSchema` + `server-methods/nodes.ts` + `shared/node-list-types.ts`

```ts
type NodeListParams = {};

type NodeListNode = {
  nodeId: string;
  displayName?: string;
  platform?: string;
  version?: string;
  coreVersion?: string;
  uiVersion?: string;
  clientId?: string;
  clientMode?: string;
  remoteIp?: string;
  deviceFamily?: string;
  modelIdentifier?: string;
  pathEnv?: string;
  caps?: string[];
  commands?: string[];
  permissions?: Record<string, boolean>;
  paired?: boolean;
  connected?: boolean;
  connectedAtMs?: UnixMs;
  approvedAtMs?: UnixMs;
};

type NodeListResult = {
  ts: UnixMs;
  nodes: NodeListNode[];
};
```

示例：

```json
{
  "type": "req",
  "id": "req_node_list",
  "method": "node.list",
  "params": {}
}
```

```json
{
  "type": "res",
  "id": "req_node_list",
  "ok": true,
  "payload": {
    "ts": 1737269000000,
    "nodes": [
      {
        "nodeId": "node-host-01",
        "displayName": "Build Node",
        "platform": "linux",
        "clientId": "node-host",
        "clientMode": "node",
        "deviceFamily": "server",
        "pathEnv": "/usr/local/bin:/usr/bin:/bin",
        "caps": ["system", "browser"],
        "commands": ["system.run", "system.which"],
        "permissions": {
          "browser.enabled": true
        },
        "paired": true,
        "connected": true,
        "connectedAtMs": 1737268900000,
        "approvedAtMs": 1737268000000
      }
    ]
  }
}
```

### 6.3 `node.describe`

来源：`NodeDescribeParamsSchema` + `server-methods/nodes.ts` + `shared/node-list-types.ts`

```ts
type NodeDescribeParams = {
  nodeId: NonEmptyString;
};

type NodeDescribeResult = {
  ts: UnixMs;
} & NodeListNode;
```

示例：

```json
{
  "type": "req",
  "id": "req_node_describe",
  "method": "node.describe",
  "params": {
    "nodeId": "node-host-01"
  }
}
```

```json
{
  "type": "res",
  "id": "req_node_describe",
  "ok": true,
  "payload": {
    "ts": 1737269005000,
    "nodeId": "node-host-01",
    "displayName": "Build Node",
    "platform": "linux",
    "clientId": "node-host",
    "clientMode": "node",
    "deviceFamily": "server",
    "pathEnv": "/usr/local/bin:/usr/bin:/bin",
    "caps": ["system", "browser"],
    "commands": ["system.run", "system.which"],
    "permissions": {
      "browser.enabled": true
    },
    "paired": true,
    "connected": true,
    "connectedAtMs": 1737268900000,
    "approvedAtMs": 1737268000000
  }
}
```

### 6.4 `node.canvas.capability.refresh`

来源：`server-methods/nodes.ts`

请求参数当前复用了空对象校验。

```ts
type NodeCanvasCapabilityRefreshParams = {};

type NodeCanvasCapabilityRefreshResult = {
  canvasCapability: string;
  canvasCapabilityExpiresAtMs: UnixMs;
  canvasHostUrl: string;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_canvas_cap",
  "method": "node.canvas.capability.refresh",
  "params": {}
}
```

```json
{
  "type": "res",
  "id": "req_canvas_cap",
  "ok": true,
  "payload": {
    "canvasCapability": "cap_001",
    "canvasCapabilityExpiresAtMs": 1737269600000,
    "canvasHostUrl": "https://gateway.example/canvas?cap=cap_001"
  }
}
```

## 7. `invoke` 调用链：最新版没有顶层 `invoke / invoke-res`

来源：`schema/nodes.ts`、`server-methods/nodes.ts`、`server-methods/nodes.handlers.invoke-result.ts`、`node-registry.ts`、[Gateway Protocol](https://docs.openclaw.ai/gateway/protocol)

关键事实：

- 顶层仍然只有 `req / res / event`
- `node.invoke` 是 RPC method，不是新帧类型
- `node.invoke.request` 是 event 名，不是新帧类型
- `node.invoke.result` 是 node 回传结果的 RPC method

### 7.1 `node.invoke`

来源：`NodeInvokeParamsSchema` + `server-methods/nodes.ts`

```ts
type NodeInvokeParams = {
  nodeId: NonEmptyString;
  command: NonEmptyString;
  params?: unknown;
  timeoutMs?: number;
  idempotencyKey: NonEmptyString;
};

type NodeInvokeSuccessResult = {
  ok: true;
  nodeId: string;
  command: string;
  payload?: unknown;
  payloadJSON?: string | null;
};
```

注意：

- `system.run` 和 `system.run.prepare` 不允许走这里
- `system.execApprovals.get` 也不允许走这里；approvals 修改不通过 `fleet invoke`
- 命令是否允许，还要经过 node 声明命令和网关 allowlist 双重检查

示例：

```json
{
  "type": "req",
  "id": "req_node_invoke",
  "method": "node.invoke",
  "params": {
    "nodeId": "node-host-01",
    "command": "system.which",
    "params": {
      "name": "git"
    },
    "timeoutMs": 15000,
    "idempotencyKey": "idem_which_git_001"
  }
}
```

```json
{
  "type": "res",
  "id": "req_node_invoke",
  "ok": true,
  "payload": {
    "ok": true,
    "nodeId": "node-host-01",
    "command": "system.which",
    "payload": {
      "name": "git",
      "path": "/usr/bin/git"
    },
    "payloadJSON": "{\"name\":\"git\",\"path\":\"/usr/bin/git\"}"
  }
}
```

### 7.2 `node.invoke.request`

来源：`NodeInvokeRequestEventSchema` + `node-registry.ts`

```ts
type NodeInvokeRequestEventPayload = {
  id: NonEmptyString;
  nodeId: NonEmptyString;
  command: NonEmptyString;
  paramsJSON?: string;
  timeoutMs?: number;
  idempotencyKey?: NonEmptyString;
};
```

示例：

```json
{
  "type": "event",
  "event": "node.invoke.request",
  "payload": {
    "id": "inv_001",
    "nodeId": "node-host-01",
    "command": "system.which",
    "paramsJSON": "{\"name\":\"git\"}",
    "timeoutMs": 15000,
    "idempotencyKey": "idem_which_git_001"
  }
}
```

### 7.3 `node.invoke.result`

来源：`NodeInvokeResultParamsSchema` + `server-methods/nodes.handlers.invoke-result.ts`

```ts
type NodeInvokeResultParams = {
  id: NonEmptyString;
  nodeId: NonEmptyString;
  ok: boolean;
  payload?: unknown;
  payloadJSON?: string;
  error?: {
    code?: NonEmptyString;
    message?: NonEmptyString;
  };
};

type NodeInvokeResultAck =
  | { ok: true }
  | { ok: true; ignored: true };
```

示例：

```json
{
  "type": "req",
  "id": "req_node_invoke_result",
  "method": "node.invoke.result",
  "params": {
    "id": "inv_001",
    "nodeId": "node-host-01",
    "ok": true,
    "payload": {
      "name": "git",
      "path": "/usr/bin/git"
    },
    "payloadJSON": "{\"name\":\"git\",\"path\":\"/usr/bin/git\"}"
  }
}
```

```json
{
  "type": "res",
  "id": "req_node_invoke_result",
  "ok": true,
  "payload": {
    "ok": true
  }
}
```

### 7.4 `node.event`

来源：`NodeEventParamsSchema` + `server-methods/nodes.ts`

```ts
type NodeEventParams = {
  event: NonEmptyString;
  payload?: unknown;
  payloadJSON?: string;
};

type NodeEventAck = {
  ok: true;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_node_event",
  "method": "node.event",
  "params": {
    "event": "voicewake.changed",
    "payload": {
      "enabled": true
    }
  }
}
```

```json
{
  "type": "res",
  "id": "req_node_event",
  "ok": true,
  "payload": {
    "ok": true
  }
}
```

### 7.5 调用链时序

最新版“invoke / invoke-res”等价链路如下：

1. Operator -> Gateway

```json
{
  "type": "req",
  "id": "req_node_invoke",
  "method": "node.invoke",
  "params": {
    "nodeId": "node-host-01",
    "command": "system.which",
    "params": { "name": "git" },
    "timeoutMs": 15000,
    "idempotencyKey": "idem_which_git_001"
  }
}
```

2. Gateway -> Node

```json
{
  "type": "event",
  "event": "node.invoke.request",
  "payload": {
    "id": "inv_001",
    "nodeId": "node-host-01",
    "command": "system.which",
    "paramsJSON": "{\"name\":\"git\"}",
    "timeoutMs": 15000,
    "idempotencyKey": "idem_which_git_001"
  }
}
```

3. Node -> Gateway

```json
{
  "type": "req",
  "id": "req_node_invoke_result",
  "method": "node.invoke.result",
  "params": {
    "id": "inv_001",
    "nodeId": "node-host-01",
    "ok": true,
    "payload": {
      "name": "git",
      "path": "/usr/bin/git"
    },
    "payloadJSON": "{\"name\":\"git\",\"path\":\"/usr/bin/git\"}"
  }
}
```

4. Gateway -> Node

```json
{
  "type": "res",
  "id": "req_node_invoke_result",
  "ok": true,
  "payload": {
    "ok": true
  }
}
```

5. Gateway -> Operator

```json
{
  "type": "res",
  "id": "req_node_invoke",
  "ok": true,
  "payload": {
    "ok": true,
    "nodeId": "node-host-01",
    "command": "system.which",
    "payload": {
      "name": "git",
      "path": "/usr/bin/git"
    },
    "payloadJSON": "{\"name\":\"git\",\"path\":\"/usr/bin/git\"}"
  }
}
```

## 8. 节点待处理工作与离线队列

来源：`schema/nodes.ts`、`server-methods/nodes.ts`、`server-methods/nodes-pending.ts`

### 8.1 `node.pending.pull`

来源：`server-methods/nodes.ts`

请求参数当前复用空对象校验。

```ts
type NodePendingPullParams = {};

type NodePendingPullAction = {
  id: string;
  command: string;
  paramsJSON: string | null;
  enqueuedAtMs: UnixMs;
};

type NodePendingPullResult = {
  nodeId: string;
  actions: NodePendingPullAction[];
};
```

示例：

```json
{
  "type": "req",
  "id": "req_pending_pull",
  "method": "node.pending.pull",
  "params": {}
}
```

```json
{
  "type": "res",
  "id": "req_pending_pull",
  "ok": true,
  "payload": {
    "nodeId": "ios-node-01",
    "actions": [
      {
        "id": "act_001",
        "command": "camera.snap",
        "paramsJSON": "{\"facing\":\"front\"}",
        "enqueuedAtMs": 1737270000000
      }
    ]
  }
}
```

### 8.2 `node.pending.ack`

来源：`NodePendingAckParamsSchema` + `server-methods/nodes.ts`

```ts
type NodePendingAckParams = {
  ids: NonEmptyString[];
};

type NodePendingAckResult = {
  nodeId: string;
  ackedIds: string[];
  remainingCount: number;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_pending_ack",
  "method": "node.pending.ack",
  "params": {
    "ids": ["act_001"]
  }
}
```

```json
{
  "type": "res",
  "id": "req_pending_ack",
  "ok": true,
  "payload": {
    "nodeId": "ios-node-01",
    "ackedIds": ["act_001"],
    "remainingCount": 0
  }
}
```

### 8.3 `node.pending.enqueue`

来源：`NodePendingEnqueueParamsSchema` + `NodePendingEnqueueResultSchema`

```ts
type NodePendingWorkType = "status.request" | "location.request";
type NodePendingWorkPriority = "normal" | "high";

type NodePendingEnqueueParams = {
  nodeId: NonEmptyString;
  type: NodePendingWorkType;
  priority?: NodePendingWorkPriority;
  expiresInMs?: number;
  wake?: boolean;
};

type NodePendingDrainItem = {
  id: NonEmptyString;
  type: NodePendingWorkType;
  priority: "default" | "normal" | "high";
  createdAtMs: UnixMs;
  expiresAtMs?: UnixMs | null;
  payload?: Record<string, unknown>;
};

type NodePendingEnqueueResult = {
  nodeId: NonEmptyString;
  revision: number;
  queued: NodePendingDrainItem;
  wakeTriggered: boolean;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_pending_enqueue",
  "method": "node.pending.enqueue",
  "params": {
    "nodeId": "ios-node-01",
    "type": "location.request",
    "priority": "high",
    "expiresInMs": 60000,
    "wake": true
  }
}
```

```json
{
  "type": "res",
  "id": "req_pending_enqueue",
  "ok": true,
  "payload": {
    "nodeId": "ios-node-01",
    "revision": 3,
    "queued": {
      "id": "work_001",
      "type": "location.request",
      "priority": "high",
      "createdAtMs": 1737271000000,
      "expiresAtMs": 1737271060000
    },
    "wakeTriggered": true
  }
}
```

### 8.4 `node.pending.drain`

来源：`NodePendingDrainParamsSchema` + `NodePendingDrainResultSchema`

```ts
type NodePendingDrainParams = {
  maxItems?: number;
};

type NodePendingDrainResult = {
  nodeId: NonEmptyString;
  revision: number;
  items: NodePendingDrainItem[];
  hasMore: boolean;
};
```

示例：

```json
{
  "type": "req",
  "id": "req_pending_drain",
  "method": "node.pending.drain",
  "params": {
    "maxItems": 5
  }
}
```

```json
{
  "type": "res",
  "id": "req_pending_drain",
  "ok": true,
  "payload": {
    "nodeId": "ios-node-01",
    "revision": 3,
    "items": [
      {
        "id": "work_001",
        "type": "location.request",
        "priority": "high",
        "createdAtMs": 1737271000000,
        "expiresAtMs": 1737271060000
      }
    ],
    "hasMore": false
  }
}
```

## 9. 远程执行语义

来源：[Nodes](https://docs.openclaw.ai/nodes)、[CLI: node](https://docs.openclaw.ai/cli/node)、[CLI: nodes](https://docs.openclaw.ai/cli/nodes)、`server-methods/nodes.ts`

结论：

- `system.run` / `system.which` 是 **node command**
- 它们不是新的顶层帧类型
- `openclaw nodes invoke` 不暴露 `system.run` / `system.run.prepare`
- 壳执行走 `exec host=node`
- `openclaw node run` / `openclaw node install` 接入后，真正的远程执行目标是 node host

协议侧限制：

- `node.invoke` 明确拒绝：
  - `system.run`
  - `system.run.prepare`
  - `system.execApprovals.get`
- `system.which`、`system.notify` 等显式 node command 可以继续通过 `node.invoke`

因此，最新版里“操作 node 执行命令”的协议分成两条线：

1. 显式 capability RPC
   - 例如 `canvas.snapshot`、`camera.snap`、`location.get`、`system.which`
   - 走 `node.invoke`
2. 壳执行 / 子进程执行
   - 例如 `system.run`
   - 走 `exec host=node`
