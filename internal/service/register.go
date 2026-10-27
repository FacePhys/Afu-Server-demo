package service

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// Register 处理注册：不先查再插，直接插入，用唯一键判断install_id是否已存在，天然处理并发。
// 已存在时换号(不记录旧编号)，返回refused(conflict)。
func (s *Service) Register(ctx context.Context, req protocol.RegisterReq) (string, any, error) {
	key, err := s.loadAppKey(ctx, req.InstallID, req.AppKey)
	if err != nil {
		return "", nil, err
	}
	if err := s.billing.ValidateApp(ctx, req.AppKey, req.AppID); err != nil { // 留桩：放行
		return "", nil, err
	}
	hasQuota, err := s.billing.HasQuota(ctx, req.AppKey)
	if err != nil {
		return "", nil, err
	}

	db := s.db.WithContext(ctx)
	inst := store.Install{InstallID: req.InstallID, AppKey: req.AppKey, AppID: req.AppID,
		Platform: req.Platform, SDK: req.SDK, ResetReason: req.Reset}
	err = db.Create(&inst).Error
	if err == nil {
		return protocol.TypeAck, s.ack(inst.InstallID, 0, 0, key, hasQuota), nil
	}
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		return "", nil, err
	}

	newInst := inst
	newInst.ResetReason = protocol.ReasonConflict
	if err := s.createWithNewID(db, &newInst); err != nil {
		return "", nil, err
	}
	s.log.Info("install_id reassigned", "reason", protocol.ReasonConflict, "old", req.InstallID, "new", newInst.InstallID)
	return protocol.TypeRefused, s.refused(newInst.InstallID, 0, 0, key, hasQuota, protocol.ReasonConflict), nil
}
