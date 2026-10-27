# 内部数据后台设计

> 仅供内部使用，不对客户、阿福公开，不写进 `readme.md`。查看安装、上传记录和用量统计；写操作只有 App Key 管理(生成或导入、定价、充值、停用 / 启用)。后台不按 app_key 区分统计。

## 总体方案

| 项目     | 方案                                                                      | 说明                                                                                                                            |
| -------- | ------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| 部署     | 和主服务同一个进程，单独监听一个端口                                      | 公网端口只有 `POST /afu/v1/meter`，内部接口从公网端口根本访问不到                                                               |
| 监听地址 | 配置项 `admin.addr`，示例配置里留空(不启动)；本地开发填 `127.0.0.1:18081` | 生产只绑内网地址并配防火墙                                                                                                      |
| 接口     | `/afu/internal/api/v1/...`，明文json                                      | 查询都是GET；App Key 管理用 POST / PATCH                                                                                        |
| 鉴权     | 固定Token：`Authorization: Bearer <token>`                                | 配置里只存Token的SHA-256，按人分配，审计日志记录是谁                                                                            |
| 数据库   | 配置项 `admin.db_dsn`，建议单独账号                                       | 留空时复用 `db.dsn`；单独账号只给三张数据表 SELECT、`app_keys` 表 SELECT / INSERT / UPDATE、`billing_ledger` 表 SELECT / INSERT |
| 前端     | React 19 + Ant Design 6 + Vite + TypeScript                               | 构建产物用 `go:embed` 打进二进制，由后台端口直接提供                                                                            |
| 时区     | 库里存UTC；接口返回RFC3339(UTC)；页面按北京时间显示                       | 按天统计按北京时间(UTC+8)分天                                                                                                   |

## 配置

在 `configs/config.yaml` 增加：

```yaml
admin:
  addr: "127.0.0.1:18081"   # 只监听内网地址；留空则不启动内部后台
  db_dsn: ""                # 建议填只读账号的DSN；留空时复用 db.dsn
  tokens:                   # 按人分配，name 写进审计日志
    - name: "liujiaxuan"
      token_sha256: "<make admin-token 打印的SHA-256>"
```

1. Token用 `make admin-token` 生成：打印32字节随机hex明文(只发给本人)和它的SHA-256(写进配置)。
2. 比较时先算请求Token的SHA-256，再用 `subtle.ConstantTimeCompare` 和配置逐个比较。
3. 停用某人：删掉配置里那一项并重启。

后台单独账号示例(生产库)：

```sql
CREATE USER 'afu_console'@'10.%' IDENTIFIED BY '<密码>';
GRANT SELECT ON afu.installs TO 'afu_console'@'10.%';
GRANT SELECT ON afu.reports TO 'afu_console'@'10.%';
GRANT SELECT ON afu.measurements TO 'afu_console'@'10.%';
GRANT SELECT, INSERT, UPDATE ON afu.app_keys TO 'afu_console'@'10.%';
GRANT SELECT, INSERT ON afu.billing_ledger TO 'afu_console'@'10.%';
```

## 接口约定

1. 前缀 `/afu/internal/api/v1`(所有接口统一带 `/afu`)；查询用GET，App Key 管理用 POST / PATCH(json请求体，上限4KB)；前端页面仍在根路径。
2. 每个请求都要带 `Authorization: Bearer <token>`，否则返回401。
3. 成功返回http 200 + 数据对象本身；失败返回对应状态码 + `{"code": "...", "msg": "..."}`，和主接口的错误格式一致。
4. 时间参数 `from`、`to` 用RFC3339，例如 `2026-10-26T00:00:00+08:00`，区间左闭右开。按天统计的区间最长92天。
5. 时间字段返回RFC3339(UTC)，例如 `2026-10-26T04:14:52.963Z`；客户端上报的 `ts`、`start_ts`、`end_ts` 原样返回毫秒时间戳。
6. 列表统一用游标分页，按id倒序(新的在前)：
   1. 参数 `limit` 默认20、最大100；`cursor` 为上一页返回的 `next_cursor`。
   2. 返回 `{"items": [...], "next_cursor": "123"}`，没有下一页时 `next_cursor` 为空字符串。
   3. 不返回总数：上报、测量表会很大，`COUNT(*)` 和 `OFFSET` 都慢，总量看「概览」。

