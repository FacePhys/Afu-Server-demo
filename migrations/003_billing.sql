-- 按次计费：每个 App Key 有单价、余额、透支额度；每次上报按「新增测量次数 × 单价」从余额扣。
-- 金额一律用整数，单位 0.0001 元(1 元 = 10000)。
-- 已部署的库不会自动执行本文件，按部署说明手动执行一次。
ALTER TABLE app_keys
  ADD COLUMN unit_price      BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '单价，0.0001元/次；0为不收费',
  ADD COLUMN balance         BIGINT          NOT NULL DEFAULT 0 COMMENT '余额，0.0001元；透支时为负',
  ADD COLUMN overdraft_limit BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '透支额度，0.0001元',
  ADD COLUMN used_count      BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '累计计费测量次数';

-- 计费流水：每笔扣费、充值一条，按 id 顺序累加 amount 等于当前余额。
CREATE TABLE IF NOT EXISTS billing_ledger (
  id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  app_key       VARCHAR(64)  COLLATE utf8mb4_bin NOT NULL,
  kind          VARCHAR(16)  NOT NULL,                     -- consume 扣费 / recharge 充值
  amount        BIGINT       NOT NULL,                     -- 余额变动，0.0001元：扣费为负、充值为正
  balance_after BIGINT       NOT NULL,                     -- 变动后的余额
  count         BIGINT UNSIGNED NOT NULL DEFAULT 0,        -- 扣费：本次计费的测量次数
  unit_price    BIGINT UNSIGNED NOT NULL DEFAULT 0,        -- 扣费：当时的单价
  install_id    CHAR(32)     NOT NULL DEFAULT '',          -- 扣费：哪台设备
  seq           BIGINT UNSIGNED NOT NULL DEFAULT 0,        -- 扣费：哪份上报
  operator      VARCHAR(64)  NOT NULL DEFAULT '',          -- 充值：后台操作人
  remark        VARCHAR(255) NOT NULL DEFAULT '',
  created_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_app_key_id (app_key, id),
  KEY idx_install_seq (install_id, seq)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
