# Texas Hold'em — MVP 骨架

在线德州扑克 MVP 实现。本提交是**最小可跑骨架**（贯穿线版本），目标是跑通端到端主干：

> 游客登录 → 大厅 → 快速入桌 → 服务端发 2 张底牌 → 客户端 PixiJS 渲染

详细 MVP 规划见 [LIU-12](https://github.com/liuqiangssss/Texas-Hold-em) issue。

## 目录

```
Texas-Hold-em/
├── server/   Go 后端（WebSocket 网关 + 一桌一 actor + 发牌）
└── web/      Web 客户端（React 18 + Vite + PixiJS + TypeScript + Zustand）
```

## 快速开始

### 方式一：Docker Compose 一键启动（推荐）

```bash
docker compose up --build       # 首次或代码变更后重新构建并启动
docker compose up -d            # 后续直接后台启动（已构建过）
docker compose down             # 停止并清理容器（保留数据卷）
docker compose down -v          # 停止并清理容器 + 删除 mongo 数据
```

启动后访问 [http://localhost:8080](http://localhost:8080)。
nginx 会把 `/ws` 反代到内网的 server 容器，浏览器只暴露一个端口，不需要单独配 CORS。

服务编排：
- `mongo`：MongoDB 7，仅在 docker 内网暴露 `27017`，数据落 `mongo_data` 卷持久化。手历落库（S3.9）将通过 `MONGO_URI=mongodb://mongo:27017` / `MONGO_DB=texas` 写入。
- `server`：Go 网关，依赖 `mongo` healthy 后启动。
- `web`：nginx + React 静态资源，反代 `/ws` 到 server。

### 方式二：本地分别运行（开发）

后端：
```bash
cd server
go run ./cmd/server
# listening on :8080
```

前端：
```bash
cd web
npm install
npm run dev
# http://localhost:5173
```

打开浏览器 → 输入昵称 → 点"游客登录" → 点"快速开始（5/10 桌）" → 等另一端也入桌（或开第二个浏览器标签）→ 看到底牌翻出。

## MVP 骨架覆盖范围

本骨架只实现贯穿主干，**不包含**以下 MVP 功能（后续 Story 填充）：

- 完整下注流程（Check/Call/Bet/Raise/All-in 合法性校验）
- 边池 (Side Pot) 计算
- 牌型比较与摊牌
- 时间银行与托管
- 金币账户、教学、签到
- 反作弊、设备指纹、同 IP 校验
- 手历落库（Mongo）、Redis 状态恢复
- 运营后台

## 架构要点

- **一桌一 goroutine actor**：每张牌桌是一个独立 goroutine，通过 channel 串行处理消息，规避并发竞态。
- **服务端权威**：客户端只发"意图"（action + amount），服务端校验后广播。
- **底牌私密下发**：`deal_hole` 消息仅通过目标玩家的 WebSocket 通道发送。
- **前后端协议共享类型**：`server/internal/proto/*.go` ↔ `web/src/proto/messages.ts`（手动对齐；后续可用 codegen）。

## 许可

MIT