| http状态码 | code           | 触发条件                                     |
| ---------- | -------------- | -------------------------------------------- |
| 400        | bad_request    | 参数格式错误、时间区间超过92天、limit超过100 |
| 401        | unauthorized   | 没带Token或Token不对                         |
| 404        | not_found      | 安装记录、上报记录不存在                     |
| 409        | app_key_exists | 导入的 App Key 已经存在                      |
| 500        | internal_error | 内部错误，详细原因只写日志                   |

## 接口列表

| 接口                           | 用途                                                     |
| ------------------------------ | -------------------------------------------------------- |
| `GET /me`                      | 校验Token，返回当前用户名                                |
| `GET /meta`                    | 服务配置(ttl_sec、report_sec、可用kid)和版本             |
| `GET /overview`                | 概览：时间区间内的关键数字                               |
| `GET /stats/daily`             | 按天统计                                                 |
| `GET /installs`                | 安装记录列表                                             |
| `GET /installs/{install_id}`   | 安装记录详情                                             |
| `GET /reports`                 | 上传记录列表：每行一次上报，带解析后的本次计次和累计计数 |
| `GET /reports/{id}`            | 单次上传详情，含解密后的原始明文                         |
| `GET /app-keys`                | App Key 列表(新的在前)                                   |
| `POST /app-keys`               | 生成 App Key；传 `app_key` 时按原值导入                  |
| `PATCH /app-keys/{id}`         | 停用 / 启用、改备注、改单价、透支额度和离线可用次数      |
| `POST /app-keys/{id}/recharge` | 充值                                                     |
| `GET /app-keys/{id}/ledger`    | 计费流水(扣费、充值)，游标分页                           |

### GET /me

```json
{"name": "liujiaxuan"}
```

### GET /meta

```json
{
  "ttl_sec": 0,
  "report_sec": 60,
  "active_kids": [1],
  "version": "d6bdf60"
}
```

`version` 取构建信息里的git提交号(`runtime/debug.ReadBuildInfo` 的 `vcs.revision`)。只返回kid编号，不返回任何密钥内容。

### GET /overview

参数：`from`、`to`，默认今天0点(北京时间)到现在。

```json
{
  "installs_total": 1234,
  "installs_new": 56,
  "reassigned": {"conflict": 1, "unregistered": 2},
  "reports": 789,
  "measurements": {"count": 700, "frames": 210000, "dt_ms": 6976200, "short_count": 3},
  "by_platform": [{"platform": "android", "installs": 800}, {"platform": "ios", "installs": 434}]
}
```

| 字段                        | 统计方式                                                                                                                                              |
| --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| installs_total、by_platform | 全部安装记录，不受时间区间影响                                                                                                                        |
| installs_new                | 区间内创建的安装记录，含换号产生的                                                                                                                    |
| reassigned                  | 区间内创建、reset_reason为conflict / unregistered的安装记录                                                                                           |
| reports                     | 区间内服务端收到的上报数(按received_at)                                                                                                               |
| measurements                | count、frames、dt_ms：区间内收到的上报的增量合计(计次、有效帧数、时长)；short_count：区间内入库的计次记录里 `dt_ms ≤ 5000` 的条数(SDK 不该发，算异常) |

### GET /stats/daily

参数：`from`、`to`(默认近30天)。

```json
{
  "days": [
    {"date": "2026-10-26", "new_installs": 5, "reports": 20, "measurements": 18, "frames": 5400, "dt_ms": 179388}
  ]
}
```

1. 按北京时间分天：`DATE(created_at + INTERVAL 8 HOUR)`。中国没有夏令时，固定偏移即可，不依赖MySQL时区表。
2. 没有数据的日期也返回一行，数值为0，方便画连续的折线。
3. 计次、帧数、时长按每份上报的增量(`reports.delta_*`)求和，按服务端收到的日期算，不用客户端的start_ts(客户端时间可能不准；断网攒下的计次算在补报那天)。一条计次记录只带到计次那一刻的数据，所以不用 measurements 求和。

### GET /installs

参数：`install_id`(精确匹配)、`platform`、`reset_reason`、`from`、`to`(按创建时间)、`cursor`、`limit`。`reset_reason` 传 `first` 表示首次安装(库里为空字符串)。

