package service

import (
	"errors"

	"github.com/brickKit/mdm-product/backend/internal/repo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ToStatus 把 repo 层的哨兵错误翻成 gRPC status——HTTP 与 gRPC 两条对外
// 接口共用同一套业务错误类型，不用为两种协议各写一遍映射（同
// mdm-customer 的判据）。
func ToStatus(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, repo.ErrVersionConflict):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, repo.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, repo.ErrCrossCategoryConversion):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
