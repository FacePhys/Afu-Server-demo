-- 用 IF NOT EXISTS，重复执行不报错；集成测试也直接执行本文件建表。
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
  KEY idx_app_key (app_key),
  KEY idx_created_at (created_at)                       -- 内部后台按时间查询、统计
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS reports (
  id           BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  install_id   CHAR(32)        NOT NULL,              -- 换号时记新编号
  seq          BIGINT UNSIGNED NOT NULL,
  client_ts    BIGINT          NOT NULL,
  payload      JSON            NOT NULL,              -- 解密后的原始明文
  received_at  DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_install_seq (install_id, seq),
  KEY idx_received_at (received_at)
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
  UNIQUE KEY uk_install_no (install_id, measure_no),
  KEY idx_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