```json
{
  "items": [
    {
      "install_id": "2638d8400d60c9fedfb1e8bdf92af87a",
      "app_id": "com.example.app",
      "platform": "android",
      "sdk": "1.1.0-beta.1",
      "reset_reason": "",
      "confirmed_seq": 1,
      "counters": {"sessions": 1, "frames_ok": 300, "dt_ms": 9966, "mono_ms": 9968, "run_ok": 10, "gated_frames": 0, "gated_runs": 0},
      "created_at": "2026-10-26T04:14:52.963Z",
      "updated_at": "2026-10-26T04:14:52.972Z"
    }
  ],
  "next_cursor": ""
}
```

### GET /installs/{install_id}

返回单条，字段同上；不存在返回404。

### GET /reports

参数：`install_id`、`from`、`to`(按received_at)、`max_dt_ms`(只看带有时长不超过该值的计次记录的上报，传5000即含异常计次)、`cursor`、`limit`。`max_dt_ms` 按 install_id + seq 关联measurements表。列表直接带上报内容：从明文解析出的 `counters`(累计值)和 `measurements`(本次上报带的计次记录，空上报为 `[]`)，以及从计费流水取的本次扣费 `charge`(0.0001元，没扣费为0)；不带原始明文，原文看详情接口。

```json
{
  "items": [
    {"id": 2, "install_id": "2638d8400d60c9fedfb1e8bdf92af87a", "seq": 1, "client_ts": 1792478748241,
     "received_at": "2026-10-26T04:14:52.972Z",
     "counters": {"sessions": 1, "frames_ok": 151, "dt_ms": 5005, "mono_ms": 5007, "run_ok": 5, "gated_frames": 0, "gated_runs": 0},
     "measurements": [{"no": 1, "start_ts": 1792478738236, "end_ts": 1792478743241, "frames": 151, "dt_ms": 5005}],
     "charge": 5000}
  ],
  "next_cursor": ""
}
```

### GET /reports/{id}

在列表字段之外加上 `payload`：解密后的原始明文，作为json对象返回。MySQL的JSON列会重排字段顺序，内容不变。

```json
{
  "id": 2,
  "install_id": "2638d8400d60c9fedfb1e8bdf92af87a",
  "seq": 1,
  "client_ts": 1792478748241,
  "received_at": "2026-10-26T04:14:52.972Z",
  "payload": {"install_id": "2638d8400d60c9fedfb1e8bdf92af87a", "seq": 1, "counters": {"frames_ok": 300}, "measurements": []}
}
```

### App Key 管理

`POST /app-keys` 请求体：

```json
{"remark": "阿福正式环境", "app_key": "", "unit_price": 5000, "initial_balance": 1000000, "overdraft_limit": 100000,
 "offline_runs": 5}
```

1. `app_key` 留空：生成 `AK-` 加32位小写hex。
2. `app_key` 有值：按原值导入，用于已经发给客户的 App Key；只能包含字母、数字、`-`、`_`，最多64个字符；已存在返回409。
3. 金额一律用整数，单位 0.0001 元(上例：单价0.50元/次、初始余额100元、透支额度10元)。`unit_price` 0或不传表示不收费；`initial_balance` 大于0时记一条「初始余额」充值流水。
4. `offline_runs` 离线可用次数，ack / refused 的 cap_runs 直接下发这个值，含义是「从 acked_sessions 往后还能新增几次计次」；不传默认5，范围0 ~ 16(一份上报最多16条 measurements，离线攒下的计次要能一份装下)；0 相当于停发。
5. 返回201和新记录；`created_by` 记录后台Token的name。

`PATCH /app-keys/{id}` 请求体，下面字段至少一个：

```json
{"status": "disabled", "remark": "已停用", "unit_price": 6000, "overdraft_limit": 200000, "offline_runs": 8}
```

1. 不提供删除，停用即可；停用后带这个 App Key 的注册、上报返回403。
2. 余额不能在这里改，只能通过充值和上报扣费变化，保证和流水对得上；新单价从下一次上报开始生效，新的离线可用次数从下一次回复开始下发(已经放出去的次数不会收回)。
3. 生成、修改、充值都会写一条操作日志(操作人、App Key、金额)，另外照常有审计日志。

