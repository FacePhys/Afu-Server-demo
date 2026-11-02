-- 异常清单：服务端发现的异常按「类型 + 设备」记一行，重复出现时次数累加，记首次、最近一次的时间和最近一次的详情。
-- 只是记录，不影响处理(该拒的请求照常拒，上报照常入库、扣费)，在内部后台「异常清单」按类型、设备、时间筛选。
-- 类型见 internal/anomaly：计次起止时间颠倒、明文不合法、App Key 无效、换号(注册冲突 / 上报编号不存在)、计数变小、新增计次超过离线可用次数。
-- 取代 006 的违规计数(达到上限停发)：install_violations 表和 app_keys.max_violations 列从本版起不再使用，
-- 先保留(切换版本时旧版本还在运行，回滚到上一版也要用)，确认不回滚后再删。
-- 已部署的库不会自动执行本文件，按部署说明手动执行一次。
CREATE TABLE IF NOT EXISTS anomalies (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,  -- 后台翻页用
  kind        VARCHAR(32)     NOT NULL,                 -- 类型
  install_id  CHAR(32)        NOT NULL DEFAULT '',      -- 设备；请求里的编号格式不对时为空
  related_id  CHAR(32)        NOT NULL DEFAULT '',      -- 换号的新编号(最近一次)
  app_key     VARCHAR(64)     COLLATE utf8mb4_bin NOT NULL DEFAULT '',  -- 最近一次请求带的，区分大小写
  platform    VARCHAR(16)     NOT NULL DEFAULT '',
  sdk         VARCHAR(32)     NOT NULL DEFAULT '',
  seq         BIGINT UNSIGNED NOT NULL DEFAULT 0,       -- 最近一次所在的上报
  detail      VARCHAR(512)    NOT NULL DEFAULT '',      -- 最近一次的详情：原因、字段、取值、新编号等，不含明文
  count       BIGINT UNSIGNED NOT NULL DEFAULT 0,       -- 累计次数
  first_at    DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  last_at     DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (kind, install_id),                       -- 同一台设备的同一类异常只有一行；按主键累加，并发写也不会死锁
  UNIQUE KEY uk_id (id),
  KEY idx_last_at (last_at),                            -- 按最近发生时间排序、筛选
  KEY idx_install_id (install_id),
  KEY idx_related_id (related_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
