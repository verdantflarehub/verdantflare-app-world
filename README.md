# verdantflare-app-world

VerdantFlare AppCenter 数字世界应用。

## 1. 当前范围

`dashboard/` 是独立 Vue 3／Vite 浏览页面，消费 Studio 注入的 AppSDK 1.1 和 DashboardHost；不依赖 Studio 源码或 Wails runtime。保留资产目录、固定版本详情及来源文件展示。“带入项目”通过宿主可选 `navigation.openApp` 把确切资产版本交给 Project，由 Project 选择可写目标、确认用途并提交；World 不直接写入其他安装的业务接口。真实跨应用与 Web／Wails 验证尚未完成。

`/publish` 承接 Project 的固定准备定位，读取经服务端核对的版本和文件范围；用户确认后持久化原请求与摘要，再调用本域注册／状态接口。响应丢失时复用同一入库操作，已知资产 ID 后优先查询原状态，不依赖 Project 再次在线或重复注册。确认成功仅把固定资源定位返回 Project，由 Project 校验回执并保存关联。实现与接口依据见中央拆分方案第 7.4 节。

`cmd/world` 实现 World 自有资产、版本、授权、提交和 Artifact Authority，固定 Project 来源与内容授权经受控 Project API 读取，不直接读取 Project 业务表。递归 World 依赖返回精确内容及授权集合；提交和撤权使用持久授权保留，发布 CAS 与协作终态／outbox 在同一本域事务内落盘。原操作的 fence 保存在准备计划中，恢复不会按新代次重新创建旧操作。

撤权或降权遇到准备中的提交时返回 `AUTHORIZATION_CHANGE_PENDING`，保留旧角色及新 prepare 屏障，同时保存原请求者和目标角色；原授权调用方重试才能完成。超时、服务失联或未知协调者状态不会释放保留，也不会把撤权误报成功。

当前提供新库初始化与独立服务源码，尚未接管旧数据、应用入口或发现注册；真实服务故障验证、可信安装、Web／Wails 和独立升级未完成。旧联合服务迁移须按中央方案冻结写入、排空提交、校验 ID／摘要及来源证明，再切换唯一写入者。`migrations/legacy/` 原文仅供校验，不由新迁移器执行。

## 2. 检查与交付

使用 Go 1.26、Node.js 22 和 pnpm 9.15.9。安装前端依赖执行 `pnpm --dir dashboard install --frozen-lockfile --ignore-scripts`。

- 本机轻量检查：`pnpm --dir dashboard typecheck`。
- 无服务单元测试：`pnpm --dir dashboard test`。
- 后端纯单元与静态检查：`go test ./...`、`go vet ./...`。
- 数据库测试编译检查：`go test -tags integration ./... -run '^$'`；真实测试仅在 5090 dev 配置受控 `WORLD_TEST_DATABASE_URL` 后运行，测试创建并清理独立随机数据库，不使用业务数据库。
- Actions 编译入口：`pnpm --dir dashboard build`，生成相对路径前端资源及绑定真实 SHA-256 的索引与 Dashboard 声明。该命令不等于已执行发布。

生成声明的稳定 appId 为 `world`，最低 AppSDK 为 `1.1.0`。声明只申请能力；部署登记、服务权限与制品校验由平台完成，构建产物不进入 Git。

`release` 触发 `.github/workflows/world.yml`，构建并推送不可覆盖的 `wod/verdantflare-app:world-v0.1.0`，复用 GHA 缓存。构建未在本机执行；流水线存在不代表已发布。

## 3. 启动与配置

`world migrate-fresh` 仅初始化全新 World 数据库，写门默认关闭；全新空环境另行显式执行 `world activate-fresh`。显式提供已由外部恢复流程确认的 `PROJECT_COORD_FENCE`、`WORLD_COORD_FENCE`；重试只校验一致性，不提高或回退代次。遇到旧 World 表直接拒绝，不自动删除、重建或接管。`world serve` 校验迁移、栅栏与已开放写门后启动，后台恢复 preparing 提交及终态通知。旧数据使用中央接管工具，按共同归档逐域导入，不在应用运行时连接旧库或 Project 库。