返回结构：

```json
{"id": 1, "app_key": "AK-f24277919892cdc6a5670c27d6fe1212", "remark": "阿福正式环境", "status": "enabled",
 "unit_price": 5000, "balance": 383000, "overdraft_limit": 100000, "used_count": 1234, "offline_runs": 5, "billing_state": "normal",
 "created_by": "liujiaxuan", "created_at": "2026-10-26T11:17:17.222Z", "updated_at": "2026-10-26T11:17:17.222Z"}
```

`billing_state`：`free` 不收费；`normal` 余额不为负；`overdraft` 余额为负但还够付一次；`exhausted` 余额加透支额度不够付下一次，停发额度。

`POST /app-keys/{id}/recharge` 请求体：

```json
{"amount": 500000, "remark": "10月续费"}
```

`amount` 必须大于0(上例50元)；锁住这个 App Key 的行加余额，记一条充值流水(操作人为后台Token的name)，返回更新后的 App Key。

`GET /app-keys/{id}/ledger` 返回计费流水，按id倒序，参数同列表的 `cursor`、`limit`：

```json
{
  "items": [
    {"id": 6, "kind": "recharge", "amount": 50000, "balance_after": 40000, "count": 0, "unit_price": 0,
     "install_id": "", "seq": 0, "operator": "liujiaxuan", "remark": "续费", "created_at": "2026-10-27T11:29:42.000Z"},
    {"id": 5, "kind": "consume", "amount": -5000, "balance_after": -10000, "count": 1, "unit_price": 5000,
     "install_id": "d625747c96a74f6c4ba7e2a6b8c3e597", "seq": 4, "operator": "", "remark": "", "created_at": "2026-10-27T11:29:41.000Z"}
  ],
  "next_cursor": ""
}
```

## 索引

上线前直接加进 `migrations/001_init.sql`；上线后改表一律新增迁移文件。

```sql
-- installs
KEY idx_created_at (created_at)
-- reports
KEY idx_received_at (received_at)
-- measurements
KEY idx_created_at (created_at)
```

1. 按install_id查安装、上报、测量用已有的唯一索引；按id倒序翻页走主键。
2. 数据量上来后，按天统计可以改成预先汇总。

## 后端实现

```text
internal/console/
├── server.go     # 后台Gin引擎：中间件(审计、recovery、安全响应头)、路由、静态文件
├── auth.go       # Token校验
├── handlers.go   # 各查询接口
├── appkeys.go    # App Key 管理(生成或导入、定价、充值、流水、停用 / 启用)
├── params.go     # 参数解析(时间区间、游标、limit)
└── types.go      # 响应结构体
web/
├── embed.go      # package web，//go:embed all:dist
└── ...           # 前端工程，见「前端」
```

1. `cmd/server` 在 `admin.addr` 不为空时，另起一个 `http.Server` 跑后台引擎，和主服务一起优雅退出。
2. 后台引擎的中间件顺序：访问与审计日志 → recovery → 鉴权(只作用于 `/afu/internal/api`)。
3. 审计日志每个请求一条：用户名、方法、路径、查询参数、状态码、耗时；鉴权失败也记，用户名为空。
4. 静态文件：`/afu/internal/api` 以外的GET请求返回 `web/dist` 里的文件；找不到的路径返回 `index.html`，交给前端路由处理。
5. `web/dist` 只提交一个占位文件 `.keep`，保证没构建前端时 `go build`、`go test` 也能通过；这时访问页面显示「前端未构建」。
6. 响应头加上 `Content-Security-Policy: default-src 'self'; style-src 'self' 'unsafe-inline'`(Ant Design的样式是运行时注入的)和 `X-Frame-Options: DENY`。
7. 查询只用GORM的读方法；单条查询带上 `context` 超时(如5秒)，防止慢查询拖住连接。

## 前端

### 技术栈

| 项目   | 选型                                                  |
| ------ | ----------------------------------------------------- |
| 框架   | React 19 + TypeScript                                 |
| 组件库 | Ant Design 6                                          |
| 图表   | `@ant-design/plots`                                   |
| 路由   | React Router 7                                        |
| 时间   | dayjs(utc、timezone插件，统一按 `Asia/Shanghai` 显示) |
| 构建   | Vite 8，产物输出到 `web/dist`                         |
| Node   | 24 LTS(本机已安装 v24.15.0)                           |

