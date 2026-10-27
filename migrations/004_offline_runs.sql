-- 离线可用次数，跟 App Key 绑定，在内部后台修改：ack / refused 的 cap_runs 直接下发这个值，
-- 含义是「从 acked_sessions 往后还能新增几次计次」，设备不联网时最多先用这么多次。
-- 默认5，范围0~16(一份上报最多16条measurements，离线攒下的计次要能一份装下)。
-- 已部署的库不会自动执行本文件，按部署说明手动执行一次。
ALTER TABLE app_keys
  ADD COLUMN offline_runs BIGINT UNSIGNED NOT NULL DEFAULT 5 COMMENT '离线可用次数，cap_runs 直接下发';
