// Package grpc 实现 mdm.product.v1.ProductService——内部 gRPC 面（§2.1）。
// HTTP 与 gRPC 共用同一个 service.Service，业务逻辑只写一遍。
package grpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/brickKit/mdm-product/gen/mdm/product/v1"

	"github.com/brickKit/mdm-product/backend/internal/repo"
	"github.com/brickKit/mdm-product/backend/internal/service"
)

type server struct {
	productv1.UnimplementedProductServiceServer
	svc *service.Service
}

// New 构造 gRPC 服务端实现。module.go 用它注册到 grpc.Server。
func New(svc *service.Service) productv1.ProductServiceServer {
	return &server{svc: svc}
}

func toProtoStatus(s string) productv1.ProductStatus {
	if s == "DISABLED" {
		return productv1.ProductStatus_PRODUCT_STATUS_DISABLED
	}
	return productv1.ProductStatus_PRODUCT_STATUS_ACTIVE
}

func fromProtoStatus(s productv1.ProductStatus) string {
	if s == productv1.ProductStatus_PRODUCT_STATUS_DISABLED {
		return "DISABLED"
	}
	return "ACTIVE"
}

func toProtoTracking(s string) productv1.TrackingType {
	switch s {
	case "BATCH":
		return productv1.TrackingType_TRACKING_TYPE_BATCH
	case "SERIAL":
		return productv1.TrackingType_TRACKING_TYPE_SERIAL
	default:
		return productv1.TrackingType_TRACKING_TYPE_NONE
	}
}

func fromProtoTracking(t productv1.TrackingType) string {
	switch t {
	case productv1.TrackingType_TRACKING_TYPE_BATCH:
		return "BATCH"
	case productv1.TrackingType_TRACKING_TYPE_SERIAL:
		return "SERIAL"
	default:
		return "NONE"
	}
}

func toProtoProduct(p *repo.Product) *productv1.Product {
	return &productv1.Product{
		Id: p.ID, Sku: p.SKU, Name: p.Name, CategoryId: p.CategoryID, BaseUomId: p.BaseUOMID,
		TrackingType: toProtoTracking(p.TrackingType), StandardCost: p.StandardCost,
		Status: toProtoStatus(p.Status), Version: p.Version,
		CreatedAt: timestamppb.New(p.CreatedAt), UpdatedAt: timestamppb.New(p.UpdatedAt),
	}
}

func (s *server) Create(ctx context.Context, req *productv1.CreateRequest) (*productv1.CreateResponse, error) {
	p, err := s.svc.Create(ctx, repo.CreateInput{
		IdempotencyKey: req.IdempotencyKey, SKU: req.Sku, Name: req.Name,
		CategoryID: req.CategoryId, BaseUOMID: req.BaseUomId,
		TrackingType: fromProtoTracking(req.TrackingType), StandardCost: req.StandardCost,
	})
	if err != nil {
		return nil, service.ToStatus(err)
	}
	return &productv1.CreateResponse{Product: toProtoProduct(p)}, nil
}

func (s *server) Update(ctx context.Context, req *productv1.UpdateRequest) (*productv1.UpdateResponse, error) {
	p, err := s.svc.Update(ctx, service.UpdateInput{
		IdempotencyKey: req.IdempotencyKey, ID: req.Id, Version: req.Version,
		Name: req.Name, CategoryID: req.CategoryId, StandardCost: req.StandardCost,
	})
	if err != nil {
		return nil, service.ToStatus(err)
	}
	return &productv1.UpdateResponse{Product: toProtoProduct(p)}, nil
}

func (s *server) SetStatus(ctx context.Context, req *productv1.SetStatusRequest) (*productv1.SetStatusResponse, error) {
	p, err := s.svc.SetStatus(ctx, service.SetStatusInput{
		IdempotencyKey: req.IdempotencyKey, ID: req.Id, Version: req.Version,
		Status: fromProtoStatus(req.Status),
	})
	if err != nil {
		return nil, service.ToStatus(err)
	}
	return &productv1.SetStatusResponse{Product: toProtoProduct(p)}, nil
}

func (s *server) Get(ctx context.Context, req *productv1.GetRequest) (*productv1.Product, error) {
	p, err := s.svc.Get(ctx, req.Id)
	if err != nil {
		return nil, service.ToStatus(err)
	}
	return toProtoProduct(p), nil
}

func (s *server) List(ctx context.Context, req *productv1.ListRequest) (*productv1.ListResponse, error) {
	in := repo.ListInput{Cursor: req.Cursor, PageSize: int(req.PageSize)}
	if req.StatusFilter != productv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED {
		in.StatusFilter = fromProtoStatus(req.StatusFilter)
	}
	if req.CreatedAfter != nil {
		in.CreatedAfter = req.CreatedAfter.AsTime()
	}
	if req.CreatedBefore != nil {
		in.CreatedBefore = req.CreatedBefore.AsTime()
	}
	out, err := s.svc.List(ctx, in)
	if err != nil {
		return nil, service.ToStatus(err)
	}
	products := make([]*productv1.Product, 0, len(out.Products))
	for _, p := range out.Products {
		products = append(products, toProtoProduct(p))
	}
	return &productv1.ListResponse{Products: products, NextCursor: out.NextCursor}, nil
}

// BatchGet 是 BFF 与 erp-sales 防 N+1 的唯一合法调用方式（§3.8）。
func (s *server) BatchGet(ctx context.Context, req *productv1.BatchGetRequest) (*productv1.BatchGetResponse, error) {
	found, missing, err := s.svc.BatchGet(ctx, req.Ids)
	if err != nil {
		return nil, service.ToStatus(err)
	}
	products := make([]*productv1.Product, 0, len(found))
	for _, p := range found {
		products = append(products, toProtoProduct(p))
	}
	return &productv1.BatchGetResponse{Products: products, MissingIds: missing}, nil
}

func (s *server) GetSummary(ctx context.Context, req *productv1.GetSummaryRequest) (*productv1.GetSummaryResponse, error) {
	found, _, err := s.svc.BatchGet(ctx, req.Ids)
	if err != nil {
		return nil, service.ToStatus(err)
	}
	summaries := make([]*productv1.ProductSummary, 0, len(found))
	for _, p := range found {
		summaries = append(summaries, &productv1.ProductSummary{
			Id: p.ID, Sku: p.SKU, Name: p.Name, BaseUomId: p.BaseUOMID,
			TrackingType: toProtoTracking(p.TrackingType), Status: toProtoStatus(p.Status), Version: p.Version,
		})
	}
	return &productv1.GetSummaryResponse{Summaries: summaries}, nil
}

func (s *server) ConvertQuantity(ctx context.Context, req *productv1.ConvertQuantityRequest) (*productv1.ConvertQuantityResponse, error) {
	qty, err := s.svc.ConvertQuantity(ctx, req.ProductId, req.Qty, req.FromUomId, req.ToUomId)
	if err != nil {
		return nil, service.ToStatus(err)
	}
	return &productv1.ConvertQuantityResponse{Qty: qty}, nil
}