```text
web/
├── index.html
├── package.json
├── vite.config.ts        # dev 时把 /afu/internal/api 代理到 127.0.0.1:18081
└── src/
    ├── main.tsx
    ├── App.tsx           # 路由、整体布局、登录守卫
    ├── api/
    │   ├── client.ts     # fetch封装：带Token、401时清Token跳登录
    │   ├── useApi.ts     # 请求hook：loading、error、过期结果丢弃
    │   └── types.ts      # 和接口一一对应的类型
    ├── pages/
    │   ├── Login.tsx
    │   ├── Overview.tsx
    │   ├── Usage.tsx
    │   ├── Installs.tsx
    │   ├── InstallDetail.tsx
    │   ├── Uploads.tsx       # 上传记录列表页
    │   └── AppKeys.tsx       # App Key 管理
    ├── components/
    │   ├── CursorTable.tsx   # 游标分页表格：上一页 / 下一页
    │   ├── TimeRange.tsx     # 今天 / 近7天 / 近30天 / 自定义
    │   ├── DailyCharts.tsx   # 按天柱状图：计次、测量时长
    │   ├── UploadsTable.tsx  # 上传记录表格(带明文抽屉)，列表页和安装详情共用
    │   └── Common.tsx        # install_id、新安装原因、时长标签、原始明文展示
    └── utils/
        ├── time.ts       # 北京时间格式化、毫秒时间戳转换
        ├── format.ts     # 时长、千分位
        └── utils.test.ts
```

### 页面

| 路由                   | 页面     | 内容                                                                                                                                                                                                                                                         |
| ---------------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `/login`               | 登录     | 输入Token，调 `/me` 校验通过后存入sessionStorage(关闭标签页即失效)                                                                                                                                                                                           |
| `/`                    | 概览     | 时间区间选择；关键数字卡片；按天的计次和时长柱状图；平台分布                                                                                                                                                                                                 |
| `/usage`               | 用量统计 | 时间区间选择；按天柱状图；按天表格(新的日期在前)，表尾是区间合计                                                                                                                                                                                             |
| `/installs`            | 安装     | 筛选(install_id、平台、新安装原因、创建时间) + 表格；每行有「查看上传记录」按钮(点整行也可以)，进入安装详情                                                                                                                                                  |
| `/installs/:installId` | 安装详情 | 左上角「返回」(回到进来前的页面，直接打开链接时回到安装列表)；基本信息、已确认seq和counters；「上传记录」列出该编号全部已入库的上报，内容同上传记录页                                                                                                        |
| `/uploads`             | 上传记录 | 筛选(install_id、收到时间、只看含5秒及以内计次的) + 表格：每行一次上传，直接显示本次计次、本次扣费和累计计数，空上报单独标出；点「查看明文」在抽屉里看原文                                                                                                   |
| `/app-keys`            | App Key  | 列表：单价、余额(负数标红)、透支额度、计费次数、离线次数、计费状态；「生成 App Key」弹窗(备注、可选的导入值、单价、透支额度、离线可用次数、初始余额)；「充值」；「修改配置」(单价、透支额度、离线可用次数)；「流水」抽屉；停用需二次确认，已停用的可重新启用 |

左侧菜单：概览、用量统计、安装、上传记录、App Key；顶部右侧显示服务版本、当前用户名和「退出」。

按天的图用柱状图：只有一天的数据时折线画不出来；日期只显示「月-日」，放不下时自动隐藏。

概览：

```text
┌──────────────────────────────────────────────────────────────────┐
│ [今天] [近7天] [近30天] [自定义 ▾]                               │
├──────────┬──────────┬──────────┬──────────┬──────────┬───────────┤
│ 安装总数 │ 新增安装 │ 上传次数 │ 计次     │ 测量时长 │ 异常计次  │
│ 1,234    │ 56       │ 789      │ 700      │ 1:56:16  │ 3         │
├──────────┴──────────┴──────────┴──────────┴──────────┴───────────┤
│ 换号：冲突 1 / 未注册 2                                          │
├────────────────────────────────────────────┬─────────────────────┤
│ 计次、测量时长(按天柱状)                   │ 平台分布            │
│                                            │ android 800         │
│                                            │ ios     434         │
└────────────────────────────────────────────┴─────────────────────┘
```