迁移账户与运行账户独立。运行账户不得是超级用户、Schema／表所有者，不得创建 Schema 对象或修改写门、迁移历史及外部 fence。运行账户需要本域数据库 CONNECT、`studio` Schema USAGE、业务／协调表适用权限，以及固定只读锁函数 `studio.world_locked_fence(text)` 的显式 EXECUTE；该函数不向运行账户授予 fence 写权限。数据库触发器同时保护业务与协调账本；关闭写门后，晚到的恢复或回调不能继续写入。

- 数据库：`WORLD_DATABASE_URL`，账号只拥有 World 业务／协作表。
- 监听及 TLS：`WORLD_LISTEN`、`WORLD_TLS_CERT`、`WORLD_TLS_KEY`；非 loopback 必须 TLS。
- 独立 MCP：`WORLD_MCP_LISTEN`、`WORLD_MCP_ADVERTISE_URL`、`ETCD_ENDPOINTS`；三项同时缺省时关闭 MCP，部分配置拒绝启动。
- 服务及 Artifact Authority：`WORLD_SERVICE_TOKEN`、`WORLD_AUTHORITY_TOKEN`。
- 协作身份：`WORLD_COORD_TOKEN` 作为 World 调用 Project 的身份，`PROJECT_COORD_TOKEN` 仅接收 Project 协作；两域代次分别为 `WORLD_COORD_FENCE`、`PROJECT_COORD_FENCE`。
- Project：`PROJECT_SERVICE_URL`、`PROJECT_SERVICE_TOKEN`、可选 `PROJECT_CA_FILE`。
- Artifact：`ARTIFACT_URL`、`ARTIFACT_SERVICE_TOKEN`、可选 `ARTIFACT_CA_FILE`。
- Dashboard：`WORLD_DASHBOARD_DIR` 默认 `/app/dashboard`；启动时核对声明、索引及全部资源的真实 SHA-256，受控服务凭据和主体头访问 `/internal/dashboard/declaration`、`index`、`navigation` 和 `assets/*`。

上述六类令牌必须独立，由环境或受控 Secret 注入。Project、Artifact 与协作客户端禁用环境代理及重定向，内部 HTTPS 可配置 CA。业务监听拒绝 `/mcp`，独立 MCP 监听复用 TLS 证书且必须使用不同端口；部署模板分别采用业务 `8443`、MCP `8444`。

服务独立维护 `/verdantflare/mcp/services/world`，只登记本域工具、制品版本及内部 `/mcp` 地址，不登记凭据；不同现存 endpoint 不自动覆盖。旧 publisher 退出及唯一写入者确认后才切换登记，MCP 仅允许 Studio 来源访问。完整切换步骤遵循中央拆分方案，不新增公网入口。

Dashboard 自有 POST `/api/v1/world/list`、`get` 与既有 World 操作经同一服务权限边界执行。跨域 Project 的 `open`、`authorize_content` 及 World 的 `references`、`commit_receipt` 为内部 API，业务契约归中央仓库。

已有独立库若仅应用 `0001`／`0002`，由迁移账户显式运行 `world migrate-gates` 追加 `0003_locked_fence.sql`；不修改原历史、gate 状态或外部 fence。正常运行不会自动升级。

## 4. 中央设计

- [Project／World 服务拆分与页面准备](https://github.com/verdantflarehub/verdantflare-design/blob/dev/changes/change_20261011_project_world_service_split.md)。
- [Project／World 旧数据接管与写门](https://github.com/verdantflarehub/verdantflare-design/blob/dev/changes/change_20261011_project_world_handoff.md)。
- [AppDashboard 接入契约](https://github.com/verdantflarehub/verdantflare-design/blob/dev/docs/design/contracts/app-dashboard-v1.md)。
- [Project／World 操作契约](https://github.com/verdantflarehub/verdantflare-design/blob/dev/docs/design/contracts/project-world-v1.md)。
- [GitHub Actions 指南](https://github.com/verdantflarehub/verdantflare-design/blob/dev/docs/GithubAction.md)。

产品与 API 契约以中央设计仓库为事实源，本仓库只维护实现、检查与构建入口。
