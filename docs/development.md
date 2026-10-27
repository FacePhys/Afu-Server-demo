# 开发文档(Go)

> 协议、报文字段、处理流程以根目录 `readme.md` 为准，本文只写 Go 实现方式。两者冲突时先改 `readme.md`。

## 技术选型

| 项目       | 选型                                                              | 说明                                                                                |
| ---------- | ----------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| 语言       | Go 1.27                                                           | 最低要求 1.25(Gin v1.12等依赖要求；代码用到 1.24 的 `cipher.NewGCMWithRandomNonce`) |
| http框架   | Gin                                                               | 只有一个路由 `POST /afu/v1/meter`                                                   |
| 数据库     | MySQL 8(InnoDB，utf8mb4)                                          | 需要事务和行锁(`SELECT ... FOR UPDATE`)                                             |
| 数据库访问 | GORM(`gorm.io/gorm` + `gorm.io/driver/mysql`)                     | 建表仍用SQL文件，不用AutoMigrate；锁和冲突处理显式写Clauses，见「GORM使用约定」     |
| 加解密     | 标准库 `crypto/rsa`、`crypto/aes`、`crypto/cipher`、`crypto/rand` | 不引入第三方加密库                                                                  |
| 配置       | YAML(`gopkg.in/yaml.v3`)，数据库连接串可用环境变量覆盖            |                                                                                     |
| 日志       | 标准库 `log/slog`，JSON输出                                       |                                                                                     |
| 测试       | 标准库 `testing` + `net/http/httptest`；集成测试连本地MySQL       |                                                                                     |

## 目录结构

```text
.
├── cmd/
│   ├── server/main.go        # 入口：加载配置、初始化依赖、启动http、优雅退出
│   └── fakeclient/main.go    # 模拟SDK：联调、端到端测试
├── internal/
│   ├── config/               # 配置加载
│   ├── keystore/             # kid → rsa私钥
│   ├── envelope/             # ct的解密、加密(rsa-oaep + aes-gcm + aad)
│   ├── protocol/             # 报文结构体、字段校验、错误码、类型常量
│   ├── handler/              # Gin路由、「通用处理」10步
│   ├── service/              # 注册处理、上报处理、换号、cap计算、install_id生成
│   ├── billing/              # 计费接口：KeyBilling 按次计费(单价 × 新增测量次数，从余额扣)，其余规则留桩
│   ├── store/                # MySQL读写
│   └── console/              # 内部数据后台的只读接口，见 docs/internal-console.md
├── web/                      # 内部数据后台前端(React + Ant Design)，构建产物打进二进制
├── migrations/               # 建表：001_init.sql、002_app_keys.sql，按文件名顺序执行
├── configs/config.example.yaml
├── keys/                     # 本地私钥，不提交
├── docker-compose.yml        # 本地MySQL
├── Dockerfile                # 多阶段构建：前端 → Go交叉编译 → alpine运行镜像
├── deploy/                   # 生产部署：docker-compose、配置模板、密钥/Token脚本、nginx示例、部署说明
├── Makefile
└── go.mod                    # module github.com/FacePhys/Afu-Server-demo
```

依赖方向：`handler → service → store / billing`，`handler → envelope / keystore / protocol`。`service` 不感知http和加密，方便单测。

## 配置

`configs/config.example.yaml`(复制为 `configs/config.yaml` 使用，后者不提交)：

```yaml
server:
  addr: ":8080"
  max_body_bytes: 65536        # 请求体上限
  read_timeout: 5s
  write_timeout: 5s

db:
  dsn: "afu:afu@tcp(127.0.0.1:3306)/afu?parseTime=true&loc=UTC&charset=utf8mb4"  # 环境变量 AFU_DB_DSN 优先
  max_open_conns: 50
  max_idle_conns: 10

keys:                           # 只加载 status = active 的密钥，其余kid一律按 unknown_kid 处理
  - kid: 1
    private_key_file: "keys/kid1.pem"
    status: active

meter:
  per_measure_frames: 300       # 单次测量额度(帧)，等算法给值
  per_measure_runs: 10          # 单次测量额度(run)
  ttl_sec: 86400                # 原样下发
  report_sec: 60                # 原样下发

log:
  level: info
```

启动时校验：每个active密钥都能解析成rsa私钥、位数为2048；任何一项失败直接退出，不带病启动。

## 数据库

`migrations/001_init.sql`(用 `IF NOT EXISTS`，重复执行不报错；集成测试用 `store.ApplyMigrations` 按文件名顺序执行全部迁移文件建表)：

