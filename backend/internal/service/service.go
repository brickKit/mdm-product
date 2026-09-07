// Package service 是 mdm-product 的业务规则层。判过 SOP-P：这个组件是
// 只读枢纽 + 常规 CRUD，不用任何设计模式，直接写就是最清楚的（同
// mdm-customer 的判据，详见 docs/手册.md）。这一层薄——真正的乐观锁
// 判断、事件发布都在 repo 层随 SQL 一起做（同一个事务里），这里只负责
// 给 http/grpc 一个不依赖 repo 内部细节的稳定入口，外加入参校验与错误
// 日志。
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/brickKit/mdm-product/backend/internal/repo"
)

// ErrInvalidArgument 是入参本身不合法（不是数据库层面的冲突/缺失），
// grpc/http 两层都通过 ToStatus 把它映射成 InvalidArgument/400（同
// mdm-customer 的判据）。
var ErrInvalidArgument = errors.New("参数不合法")

type Service struct {
	repo   *repo.Repo
	logger *slog.Logger
}

func New(r *repo.Repo, logger *slog.Logger) *Service {
	return &Service{repo: r, logger: logger}
}

var validTrackingTypes = map[string]bool{"": true, "NONE": true, "BATCH": true, "SERIAL": true}

func validateTrackingType(s string) error {
	if !validTrackingTypes[s] {
		return fmt.Errorf("%w: tracking_type 不是合法值：%q", ErrInvalidArgument, s)
	}
	return nil
}

// validateStandardCost 只做格式/正负号校验（合法非负小数），不代替
// NUMERIC(18,2) 的精度校验——那是数据库自己的事（同 mdm-customer 的
// validateCreditLimit 判据）。
func validateStandardCost(s string) error {
	if s == "" {
		return nil // repo 层留空时默认成 "0"
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("%w: standard_cost 不是合法数字：%q", ErrInvalidArgument, s)
	}
	if f < 0 {
		return fmt.Errorf("%w: standard_cost 不能为负数：%q", ErrInvalidArgument, s)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, in repo.CreateInput) (*repo.Product, error) {
	if in.Name == "" {
		return nil, fmt.Errorf("%w: name 不能为空", ErrInvalidArgument)
	}
	if in.BaseUOMID == "" {
		return nil, fmt.Errorf("%w: base_uom_id 不能为空", ErrInvalidArgument)
	}
	if err := validateTrackingType(in.TrackingType); err != nil {
		return nil, err
	}
	if err := validateStandardCost(in.StandardCost); err != nil {
		return nil, err
	}
	p, err := s.repo.Create(ctx, in)
	if err != nil {
		s.logger.Error("创建产品失败", "sku", in.SKU, "error", err)
		return nil, err
	}
	return p, nil
}

type UpdateInput struct {
	IdempotencyKey string
	ID             string
	Version        int64
	Name           string
	CategoryID     string
	StandardCost   string
}

func (s *Service) Update(ctx context.Context, in UpdateInput) (*repo.Product, error) {
	if in.Name == "" {
		return nil, fmt.Errorf("%w: name 不能为空", ErrInvalidArgument)
	}
	if err := validateStandardCost(in.StandardCost); err != nil {
		return nil, err
	}
	p, err := s.repo.Update(ctx, repo.UpdateInput{
		IdempotencyKey: in.IdempotencyKey,
		ID:             in.ID,
		Version:        in.Version,
		Name:           in.Name,
		CategoryID:     in.CategoryID,
		StandardCost:   in.StandardCost,
	})
	if err != nil {
		s.logger.Error("更新产品失败", "id", in.ID, "error", err)
		return nil, err
	}
	return p, nil
}

type SetStatusInput struct {
	IdempotencyKey string
	ID             string
	Version        int64
	Status         string
}

func (s *Service) SetStatus(ctx context.Context, in SetStatusInput) (*repo.Product, error) {
	p, err := s.repo.SetStatus(ctx, repo.SetStatusInput{
		IdempotencyKey: in.IdempotencyKey,
		ID:             in.ID,
		Version:        in.Version,
		Status:         in.Status,
	})
	if err != nil {
		s.logger.Error("变更产品状态失败", "id", in.ID, "status", in.Status, "error", err)
		return nil, err
	}
	return p, nil
}

func (s *Service) Get(ctx context.Context, id string) (*repo.Product, error) {
	got, _, err := s.repo.BatchGet(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	if len(got) == 0 {
		return nil, repo.ErrNotFound
	}
	return got[0], nil
}

func (s *Service) BatchGet(ctx context.Context, ids []string) (found []*repo.Product, missing []string, err error) {
	return s.repo.BatchGet(ctx, ids)
}

func (s *Service) List(ctx context.Context, in repo.ListInput) (*repo.ListResult, error) {
	return s.repo.List(ctx, in)
}

func (s *Service) ConvertQuantity(ctx context.Context, productID, qty, fromUOMID, toUOMID string) (string, error) {
	if qty == "" {
		return "", fmt.Errorf("%w: qty 不能为空", ErrInvalidArgument)
	}
	if fromUOMID == "" || toUOMID == "" {
		return "", fmt.Errorf("%w: from_uom_id/to_uom_id 不能为空", ErrInvalidArgument)
	}
	return s.repo.ConvertQuantity(ctx, productID, qty, fromUOMID, toUOMID)
}
