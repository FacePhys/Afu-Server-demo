-- 违规计数：上报里的计次记录起始时间晚于结束时间(start_ts > end_ts)算一次违规，照常入库、扣费，按 install_id 累计；
-- 设备的违规次数达到 App Key 的违规上限后，回复的 cap_runs 为0(停发)，后台清零后恢复。
-- 一条计次记录(install_id + no)只算一次；明细就是 measurements 里 start_ts > end_ts 的行。
-- 本表是唯一准数据，Redis 里只是缓存，见 docs/development.md「违规计数」。
-- 已部署的库不会自动执行本文件，按部署说明手动执行一次。
CREATE TABLE IF NOT EXISTS install_violations (
  install_id      CHAR(32)        NOT NULL PRIMARY KEY,
  violation_count BIGINT UNSIGNED NOT NULL DEFAULT 0,   -- 当前违规次数，清零后重新累计
  total_count     BIGINT UNSIGNED NOT NULL DEFAULT 0,   -- 历史累计，清零不变
  epoch           BIGINT UNSIGNED NOT NULL DEFAULT 0,   -- 清零次数；Redis 缓存按 (epoch, 违规次数) 只增不减
  last_seq        BIGINT UNSIGNED NOT NULL DEFAULT 0,   -- 最近一次违规所在的上报
  last_no         BIGINT UNSIGNED NOT NULL DEFAULT 0,   -- 最近一次违规的计次编号
  last_at         DATETIME(3)     NULL,
  reset_by        VARCHAR(64)     NOT NULL DEFAULT '',  -- 最近一次清零的后台操作人
  reset_at        DATETIME(3)     NULL,
  created_at      DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_updated_at (updated_at)                       -- Redis 全量重建后补上重建期间的改动
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 违规上限跟 App Key 绑定，在内部后台修改，从下一次回复开始生效；0 表示不限(只计数、不停发)。
ALTER TABLE app_keys
  ADD COLUMN max_violations BIGINT UNSIGNED NOT NULL DEFAULT 3 COMMENT '违规上限：设备违规次数达到后 cap_runs 下发0；0为不限';