安装详情：

```text
┌──────────────────────────────────────────────────────────────────────┐
│ 2638d8400d60c9fedfb1e8bdf92af87a [复制]                              │
│ app_id com.example.app    平台 android                               │
│ sdk 1.1.0-beta.1   新安装原因 首次   创建 2026-10-26 12:14:52        │
├──────────────────────────────────────────────────────────────────────┤
│ 已确认 seq 1   累计计次 1   有效帧数 151   时长 5.01秒   run 5       │
├──────────────────────────────────────────────────────────────────────┤
│ 上传记录                                                             │
├─────┬─────────────────────┬─────────────────────┬────────────────────┤
│ seq │ 客户端时间          │ 服务端收到时间      │ 操作               │
│ 1   │ 2026-10-26 12:14:52 │ 2026-10-26 12:14:52 │ 查看明文           │
├─────┴─────────────────────┴─────────────────────┴────────────────────┤
│                                                    [上一页] [下一页] │
└──────────────────────────────────────────────────────────────────────┘
```

### 展示约定

1. 时间统一显示北京时间 `YYYY-MM-DD HH:mm:ss`；客户端毫秒时间戳显示成时间，悬停显示原始值。
2. 时长：`dt_ms` 小于1分钟显示「9.97秒」，否则显示「1:56:16」。
3. 数字加千分位；install_id用等宽字体并带复制按钮。
4. 计次记录的时长不超过5秒的标橙色(异常)；新安装原因为conflict / unregistered的标蓝色。
5. 原始明文按纯文本格式化显示，全项目禁止 `dangerouslySetInnerHTML`。

## 开发与构建

| 目标               | 作用                                                                  |
| ------------------ | --------------------------------------------------------------------- |
| `make admin-token` | 生成一个Token：打印明文和SHA-256                                      |
| `make web-dev`     | `cd web && npm run dev`，Vite开发服务器，接口代理到 `127.0.0.1:18081` |
| `make web`         | `cd web && npm ci && npm run build`，产物到 `web/dist`                |
| `make build`       | 先 `make web` 再 `make build-go`，前端打进二进制                      |
| `make build-go`    | 只编译Go；没构建前端时访问页面显示「前端未构建」                      |

## 测试

1. 后端单元测试：鉴权(无Token、错误Token、正确Token)、参数解析(时间格式、区间上限、limit上限、游标)。
2. 后端集成测试(`AFU_TEST_DSN`，和主流程的集成测试共用测试库，`make itest` 用 `-p 1` 串行跑)：造几条安装、上报、测量，校验每个接口的统计数字、过滤条件、翻页不重不漏、按北京时间分天；App Key 的生成、导入、重复导入、停用、定价、充值、流水与计费状态；上传记录的本次扣费。
3. 前端：`tsc --noEmit` 类型检查和构建必须通过；`utils/` 里的时间、时长格式化写单元测试(Vitest)。

## 开发顺序

| 阶段 | 内容                                      | 验收                                                                           |
| ---- | ----------------------------------------- | ------------------------------------------------------------------------------ |
| 1    | 配置、Token鉴权、审计日志、后台监听、索引 | 无Token返回401，正确Token访问 `/me` 返回用户名                                 |
| 2    | 查询接口：installs、reports、measurements | 集成测试覆盖过滤和翻页                                                         |
| 3    | 统计接口：overview、stats/daily           | 集成测试校验统计数字和分天                                                     |
| 4    | 前端：登录、布局、安装 / 上传记录页面     | 本地 `make web-dev` 能查到fakeclient造的数据                                   |
| 5    | 前端：概览、用量统计图表                  | 同上                                                                           |
| 6    | 前端打进二进制，`make build` 一个产物     | 只启动server就能打开后台                                                       |
| 7    | App Key 管理：接口 + 页面                 | 集成测试覆盖生成、导入、重复导入409、停用、定价、充值、流水；停用后注册返回403 |

## 后续(本期不做)

1. 计数变小的异常：现在只写日志，要在后台展示需要新增异常表，由上报处理写入。
2. 导出CSV。
3. 客户、额度、扣费相关的页面：等计费细则同步后再设计。