```sql
CREATE TABLE IF NOT EXISTS installs (
  id              BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  install_id      CHAR(32)        NOT NULL,
  app_key         VARCHAR(64)     NOT NULL,
  app_id          VARCHAR(255)    NOT NULL,
  platform        VARCHAR(16)     NOT NULL,
  sdk             VARCHAR(32)     NOT NULL,
  reset_reason    VARCHAR(16)     NOT NULL DEFAULT '',   -- ''/corrupt/conflict/unregistered
  confirmed_seq   BIGINT UNSIGNED NOT NULL DEFAULT 0,
  c_sessions      BIGINT UNSIGNED NOT NULL DEFAULT 0,    -- 已确认counters，只增不减
  c_frames_ok     BIGINT UNSIGNED NOT NULL DEFAULT 0,
  c_dt_ms         BIGINT UNSIGNED NOT NULL DEFAULT 0,
  c_mono_ms       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  c_run_ok        BIGINT UNSIGNED NOT NULL DEFAULT 0,
  c_gated_frames  BIGINT UNSIGNED NOT NULL DEFAULT 0,
  c_gated_runs    BIGINT UNSIGNED NOT NULL DEFAULT 0,
  created_at      DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_install_id (install_id),
  KEY idx_app_key (app_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS reports (
  id           BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  install_id   CHAR(32)        NOT NULL,              -- 换号时记新编号
  seq          BIGINT UNSIGNED NOT NULL,
  client_ts    BIGINT          NOT NULL,
  payload      JSON            NOT NULL,              -- 解密后的原始明文
  received_at  DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_install_seq (install_id, seq)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS measurements (
  id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  install_id  CHAR(32)        NOT NULL,
  measure_no  BIGINT UNSIGNED NOT NULL,               -- 对应 measurements[].no
  seq         BIGINT UNSIGNED NOT NULL,               -- 所属上报
  start_ts    BIGINT          NOT NULL,
  end_ts      BIGINT          NOT NULL,
  frames      BIGINT UNSIGNED NOT NULL,
  dt_ms       BIGINT UNSIGNED NOT NULL,
  created_at  DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_install_no (install_id, measure_no)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

1. 列名避开MySQL关键字：`reset` → `reset_reason`，`no` → `measure_no`。
2. App Key 表在 `migrations/002_app_keys.sql`(app_key 列用 utf8mb4_bin，区分大小写)，由内部后台生成或导入；`003_billing.sql` 加了单价 `unit_price`、余额 `balance`、透支额度 `overdraft_limit`、累计计费次数 `used_count` 和计费流水表 `billing_ledger`；金额一律用整数，单位 0.0001 元。
3. 客户、额度、扣费流水的表等计费细则同步后再设计，现在不建。
3. 写measurements要忽略重复的no：`Clauses(clause.OnConflict{DoNothing: true})`，在MySQL下生成 `ON DUPLICATE KEY UPDATE id=id`。不要用 `INSERT IGNORE`，它会把截断等其他错误也吞掉。
4. 唯一键冲突判断：开启 `TranslateError` 后用 `errors.Is(err, gorm.ErrDuplicatedKey)`，驱动会把MySQL错误码1062转换成它。
5. `reports.payload` 在Go模型里用 `string`，不要用 `[]byte`：驱动按binary字符集发送 `[]byte`，写JSON列会报错3144。

## GORM使用约定

已用 gorm v1.31.2 + driver/mysql v1.6.0 的DryRun确认过下面几条生成的SQL。

1. 建表、改表只用 `migrations/*.sql`，不用 `AutoMigrate`。
2. 更新一律用map(或 `Select` 列出字段)。用结构体 `Updates` 时零值字段会被跳过，例如余额扣到0时不会写入，库里还是旧余额；禁止用 `Save`，它会覆盖整行。
3. 加锁写 `Clauses(clause.Locking{Strength: "UPDATE"})`，生成 `SELECT ... FOR UPDATE`；查单行用 `Take`，不用 `First`(它会多加 `ORDER BY` 主键)。
4. 事务用 `db.Transaction(fn, &sql.TxOptions{Isolation: sql.LevelReadCommitted})`。闭包里只能用参数 `tx`，误用外层 `db` 的语句不在事务里；如果它去更新事务已加锁的行，会一直等锁，默认50秒后报错1205。
5. 模型字段全部写明 `gorm:"column:..."`，表名用 `TableName()` 指定，不依赖GORM的命名推断。
6. 用于更新的map每次新建，不能做成包级变量复用，避免多个goroutine同时读写同一个map。
7. 在Go里算好值再写回的更新(比如已确认counters)，必须放在已经 `FOR UPDATE` 加锁的同一个事务里，否则并发请求会互相覆盖。余额这类累加、扣减的字段用 `gorm.Expr("balance - ?", n)` 让数据库原子计算。

初始化：

```go
db, err := gorm.Open(mysql.Open(cfg.DB.DSN), &gorm.Config{
	TranslateError:         true, // 1062 → gorm.ErrDuplicatedKey
	SkipDefaultTransaction: true, // 单条写入不自动包事务，需要事务的地方显式写
	// ignoreDuplicateKey 包一层：注册时编号已存在是换号的正常分支，唯一键冲突不记错误日志
	Logger: ignoreDuplicateKey{logger.New(slogWriter, logger.Config{
		SlowThreshold:             200 * time.Millisecond,
		IgnoreRecordNotFoundError: true, // 查不到安装记录是正常分支，不打错误日志
		ParameterizedQueries:      true, // 日志里不带参数值
		LogLevel:                  logger.Warn,
	})},
})
```

模型(internal/store)：

```go
type Install struct {
	ID           uint64    `gorm:"column:id;primaryKey"`
	InstallID    string    `gorm:"column:install_id"`
	AppKey       string    `gorm:"column:app_key"`
	AppID        string    `gorm:"column:app_id"`
	Platform     string    `gorm:"column:platform"`
	SDK          string    `gorm:"column:sdk"`
	ResetReason  string    `gorm:"column:reset_reason"`
	ConfirmedSeq uint64    `gorm:"column:confirmed_seq"`
	CSessions    uint64    `gorm:"column:c_sessions"`
	CFramesOK    uint64    `gorm:"column:c_frames_ok"`
	CDtMs        uint64    `gorm:"column:c_dt_ms"`
	CMonoMs      uint64    `gorm:"column:c_mono_ms"`
	CRunOK       uint64    `gorm:"column:c_run_ok"`
	CGatedFrames uint64    `gorm:"column:c_gated_frames"`
	CGatedRuns   uint64    `gorm:"column:c_gated_runs"`
	CreatedAt    time.Time `gorm:"column:created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
}

func (Install) TableName() string { return "installs" }

type Report struct {
	ID         uint64    `gorm:"column:id;primaryKey"`
	InstallID  string    `gorm:"column:install_id"`
	Seq        uint64    `gorm:"column:seq"`
	ClientTS   int64     `gorm:"column:client_ts"`
	Payload    string    `gorm:"column:payload"` // JSON列用string
	ReceivedAt time.Time `gorm:"column:received_at;autoCreateTime"`
}

func (Report) TableName() string { return "reports" }

type Measurement struct {
	ID        uint64    `gorm:"column:id;primaryKey"`
	InstallID string    `gorm:"column:install_id"`
	MeasureNo uint64    `gorm:"column:measure_no"`
	Seq       uint64    `gorm:"column:seq"`
	StartTS   int64     `gorm:"column:start_ts"`
	EndTS     int64     `gorm:"column:end_ts"`
	Frames    uint64    `gorm:"column:frames"`
	DtMs      uint64    `gorm:"column:dt_ms"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (Measurement) TableName() string { return "measurements" }
```

store包另外提供 `Install.Counters()` 和 `ConfirmedUpdates(seq, counters) map[string]any`，service层不直接拼列名。

## 报文结构体(internal/protocol)

```go
const (
	TypeRegister = "register"
	TypeReport   = "report"
	TypeAck      = "ack"
	TypeRefused  = "refused"

	ReasonConflict     = "conflict"
	ReasonUnregistered = "unregistered"
)

// 请求外层；v、kid用指针，区分「缺失」(bad_request)和「取值不支持」
type Envelope struct {
	V    *int   `json:"v"`
	Kid  *int   `json:"kid"`
	Type string `json:"type"`
	CT   string `json:"ct"`
}

// 响应外层，没有kid
type RespEnvelope struct {
	V    int    `json:"v"`
	Type string `json:"type"`
	CT   string `json:"ct"`
}

type RegisterReq struct {
	InstallID string `json:"install_id"`
	AppKey    string `json:"app_key"`
	AppID     string `json:"app_id"`
	SDK       string `json:"sdk"`
	Platform  string `json:"platform"`
	Reset     string `json:"reset"`
	TS        int64  `json:"ts"`
}

type Counters struct {
	Sessions    uint64 `json:"sessions"`
	FramesOK    uint64 `json:"frames_ok"`
	DtMs        uint64 `json:"dt_ms"`
	MonoMs      uint64 `json:"mono_ms"`
	RunOK       uint64 `json:"run_ok"`
	GatedFrames uint64 `json:"gated_frames"`
	GatedRuns   uint64 `json:"gated_runs"`
}

type Measurement struct {
	No      uint64 `json:"no"`
	StartTS int64  `json:"start_ts"`
	EndTS   int64  `json:"end_ts"`
	Frames  uint64 `json:"frames"`
	DtMs    uint64 `json:"dt_ms"`
}

type ReportReq struct {
	InstallID    string        `json:"install_id"`
	AppKey       string        `json:"app_key"`
	AppID        string        `json:"app_id"`
	Platform     string        `json:"platform"`
	Seq          uint64        `json:"seq"`
	TS           int64         `json:"ts"`
	SDK          string        `json:"sdk"`
	Counters     Counters      `json:"counters"`
	Measurements []Measurement `json:"measurements"`
}

type Ack struct {
	InstallID string `json:"install_id"`
	Seq       uint64 `json:"seq"`
	CapFrames uint64 `json:"cap_frames"`
	CapRuns   uint64 `json:"cap_runs"`
	TTLSec    int    `json:"ttl_sec"`
	ReportSec int    `json:"report_sec"`
}

// refused = ack全部字段 + reason
type Refused struct {
	Ack
	Reason string `json:"reason"`
}
```

### 字段校验(不通过返回 bad_payload)

| 字段                 | 规则                                                                                                                                             |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| install_id           | 32位小写hex                                                                                                                                      |
| app_key、app_id、sdk | 非空，长度不超过列宽(64 / 255 / 32)；上报也必填(自动注册要用)；app_key 还要在 app_keys 表里且已启用，否则返回403 invalid_app_key(业务层查库校验) |
| platform             | android / ios                                                                                                                                    |
| reset                | 空、corrupt、conflict、unregistered之一(后两个客户端不会发，收到也不拒绝)                                                                        |
| ts                   | 大于0                                                                                                                                            |
| seq(上报)            | 大于等于1                                                                                                                                        |
| measurements         | 最多16条(防御性上限)；每条no大于等于1、end_ts大于等于start_ts                                                                                    |

1. 计数类字段是 `uint64`，传负数会在json解析时失败，同样返回bad_payload。
2. 明文里出现未知字段不报错，方便客户端先加字段。
3. `measurements[].dt_ms ≤ 5000` 不拒绝，只记录异常日志；拒绝会让客户端一直重试同一份上报。

### 错误码

```go
type Error struct {
	Status int    // http状态码
	Code   string // 返回给客户端的code
	Msg    string
}

var (
	ErrBadRequest         = &Error{400, "bad_request", "bad request"}
	ErrUnsupportedVersion = &Error{400, "unsupported_version", "unsupported version"}
	ErrUnknownType        = &Error{400, "unknown_type", "unknown type"}
	ErrUnknownKid         = &Error{400, "unknown_kid", "unknown kid"}
	ErrDecryptFailed      = &Error{400, "decrypt_failed", "decrypt failed"} // rsa、gcm失败都用它，msg固定
	ErrBadPayload         = &Error{400, "bad_payload", "bad payload"}
	ErrInvalidAppKey      = &Error{403, "invalid_app_key", "invalid app key"} // app_key 不存在或已停用
	ErrInternal           = &Error{500, "internal_error", "internal error"}
)
```

handler里只认 `*protocol.Error`，其他任何error都转成 `ErrInternal`，详细原因只写日志，不返回给客户端。

## 加解密(internal/envelope)

已在 Go 1.27 上验证：模拟客户端按readme手工拼接 `ek + iv + 密文 + tag`，服务端能正确解开；aad不一致会被拒绝；响应每次使用新iv。

```go
// OpenRequest 解开请求ct = Base64(ek + iv + 密文 + tag)，返回aes密钥和明文。
func OpenRequest(priv *rsa.PrivateKey, v, kid int, typ, ct string) (aesKey, plain []byte, err error) {
	raw, err := base64.StdEncoding.DecodeString(ct)
	if err != nil {
		return nil, nil, protocol.ErrBadRequest
	}
	k := priv.Size() // 2048位为256字节
	if len(raw) < k+12+16 {
		return nil, nil, protocol.ErrBadRequest
	}
	// DecryptOAEP的hash同时用于MGF1，正好是 SHA-256 / MGF1-SHA-256
	aesKey, err = rsa.DecryptOAEP(sha256.New(), nil, priv, raw[:k], nil)
	if err != nil || len(aesKey) != 32 {
		return nil, nil, protocol.ErrDecryptFailed
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, nil, protocol.ErrDecryptFailed
	}
	// RandomNonce模式的Open会从开头取12字节作为iv，正好对应 iv + 密文 + tag
	gcm, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, nil, err
	}
	aad := []byte(fmt.Sprintf("%d:%d:%s", v, kid, typ))
	plain, err = gcm.Open(nil, nil, raw[k:], aad)
	if err != nil {
		return nil, nil, protocol.ErrDecryptFailed
	}
	return aesKey, plain, nil
}

// SealResponse 加密响应，返回ct = Base64(iv + 密文 + tag)。
func SealResponse(aesKey []byte, v int, typ string, plain []byte) (string, error) {
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return "", err
	}
	// Seal自动生成随机iv并放在开头，杜绝iv复用
	gcm, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return "", err
	}
	aad := []byte(fmt.Sprintf("%d:%s", v, typ))
	return base64.StdEncoding.EncodeToString(gcm.Seal(nil, nil, plain, aad)), nil
}
```

1. aes密钥只在本次请求的函数栈里传递，不进日志、不进数据库、不放进context。
2. 不要自己实现「先生成iv再Seal」，统一用 `NewGCMWithRandomNonce`，避免有人误用请求的iv。

`internal/envelope/client.go` 另外提供客户端一侧的 `SealRequest`、`OpenResponse`，给 `cmd/fakeclient` 和测试模拟SDK用，服务端不调用。

### 密钥生成

```bash
# 服务端私钥(PKCS#8)，用 x509.ParsePKCS8PrivateKey 解析
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out keys/kid1.pem
# 给Android的公钥(SubjectPublicKeyInfo，对应 X509EncodedKeySpec)
openssl pkey -in keys/kid1.pem -pubout -out keys/kid1.pub.pem
# 给iOS的公钥(PKCS#1，SecKeyCreateWithData 需要这个格式)
openssl rsa -in keys/kid1.pem -RSAPublicKey_out -out keys/kid1.pkcs1.pub.pem
```

`keys/` 写进 `.gitignore`；私钥文件权限设为600。

## http层(internal/handler)

1. `gin.New()`，不用 `gin.Default()`：自己挂两个中间件。
   1. recovery：panic时返回500 `internal_error`，记录堆栈。
   2. 访问日志(slog)：记录type、kid、install_id、seq、响应type或错误code、耗时；不记ct、明文、aes密钥。
2. 只注册 `POST /afu/v1/meter`。
3. 生产环境 `gin.SetMode(gin.ReleaseMode)`；没有前置代理时 `r.SetTrustedProxies(nil)`。
4. 请求体用 `http.MaxBytesReader` 限制大小；超限暂按 bad_request 返回(readme未定义，见「待定」)。
5. 不要用 `c.BindJSON`：它解析失败会自己写400和纯文本响应，格式和错误码对不上。统一用 `json.NewDecoder` 解析，自己返回错误。
6. 外层用 `http.Server` 包Gin，设置 `ReadHeaderTimeout`、`ReadTimeout`、`WriteTimeout`；收到SIGTERM后调用 `Shutdown` 优雅退出。

「通用处理」10步对应的代码骨架：

```go
func (h *Handler) Meter(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxBody)

	var env protocol.Envelope
	if err := json.NewDecoder(c.Request.Body).Decode(&env); err != nil {
		h.fail(c, protocol.ErrBadRequest)
		return
	}
	if err := env.Validate(); err != nil { // bad_request / unsupported_version / unknown_type
		h.fail(c, err)
		return
	}
	priv, ok := h.keys.Get(*env.Kid)
	if !ok {
		h.fail(c, protocol.ErrUnknownKid)
		return
	}
	aesKey, plain, err := envelope.OpenRequest(priv, *env.V, *env.Kid, env.Type, env.CT)
	if err != nil {
		h.fail(c, err)
		return
	}

	var respType string
	var resp any
	switch env.Type {
	case protocol.TypeRegister:
		var req protocol.RegisterReq
		if err := protocol.DecodeAndValidate(plain, &req); err != nil {
			h.fail(c, protocol.ErrBadPayload)
			return
		}
		respType, resp, err = h.svc.Register(c.Request.Context(), req)
	case protocol.TypeReport:
		var req protocol.ReportReq
		if err := protocol.DecodeAndValidate(plain, &req); err != nil {
			h.fail(c, protocol.ErrBadPayload)
			return
		}
		respType, resp, err = h.svc.Report(c.Request.Context(), req, plain)
	}
	if err != nil {
		h.fail(c, err) // 非 *protocol.Error 一律转 internal_error
		return
	}

	body, err := json.Marshal(resp)
	if err != nil {
		h.fail(c, err)
		return
	}
	ct, err := envelope.SealResponse(aesKey, *env.V, respType, body)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, protocol.RespEnvelope{V: *env.V, Type: respType, CT: ct})
}
```

## 业务层(internal/service)

### install_id生成

`crypto/rand` 取16字节，`hex.EncodeToString` 得到32位小写hex，和客户端格式一致。插入时撞上唯一键(概率可以忽略)就重新生成。

### 注册处理

不先查再插，直接插入，用唯一键判断是否已存在，天然处理并发：

```go
func (s *Service) Register(ctx context.Context, req protocol.RegisterReq) (string, any, error) {
	if err := s.checkAppKey(ctx, req.InstallID, req.AppKey); err != nil { // 不存在或已停用：403，不入库
		return "", nil, err
	}
	if err := s.billing.ValidateApp(ctx, req.AppKey, req.AppID); err != nil { // 留桩：放行
		return "", nil, err
	}
	hasQuota, err := s.billing.HasQuota(ctx, req.AppKey)
	if err != nil {
		return "", nil, err
	}

	inst := store.Install{InstallID: req.InstallID, AppKey: req.AppKey, AppID: req.AppID,
		Platform: req.Platform, SDK: req.SDK, ResetReason: req.Reset}
	db := s.db.WithContext(ctx)
	err = db.Create(&inst).Error
	if err == nil {
		return protocol.TypeAck, s.ack(inst.InstallID, 0, protocol.Counters{}, hasQuota), nil
	}
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		return "", nil, err
	}

	// 已存在：换号，不记录旧编号
	newInst := inst
	newInst.ResetReason = protocol.ReasonConflict
	if err := s.createWithNewID(db, &newInst); err != nil { // 生成新编号插入，撞上唯一键就重新生成
		return "", nil, err
	}
	return protocol.TypeRefused, protocol.Refused{
		Ack:    s.ack(newInst.InstallID, 0, protocol.Counters{}, hasQuota),
		Reason: protocol.ReasonConflict,
	}, nil
}
```

### 上报处理

一个事务完成，隔离级别用 `READ COMMITTED`：`SELECT ... FOR UPDATE` 查不到行时不会加间隙锁，避免并发换号时死锁。

```go
type reportResult struct {
	respType  string
	installID string
	seq       uint64
	counters  protocol.Counters
	appKey    string
	needQuota bool // 重复上报、换号：事务提交后再查额度
	charge    billing.ChargeResult
	reason    string // 换号时为 unregistered
}

func (s *Service) Report(ctx context.Context, req protocol.ReportReq, raw []byte) (string, any, error) {
	var res reportResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var inst store.Install
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("install_id = ?", req.InstallID).Take(&inst).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return s.reassignOnReport(ctx, tx, req, raw, &res) // 换号
		}
		if err != nil {
			return err
		}
		res.respType, res.installID, res.appKey = protocol.TypeAck, inst.InstallID, inst.AppKey

		// 重复上报：不入库、不扣费，按当前状态回ack
		if req.Seq <= inst.ConfirmedSeq {
			res.seq, res.counters, res.needQuota = inst.ConfirmedSeq, inst.Counters(), true
			return nil
		}

		if err := tx.Create(&store.Report{InstallID: inst.InstallID, Seq: req.Seq,
			ClientTS: req.TS, Payload: string(raw)}).Error; err != nil {
			return err
		}
		if ms := store.ToMeasurements(inst.InstallID, req.Seq, req.Measurements); len(ms) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ms).Error; err != nil {
				return err
			}
		}

		delta, regressed := protocol.Delta(inst.Counters(), req.Counters) // 每项 max(0, 新 - 旧)
		if regressed {
			s.log.Warn("counters regressed", "install_id", inst.InstallID, "seq", req.Seq)
		}
		charge, err := s.billing.Charge(ctx, tx, billing.ChargeRequest{ // 按新增测量次数扣费、判断额度
			AppKey: req.AppKey, InstallID: inst.InstallID, Seq: req.Seq, Delta: delta})
		if err != nil {
			return err
		}

		confirmed := protocol.MaxCounters(inst.Counters(), req.Counters) // 只增不减
		if err := tx.Model(&store.Install{}).Where("id = ?", inst.ID).
			Updates(store.ConfirmedUpdates(req.Seq, confirmed)).Error; err != nil { // map，零值也会写入
			return err
		}
		res.seq, res.counters, res.charge = req.Seq, confirmed, charge
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return "", nil, err
	}

	hasQuota := res.charge.HasQuota
	if res.needQuota {
		if hasQuota, err = s.billing.HasQuota(ctx, res.appKey); err != nil {
			return "", nil, err
		}
	}
	if res.charge.Warn { // 预警通知放在事务外异步发送
		go s.notifier.QuotaWarning(context.WithoutCancel(ctx), res.appKey)
	}

	ack := s.ack(res.installID, res.seq, res.counters, hasQuota)
	if res.respType == protocol.TypeRefused {
		return protocol.TypeRefused, protocol.Refused{Ack: ack, Reason: res.reason}, nil
	}
	return protocol.TypeAck, ack, nil
}
```

`reassignOnReport` 在同一个事务(参数 `tx`)里，结果写进 `res`(`respType = refused`，`reason = unregistered`)：

1. 校验app_key、app_id(留桩放行)。
2. 生成新install_id，插入安装记录：`reset_reason = unregistered`，`confirmed_seq = req.Seq`，已确认counters = `req.Counters`(作为基线)。
3. 以新编号保存本次上报和measurements。
4. 触发换号的这次测量是否计费：调用 `billing.OnReassign` 留桩，默认不扣。
5. 设置 `res.appKey = req.AppKey`、`res.needQuota = true`；事务提交后查额度，返回 `refused(unregistered)`：install_id为新编号，seq为 `req.Seq`，cap按基线计算。

### cap计算

```go
func (s *Service) ack(installID string, seq uint64, c protocol.Counters, hasQuota bool) protocol.Ack {
	a := protocol.Ack{InstallID: installID, Seq: seq, CapFrames: c.FramesOK, CapRuns: c.RunOK,
		TTLSec: s.cfg.TTLSec, ReportSec: s.cfg.ReportSec}
	if hasQuota {
		a.CapFrames += s.cfg.PerMeasureFrames
		a.CapRuns += s.cfg.PerMeasureRuns
	}
	return a
}
```

## 计费(internal/billing)

```go
type ChargeResult struct {
	HasQuota bool // 扣费后余额 + 透支额度还够不够付下一次
	Warn     bool // 是否需要发送额度预警
}

type Billing interface {
	ValidateApp(ctx context.Context, appKey, appID string) error
	HasQuota(ctx context.Context, appKey string) (bool, error)
	// Charge 在上报事务内调用，按增量扣费；req 带 App Key、install_id、seq 和计数增量
	Charge(ctx context.Context, tx *gorm.DB, req ChargeRequest) (ChargeResult, error)
	// OnReassign 上报换号时调用，决定触发换号的这次测量是否计费
	OnReassign(ctx context.Context, tx *gorm.DB, appKey string, m []protocol.Measurement) error
}

type Notifier interface {
	QuotaWarning(ctx context.Context, appKey string)
}
```

服务启动用 `billing.KeyBilling`，按次计费：

1. 金额一律用整数，单位 0.0001 元(`store.MoneyUnitsPerYuan` = 10000)，避免小数误差。
2. 扣费：`Charge` 在上报事务里先 `FOR UPDATE` 锁住这个 App Key 的行，余额减去 `delta.Sessions × unit_price`，`used_count += delta.Sessions`，再记一条扣费流水(install_id、seq、次数、单价、扣后余额)。同一个 App Key 的扣费串行，加锁顺序固定为「安装记录 → App Key」，不会死锁。
3. 额度：单价为0不收费(照常计次、记流水)；`balance + overdraft_limit ≥ unit_price` 时还发额度，否则停发(ack 的 cap 等于已确认值)。按发出去的额度扣，不会扣穿透支额度。
4. 计费状态：`free` 不收费、`normal` 余额不为负、`overdraft` 余额为负但还够付一次、`exhausted` 停发。进入 `overdraft` 或 `exhausted` 时 `ChargeResult.Warn` 为 true，`LogNotifier` 记一条 `quota warning` 日志。
5. 重复上报、空上报、计数变小时增量为0，不扣费；上报换号时以本次计数为基线，触发换号的这次测量不扣费。
6. 允许少量超出：额度是在发 ack 时判断的，停发之前已经拿到额度的设备还能各测一次，所以余额最多超出透支额度「当时手里还有额度的设备数 × 单价」。
7. 余额只能通过充值(内部后台)和上报扣费变化，按 id 顺序累加流水金额等于当前余额。
8. `ValidateApp`、`OnReassign` 仍用 `Noop` 的留桩实现；计费细则同步后在 `KeyBilling` 上扩展，service层不用改。

## 本地开发

`docker-compose.yml`：

```yaml
services:
  mysql:
    image: mysql:8.4
    environment:
      MYSQL_ROOT_PASSWORD: root
      MYSQL_DATABASE: afu
      MYSQL_USER: afu
      MYSQL_PASSWORD: afu
    ports:
      - "${AFU_MYSQL_PORT:-3306}:3306"   # 本机3306被占用时: AFU_MYSQL_PORT=3307 make db
    volumes:
      - ./migrations:/docker-entrypoint-initdb.d:ro   # 首次启动自动建表
```

`Makefile` 目标：

| 目标           | 作用                                                                                                    |
| -------------- | ------------------------------------------------------------------------------------------------------- |
| `make db`      | `docker compose up -d mysql`                                                                            |
| `make keys`    | 按「密钥生成」生成kid1的私钥和两种公钥；`keys/kid1.pem` 已存在时不覆盖，避免已发给客户端的公钥失效      |
| `make run`     | `go run ./cmd/server -config configs/config.yaml`                                                       |
| `make test`    | `go test ./...`(不连数据库)                                                                             |
| `make itest`   | 设置 `AFU_TEST_DSN` 后跑集成测试                                                                        |
| `make lint`    | `go vet ./...`                                                                                          |
| `make image`   | 构建Docker镜像(默认 linux/amd64，本机交叉编译)，版本号取git提交号                                       |
| `make package` | 打部署包到 `release/`：镜像 + docker-compose + 配置模板 + 建表SQL + 脚本，部署步骤见 `deploy/README.md` |

## 测试

### 单元测试(不连数据库)

1. envelope：
   1. 模拟客户端加密 → `OpenRequest` 解开，明文一致。
   2. aad里的type或kid不一致 → decrypt_failed。
   3. 篡改tag、篡改ek → decrypt_failed，两种情况返回的code和msg完全相同。
   4. ct不是Base64、长度小于284字节 → bad_request。
   5. `SealResponse` 连续两次加密同一明文，iv不同。
2. protocol：字段校验表里每条规则一正一反；`Delta`、`MaxCounters` 覆盖计数变小的情况。
3. handler：用httptest覆盖每个错误码的http状态和响应体；service用假实现。

### 集成测试(连本地MySQL，没设 `AFU_TEST_DSN` 就跳过)

> 测试会清空 installs、reports、measurements 三张表，`AFU_TEST_DSN` 必须指向专用测试库。

| 场景                                           | 期望                                                                                                                              |
| ---------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| app_key 不存在、已停用、大小写不同             | 403 invalid_app_key，不入库、不扣费                                                                                               |
| 新install_id注册                               | ack，seq = 0，cap = 单次测量额度                                                                                                  |
| 已存在的install_id注册                         | refused(conflict)，install_id为新编号且已入库                                                                                     |
| 正常上报                                       | ack，已确认seq和counters更新                                                                                                      |
| 重复seq                                        | ack，不新增reports行，不调用Charge                                                                                                |
| 计数变小                                       | 已确认counters不变，增量按0，记录异常日志                                                                                         |
| 空上报                                         | ack，增量为0                                                                                                                      |
| 未知install_id上报                             | refused(unregistered)，新记录以本次counters为基线                                                                                 |
| 同一install_id并发上报                         | 串行处理，只扣一次                                                                                                                |
| App Key 单价0.50元、余额1.00元、透支额度1.00元 | 余额依次0.50 → 0 → −0.50(透支) → −1.00(停发，cap = 已确认)；重复上报、空上报不扣费；每笔扣费一条流水；停发后注册 cap = 0；预警2次 |
| 20台设备并发上报同一个 App Key(单价0.01元)     | 余额正好少0.20元，流水合计−0.20元，计费次数20                                                                                     |

### 端到端

`cmd/fakeclient` 用测试公钥模拟SDK：注册 → 上报(`-reports N` 次，默认1) → 重复上报 → 空上报 → 重复注册换号 → 未知编号上报换号，逐步打印解密后的响应。客户端联调前先用它跑通，SDK同学可以对照它的报文排查问题。

## 开发顺序

| 阶段 | 内容                                                              | 验收                                           |
| ---- | ----------------------------------------------------------------- | ---------------------------------------------- |
| 1    | 骨架：go mod、配置、日志、Gin启动与优雅退出、docker-compose、建表 | `make run` 能启动，随便POST返回400 bad_request |
| 2    | envelope、protocol、handler通用10步(service用假实现)              | 单元测试全过；fakeclient能拿到解密后的假响应   |
| 3    | store + 注册处理                                                  | 注册两个集成测试场景通过                       |
| 4    | 上报处理：事务、行锁、增量、只增不减、换号                        | 上报相关集成测试场景通过                       |
| 5    | 计费桩、cap计算                                                   | 「无额度」场景通过                             |
| 6    | fakeclient端到端、与SDK联调                                       | 客户端用测试密钥跑通注册和上报                 |
| 7    | 计费细则同步后：计费、预警通知、app_key校验                       | 随细则定                                       |

## 待定

1. 计费细则：单次测量额度的值、客户与App Key的关系、预警通知渠道。按次计费(单价、余额、透支额度、充值、流水)已实现(`billing.KeyBilling`)，其余规则待同步。
2. 请求体超限的错误码：readme未定义，暂按bad_request实现。
3. ttl_sec、report_sec的语义：暂取配置值原样下发。
4. 部署：https在服务里终止还是由前置负载均衡处理；阿福转发阶段的身份认证；限流。
5. 已知风险：克隆设备用同一个install_id时，落后的一台会被当成重试而不扣费。
