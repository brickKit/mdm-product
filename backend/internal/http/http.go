// Package http 是 mdm-product 的 REST 面（对外路径前缀 /mdm/product，
// 与 assembly.yaml 的 edge_routes 一致）。/healthz、/metrics 已经由
// besdk.NewGinEngine 统一挂好（零依赖、恒 200，§12.3.6），这里不重复挂、
// 也不写进 contracts/product.openapi.yaml。
package http

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	besdk "github.com/brickKit/be-sdk-go"
	"github.com/brickKit/mdm-product/backend/internal/repo"
	"github.com/brickKit/mdm-product/backend/internal/service"
)

// RegisterRoutes 挂载业务路由。eng 已经是 besdk.NewGinEngine 产出的、
// 挂好中间件的 engine——这里只负责注册业务 handler。
//
// 阶段三 Task 6：权限键从阶段二的 besdk.Public 换成 assembly.yaml 里
// 声明的真实键。`convert-quantity` 是纯计算（换算因子查表，不读写任何
// 具体产品的敏感字段），归到 view 档——它是"看产品能不能换算"的一部分，
// 不是独立的业务动作，不需要单独的权限键。`set_status`（启用/停用）算
// 编辑动作，归 edit。`data_scopes: none`，本组件不需要任何数据范围过滤。
func RegisterRoutes(eng *gin.Engine, svc *service.Service) {
	g := eng.Group("/mdm/product")
	besdk.GET(g, "/products", "mdm.product.view", listHandler(svc))
	besdk.GET(g, "/products/:id", "mdm.product.view", getHandler(svc))
	besdk.POST(g, "/products", "mdm.product.create", createHandler(svc))
	besdk.PATCH(g, "/products/:id", "mdm.product.edit", updateHandler(svc))
	besdk.POST(g, "/products/:id/status", "mdm.product.edit", setStatusHandler(svc))
	besdk.POST(g, "/products/convert-quantity", "mdm.product.view", convertQuantityHandler(svc))
}

type productDTO struct {
	ID           string `json:"id"`
	SKU          string `json:"sku"`
	Name         string `json:"name"`
	CategoryID   string `json:"category_id"`
	BaseUOMID    string `json:"base_uom_id"`
	TrackingType string `json:"tracking_type"`
	StandardCost string `json:"standard_cost"`
	Status       string `json:"status"`
	Version      int64  `json:"version"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func toDTO(p *repo.Product) productDTO {
	return productDTO{
		ID: p.ID, SKU: p.SKU, Name: p.Name, CategoryID: p.CategoryID,
		BaseUOMID: p.BaseUOMID, TrackingType: p.TrackingType, StandardCost: p.StandardCost,
		Status: p.Status, Version: p.Version,
		CreatedAt: p.CreatedAt.Format(rfc3339), UpdatedAt: p.UpdatedAt.Format(rfc3339),
	}
}

const rfc3339 = "2006-01-02T15:04:05.999999999Z07:00"

type createRequest struct {
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
	SKU            string `json:"sku"`
	Name           string `json:"name" binding:"required"`
	CategoryID     string `json:"category_id"`
	BaseUOMID      string `json:"base_uom_id" binding:"required"`
	TrackingType   string `json:"tracking_type"`
	StandardCost   string `json:"standard_cost"`
}

func createHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		out, err := svc.Create(c.Request.Context(), repo.CreateInput{
			IdempotencyKey: req.IdempotencyKey, SKU: req.SKU, Name: req.Name,
			CategoryID: req.CategoryID, BaseUOMID: req.BaseUOMID,
			TrackingType: req.TrackingType, StandardCost: req.StandardCost,
		})
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		c.JSON(http.StatusOK, toDTO(out))
	}
}

func getHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		out, err := svc.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		c.JSON(http.StatusOK, toDTO(out))
	}
}

func listHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		pageSize, _ := strconv.Atoi(c.Query("page_size"))
		out, err := svc.List(c.Request.Context(), repo.ListInput{
			Cursor:       c.Query("cursor"),
			PageSize:     pageSize,
			StatusFilter: c.Query("status_filter"),
		})
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		dtos := make([]productDTO, 0, len(out.Products))
		for _, p := range out.Products {
			dtos = append(dtos, toDTO(p))
		}
		c.JSON(http.StatusOK, gin.H{"products": dtos, "next_cursor": out.NextCursor})
	}
}

type updateRequest struct {
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
	Version        int64  `json:"version" binding:"required"`
	Name           string `json:"name"`
	CategoryID     string `json:"category_id"`
	StandardCost   string `json:"standard_cost"`
}

func updateHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req updateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		out, err := svc.Update(c.Request.Context(), service.UpdateInput{
			IdempotencyKey: req.IdempotencyKey, ID: c.Param("id"), Version: req.Version,
			Name: req.Name, CategoryID: req.CategoryID, StandardCost: req.StandardCost,
		})
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		c.JSON(http.StatusOK, toDTO(out))
	}
}

type setStatusRequest struct {
	IdempotencyKey string `json:"idempotency_key" binding:"required"`
	Version        int64  `json:"version" binding:"required"`
	Status         string `json:"status" binding:"required"`
}

func setStatusHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req setStatusRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		out, err := svc.SetStatus(c.Request.Context(), service.SetStatusInput{
			IdempotencyKey: req.IdempotencyKey, ID: c.Param("id"),
			Version: req.Version, Status: req.Status,
		})
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		c.JSON(http.StatusOK, toDTO(out))
	}
}

type convertQuantityRequest struct {
	ProductID string `json:"product_id" binding:"required"`
	Qty       string `json:"qty" binding:"required"`
	FromUOMID string `json:"from_uom_id" binding:"required"`
	ToUOMID   string `json:"to_uom_id" binding:"required"`
}

func convertQuantityHandler(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req convertQuantityRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		qty, err := svc.ConvertQuantity(c.Request.Context(), req.ProductID, req.Qty, req.FromUOMID, req.ToUOMID)
		if err != nil {
			_ = c.Error(service.ToStatus(err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"qty": qty})
	}
}
