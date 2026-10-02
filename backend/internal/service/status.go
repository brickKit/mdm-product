package service

import (
	"errors"

	"github.com/brickKit/mdm-product/v2/backend/internal/repo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ToStatus 把 repo / service 的哨兵错误翻成 gRPC status。gRPC 直接返回它；
// REST handler 交给 c.Error，由 besdk.NewGinEngine 的中间件按同一张表翻成
// HTTP 状态码——两种协议共用一份映射。
func ToStatus(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, repo.ErrVersionConflict):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, repo.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, repo.ErrInvalidCursor), errors.Is(err, repo.ErrInvalidReference):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, repo.ErrSKUTaken):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, repo.ErrCrossCategoryConversion):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
